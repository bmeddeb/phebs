package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestPartitionBoundaryFailureDiagnosticsRetainsPrivateChain(t *testing.T) {
	if partitionFailureDiagnostics {
		t.Skip("environment already enabled the diagnostics flag; the test mutates the package default")
	}
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	partitionFailureDiagnostics = true
	t.Cleanup(func() { partitionFailureDiagnostics = false })

	private := "get evidence chunk accounting: surreal: private cause"
	err := partitionBoundaryFailure(
		context.Background(),
		fmt.Errorf("wrap: %w", errors.New(private)),
		pipelinerefusal.StageEvidenceStaging,
	)
	if !store.IsTerminal(err) {
		t.Fatalf("boundary failure is not terminal: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "stage="+string(pipelinerefusal.StageEvidenceStaging)) ||
		!strings.Contains(out, private) {
		t.Fatalf("diagnostics log lost the private chain: %q", out)
	}
}

func TestPartitionBoundaryFailureDiagnosticsTruncateLongChain(t *testing.T) {
	if partitionFailureDiagnostics {
		t.Skip("environment already enabled the diagnostics flag; the test mutates the package default")
	}
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	partitionFailureDiagnostics = true
	t.Cleanup(func() { partitionFailureDiagnostics = false })

	long := strings.Repeat("x", partitionFailureDiagnosticLimit+512)
	_ = partitionBoundaryFailure(
		context.Background(),
		errors.New(long),
		pipelinerefusal.StageEvidenceStaging,
	)
	out := buf.String()
	if !strings.Contains(out, "stage="+string(pipelinerefusal.StageEvidenceStaging)) {
		t.Fatalf("diagnostics log missing stage: %q", out)
	}
	if strings.Contains(out, long) {
		t.Fatalf("diagnostics log did not truncate the chain (%d bytes)", len(out))
	}
}
