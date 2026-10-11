package extract

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

// Receipts are external run custody, never product authority or retention roots.
type partitionSourceOutcomes struct {
	file          *os.File
	encoder       *json.Encoder
	corpus        *verifiedCorpus
	expected      map[string]struct{}
	expectedCount int
	seen          map[string]struct{}
}

const sourceOutcomeReceiptBytes = 8 << 20

type sourceOutcomeWriter struct {
	file      *os.File
	remaining int
}

func (writer *sourceOutcomeWriter) Write(data []byte) (int, error) {
	if len(data) > writer.remaining {
		return 0, errors.New("source outcome receipt exceeds byte bound")
	}
	n, err := writer.file.Write(data)
	writer.remaining -= n
	return n, err
}

func beginPartitionSourceOutcomes(root string, corpus *verifiedCorpus, plan candidate.DomainResultPlan, ordinal int, runID string) (*partitionSourceOutcomes, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("source outcome root must be absolute")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create source outcome root: %w", err)
	}
	file, err := os.CreateTemp(root, ".source-outcomes-*")
	if err != nil {
		return nil, fmt.Errorf("create source outcome receipt: %w", err)
	}
	capture := &partitionSourceOutcomes{file: file, encoder: json.NewEncoder(&sourceOutcomeWriter{file: file, remaining: sourceOutcomeReceiptBytes}), corpus: corpus, seen: make(map[string]struct{})}
	suppressed := corpus.SCIPTypedDomain() && !corpus.SCIPTypedPartition()
	if !suppressed {
		capture.expected = corpus.enumerated
		for path := range corpus.enumerated {
			if strings.HasSuffix(path, ".go") {
				capture.expectedCount++
			}
		}
	}
	header := struct {
		Schema     string `json:"schema"`
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
		RunID      string `json:"run_id"`
		PlanDigest string `json:"plan_digest"`
		Ordinal    int    `json:"partition_ordinal"`
	}{"t472-source-outcomes-v1", corpus.repo, corpus.commit, runID, plan.Digest, ordinal}
	if err := capture.encoder.Encode(header); err != nil {
		capture.close()
		return nil, fmt.Errorf("write source outcome header: %w", err)
	}
	return capture, nil
}

func (capture *partitionSourceOutcomes) append(outcome sdk.SourceOutcome) error {
	if _, ok := capture.expected[outcome.Path]; !ok || !strings.HasSuffix(outcome.Path, ".go") {
		return errors.New("source outcome is outside processing scope")
	}
	if _, ok := capture.seen[outcome.Path]; ok {
		return errors.New("duplicate source outcome")
	}
	valid := false
	switch outcome.Reason {
	case "source_scan_complete":
		valid = outcome.State == "analyzed"
	case "source_parse_failed":
		valid = outcome.State == "failed" || outcome.State == "partial"
	case "source_byte_limit", "source_invalid_utf8", "no_generated_methods":
		valid = outcome.State == "excluded" || outcome.State == "partial"
	case "typed_input_invalid":
		valid = outcome.State == "partial"
	}
	capture.corpus.mu.Lock()
	trusted, read := capture.corpus.sources[outcome.Path]
	capture.corpus.mu.Unlock()
	if !valid || !read || trusted.digest != outcome.BlobDigest {
		return errors.New("invalid source outcome")
	}
	if err := capture.encoder.Encode(outcome); err != nil {
		return fmt.Errorf("write source outcome: %w", err)
	}
	capture.seen[outcome.Path] = struct{}{}
	return nil
}

func (capture *partitionSourceOutcomes) finish(ctx context.Context, plan candidate.DomainResultPlan, ordinal int, spec candidate.PartitionResultSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(capture.seen) != capture.expectedCount {
		return errors.New("source outcome receipt has incomplete coverage")
	}
	result, err := candidate.BuildPartitionResult(plan, ordinal, spec)
	if err != nil {
		return err
	}
	if err := capture.encoder.Encode(struct {
		Result candidate.PartitionResult `json:"result"`
	}{result}); err != nil {
		return fmt.Errorf("write source outcome completion: %w", err)
	}
	if err := capture.file.Sync(); err != nil {
		return fmt.Errorf("sync source outcomes: %w", err)
	}
	if err := capture.file.Close(); err != nil {
		return fmt.Errorf("close source outcomes: %w", err)
	}
	name := filepath.Join(filepath.Dir(capture.file.Name()), strings.TrimPrefix(result.Digest, "sha256:")+".jsonl")
	if err := os.Link(capture.file.Name(), name); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("install source outcomes: %w", err)
		}
		prior, priorErr := sourceOutcomeFileDigest(name)
		next, nextErr := sourceOutcomeFileDigest(capture.file.Name())
		if priorErr != nil || nextErr != nil || prior != next {
			return errors.Join(errors.New("source outcome receipt collision"), priorErr, nextErr)
		}
	}
	directory, err := os.Open(filepath.Dir(name))
	if err != nil {
		return fmt.Errorf("open source outcome directory: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync source outcome directory: %w", err)
	}
	return nil
}

func (capture *partitionSourceOutcomes) close() {
	_ = capture.file.Close()
	_ = os.Remove(capture.file.Name())
}

func sourceOutcomeFileDigest(path string) ([32]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return [32]byte{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > sourceOutcomeReceiptBytes {
		return [32]byte{}, errors.New("invalid source outcome receipt file")
	}
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, sourceOutcomeReceiptBytes+1))
	if err != nil {
		return [32]byte{}, err
	}
	if n > sourceOutcomeReceiptBytes {
		return [32]byte{}, errors.New("source outcome receipt exceeds byte bound")
	}
	return [32]byte(hash.Sum(nil)), nil
}
