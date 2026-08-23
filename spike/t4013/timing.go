package t4013

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/generationscheduler"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
)

const maxPartitionTimingLineBytes = 64 << 10

type partitionTimingCursor struct {
	file    *os.File
	reader  *bufio.Reader
	pending []byte
}

func addSchedulerTiming(
	observation *ExtractionTimingObservation,
	report generationscheduler.ChunkLifecycleReport,
) error {
	if observation == nil || report.Event != "settled" {
		return nil
	}
	var err error
	observation.SchedulerSettled, err = checkedAddInt64(observation.SchedulerSettled, 1)
	if err != nil {
		return errors.New("T40.13 scheduler timing count overflowed")
	}
	observation.SchedulerTotalMS, err = checkedAddInt64(observation.SchedulerTotalMS, report.DurationMS)
	if err != nil {
		return errors.New("T40.13 scheduler timing aggregate overflowed")
	}
	observation.SchedulerMaxMS = max(observation.SchedulerMaxMS, report.DurationMS)
	return nil
}

func newPartitionTimingCursor(logPath string) (*partitionTimingCursor, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	return &partitionTimingCursor{
		file: file, reader: bufio.NewReaderSize(file, maxPartitionTimingLineBytes),
	}, nil
}

func (cursor *partitionTimingCursor) Close() error {
	if cursor == nil || cursor.file == nil {
		return nil
	}
	err := cursor.file.Close()
	cursor.file = nil
	return err
}

