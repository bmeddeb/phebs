package t4110

import (
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/recovery"
	phebssearch "github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/spike/t411"
)

func TestRecoveryManifestCostRequiresExactBoundedInventory(t *testing.T) {
	manifest := recovery.Manifest{Inventory: []recovery.Artifact{
		{Path: recovery.DatabaseName, Size: 1},
		{Path: recovery.FocusedIndexName, Size: 1},
		{Path: recovery.ResolverCatalogName, Size: 1},
		{Path: recovery.CallerPublicationName, Size: 1},
		{Path: recovery.ObservationPublicationName, Size: 1},
		{Path: recovery.RelationshipPublicationName, Size: 1},
	}}
	artifacts, bytes, err := recoveryManifestCost(manifest)
	if err != nil || artifacts != recoveryArtifactCount || bytes != recoveryArtifactCount {
		t.Fatalf("exact recovery cost = %d artifacts, %d bytes, %v", artifacts, bytes, err)
	}

	tests := []struct {
		name   string
		mutate func(*recovery.Manifest)
	}{
		{name: "missing artifact", mutate: func(value *recovery.Manifest) {
			value.Inventory = value.Inventory[:len(value.Inventory)-1]
		}},
		{name: "wrong order", mutate: func(value *recovery.Manifest) {
			value.Inventory[0], value.Inventory[1] = value.Inventory[1], value.Inventory[0]
		}},
		{name: "zero bytes", mutate: func(value *recovery.Manifest) {
			value.Inventory[0].Size = 0
		}},
		{name: "regular artifact over bound", mutate: func(value *recovery.Manifest) {
			value.Inventory[0].Size = (1 << 40) + 1
		}},
		{name: "caller artifact over bound", mutate: func(value *recovery.Manifest) {
			value.Inventory[3].Size = (4 << 40) + 1
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := manifest
			value.Inventory = append([]recovery.Artifact(nil), manifest.Inventory...)
			testCase.mutate(&value)
			if _, _, err := recoveryManifestCost(value); err == nil {
				t.Fatal("invalid recovery manifest cost was accepted")
			}
		})
	}
}

func TestProductSearchInputsAndCitationRequireExactContext(t *testing.T) {
	const (
		marker = "t411-neutral-fixture-v1"
		path   = "service/fixture.go"
	)
	httpInput, mcpInput := productSearchInputs("svc.fixture")
	if values := httpInput["context_lines"]; len(values) != 1 || values[0] != "1" {
		t.Fatalf("HTTP product search context = %v", values)
	}
	if value, ok := mcpInput["context_lines"].(int); !ok || value != 1 {
		t.Fatalf("MCP product search context = %#v", mcpInput["context_lines"])
	}
	harness := liveHarness{corpus: t411.Corpus{Files: []t411.FixtureFile{
		{Path: path, Content: []byte(marker + "\n" + path + "\n")},
	}}}
	exact := phebssearch.FileResult{Path: path, Chunks: []phebssearch.Chunk{{
		Content: marker + "\n" + path + "\n", StartLine: 1,
		Ranges: []phebssearch.Range{{
			StartLine: 1, StartCol: 1, EndLine: 1, EndCol: len(marker) + 1,
		}},
	}}}
	if err := harness.validateFixtureSearchCitation(exact); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*phebssearch.FileResult)
	}{
		{name: "wrong path content", mutate: func(value *phebssearch.FileResult) {
			value.Chunks[0].Content = marker + "\nother.go\n"
		}},
		{name: "zero-context truncation", mutate: func(value *phebssearch.FileResult) {
			value.Chunks[0].Content = marker + "\n"
		}},
		{name: "wrong marker", mutate: func(value *phebssearch.FileResult) {
			value.Chunks[0].Content = "wrong-marker\n" + path + "\n"
		}},
		{name: "wrong start column", mutate: func(value *phebssearch.FileResult) {
			value.Chunks[0].Ranges[0].StartCol = 2
		}},
		{name: "wrong end column", mutate: func(value *phebssearch.FileResult) {
			value.Chunks[0].Ranges[0].EndCol++
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := exact
			value.Chunks = slices.Clone(exact.Chunks)
			value.Chunks[0].Ranges = slices.Clone(exact.Chunks[0].Ranges)
			testCase.mutate(&value)
			if err := harness.validateFixtureSearchCitation(value); err == nil {
				t.Fatal("inexact fixture citation was accepted")
			}
		})
	}
}
