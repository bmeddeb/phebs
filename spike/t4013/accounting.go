package t4013

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	allocationSampleInterval = time.Second
	dataMeasurementTimeout   = time.Duration(frozenDataMeasurementDeadlineMS) * time.Millisecond
	dataMeasurementScope     = "custody"
	dataMeasurementAllocated = "allocated"
	dataMeasurementLogical   = "logical"
	dataMeasurementDeadline  = "deadline"
)

type phaseMeter struct {
	started    time.Time
	server     *privateServer
	dataDir    string
	logOffset  int64
	before     *privateProfileSnapshot
	allocation *allocationSampler
	strict     bool
	captureRaw bool
	rawEnd     *dataMeasurementBoundary
}

// dataMeasurementBoundary is the successful raw allocated-byte end gauge of
// one meter. A V27 interruption restart may consume it once, and only for the
// same canonical custody workspace, instead of immediately walking that large
// workspace again.
type dataMeasurementBoundary struct {
	workspace string
	allocated int64
	consumed  bool
}

type dataMeasurementDeadlineError struct {
	gauge string
}

func (*dataMeasurementDeadlineError) Error() string {
	return "T40.13 data-byte measurement exceeded its deadline"
}

func newDataMeasurementDeadlineError(apparent bool) error {
	gauge := dataMeasurementAllocated
	if apparent {
		gauge = dataMeasurementLogical
	}
	return &dataMeasurementDeadlineError{gauge: gauge}
}

func dataMeasurementContextError(ctx context.Context, apparent bool) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return newDataMeasurementDeadlineError(apparent)
	}
	// Preserve the historical sanitized text for a canceled direct helper
	// call, but do not mis-project cancellation as a 30-second gauge deadline.
	return errors.New("T40.13 data-byte measurement exceeded its deadline")
}

func dataMeasurementDeadlineCause(err error) *dataMeasurementDeadlineError {
	var deadline *dataMeasurementDeadlineError
	if !errors.As(err, &deadline) ||
		(deadline.gauge != dataMeasurementAllocated && deadline.gauge != dataMeasurementLogical) {
		return nil
	}
	return deadline
}

// projectDataMeasurementDeadline returns the fixed source-free public
// projection of a typed custody gauge deadline. It deliberately carries no
// measured path, command, output, or raw error text.
func projectDataMeasurementDeadline(err error) *DataMeasurementFailureObservation {
	deadline := dataMeasurementDeadlineCause(err)
	if deadline == nil {
		return nil
	}
	return &DataMeasurementFailureObservation{
		Schema: dataMeasurementFailureSchemaV1, Scope: dataMeasurementScope,
		Gauge: deadline.gauge, Reason: dataMeasurementDeadline,
		DeadlineMS: frozenDataMeasurementDeadlineMS,
	}
}

func newDataMeasurementBoundary(workspace string, allocated int64) (*dataMeasurementBoundary, error) {
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || allocated < 0 {
		return nil, errors.New("T40.13 data-measurement boundary is invalid")
	}
	return &dataMeasurementBoundary{workspace: workspace, allocated: allocated}, nil
}

func (boundary *dataMeasurementBoundary) consume(workspace string) (int64, error) {
	if boundary == nil || boundary.consumed || !filepath.IsAbs(workspace) ||
		filepath.Clean(workspace) != workspace || boundary.workspace != workspace {
		return 0, errors.New("T40.13 data-measurement boundary handoff is invalid")
	}
	boundary.consumed = true
	return boundary.allocated, nil
}

func (meter *phaseMeter) takeRawEndBoundary() (*dataMeasurementBoundary, error) {
	if meter == nil || meter.rawEnd == nil {
		return nil, errors.New("T40.13 successful phase meter lacks its raw data boundary")
	}
	boundary := meter.rawEnd
	meter.rawEnd = nil
	return boundary, nil
}

type allocationSampler struct {
	root              string
	strict            bool
	baselineAllocated int64
	baselineAvailable int64
	minimumAvailable  int64
	stop              chan struct{}
	done              chan struct{}
	mu                sync.Mutex
	err               error
	failedSamples     uint64
	closeOnce         sync.Once
	peak              int64
	closeErr          error
}

var errAllocationSamplingFailed = errors.New("T40.13 allocation sampling failed")

