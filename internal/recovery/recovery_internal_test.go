package recovery

import (
	"testing"

	"github.com/bmeddeb/phebs/internal/callerpublication"
)

func TestCallerArchiveInventoryUsesItsFourTiBEnvelope(t *testing.T) {
	if got := artifactByteLimit(CallerPublicationName); got != callerpublication.MaxArchiveBytes {
		t.Fatalf("caller artifact byte limit = %d, want %d", got, callerpublication.MaxArchiveBytes)
	}
	for _, size := range []int64{maxArtifactBytes + 1, callerpublication.MaxArchiveBytes} {
		if !validArtifactSize(CallerPublicationName, size) {
			t.Fatalf("caller archive size %d was refused", size)
		}
	}
	if validArtifactSize(CallerPublicationName, callerpublication.MaxArchiveBytes+1) {
		t.Fatal("caller archive above four TiB was accepted")
	}
	if !validArtifactSize(DatabaseName, maxArtifactBytes) ||
		validArtifactSize(DatabaseName, maxArtifactBytes+1) {
		t.Fatal("non-caller artifact did not retain the one TiB envelope")
	}
}
