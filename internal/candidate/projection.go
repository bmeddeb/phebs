package candidate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const projectionChunkRecords = 512

// candidateProjection is the minimum identity needed to validate uniqueness,
// cross-plane agreement, and the corpus lower bounds. It is externally sorted
// in bounded chunks so strict Open never retains the selected path set.
type candidateProjection struct {
	Path          string `json:"path"`
	OID           string `json:"oid"`
	DeclaredBytes int64  `json:"declared_bytes"`
	InUnit        bool   `json:"in_unit"`
	Shared        bool   `json:"shared"`
	Plane         Plane  `json:"plane"`
}

func projectionFromRecord(record Record, plane Plane) candidateProjection {
	return candidateProjection{
		Path: record.Path, OID: record.OID,
		DeclaredBytes: record.DeclaredBytes,
		InUnit:        record.InUnit,
		Shared:        record.Shared, Plane: plane,
	}
}

func compareProjections(left, right candidateProjection) int {
	if value := strings.Compare(left.Path, right.Path); value != 0 {
		return value
	}
	return strings.Compare(string(left.Plane), string(right.Plane))
}

func sameProjectionIdentity(left, right candidateProjection) bool {
	return left.Path == right.Path &&
		left.OID == right.OID &&
		left.DeclaredBytes == right.DeclaredBytes &&
		left.InUnit == right.InUnit &&
		left.Shared == right.Shared
}

type projectionSorter struct {
	ctx       context.Context
	directory string
	chunk     []candidateProjection
	levels    []string
	sequence  int
	finished  bool
}

func newProjectionSorter(
	ctx context.Context,
	directory string,
) *projectionSorter {
	return &projectionSorter{
		ctx:       ctx,
		directory: directory,
		chunk:     make([]candidateProjection, 0, projectionChunkRecords),
	}
}

func (sorter *projectionSorter) add(current candidateProjection) error {
	if sorter == nil || sorter.finished {
		return errors.New("candidate projection sorter is closed")
	}
	if err := sorter.ctx.Err(); err != nil {
		return err
	}
	sorter.chunk = append(sorter.chunk, current)
	if len(sorter.chunk) < projectionChunkRecords {
		return nil
	}
	return sorter.flushChunk()
}

func (sorter *projectionSorter) finish() (string, error) {
	if sorter == nil || sorter.finished {
		return "", errors.New("candidate projection sorter is closed")
	}
	if err := sorter.ctx.Err(); err != nil {
		return "", err
	}
	sorter.finished = true
	if err := sorter.flushChunk(); err != nil {
		return "", err
	}
	var result string
	for level := range sorter.levels {
		current := sorter.levels[level]
		if current == "" {
			continue
		}
		if result == "" {
			result = current
			continue
		}
		merged, err := sorter.mergeRuns(result, current)
		if err != nil {
			return "", err
		}
		result = merged
	}
	return result, nil
}

func (sorter *projectionSorter) flushChunk() error {
	if err := sorter.ctx.Err(); err != nil {
		return err
	}
	if len(sorter.chunk) == 0 {
		return nil
	}
	slices.SortFunc(sorter.chunk, compareProjections)
	for index := 1; index < len(sorter.chunk); index++ {
		if compareProjections(sorter.chunk[index-1], sorter.chunk[index]) == 0 {
			return fmt.Errorf(
				"%w: duplicate %s path %q",
				ErrInvalidManifest, sorter.chunk[index].Plane,
				sorter.chunk[index].Path,
			)
		}
	}
	run, err := sorter.writeRun(sorter.chunk)
	if err != nil {
		return err
	}
	sorter.chunk = sorter.chunk[:0]
	return sorter.addRun(run, 0)
}

func (sorter *projectionSorter) addRun(run string, level int) error {
	for {
		if err := sorter.ctx.Err(); err != nil {
			return err
		}
		if level == len(sorter.levels) {
			sorter.levels = append(sorter.levels, run)
			return nil
		}
		if sorter.levels[level] == "" {
			sorter.levels[level] = run
			return nil
		}
		merged, err := sorter.mergeRuns(sorter.levels[level], run)
		if err != nil {
			return err
		}
		sorter.levels[level] = ""
		run = merged
		level++
	}
}

func (sorter *projectionSorter) nextPath() string {
	path := filepath.Join(
		sorter.directory,
		fmt.Sprintf("projection-%08d.ndjson", sorter.sequence),
	)
	sorter.sequence++
	return path
}

func (sorter *projectionSorter) writeRun(
	records []candidateProjection,
) (path string, resultErr error) {
	path = sorter.nextPath()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
		if resultErr != nil {
			_ = os.Remove(path)
			path = ""
		}
	}()
	writer := bufio.NewWriterSize(file, 64<<10)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	for _, current := range records {
		if err := sorter.ctx.Err(); err != nil {
			return "", err
		}
		if err := encoder.Encode(current); err != nil {
			return "", err
		}
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	return path, nil
}

func (sorter *projectionSorter) mergeRuns(
	leftPath, rightPath string,
) (path string, resultErr error) {
	left, err := openProjectionRun(leftPath)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := left.close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	right, err := openProjectionRun(rightPath)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := right.close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()

	path = sorter.nextPath()
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	outputClosed := false
	defer func() {
		if !outputClosed {
			if closeErr := output.Close(); resultErr == nil && closeErr != nil {
				resultErr = closeErr
			}
		}
		if resultErr != nil {
			_ = os.Remove(path)
			path = ""
		}
	}()
	writer := bufio.NewWriterSize(output, 64<<10)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	for left.current != nil || right.current != nil {
		if err := sorter.ctx.Err(); err != nil {
			return "", err
		}
		var current candidateProjection
		switch {
		case left.current == nil:
			current = *right.current
			if err := right.advance(); err != nil {
				return "", err
			}
		case right.current == nil:
			current = *left.current
			if err := left.advance(); err != nil {
				return "", err
			}
		default:
			comparison := compareProjections(*left.current, *right.current)
			if comparison == 0 {
				return "", fmt.Errorf(
					"%w: duplicate %s path %q",
					ErrInvalidManifest, left.current.Plane,
					left.current.Path,
				)
			}
			if comparison < 0 {
				current = *left.current
				if err := left.advance(); err != nil {
					return "", err
				}
			} else {
				current = *right.current
				if err := right.advance(); err != nil {
					return "", err
				}
			}
		}
		if err := encoder.Encode(current); err != nil {
			return "", err
		}
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	closeErr := errors.Join(left.close(), right.close(), output.Close())
	outputClosed = true
	if closeErr != nil {
		return "", closeErr
	}
	if err := errors.Join(
		os.Remove(leftPath), os.Remove(rightPath),
	); err != nil {
		return "", err
	}
	return path, nil
}

type projectionRun struct {
	file    *os.File
	decoder *json.Decoder
	current *candidateProjection
}

func openProjectionRun(path string) (*projectionRun, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	run := &projectionRun{
		file: file,
		decoder: json.NewDecoder(
			bufio.NewReaderSize(file, 64<<10),
		),
	}
	if err := run.advance(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return run, nil
}

func (run *projectionRun) advance() error {
	if run == nil || run.decoder == nil {
		return errors.New("candidate projection run is closed")
	}
	var current candidateProjection
	if err := run.decoder.Decode(&current); errors.Is(err, io.EOF) {
		run.current = nil
		return nil
	} else if err != nil {
		return err
	}
	run.current = &current
	return nil
}

func (run *projectionRun) close() error {
	if run == nil || run.file == nil {
		return nil
	}
	file := run.file
	run.file = nil
	run.decoder = nil
	run.current = nil
	return file.Close()
}