func retainedMeasurementFailure(err error) error {
	var retained error
	if errors.Is(err, errProcessSamplingFailed) {
		retained = errors.Join(retained, errProcessSamplingFailed)
	}
	if errors.Is(err, errAllocationSamplingFailed) {
		retained = errors.Join(retained, errAllocationSamplingFailed)
	}
	return retained
}

func retainedMeasuredCommandFailure(err error) error {
	retained := retainedMeasurementFailure(err)
	if errors.Is(err, errPrivateServerShutdownUnproven) {
		retained = errors.Join(retained, errPrivateServerShutdownUnproven)
	}
	return retained
}

func sanitizeMeasuredCommandFailure(message string, err error, retainDataMeasurement bool) error {
	retained := retainedMeasuredCommandFailure(err)
	if retainDataMeasurement {
		retained = errors.Join(retained, dataMeasurementDeadlineCause(err))
	}
	return errors.Join(errors.New(message), retained)
}

func newAllocationSampler(root string, baselineAllocated int64, strict bool) (*allocationSampler, error) {
	capacity, err := lifecycle.ProbeCapacity(context.Background(), root)
	if err != nil {
		return nil, err
	}
	sampler := &allocationSampler{
		root: root, strict: strict, baselineAllocated: baselineAllocated,
		baselineAvailable: capacity.AvailableBytes, minimumAvailable: capacity.AvailableBytes,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go sampler.run()
	return sampler, nil
}

func (sampler *allocationSampler) run() {
	defer close(sampler.done)
	ticker := time.NewTicker(allocationSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-sampler.stop:
			return
		case <-ticker.C:
			sampler.sample()
		}
	}
}

func (sampler *allocationSampler) sample() {
	capacity, err := lifecycle.ProbeCapacity(context.Background(), sampler.root)
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	if err != nil {
		sampler.recordFailureLocked(err)
	} else if capacity.AvailableBytes < sampler.minimumAvailable {
		sampler.minimumAvailable = capacity.AvailableBytes
	}
}

func (sampler *allocationSampler) recordFailure(err error) {
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	sampler.recordFailureLocked(err)
}

func (sampler *allocationSampler) recordFailureLocked(err error) {
	if !sampler.strict {
		sampler.err = errors.Join(sampler.err, err)
		return
	}
	if sampler.err == nil {
		sampler.err = err
	}
	if sampler.failedSamples < ^uint64(0) {
		sampler.failedSamples++
	}
}

func (sampler *allocationSampler) close() (int64, error) {
	if sampler == nil {
		return 0, errors.New("T40.13 allocation sampler is invalid")
	}
	sampler.closeOnce.Do(func() {
		close(sampler.stop)
		<-sampler.done
		sampler.sample()
		sampler.mu.Lock()
		defer sampler.mu.Unlock()
		consumed := sampler.baselineAvailable - sampler.minimumAvailable
		if consumed < 0 {
			consumed = 0
		}
		if consumed > 1<<63-1-sampler.baselineAllocated {
			sampler.closeErr = errors.New("T40.13 allocation sampler overflowed")
			return
		}
		sampler.peak = sampler.baselineAllocated + consumed
		if sampler.err != nil {
			sampler.closeErr = sampler.err
			if sampler.strict {
				sampler.closeErr = fmt.Errorf("%w after %d failed samples: %w",
					errAllocationSamplingFailed, sampler.failedSamples, sampler.err)
			}
		}
	})
	return sampler.peak, sampler.closeErr
}

func beginPhaseMeter(server *privateServer, dataDir string, before *privateProfileSnapshot) (*phaseMeter, error) {
	if server == nil || server.log == nil {
		return nil, errors.New("T40.13 phase meter requires a running server")
	}
	offset, err := server.log.Seek(0, 1)
	if err != nil {
		return nil, err
	}
	_, allocated, err := measureDataBytesForContract(dataDir, server.sessionIsolated)
	if err != nil {
		return nil, err
	}
	allocation, err := newAllocationSampler(dataDir, allocated, server.sessionIsolated)
	if err != nil {
		return nil, err
	}
	server.sampler.resetWindow()
	return &phaseMeter{
		started: time.Now(), server: server, dataDir: dataDir, logOffset: offset,
		before: before, allocation: allocation, strict: server.sessionIsolated,
	}, nil
}

