package resolvermaterialize

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestResolverBlobSuccessfulReturnObservation(t *testing.T) {
	for _, mode := range []string{"ordinary", "success", "empty", "mismatch", "failed", "partial_error", "canceled_return", "sink", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var observedReads, observedBytes uint64
			if mode != "ordinary" {
				var err error
				ctx, err = readaccounting.WithResolverBlobObserver(ctx, func(value uint64) error {
					observedReads++
					observedBytes += value
					if mode == "sink" {
						return readaccounting.ErrEvent
					}
					if mode == "panic" {
						panic("observer")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			file := extract.CandidateManifestFile{Path: "go.mod", ObjectID: strings.Repeat("a", 40),
				Mode: "100644", DeclaredBytes: 2, SourceLane: candidate.SourceLaneBase}
			if mode == "empty" {
				file.DeclaredBytes = 0
			}
			nativeErr := errors.New("native read failure")
			calls := 0
			budget := blobBudget{read: func(context.Context, string, string, int64) ([]byte, error) {
				calls++
				switch mode {
				case "empty":
					return nil, nil
				case "failed":
					return nil, nativeErr
				case "partial_error":
					return []byte("x"), nativeErr
				case "mismatch":
					return []byte("x"), nil
				case "canceled_return":
					cancel()
				}
				return []byte("xx"), nil
			}}
			_, err := budget.load(ctx, file, 2)
			wantReads, wantBytes := uint64(1), uint64(2)
			switch mode {
			case "ordinary", "failed", "partial_error":
				wantReads, wantBytes = 0, 0
			case "empty":
				wantBytes = 0
			case "mismatch":
				wantBytes = 1
			}
			wantSuccess := mode == "ordinary" || mode == "success" || mode == "empty"
			if calls != 1 || observedReads != wantReads || observedBytes != wantBytes || (err == nil) != wantSuccess {
				t.Fatalf("calls=%d observed=%d/%d err=%v", calls, observedReads, observedBytes, err)
			}
			if mode == "failed" || mode == "partial_error" {
				if err != nativeErr || budget.reads != 0 || budget.bytes != 0 {
					t.Fatal("failed native read was relabeled as successful")
				}
			} else if budget.reads != 1 || mode != "ordinary" && uint64(budget.bytes) != observedBytes {
				t.Fatal("native successful budget no longer agrees with observation")
			}
			if mode == "success" {
				if _, err := budget.load(ctx, file, 2); err != nil || calls != 2 || observedReads != 2 || observedBytes != 4 {
					t.Fatal("repeated actual load was deduplicated", err)
				}
			}
		})
	}
}

func TestBuildObservesEveryNativeResolverReturn(t *testing.T) {
	request, blobs := resolvedMaterializeRequest(t, t.TempDir(), newMaterializeTestRegistry(t, ProtocolGRPC, ProtocolThrift))
	var actualReads, actualBytes, observedReads, observedBytes uint64
	request.ReadBlob = func(ctx context.Context, dir, oid string, limit int64) ([]byte, error) {
		content, err := blobs.read(ctx, dir, oid, limit)
		if err == nil {
			actualReads++
			actualBytes += uint64(len(content))
		}
		return content, err
	}
	ctx, err := readaccounting.WithResolverBlobObserver(t.Context(), func(value uint64) error {
		observedReads++
		observedBytes += value
		if observedReads != actualReads || observedBytes != actualBytes {
			t.Fatal("observation did not follow the exact successful native return")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := Build(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Discard(); err != nil {
		t.Fatal(err)
	}
	// Two fixed snapshots, two modules and one generated source per protocol.
	if actualReads != 6 || observedReads != actualReads || observedBytes != actualBytes || observedBytes == 0 {
		t.Fatalf("Build native=%d/%d observed=%d/%d paths=%v", actualReads, actualBytes, observedReads, observedBytes, blobs.readPaths)
	}
}
