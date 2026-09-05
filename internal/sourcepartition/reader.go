package sourcepartition

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

// ReadPartition validates one bounded plan member and reads every admitted
// immutable object exactly once through one hardened cat-file --batch child.
// Reader-boundary errors identify only the bounded record ordinal; wrapped
// classifications remain available through errors.Is without rendering a
// repository path, object id, child stderr, visitor detail, or blob content.
func (plan *Plan) ReadPartition(
	ctx context.Context,
	repositoryDirectory string,
	ordinal int,
	visit func(BlobRecord, []byte) error,
) (ReadStats, error) {
	if plan == nil || !filepath.IsAbs(repositoryDirectory) {
		return ReadStats{}, invalidf("reader directories must be absolute")
	}
	manifest := plan.manifest
	if err := ValidateManifest(manifest); err != nil {
		return ReadStats{}, err
	}
	if ordinal < 0 || ordinal >= len(manifest.Members) || visit == nil {
		return ReadStats{}, invalidf("reader partition or visitor is invalid")
	}
	if err := gitobj.RejectAlternates(repositoryDirectory); err != nil {
		return ReadStats{}, boundaryError{"inspect immutable object store", err}
	}
	records, err := readMember(
		ctx, plan.directory, len(manifest.Members), manifest.RevisionCount,
		manifest.Members[ordinal],
	)
	if err != nil {
		return ReadStats{}, boundaryError{"validate immutable batch member", err}
	}
	member := manifest.Members[ordinal]
	if member.DeclaredBytes > MaxPartitionBlobBytes ||
		member.BlobCount > MaxBlobsPerPartition {
		return ReadStats{}, fmt.Errorf("%w: partition exceeds batch admission", ErrTooLarge)
	}

	batchContext, cancel := context.WithTimeout(ctx, MaxBatchDuration)
	defer cancel()
	command := gitobj.Command(batchContext, repositoryDirectory, "cat-file", "--batch")
	var pipes dispatchadmission.CommandPipes
	input, err := pipes.StdinPipe(command)
	if err != nil {
		return ReadStats{}, errors.New("open immutable batch input")
	}
	output, err := pipes.StdoutPipe(command)
	if err != nil {
		_ = input.Close()
		return ReadStats{}, errors.New("open immutable batch output")
	}
	var stderr gitobj.StderrBuffer
	command.Stderr = &stderr
	handle, err := dispatchadmission.StartPipedProduction(batchContext, dispatchadmission.SiteSourcePartitionBatch, command, &pipes)
	if err != nil {
		_ = input.Close()
		_ = output.Close()
		return ReadStats{}, errors.New("start immutable batch reader")
	}

	reader := bufio.NewReaderSize(output, 64<<10)
	writer := bufio.NewWriterSize(input, 64<<10)
	stats := ReadStats{Descriptors: MaxBatchDescriptors}
	failed := true
	defer func() {
		if failed {
			_ = command.Process.Kill()
			_ = input.Close()
			_ = handle.Wait()
		}
	}()
	for index, record := range records {
		if err := batchContext.Err(); err != nil {
			return ReadStats{}, err
		}
		if _, err := writer.WriteString(record.ObjectID + "\n"); err != nil {
			return ReadStats{}, batchFailure(batchContext, index, "write request", err)
		}
		if err := writer.Flush(); err != nil {
			return ReadStats{}, batchFailure(batchContext, index, "flush request", err)
		}
		header, err := readLine(
			reader, 256, pipelinerefusal.DimensionBatchHeaderBytes,
		)
		if err != nil {
			return ReadStats{}, batchFailure(batchContext, index, "read header", err)
		}
		if int64(len(header)+1) > MaxBatchOutputBytes-stats.OutputBytes {
			return ReadStats{}, fmt.Errorf(
				"read immutable blob %d: %w", index,
				checkPlanLimit(
					pipelinerefusal.DimensionBatchOutputBytes,
					stats.OutputBytes+int64(len(header)+1),
					MaxBatchOutputBytes,
					"batch output bytes exceed their bound",
				),
			)
		}
		stats.OutputBytes += int64(len(header) + 1)
		fields := strings.Fields(string(header))
		if len(fields) == 2 && fields[0] == record.ObjectID && fields[1] == "missing" {
			return ReadStats{}, fmt.Errorf(
				"read immutable blob %d: %w", index,
				errors.Join(ErrMissingObject, store.ErrNotFound),
			)
		}
		if len(fields) != 3 || fields[0] != record.ObjectID {
			return ReadStats{}, fmt.Errorf("read immutable blob %d: invalid batch header", index)
		}
		if fields[1] != "blob" {
			return ReadStats{}, fmt.Errorf("read immutable blob %d: %w", index, ErrNotBlob)
		}
		size, parseErr := strconv.ParseInt(fields[2], 10, 64)
		if parseErr != nil || size < 0 || size > MaxBlobBytes {
			return ReadStats{}, fmt.Errorf("read immutable blob %d: invalid bounded size", index)
		}
		if size != record.DeclaredBytes {
			return ReadStats{}, fmt.Errorf("read immutable blob %d: %w", index, ErrSizeMismatch)
		}
		if size+1 > MaxBatchOutputBytes-stats.OutputBytes {
			return ReadStats{}, fmt.Errorf(
				"read immutable blob %d: %w", index,
				checkPlanLimit(
					pipelinerefusal.DimensionBatchOutputBytes,
					stats.OutputBytes+size+1, MaxBatchOutputBytes,
					"batch output bytes exceed their bound",
				),
			)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return ReadStats{}, batchFailure(batchContext, index, "read content", err)
		}
		terminator, err := reader.ReadByte()
		if err != nil || terminator != '\n' {
			return ReadStats{}, batchFailure(batchContext, index, "read terminator", err)
		}
		stats.OutputBytes += size + 1
		stats.Blobs++
		stats.Placements += len(record.Placements)
		stats.BlobBytes += size
		if err := visit(record, content); err != nil {
			return ReadStats{}, boundaryError{
				fmt.Sprintf("consume immutable blob %d failed", index), err,
			}
		}
	}
	if err := writer.Flush(); err != nil {
		return ReadStats{}, batchFailure(batchContext, len(records), "flush final request", err)
	}
	if err := input.Close(); err != nil {
		return ReadStats{}, batchFailure(batchContext, len(records), "close request stream", err)
	}
	if extra, err := reader.ReadByte(); err == nil {
		_ = extra
		return ReadStats{}, errors.New("immutable batch reader emitted trailing output")
	} else if !errors.Is(err, io.EOF) {
		return ReadStats{}, batchFailure(batchContext, len(records), "finish output", err)
	}
	// Wait joins even on error; never settle the same admitted attempt twice.
	failed = false
	if err := handle.Wait(); err != nil {
		return ReadStats{}, batchFailure(batchContext, len(records), "wait for reader", err)
	}
	if stats.Blobs != member.BlobCount || stats.Placements != member.PlacementCount ||
		stats.BlobBytes != member.DeclaredBytes || stats.OutputBytes > MaxBatchOutputBytes {
		return ReadStats{}, invalidf("batch reader totals disagree with the partition")
	}
	return stats, nil
}

type boundaryError struct {
	message string
	cause   error
}

func (failure boundaryError) Error() string { return failure.message }
func (failure boundaryError) Unwrap() error { return failure.cause }

func batchFailure(ctx context.Context, ordinal int, operation string, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cause == nil {
		return fmt.Errorf("immutable batch blob %d: %s failed", ordinal, operation)
	}
	var exit *exec.ExitError
	if errors.As(cause, &exit) {
		return fmt.Errorf("immutable batch blob %d: %s failed", ordinal, operation)
	}
	return fmt.Errorf("immutable batch blob %d: %s: %w", ordinal, operation, cause)
}