func beginInitialPhaseMeter(server *privateServer, dataDir string, before *privateProfileSnapshot) (*phaseMeter, error) {
	if server == nil || server.log == nil || server.started.IsZero() {
		return nil, errors.New("T40.13 initial phase meter requires a running server")
	}
	_, allocated, err := measureDataBytesForContract(dataDir, server.sessionIsolated)
	if err != nil {
		return nil, err
	}
	allocation, err := newAllocationSampler(dataDir, allocated, server.sessionIsolated)
	if err != nil {
		return nil, err
	}
	return &phaseMeter{
		started: server.started, server: server, dataDir: dataDir, logOffset: 0, before: before,
		allocation: allocation, strict: server.sessionIsolated,
	}, nil
}

// runMeasuredCommand accounts for a recovery command rooted outside the
// measured server process tree. The root command is counted as one other child;
// descendants retain the same closed Git/index/other classification used by a
// server meter.
func runMeasuredCommand(command *exec.Cmd, dataDir string, strict bool) (PhaseMetrics, error) {
	if command == nil || !filepath.IsAbs(dataDir) {
		return PhaseMetrics{}, errors.New("T40.13 measured command is invalid")
	}
	_, allocatedBefore, err := measureDataBytesForContract(dataDir, strict)
	if err != nil {
		return PhaseMetrics{}, err
	}
	allocation, err := newAllocationSampler(dataDir, allocatedBefore, strict)
	if err != nil {
		return PhaseMetrics{}, err
	}
	if strict {
		if err := isolatePrivateServerSession(command); err != nil {
			_, allocationErr := allocation.close()
			return PhaseMetrics{}, errors.Join(err, allocationErr)
		}
	}
	started := time.Now()
	if err := command.Start(); err != nil {
		_, allocationErr := allocation.close()
		return PhaseMetrics{}, errors.Join(err, allocationErr)
	}
	sampler := newRSSSampler(command.Process.Pid, strict)
	if strict {
		sampler.captureRootIdentity()
		sampler.sample()
		sampler.expectConcurrentRootWait()
	} else {
		sampler.sample()
	}
	go sampler.run()
	waitErr := command.Wait()
	sampler.observeRootExit()
	var sessionErr error
	if strict {
		sessionErr = finishCustodyCommandSession(command.Process.Pid)
	}
	_ = sampler.close()
	logical, allocated, measureErr := measureDataBytesForContract(dataDir, strict)
	peakAllocated, allocationErr := allocation.close()
	allocated = max(allocated, peakAllocated)
	metrics := PhaseMetrics{
		WallMS: time.Since(started).Milliseconds(), DataLogicalBytes: logical,
		DataAllocatedBytes: allocated, OtherChildren: 1,
	}
	processMetrics, samplerErr := sampler.phaseMetrics()
	metrics, mergeErr := mergeMetrics(metrics, processMetrics)
	var shutdownErr error
	if strict {
		shutdownErr = signaledCommandShutdownUnproven(waitErr)
	}
	return metrics, errors.Join(
		waitErr, shutdownErr, sessionErr, samplerErr, mergeErr, measureErr, allocationErr,
	)
}

func (meter *phaseMeter) finish(after *privateProfileSnapshot) (PhaseMetrics, error) {
	if meter == nil || meter.server == nil {
		return PhaseMetrics{}, errors.New("T40.13 phase meter is invalid")
	}
	logical, allocated, measureErr := measureDataBytesForContract(meter.dataDir, meter.strict)
	peakAllocated, allocationErr := meter.allocation.close()
	if measureErr != nil || allocationErr != nil {
		return PhaseMetrics{}, errors.Join(measureErr, allocationErr)
	}
	rawAllocated := allocated
	allocated = max(allocated, peakAllocated)
	metrics := PhaseMetrics{
		WallMS:           time.Since(meter.started).Milliseconds(),
		DataLogicalBytes: logical, DataAllocatedBytes: allocated,
	}
	processMetrics, samplerErr := meter.server.sampler.phaseMetrics()
	metrics, mergeErr := mergeMetrics(metrics, processMetrics)
	if samplerErr != nil || mergeErr != nil {
		return PhaseMetrics{}, errors.Join(samplerErr, mergeErr)
	}
	logMetrics, err := parseLogMetrics(meter.server.logPath, meter.logOffset)
	if err != nil {
		return PhaseMetrics{}, err
	}
	metrics.OrchestrationTransactions = logMetrics.OrchestrationTransactions
	metrics.Retries = logMetrics.Retries
	metrics.ReusedControls = logMetrics.ReusedControls
	metrics.ReusedMembers = logMetrics.ReusedMembers
	if after != nil {
		published, err := checkedMulInt64(int64(after.PublishedDomains), 3)
		if err != nil {
			return PhaseMetrics{}, err
		}
		metrics.ControlReads, err = checkedSumInt64(7, published, int64(after.ApplicablePartitions))
		if err != nil {
			return PhaseMetrics{}, err
		}
		metrics.MemberReads, err = checkedSumInt64(int64(after.BlobReader.FallbackReads), int64(after.SettledPartitions))
		if err != nil {
			return PhaseMetrics{}, err
		}
	}
	if after != nil {
		before := privateProfileSnapshot{}
		if meter.before != nil {
			before = *meter.before
		}
		metrics.PublicationTransactions = authorityChanges(before, *after)
		metrics.PublicationWrites = metrics.PublicationTransactions
	}
	if meter.captureRaw {
		boundary, err := newDataMeasurementBoundary(meter.dataDir, rawAllocated)
		if err != nil {
			return PhaseMetrics{}, err
		}
		meter.rawEnd = boundary
	}
	return metrics, nil
}

