package t385

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetainedReceiptMatchesMicroserviceProductClosure(t *testing.T) {
	first, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("T38.5 receipt is not deterministic")
	}
	root := repositoryRoot(t)
	retained, err := os.ReadFile(filepath.Join(root, "spike/t385/results.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, retained) {
		t.Fatalf("retained T38.5 receipt differs: want %s", first)
	}
	receipt := Build()
	if receipt.Outcome != "completed" || len(receipt.Inputs) != 5 ||
		len(receipt.Stages) != 7 || len(receipt.Stories) != 4 ||
		len(receipt.Cases) != 6 || receipt.Browser.MobileViewport != "390x844" ||
		receipt.Topology.RelationshipRepositories != 3 ||
		receipt.Topology.RelationshipServices != 4 ||
		receipt.Topology.SearchServices != 5 ||
		receipt.Topology.WorkflowServices != 5 ||
		!receipt.Claims.SourceFree || !receipt.Claims.NoAccuracy ||
		!receipt.Claims.NoCompleteness || !receipt.Claims.NoRuntimeTopology ||
		!receipt.Claims.NoTaskOrDecision || !receipt.Claims.NoScale ||
		!receipt.Claims.NoSLO || !receipt.Claims.NoMigrationSafety ||
		!receipt.Claims.NoDecommissionSafety || !receipt.Claims.NoReleaseAuthority {
		t.Fatalf("unexpected T38.5 receipt: %+v", receipt)
	}
}

func TestMicroserviceProductClosureBindsExactPriorReceipts(t *testing.T) {
	root := repositoryRoot(t)
	for _, input := range Build().Inputs {
		path := filepath.Join(root, "spike", strings.ToLower(strings.ReplaceAll(input.Ticket, ".", "")), "results.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s input: %v", input.Ticket, err)
		}
		sum := sha256.Sum256(raw)
		gotDigest := "sha256:" + hex.EncodeToString(sum[:])
		if gotDigest != input.Digest {
			t.Errorf("%s digest = %s, want %s", input.Ticket, gotDigest, input.Digest)
		}
		var header struct {
			Schema  string `json:"schema"`
			Outcome string `json:"outcome"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Fatal(err)
		}
		if header.Schema != input.Schema || header.Outcome != "completed" {
			t.Errorf("%s header = %+v, want schema %s completed", input.Ticket, header, input.Schema)
		}
	}
}

// T46.1 retired live gate-name checks; the receipt bytes remain pinned above.

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
