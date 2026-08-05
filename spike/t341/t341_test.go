// Package t341 retains source-free production gates for T34.1.
package t341

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t322"
	"github.com/bmeddeb/phebs/spike/t323"
	"github.com/bmeddeb/phebs/spike/t324"
)

func TestNeutralProfileDoesNotMultiplyPhysicalOwnersByMembership(t *testing.T) {
	profile, err := t323.GenerateLoadProfile(1_000)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := t323.GenerateLoadProfileContents(1_000)
	if err != nil {
		t.Fatal(err)
	}
	repositoryDir := t.TempDir()
	git(t, repositoryDir, "init", "-b", "main")
	for name, content := range contents {
		path := filepath.Join(repositoryDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repositoryDir, "add", ".")
	git(t, repositoryDir, "commit", "-m", "neutral profile")
	commit := git(t, repositoryDir, "rev-parse", "HEAD")
	manifest, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDir, filepath.Join(t.TempDir(), "source"),
		"example.invalid/t341-neutral",
		[]store.IndexedRevision{{
			Selector: "HEAD", Branch: "HEAD", Commit: commit,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.OwnerCount != profile.Cardinality.Files ||
		manifest.RegularOwnerCount != profile.Cardinality.Files ||
		manifest.PlacementCount != profile.Cardinality.Files {
		t.Fatalf(
			"source counts = owners %d regular %d placements %d, want files %d",
			manifest.OwnerCount, manifest.RegularOwnerCount,
			manifest.PlacementCount, profile.Cardinality.Files,
		)
	}
	if manifest.OwnerCount >= profile.Cardinality.Memberships {
		t.Fatalf(
			"physical owners %d were multiplied toward %d memberships",
			manifest.OwnerCount, profile.Cardinality.Memberships,
		)
	}
}

func TestTargetDerivedDirectGateRemainsInputBound(t *testing.T) {
	root := filepath.Join("..", "..")
	t322Bytes := read(t, filepath.Join(root, "spike/t322/results.json"))
	var baseline t322.Receipt
	if err := json.Unmarshal(t322Bytes, &baseline); err != nil {
		t.Fatal(err)
	}
	t324Bytes := read(t, filepath.Join(root, "spike/t324/results.json"))
	decision, err := t324.DecodeStrict[t324.Receipt](t324Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := t324.ValidateReceipt(decision); err != nil {
		t.Fatal(err)
	}
	if baseline.Schema != t322.ReceiptSchema || baseline.Outcome != "completed" ||
		baseline.Index.Outcome != "succeeded" || baseline.Index.ShardCount < 1 ||
		baseline.Index.ShardBytes < 1 || baseline.Restart.IndexChildRuns != 0 ||
		decision.Inputs.T322ResultsSHA256 != sha256Digest(t322Bytes) ||
		decision.Trigger.Triggered || decision.Decisions[0].Topology != "direct" ||
		decision.Decisions[0].Decision != "go" {
		t.Fatalf("target-derived direct gate is incomplete or unbound")
	}
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func sha256Digest(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := []string{
		"-c", "user.name=T34.1", "-c", "user.email=t341@example.invalid",
		"-C", directory,
	}
	commandArgs = append(commandArgs, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