func authorityChanges(before, after privateProfileSnapshot) int64 {
	left := []string{
		before.SourceGeneration, before.SearchGeneration, before.ObservationGeneration,
		before.ExtractionGeneration, before.CallerGeneration,
		before.RelationshipGeneration, before.RelationshipRootDigest,
	}
	right := []string{
		after.SourceGeneration, after.SearchGeneration, after.ObservationGeneration,
		after.ExtractionGeneration, after.CallerGeneration,
		after.RelationshipGeneration, after.RelationshipRootDigest,
	}
	var changed int64
	for index := range left {
		if left[index] != right[index] {
			changed++
		}
	}
	return changed
}

func measureDataBytes(path string) (logical, allocated int64, err error) {
	if !filepath.IsAbs(path) {
		return 0, 0, errors.New("T40.13 data measurement path is invalid")
	}
	allocated, err = duKilobytes(path, false)
	if err != nil {
		return 0, 0, err
	}
	logical, err = duKilobytes(path, true)
	if err != nil {
		return 0, 0, err
	}
	logical, err = checkedMulInt64(logical, 1024)
	if err != nil {
		return 0, 0, err
	}
	allocated, err = checkedMulInt64(allocated, 1024)
	if err != nil {
		return 0, 0, err
	}
	return logical, allocated, nil
}

func measureDataBytesForPlan(plan Plan, path string) (logical, allocated int64, err error) {
	return measureDataBytesForContract(path, planSchemaVersion(plan.Schema) >= 25)
}

func measureDataBytesForContract(path string, strict bool) (logical, allocated int64, err error) {
	if !strict {
		return measureDataBytes(path)
	}
	return measureDataBytesContext(context.Background(), path)
}

func measureDataAllocatedBytesForContract(path string, strict bool) (int64, error) {
	if !filepath.IsAbs(path) {
		return 0, errors.New("T40.13 data measurement path is invalid")
	}
	var (
		allocated int64
		err       error
	)
	if strict {
		allocated, err = duKilobytesWithin(context.Background(), path, false)
	} else {
		allocated, err = duKilobytes(path, false)
	}
	if err != nil {
		return 0, err
	}
	return checkedMulInt64(allocated, 1024)
}

func measureDataBytesContext(ctx context.Context, path string) (logical, allocated int64, err error) {
	if ctx == nil || !filepath.IsAbs(path) {
		return 0, 0, errors.New("T40.13 data measurement path is invalid")
	}
	allocated, err = duKilobytesWithin(ctx, path, false)
	if err != nil {
		return 0, 0, err
	}
	logical, err = duKilobytesWithin(ctx, path, true)
	if err != nil {
		return 0, 0, err
	}
	logical, err = checkedMulInt64(logical, 1024)
	if err != nil {
		return 0, 0, err
	}
	allocated, err = checkedMulInt64(allocated, 1024)
	if err != nil {
		return 0, 0, err
	}
	return logical, allocated, nil
}

func duKilobytesWithin(parent context.Context, path string, apparent bool) (int64, error) {
	ctx, cancel := context.WithTimeout(parent, dataMeasurementTimeout)
	defer cancel()
	return duKilobytesContext(ctx, path, apparent)
}

