package t4110

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/spike/t411"
)

func TestMeasuredUTCDateUsesActualUTCDay(t *testing.T) {
	local := time.Date(
		2026, time.September, 1, 23, 30, 0, 0,
		time.FixedZone("test-offset", -2*60*60),
	)
	if got := measuredUTCDate(local); got != "2026-09-02" {
		t.Fatalf("measured UTC date = %q", got)
	}
}

func TestLiveTargetCatalogUsesExpandedV3Input(t *testing.T) {
	target, err := t411.BuildTargetCorpus()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeLiveV3Catalog(target.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := servicecatalogv3.DecodeCatalog(raw)
	if err != nil || len(decoded.Services) != t411.AcceptedServiceTarget ||
		len(decoded.Memberships) != target.Profile.Memberships {
		t.Fatalf(
			"expanded live catalog services=%d memberships=%d: %v",
			len(decoded.Services), len(decoded.Memberships), err,
		)
	}
}

func TestMappedTransitionRetainsExpandedV3Target(t *testing.T) {
	target, err := t411.BuildTargetCorpus()
	if err != nil {
		t.Fatal(err)
	}
	transition, err := t411.BuildTransitionCorpus()
	if err != nil {
		t.Fatal(err)
	}
	mapping := make(map[string]string, len(transition.Profile.Revisions[0].Catalog.Services))
	for index, service := range transition.Profile.Revisions[0].Catalog.Services {
		mapping[service.Key] = target.Catalog.Services[index].Key
	}
	mapped, err := mappedTransitionCatalog(
		target.Catalog, transition.Profile.Revisions[0], mapping,
	)
	if err != nil || len(mapped.Services) != t411.AcceptedServiceTarget {
		t.Fatalf("mapped expanded transition services=%d: %v", len(mapped.Services), err)
	}
}

func TestFrozenTargetRelationshipInputShape(t *testing.T) {
	target, err := t411.BuildTargetCorpus()
	if err != nil {
		t.Fatal(err)
	}
	extractor := protodecl.New()
	bins := make([]int, 1<<sourcepartition.InitialPrefixBits)
	selected, declared := 0, 0
	var first []byte
	for _, file := range target.Files {
		if !extractor.Candidate(file.Path) {
			continue
		}
		selected++
		declared += len(file.Content)
		if first == nil {
			first = file.Content
		}
		blob := sha1.New()
		_, _ = fmt.Fprintf(blob, "blob %d\x00", len(file.Content))
		_, _ = blob.Write(file.Content)
		objectID := hex.EncodeToString(blob.Sum(nil))
		sum := sha256.Sum256([]byte(sourcepartition.BlobHashPolicy + "\x00" + objectID))
		bins[int(sum[0]>>(8-sourcepartition.InitialPrefixBits))]++
	}
	wantBins := []int{598, 640, 649, 669, 575, 598, 643, 634, 635, 648, 573, 598, 642, 667, 595, 636}
	candidateMembers := (selected + candidate.MaxRecordsPerArtifact - 1) / candidate.MaxRecordsPerArtifact
	if selected != targetRelationshipRecords || declared != targetRelationshipDeclaredBytes ||
		candidateMembers != targetRelationshipCandidateMembers || !slices.Equal(bins, wantBins) {
		t.Fatalf(
			"relationship input selected=%d bytes=%d candidate_members=%d bins=%v",
			selected, declared, candidateMembers, bins,
		)
	}
	if _, err := sourceobservation.Parse(t.Context(), sourceobservation.Input{
		Path: "source.go", Content: string(first),
	}); err == nil {
		t.Fatal("neutral proto-path fixture unexpectedly produced an observed source record")
	}
}
