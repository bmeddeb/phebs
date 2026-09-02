package t4110

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
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

func TestSuccessorCatalogRebindsFrozenSourceComplement(t *testing.T) {
	target, err := t411.BuildTargetCorpus()
	if err != nil {
		t.Fatal(err)
	}
	binding := servicecatalogv3.Binding{
		Repository: liveRepository,
		Source: servicecatalogv3.Source{
			Kind: servicecatalog.SourceOperator, Path: "/tmp/t4110-catalog.json",
			Commit: strings.Repeat("a", 40), CensusDigest: target.Profile.Fixture.SHA256,
			FileCount: len(target.Files), AcceptedFileCount: len(target.Files) - len(target.Catalog.Unowned),
			UnownedFileCount: len(target.Catalog.Unowned),
		},
		Authority: target.Catalog.Authority,
	}
	base, err := servicecatalogv3.Build(binding, target.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	harness := liveHarness{corpus: target, generation: base}
	serviceKey := target.Catalog.Services[len(target.Catalog.Services)/2].Key
	removed := cloneCatalog(target.Catalog)
	removed.Services = removeService(removed.Services, serviceKey)
	removed.Memberships = removeMemberships(removed.Memberships, serviceKey)

	transition, err := t411.BuildTransitionCorpus()
	if err != nil {
		t.Fatal(err)
	}
	mapping := make(map[string]string, len(transition.Profile.Revisions[0].Catalog.Services))
	for index, service := range transition.Profile.Revisions[0].Catalog.Services {
		mapping[service.Key] = target.Catalog.Services[index].Key
	}
	mapped := make([]servicecatalog.Catalog, len(transition.Profile.Revisions))
	for index, revision := range transition.Profile.Revisions {
		mapped[index], err = mappedTransitionCatalog(target.Catalog, revision, mapping)
		if err != nil {
			t.Fatal(err)
		}
	}

	serviceFiles := func(indices ...int) []servicecatalog.UnownedPlacement {
		result := slices.Clone(target.Catalog.Unowned)
		for _, index := range indices {
			key := fmt.Sprintf("service-%05d", index)
			for _, value := range []string{
				"contracts/" + key + "/api.proto",
				"generated/" + key + "/client.pb.go",
				"services/" + key + "/main.go",
			} {
				result = append(result, servicecatalog.UnownedPlacement{
					Path: value, Origin: servicecatalog.OriginBase,
				})
			}
		}
		slices.SortFunc(result, func(left, right servicecatalog.UnownedPlacement) int {
			return strings.Compare(left.Path, right.Path)
		})
		return result
	}
	for _, test := range []struct {
		name    string
		catalog servicecatalog.Catalog
		unowned []servicecatalog.UnownedPlacement
	}{
		{name: "base", catalog: cloneCatalog(target.Catalog), unowned: serviceFiles()},
		{name: "removal", catalog: removed, unowned: serviceFiles(5000)},
		{name: "re-add", catalog: cloneCatalog(target.Catalog), unowned: serviceFiles()},
		{name: "transition-r0", catalog: mapped[0], unowned: serviceFiles(1, 2, 3)},
		{name: "transition-r1", catalog: mapped[1], unowned: serviceFiles(2, 3, 4)},
		{name: "transition-r2", catalog: mapped[2], unowned: serviceFiles(1, 2, 3)},
		{name: "transition-final", catalog: cloneCatalog(target.Catalog), unowned: serviceFiles()},
	} {
		t.Run(test.name, func(t *testing.T) {
			next, nextBinding, err := harness.bindSuccessorSource(test.catalog)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(next.Unowned, test.unowned) ||
				nextBinding.Source.AcceptedFileCount != len(target.Files)-len(test.unowned) ||
				nextBinding.Source.UnownedFileCount != len(test.unowned) {
				t.Fatalf("source binding = %+v, unowned=%v, want %v", nextBinding.Source, next.Unowned, test.unowned)
			}
			if _, err := servicecatalogv3.Build(nextBinding, next); err != nil {
				t.Fatalf("build source-bound successor: %v", err)
			}
		})
	}
	missing := cloneCatalog(target.Catalog)
	missing.Memberships = append(missing.Memberships, servicecatalog.Membership{
		ServiceKey: missing.Services[0].Key, Path: "missing/source/path",
		Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase,
	})
	if _, _, err := harness.bindSuccessorSource(missing); err == nil ||
		!strings.Contains(err.Error(), "absent from the frozen corpus") {
		t.Fatalf("missing successor placement = %v", err)
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