func duKilobytes(path string, apparent bool) (int64, error) {
	args := []string{"-sk"}
	if apparent {
		switch runtime.GOOS {
		case "darwin":
			args = []string{"-skA"}
		case "linux":
			args = []string{"-sk", "--apparent-size"}
		default:
			return 0, errors.New("T40.13 logical-byte measurement is unsupported")
		}
	}
	args = append(args, path)
	var output []byte
	var err error
	for attempt := range 3 {
		output, err = exec.Command("du", args...).Output()
		if err == nil {
			break
		}
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err != nil {
		return 0, errors.New("T40.13 data-byte measurement failed")
	}
	return parseDUKilobytes(output)
}

func duKilobytesContext(ctx context.Context, path string, apparent bool) (int64, error) {
	args := []string{"-sk"}
	if apparent {
		switch runtime.GOOS {
		case "darwin":
			args = []string{"-skA"}
		case "linux":
			args = []string{"-sk", "--apparent-size"}
		default:
			return 0, errors.New("T40.13 logical-byte measurement is unsupported")
		}
	}
	args = append(args, path)
	var output []byte
	var err error
	for attempt := range 3 {
		output, err = exec.CommandContext(ctx, "/usr/bin/du", args...).Output()
		if err == nil {
			break
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return 0, dataMeasurementContextError(ctx, apparent)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if ctx.Err() != nil {
		return 0, dataMeasurementContextError(ctx, apparent)
	}
	if err != nil {
		return 0, errors.New("T40.13 data-byte measurement failed")
	}
	return parseDUKilobytes(output)
}

func parseDUKilobytes(output []byte) (int64, error) {
	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return 0, errors.New("T40.13 data-byte measurement is invalid")
	}
	value, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || value < 0 || value > (1<<63-1)/1024 {
		return 0, errors.New("T40.13 data-byte measurement is invalid")
	}
	return value, nil
}

func parseLogMetrics(path string, offset int64) (PhaseMetrics, error) {
	file, err := os.Open(path)
	if err != nil {
		return PhaseMetrics{}, err
	}
	if _, err := file.Seek(offset, 0); err != nil {
		_ = file.Close()
		return PhaseMetrics{}, err
	}
	var result PhaseMetrics
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		switch {
		case bytes.Contains(line, []byte("job lifecycle: ")):
			var report store.JobLifecycleReport
			if decodeLogObject(line, "job lifecycle: ", &report) == nil && report.Schema == store.JobLifecycleSchema {
				if report.Event != "started" {
					result.OrchestrationTransactions++
				}
				if report.Event == "requeued" || report.Outcome == "retryable" {
					result.Retries++
				}
			}
		case bytes.Contains(line, []byte("candidate operation: ")):
			var report candidatejob.CandidateOperationReport
			if decodeLogObject(line, "candidate operation: ", &report) == nil && report.Schema == candidatejob.CandidateOperationSchema {
				switch report.Decision {
				case "warm_noop":
					result.ReusedControls++
				case "cold_reuse", "marker_recovery":
					result.ReusedControls++
					members, addErr := checkedSumInt64(int64(report.Planes.Repository.Members), int64(report.Planes.Local.Members), int64(report.Planes.Caller.Members))
					if addErr != nil {
						return result, addErr
					}
					result.ReusedMembers, addErr = checkedAddInt64(result.ReusedMembers, members)
					if addErr != nil {
						return result, addErr
					}
				}
			}
		case bytes.Contains(line, []byte("extraction operation: ")):
			var report extract.ExtractionOperationReport
			if decodeLogObject(line, "extraction operation: ", &report) == nil && report.Schema == extract.ExtractionOperationSchema {
				for _, domain := range report.Domains {
					if domain.Reason == extract.OperationReasonAlreadyCurrent {
						result.ReusedControls++
					}
				}
			}
		}
	}
	return result, errors.Join(scanner.Err(), file.Close())
}

func decodeLogObject(line []byte, marker string, target any) error {
	index := bytes.Index(line, []byte(marker))
	if index < 0 {
		return errors.New("log marker absent")
	}
	raw := line[index+len(marker):]
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func phaseContext(parent context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	if limit <= 0 {
		limit = 30 * time.Minute
	}
	return context.WithTimeout(parent, limit)
}
