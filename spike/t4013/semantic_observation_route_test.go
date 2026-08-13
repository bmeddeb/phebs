package t4013

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/bmeddeb/phebs/internal/observationpublication"
)

func TestFrozenSemanticProfileRequiresSelectedObservationV2(t *testing.T) {
	raw, err := os.ReadFile("../t401/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Schema   string `json:"schema"`
		Profiles []struct {
			Name  string `json:"name"`
			Shape struct {
				UniqueGoBlobs int `json:"go_blob_reuse_classes"`
			} `json:"shape"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil ||
		envelope.Schema != "t401-neutral-scale-envelope-v1" {
		t.Fatalf("decode frozen envelope: %v", err)
	}
	semanticRecords := 0
	for _, profile := range envelope.Profiles {
		if profile.Name == "semantic-262144-v1" {
			semanticRecords = profile.Shape.UniqueGoBlobs
			break
		}
	}
	if semanticRecords != 262_144 ||
		semanticRecords <= observationpublication.MaxGenerationRecords ||
		semanticRecords > observationpublication.MaxInventoryRecordsV2 {
		t.Fatalf(
			"semantic observation admission = %d, legacy=%d, selected-v2=%d",
			semanticRecords, observationpublication.MaxGenerationRecords,
			observationpublication.MaxInventoryRecordsV2,
		)
	}
}
