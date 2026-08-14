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
) {
	if observation == nil || report.Event != "settled" {
		return
	}
	observation.SchedulerSettled++
	observation.SchedulerTotalMS += report.DurationMS
	observation.SchedulerMaxMS = max(observation.SchedulerMaxMS, report.DurationMS)
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
) {
	if observation == nil {
		return
	}
	if report.Schema == extractionpublication.PartitionTimingSchemaV2 &&
		observation.Schema == "" {
		// A mixed-version log remains exact: any earlier v1 terminal attempts
		// become explicitly unknown when the first v2 report appears.
		observation.Schema = extractionTimingSchemaV2
		observation.UnknownRefusals = observation.TerminalRefusals
	}
	observation.Attempts++
	switch report.Outcome {
	case "completed":
		observation.Completed++
	case "terminal_refusal":
		observation.TerminalRefusals++
		addPartitionRefusal(observation, report)
	default:
		observation.Failed++
	}
	if report.Reused {
		observation.Reused++
	}
	observation.SourceAcquireTotalMS += report.SourceAcquireMS
	observation.SourceAcquireMaxMS = max(observation.SourceAcquireMaxMS, report.SourceAcquireMS)
	observation.ExecutorTotalMS += report.ExecutorMS
	observation.ExecutorMaxMS = max(observation.ExecutorMaxMS, report.ExecutorMS)
	observation.ResultTotalMS += report.ResultMS
	observation.ResultMaxMS = max(observation.ResultMaxMS, report.ResultMS)
	observation.AssemblyTotalMS += report.AssemblyMS
	observation.AssemblyMaxMS = max(observation.AssemblyMaxMS, report.AssemblyMS)
	observation.RuntimeTotalMS += report.TotalMS
	observation.RuntimeMaxMS = max(observation.RuntimeMaxMS, report.TotalMS)
}

func addPartitionRefusal(
	observation *ExtractionTimingObservation,
	report extractionpublication.PartitionTimingReport,
) {
	if observation == nil || report.Schema != extractionpublication.PartitionTimingSchemaV2 {
		if observation != nil && observation.Schema == extractionTimingSchemaV2 {
			observation.UnknownRefusals++
		}
		return
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
		observation.UnknownRefusals++
		return
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
		observation.Refusals[index].Count++
		observation.Refusals[index].MaxObserved = max(
			observation.Refusals[index].MaxObserved, summary.MaxObserved,
		)
		return
	}
	if len(observation.Refusals) >= maxExtractionRefusalSummaries {
		observation.UnknownRefusals++
		return
	}
	observation.Refusals = append(observation.Refusals, summary)
	sort.Slice(observation.Refusals, func(left, right int) bool {
		return extractionRefusalSummaryKey(observation.Refusals[left]) <
			extractionRefusalSummaryKey(observation.Refusals[right])
	})
}
