package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/observationpublication"
)

type observationProgressToolFixture struct {
	calls int
}

func (fixture *observationProgressToolFixture) Read(
	_ context.Context, repository string,
) (*observationpublication.Progress, error) {
	fixture.calls++
	return &observationpublication.Progress{
		SchemaVersion: observationpublication.ProgressSchema, Repository: repository,
		State: "current", SourceGenerationDigest: "sha256:" + strings.Repeat("a", 64),
		Publication: &observationpublication.PublicationProgress{
			State: "current", GenerationDigest: "sha256:" + strings.Repeat("b", 64),
			SourceGenerationDigest: "sha256:" + strings.Repeat("a", 64),
			RecordCount:            1, ObservedCount: 1, ReceiptState: "complete",
			Receipt: &observationpublication.OperationReceipt{
				Schema: observationpublication.ReceiptSchema, InputBlobs: 1,
				SourceBlobReads: 1, ParsedObservations: 1, ObservedBlobs: 1,
				UnsupportedReasons: []observationpublication.ReasonCount{},
			},
		},
	}, nil
}

func TestObservationProgressToolReturnsSharedProjection(t *testing.T) {
	fixture := &observationProgressToolFixture{}
	server := NewServer(Options{Version: "test", ObservationProgress: fixture})
	progress, result := callTool[observationpublication.Progress](
		t, server, "get_observation_progress",
		map[string]any{"repository": "example.com/acme/mono"},
	)
	if result.IsError || fixture.calls != 1 || progress.State != "current" ||
		progress.Publication == nil || progress.Publication.Receipt == nil ||
		progress.Publication.Receipt.SourceBlobReads != 1 {
		t.Fatalf("observation progress tool = %+v result=%+v calls=%d", progress, result, fixture.calls)
	}
	tools := listServiceDirectoryTools(t, server)
	if tools["get_observation_progress"] == nil {
		t.Fatal("observation progress tool is absent")
	}
}

func TestObservationProgressToolRemainsDarkWithoutSharedService(t *testing.T) {
	tools := listServiceDirectoryTools(t, NewServer(Options{Version: "test"}))
	if tools["get_observation_progress"] != nil {
		t.Fatal("dark server exposed observation progress")
	}
}
