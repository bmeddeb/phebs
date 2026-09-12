package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

// Supplied Git text exercises the actual existing streaming verifier, not a
// subprocess or an independently owned predecessor. Real A/B/A is separate.
func TestCorpusAuthorChangedInventory(t *testing.T) {
	for _, mode := range []string{"cold", "delta", "return", "unchanged", "missing", "extra", "mode", "blob", "extra difference", "missing prior", "missing target", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			source := corpusAuthorTestSource(t)
			index := 1
			switch mode {
			case "cold":
				index = 0
			case "return":
				index = 2
			}
			var raw strings.Builder
			if err := source.walkRecords(t.Context(), source.revisions[index].Name, func(record sourceTreeRecord) error {
				_, _ = raw.WriteString("100644 blob " + record.BlobOID + "\t" + record.Path + "\x00")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var prior *sourceTreeRecord
			if index > 0 {
				value, err := source.previousLeaf(t.Context(), index)
				if err != nil {
					t.Fatal(err)
				}
				prior = &value
			}
			input := raw.String()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := uint64(1)
			success := false
			switch mode {
			case "cold":
				want, success = 3, true
			case "delta", "return":
				success = true
			case "unchanged":
				// This helper-only same-leaf model proves the comparison is not
				// the frozen literal 1. It is not an admitted prior binding.
				value, err := source.previousLeaf(ctx, 2)
				if err != nil {
					t.Fatal(err)
				}
				prior, want, success = &value, 0, true
			case "missing":
				input = input[:strings.LastIndex(input[:len(input)-1], "\x00")+1]
			case "extra":
				input += "x"
			case "mode":
				input = strings.Replace(input, "100644", "100755", 1)
			case "blob", "extra difference":
				input = input[:12] + strings.Repeat("0", 40) + input[52:]
			case "missing prior":
				prior = nil
			case "missing target":
				prior.Path = "missing.go"
			case "canceled":
				cancel()
			}
			_, count, err := source.verifyInventoryChanges(ctx, strings.NewReader(input), index, true, prior)
			if (err == nil) != success || success && count != want || !success && count != 0 {
				t.Fatalf("observed count=%d success=%t err=%v", count, success, err)
			}
		})
	}
}

func TestCorpusAuthorObservedWire(t *testing.T) {
	result := AuthoredExecutionRevision{Name: "a"}
	legacy := ExecutionCorpusAuthorResponse{Result: result, ConfigSHA256: SHA256(nil)}
	old, err := corpusAuthorCanonical(legacy, MaxExecutionCorpusAuthorResponseBytes)
	if err != nil || bytes.Contains(old, []byte("changed_physical_files")) {
		t.Fatal("legacy response bytes gained observation", err)
	}
	for _, mode := range []string{"valid", "zero", "missing", "incomplete", "overlimit", "unknown", "duplicate", "missing count", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			response := legacy
			response.ChangedPhysicalFiles = &ExecutionChangedPhysicalFiles{Count: maxCorpusAuthorRecords, Complete: true}
			switch mode {
			case "zero":
				response.ChangedPhysicalFiles.Count = 0
			case "missing":
				response.ChangedPhysicalFiles = nil
			case "incomplete":
				response.ChangedPhysicalFiles.Complete = false
			case "overlimit":
				response.ChangedPhysicalFiles.Count++
			}
			raw, err := corpusAuthorCanonical(response, MaxExecutionCorpusAuthorResponseBytes)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unknown":
				raw = bytes.Replace(raw, []byte(`"count":`), []byte(`"extra":0,"count":`), 1)
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"count":`), []byte(`"count":1,"count":`), 1)
			case "missing count":
				raw = bytes.Replace(raw, []byte(`"count":2031604,`), nil, 1)
			case "trailing":
				raw = append(raw, '\n')
			}
			_, decodeErr := authorCustodyCanonicalResponseFor(raw, result, true)
			if (decodeErr == nil) != (mode == "valid" || mode == "zero") {
				t.Fatal("closed measured response decoder", decodeErr)
			}
			if mode == "valid" {
				if len(raw)-len(old) != 59 {
					t.Fatal("maximum measured response growth changed")
				}
				if _, err := authorCustodyCanonicalResponse(raw, result); err == nil {
					t.Fatal("historical decoder accepted observation")
				}
			}
		})
	}
	for _, version := range []string{ExecutionCorpusAuthorRequestSchema, ExecutionCorpusAuthorObservedRequestSchema} {
		for _, measured := range []bool{false, true} {
			previous := legacy
			if measured {
				previous.ChangedPhysicalFiles = &ExecutionChangedPhysicalFiles{Count: 3, Complete: true}
			}
			request := ExecutionCorpusAuthorRequest{Schema: version, PlanPath: "/private/plan", PlanSHA256: SHA256(nil), SourcePath: "/private/source", SourceIdentity: ExecutionCorpusSourceIdentity{Inode: 1}, Revision: "b", Previous: &previous}
			raw, err := corpusAuthorCanonical(request, MaxExecutionCorpusAuthorRequestBytes)
			if err != nil {
				t.Fatal(err)
			}
			_, err = decodeCorpusAuthorRequest(raw, sha256.Sum256(raw))
			if (err == nil) != (measured == (version == ExecutionCorpusAuthorObservedRequestSchema)) {
				t.Fatal("nested previous crossed wire version", err)
			}
		}
	}
	response := legacy
	response.ChangedPhysicalFiles = &ExecutionChangedPhysicalFiles{Count: 3, Complete: true}
	copyValue := cloneAuthorCustodyResult(ExecutionAuthorResult{Response: &response})
	copyValue.Response.ChangedPhysicalFiles.Count = 99
	if response.ChangedPhysicalFiles.Count != 3 || copyValue.Completed {
		t.Fatal("child measurement aliases parent or implies whole operation success")
	}
	if _, err := authorCustodyCanonicalResponseFor(old, result, true); !errors.Is(err, ErrExecutionAuthorCustody) {
		t.Fatal("missing observation accepted")
	}
}
