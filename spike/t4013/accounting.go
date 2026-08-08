package t4013

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

type phaseMeter struct {
	started   time.Time
	server    *privateServer
	dataDir   string
	logOffset int64
	before    *privateProfileSnapshot
}

func beginPhaseMeter(server *privateServer, dataDir string, before *privateProfileSnapshot) (*phaseMeter, error) {
	if server == nil || server.log == nil {
		return nil, errors.New("T40.13 phase meter requires a running server")
	}
	offset, err := server.log.Seek(0, 1)
	if err != nil {
		return nil, err
	}
	if _, _, err := measureDataBytes(dataDir); err != nil {
		return nil, err
	}
	server.sampler.resetWindow()
	return &phaseMeter{
		started: time.Now(), server: server, dataDir: dataDir, logOffset: offset,
		before: before,
	}, nil
}

func beginInitialPhaseMeter(server *privateServer, dataDir string, before *privateProfileSnapshot) (*phaseMeter, error) {
	if server == nil || server.log == nil || server.started.IsZero() {
		return nil, errors.New("T40.13 initial phase meter requires a running server")
	}
	if _, _, err := measureDataBytes(dataDir); err != nil {
		return nil, err
	}
	return &phaseMeter{
		started: server.started, server: server, dataDir: dataDir, logOffset: 0, before: before,
	}, nil
}

func (meter *phaseMeter) finish(after *privateProfileSnapshot) (PhaseMetrics, error) {
	if meter == nil || meter.server == nil {
		return PhaseMetrics{}, errors.New("T40.13 phase meter is invalid")
	}
	logical, allocated, err := measureDataBytes(meter.dataDir)
	if err != nil {
		return PhaseMetrics{}, err
	}
	metrics := PhaseMetrics{
		WallMS:           time.Since(meter.started).Milliseconds(),
		DataLogicalBytes: logical, DataAllocatedBytes: allocated,
	}
	metrics.PeakRSSBytes, metrics.GitChildren, metrics.IndexChildren, metrics.OtherChildren = meter.server.sampler.metrics()
	logMetrics, err := parseLogMetrics(meter.server.logPath, meter.logOffset)
	if err != nil {
		return PhaseMetrics{}, err
	}
	metrics.OrchestrationTransactions = logMetrics.OrchestrationTransactions
	metrics.Retries = logMetrics.Retries
	metrics.ReusedControls = logMetrics.ReusedControls
	metrics.ReusedMembers = logMetrics.ReusedMembers
	if after != nil {
		metrics.ControlReads = int64(7 + after.PublishedDomains*3 + after.ApplicablePartitions)
		metrics.MemberReads = int64(after.BlobReader.FallbackReads) + int64(after.SettledPartitions)
	}
	if after != nil {
		before := privateProfileSnapshot{}
		if meter.before != nil {
			before = *meter.before
		}
		metrics.PublicationTransactions = authorityChanges(before, *after)
		metrics.PublicationWrites = metrics.PublicationTransactions
	}
	return metrics, nil
}

func authorityChanges(before, after privateProfileSnapshot) int64 {
	left := []string{
		before.SourceGeneration, before.SearchGeneration, before.ObservationGeneration,
		before.ExtractionGeneration, before.RelationshipGeneration, before.RelationshipRootDigest,
	}
	right := []string{
		after.SourceGeneration, after.SearchGeneration, after.ObservationGeneration,
		after.ExtractionGeneration, after.RelationshipGeneration, after.RelationshipRootDigest,
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
	return logical * 1024, allocated * 1024, nil
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
	command := exec.Command("du", args...)
	output, err := command.Output()
	if err != nil {
		return 0, errors.New("T40.13 data-byte measurement failed")
	}
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
					result.ReusedMembers += int64(report.Planes.Repository.Members + report.Planes.Local.Members + report.Planes.Caller.Members)
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