func (cursor *partitionTimingCursor) poll() ([]extractionpublication.PartitionTimingReport, error) {
	if cursor == nil || cursor.file == nil || cursor.reader == nil {
		return nil, errors.New("T40.13 partition timing cursor is invalid")
	}
	reports := make([]extractionpublication.PartitionTimingReport, 0, 8)
	for {
		line, err := cursor.reader.ReadBytes('\n')
		if len(line) > 0 {
			cursor.pending = append(cursor.pending, line...)
			if len(cursor.pending) > maxPartitionTimingLineBytes {
				return nil, errors.New("T40.13 partition timing line exceeds its bound")
			}
			if cursor.pending[len(cursor.pending)-1] == '\n' {
				report, found, parseErr := parsePartitionTimingLine(bytes.TrimSuffix(cursor.pending, []byte{'\n'}))
				cursor.pending = cursor.pending[:0]
				if parseErr != nil {
					return nil, parseErr
				}
				if found {
					reports = append(reports, report)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return reports, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func parsePartitionTimingLine(
	line []byte,
) (extractionpublication.PartitionTimingReport, bool, error) {
	const prefix = "extraction partition timing: "
	if !bytes.Contains(line, []byte(prefix)) {
		return extractionpublication.PartitionTimingReport{}, false, nil
	}
	var report extractionpublication.PartitionTimingReport
	if err := decodeLogObject(line, prefix, &report); err != nil ||
		extractionpublication.ValidatePartitionTimingReport(report) != nil {
		return extractionpublication.PartitionTimingReport{}, false,
			errors.New("T40.13 partition timing report is malformed")
	}
	return report, true, nil
}

func addPartitionTiming(
	observation *ExtractionTimingObservation,
	report extractionpublication.PartitionTimingReport,
) error {
	if observation == nil {
		return nil
	}
	if report.Schema == extractionpublication.PartitionTimingSchemaV3 &&
		observation.Schema != extractionTimingSchemaV3 {
		upgradeExtractionTimingV3(observation)
	} else if report.Schema == extractionpublication.PartitionTimingSchemaV2 &&
		observation.Schema == "" {
		// A mixed-version log remains exact: any earlier v1 terminal attempts
		// become explicitly unknown when the first v2 report appears.
		observation.Schema = extractionTimingSchemaV2
		observation.UnknownRefusals = observation.TerminalRefusals
	}
	var err error
	observation.Attempts, err = checkedAddInt64(observation.Attempts, 1)
	if err != nil {
		return errors.New("T40.13 extraction timing attempt count overflowed")
	}
	switch report.Outcome {
	case "completed":
		observation.Completed, err = checkedAddInt64(observation.Completed, 1)
	case "terminal_refusal":
		observation.TerminalRefusals, err = checkedAddInt64(observation.TerminalRefusals, 1)
		if err == nil {
			err = addPartitionRefusal(observation, report)
		}
	default:
		observation.Failed, err = checkedAddInt64(observation.Failed, 1)
	}
	if err != nil {
		return errors.New("T40.13 extraction timing count overflowed")
	}
	if report.Reused {
		observation.Reused, err = checkedAddInt64(observation.Reused, 1)
		if err != nil {
			return errors.New("T40.13 extraction timing reuse count overflowed")
		}
	}
	observation.SourceAcquireTotalMS, err = checkedAddInt64(observation.SourceAcquireTotalMS, report.SourceAcquireMS)
	if err != nil {
		return errors.New("T40.13 extraction timing aggregate overflowed")
	}
	observation.SourceAcquireMaxMS = max(observation.SourceAcquireMaxMS, report.SourceAcquireMS)
	observation.ExecutorTotalMS, err = checkedAddInt64(observation.ExecutorTotalMS, report.ExecutorMS)
	if err != nil {
		return errors.New("T40.13 extraction timing aggregate overflowed")
	}
	observation.ExecutorMaxMS = max(observation.ExecutorMaxMS, report.ExecutorMS)
	observation.ResultTotalMS, err = checkedAddInt64(observation.ResultTotalMS, report.ResultMS)
	if err != nil {
		return errors.New("T40.13 extraction timing aggregate overflowed")
	}
	observation.ResultMaxMS = max(observation.ResultMaxMS, report.ResultMS)
	observation.AssemblyTotalMS, err = checkedAddInt64(observation.AssemblyTotalMS, report.AssemblyMS)
	if err != nil {
		return errors.New("T40.13 extraction timing aggregate overflowed")
	}
	observation.AssemblyMaxMS = max(observation.AssemblyMaxMS, report.AssemblyMS)
	observation.RuntimeTotalMS, err = checkedAddInt64(observation.RuntimeTotalMS, report.TotalMS)
	if err != nil {
		return errors.New("T40.13 extraction timing aggregate overflowed")
	}
	observation.RuntimeMaxMS = max(observation.RuntimeMaxMS, report.TotalMS)
	if observation.Schema == extractionTimingSchemaV3 {
		if err := addExtractionDomainTiming(observation, report); err != nil {
			return err
		}
	}
	return nil
}

func upgradeExtractionTimingV3(observation *ExtractionTimingObservation) {
	if observation == nil || observation.Schema == extractionTimingSchemaV3 {
		return
	}
	if observation.Attempts > 0 {
		observation.DomainTimings = []ExtractionDomainTiming{{
			Domain: "unknown", Attempts: observation.Attempts,
			Completed: observation.Completed, Failed: observation.Failed,
			TerminalRefusals: observation.TerminalRefusals, Reused: observation.Reused,
			UnknownFailures: observation.Failed, ExecutorUnbucketed: observation.Attempts,
			ExecutorTotalMS: observation.ExecutorTotalMS, ExecutorMaxMS: observation.ExecutorMaxMS,
		}}
	}
	if observation.Schema == "" {
		observation.UnknownRefusals = observation.TerminalRefusals
	}
	observation.Schema = extractionTimingSchemaV3
}

func addExtractionDomainTiming(
	observation *ExtractionTimingObservation,
	report extractionpublication.PartitionTimingReport,
) error {
	if observation == nil {
		return nil
	}
	domain := report.Domain
	if report.Schema != extractionpublication.PartitionTimingSchemaV3 {
		domain = "unknown"
	}
	index := -1
	for current := range observation.DomainTimings {
		if observation.DomainTimings[current].Domain == domain {
			index = current
			break
		}
	}
	if index < 0 {
		if len(observation.DomainTimings) >= maxExtractionTimingDomains {
			domain = "unknown"
			for current := range observation.DomainTimings {
				if observation.DomainTimings[current].Domain == domain {
					index = current
					break
				}
			}
		}
		if index < 0 {
			observation.DomainTimings = append(observation.DomainTimings, ExtractionDomainTiming{Domain: domain})
			index = len(observation.DomainTimings) - 1
		}
	}
	current := &observation.DomainTimings[index]
	var err error
	current.Attempts, err = checkedAddInt64(current.Attempts, 1)
	if err != nil {
		return errors.New("T40.13 extraction domain attempt count overflowed")
	}
	switch report.Outcome {
	case "completed":
		current.Completed, err = checkedAddInt64(current.Completed, 1)
	case "terminal_refusal":
		current.TerminalRefusals, err = checkedAddInt64(current.TerminalRefusals, 1)
	default:
		current.Failed, err = checkedAddInt64(current.Failed, 1)
		if err != nil {
			return errors.New("T40.13 extraction domain count overflowed")
		}
		switch report.FailureClass {
		case extractionpublication.PartitionFailureDeadline:
			current.DeadlineFailures, err = checkedAddInt64(current.DeadlineFailures, 1)
		case extractionpublication.PartitionFailureCanceled:
			current.CanceledFailures, err = checkedAddInt64(current.CanceledFailures, 1)
		case extractionpublication.PartitionFailureOther:
			current.OtherFailures, err = checkedAddInt64(current.OtherFailures, 1)
		default:
			current.UnknownFailures, err = checkedAddInt64(current.UnknownFailures, 1)
		}
	}
	if err != nil {
		return errors.New("T40.13 extraction domain count overflowed")
	}
	if report.Reused {
		current.Reused, err = checkedAddInt64(current.Reused, 1)
		if err != nil {
			return errors.New("T40.13 extraction domain reuse count overflowed")
		}
	}
	switch {
	case report.ExecutorMS < 1_000:
		current.ExecutorLT1S++
	case report.ExecutorMS < 10_000:
		current.ExecutorLT10S++
	case report.ExecutorMS < 60_000:
		current.ExecutorLT60S++
	case report.ExecutorMS < 240_000:
		current.ExecutorLT240S++
	case report.ExecutorMS < 300_000:
		current.ExecutorLT300S++
	default:
		current.ExecutorGE300S++
	}
	current.ExecutorTotalMS, err = checkedAddInt64(current.ExecutorTotalMS, report.ExecutorMS)
	if err != nil {
		return errors.New("T40.13 extraction domain timing aggregate overflowed")
	}
	current.ExecutorMaxMS = max(current.ExecutorMaxMS, report.ExecutorMS)
	sort.Slice(observation.DomainTimings, func(left, right int) bool {
		return observation.DomainTimings[left].Domain < observation.DomainTimings[right].Domain
	})
	return nil
}

func addPartitionRefusal(
	observation *ExtractionTimingObservation,
	report extractionpublication.PartitionTimingReport,
) error {
	if observation == nil || (report.Schema != extractionpublication.PartitionTimingSchemaV2 &&
		report.Schema != extractionpublication.PartitionTimingSchemaV3) {
		if observation != nil && (observation.Schema == extractionTimingSchemaV2 ||
			observation.Schema == extractionTimingSchemaV3) {
			updated, err := checkedAddInt64(observation.UnknownRefusals, 1)
			if err != nil {
				return errors.New("T40.13 unknown refusal count overflowed")
			}
			observation.UnknownRefusals = updated
		}
		return nil
	}
	receipt := pipelinerefusal.Receipt{
		Schema: pipelinerefusal.Schema, Stage: report.RefusalStage,
		GenerationKind: report.RefusalGenerationKind,
		Classification: report.RefusalClassification,
		Dimension:      report.RefusalDimension, Observed: report.RefusalObserved,
		Limit: report.RefusalLimit,
	}
	if pipelinerefusal.Validate(receipt) != nil ||
		receipt.Classification == pipelinerefusal.ClassificationUnknown {
		updated, err := checkedAddInt64(observation.UnknownRefusals, 1)
		if err != nil {
			return errors.New("T40.13 unknown refusal count overflowed")
		}
		observation.UnknownRefusals = updated
		return nil
	}
	summary := ExtractionRefusalSummary{
		Stage: string(receipt.Stage), GenerationKind: string(receipt.GenerationKind),
		Classification: string(receipt.Classification), Dimension: string(receipt.Dimension),
		Limit: receipt.Limit, MaxObserved: receipt.Observed, Count: 1,
	}
	key := extractionRefusalSummaryKey(summary)
	for index := range observation.Refusals {
		if extractionRefusalSummaryKey(observation.Refusals[index]) != key {
			continue
		}
		updated, err := checkedAddInt64(observation.Refusals[index].Count, 1)
		if err != nil {
			return errors.New("T40.13 refusal count overflowed")
		}
		observation.Refusals[index].Count = updated
		observation.Refusals[index].MaxObserved = max(
			observation.Refusals[index].MaxObserved, summary.MaxObserved,
		)
		return nil
	}
	if len(observation.Refusals) >= maxExtractionRefusalSummaries {
		updated, err := checkedAddInt64(observation.UnknownRefusals, 1)
		if err != nil {
			return errors.New("T40.13 unknown refusal count overflowed")
		}
		observation.UnknownRefusals = updated
		return nil
	}
	observation.Refusals = append(observation.Refusals, summary)
	sort.Slice(observation.Refusals, func(left, right int) bool {
		return extractionRefusalSummaryKey(observation.Refusals[left]) <
			extractionRefusalSummaryKey(observation.Refusals[right])
	})
	return nil
}
