package t323

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSmallSnapshotsCoverFrozenContract(t *testing.T) {
	snapshots, err := SmallSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 5 {
		t.Fatalf("snapshots = %d, want 5", len(snapshots))
	}
	for _, snapshot := range snapshots {
		if err := ValidateSnapshot(snapshot); err != nil {
			t.Fatalf("%s: %v", snapshot.Revision, err)
		}
		assertExactQueries(t, snapshot)
		roles := map[string]bool{}
		for _, membership := range snapshot.Oracle.Memberships {
			roles[membership.Role] = true
		}
		for _, role := range []string{"primary", "supporting", "shared", "generated", "typed"} {
			if !roles[role] {
				t.Fatalf("%s omits %s membership", snapshot.Revision, role)
			}
		}
		if len(snapshot.Oracle.UnownedPaths) == 0 || len(snapshot.Oracle.Ambiguities) == 0 ||
			len(snapshot.Catalog.Invalid) == 0 {
			t.Fatalf("%s omits unowned, conflicting, or malformed authority", snapshot.Revision)
		}
	}

	if !hasPlacement(snapshots[0].Catalog, "svc.billing", "services/billing", "primary") ||
		!hasPlacement(snapshots[1].Catalog, "svc.billing", "services/payments", "primary") {
		t.Fatal("rename did not preserve svc.billing while changing placement")
	}
	if !hasTombstone(snapshots[2].Oracle, "svc.orders", "split", "svc.orders-api", "svc.orders-worker") {
		t.Fatal("split tombstone is absent")
	}
	if !hasTombstone(snapshots[3].Oracle, "svc.shipping", "merge", "svc.fulfillment") ||
		!hasState(snapshots[3].Oracle, "svc.fulfillment", "current", "current", "partial") {
		t.Fatal("merge or partial-publication state is absent")
	}
	if !hasTombstone(snapshots[4].Oracle, "svc.billing", "removed") ||
		!hasState(snapshots[4].Oracle, "svc.risk", "current", "unavailable", "unavailable") {
		t.Fatal("removal tombstone or inaccessible unavailable state is absent")
	}
}

func TestLoadProfilesHaveFrozenCardinality(t *testing.T) {
	tests := []struct {
		services, files, contents, memberships, shared, contracts, unowned int
		contentBytes                                                       int64
	}{
		{1_000, 3_151, 3_151, 5_000, 100, 40, 10, 208_662},
		{5_000, 15_751, 15_751, 25_000, 500, 200, 50, 1_043_142},
	}
	for _, testCase := range tests {
		profile, err := GenerateLoadProfile(testCase.services)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateProfile(profile); err != nil {
			t.Fatal(err)
		}
		got := profile.Cardinality
		if got.Services != testCase.services || got.Files != testCase.files ||
			got.DistinctContents != testCase.contents || got.ContentBytes != testCase.contentBytes ||
			got.Memberships != testCase.memberships || got.SharedFiles != testCase.shared ||
			got.ContractFiles != testCase.contracts || got.UnownedFiles != testCase.unowned ||
			got.MaxPathFanout != 25 {
			t.Fatalf("profile %d cardinality = %+v", testCase.services, got)
		}
		for _, role := range []string{"primary", "supporting", "shared", "generated", "typed"} {
			if got.RoleMemberships[role] != testCase.services {
				t.Fatalf("profile %d role %s = %d", testCase.services, role, got.RoleMemberships[role])
			}
		}
	}
}

func TestValidateProfileRecomputesFileCategories(t *testing.T) {
	profile, err := GenerateLoadProfile(1_000)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ProfileCardinality)
	}{
		{"shared", func(cardinality *ProfileCardinality) { cardinality.SharedFiles++ }},
		{"contract", func(cardinality *ProfileCardinality) { cardinality.ContractFiles++ }},
		{"unowned", func(cardinality *ProfileCardinality) { cardinality.UnownedFiles++ }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			altered := profile
			testCase.mutate(&altered.Cardinality)
			if err := ValidateProfile(altered); err == nil {
				t.Fatal("ValidateProfile accepted an incorrect file-category count")
			}
		})
	}
}

func TestArtifactsAndGitTreesAreDeterministic(t *testing.T) {
	firstArtifacts, err := BuildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	secondArtifacts, err := BuildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstArtifacts, secondArtifacts) {
		t.Fatal("two generations produced different catalog, oracle, or profile bytes")
	}

	first, err := Author(filepath.Join(t.TempDir(), "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Author(filepath.Join(t.TempDir(), "second"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two author runs differ\nfirst: %+v\nsecond: %+v", first, second)
	}
	retainedBytes, err := os.ReadFile("receipt.json")
	if err != nil {
		t.Fatal(err)
	}
	retained, err := DecodeStrict[Receipt](retainedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, retained) {
		t.Fatalf("retained receipt differs from deterministic author\ngenerated: %+v\nretained: %+v", first, retained)
	}
	for index := range first.Revisions {
		if first.Revisions[index].Tree == "" || first.Revisions[index].Commit == "" {
			t.Fatalf("revision identity %d is incomplete", index)
		}
	}
}

func TestRetainedArtifactsMatchGenerator(t *testing.T) {
	wantArtifacts, err := BuildArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	for artifactPath, want := range wantArtifacts {
		got, err := os.ReadFile(filepath.FromSlash(artifactPath))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("retained artifact %s differs from generator", artifactPath)
		}
	}
	receiptBytes, err := os.ReadFile("receipt.json")
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := DecodeStrict[Receipt](receiptBytes)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Schema != ReceiptSchema || receipt.Repository != RepositoryName ||
		len(receipt.Revisions) != 5 || len(receipt.Artifacts) != len(wantArtifacts) ||
		!receipt.Claims.Synthetic || receipt.Claims.EstablishesTargetSLO ||
		receipt.Claims.EstablishesAccuracy || receipt.Claims.SelectsTopology ||
		receipt.Claims.ProductionRegistration {
		t.Fatalf("retained receipt authority = %+v", receipt)
	}
	bundle, err := os.ReadFile(receipt.Bundle.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle) != receipt.Bundle.Bytes || SHA256(bundle) != receipt.Bundle.SHA256 {
		t.Fatal("retained bundle identity differs from receipt")
	}
	for _, artifact := range receipt.Artifacts {
		content, ok := wantArtifacts[artifact.Path]
		if !ok || len(content) != artifact.Bytes || SHA256(content) != artifact.SHA256 {
			t.Fatalf("retained identity differs for %s", artifact.Path)
		}
	}
}

func TestStrictDecodingRejectsOpenCatalog(t *testing.T) {
	snapshots, err := SmallSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalCanonical(snapshots[0].Catalog)
	if err != nil {
		t.Fatal(err)
	}
	var open map[string]any
	if err := json.Unmarshal(encoded, &open); err != nil {
		t.Fatal(err)
	}
	open["production_schema"] = true
	encoded, err = json.Marshal(open)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStrict[Catalog](encoded); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("open catalog decode error = %v", err)
	}
}

func assertExactQueries(t *testing.T, snapshot Snapshot) {
	t.Helper()
	membership := map[string][]Placement{}
	for _, record := range snapshot.Catalog.Records {
		if record.Disposition == "accepted" {
			membership[record.ServiceKey] = record.Placements
		}
	}
	for _, query := range snapshot.Oracle.Queries {
		if query.State != "exact" && query.State != "stale" {
			if len(query.Paths) != 0 {
				t.Fatalf("%s query %s has paths in state %s", snapshot.Revision, query.ID, query.State)
			}
			continue
		}
		var actual []string
		for filePath, content := range snapshot.Files {
			if !bytes.Contains(content, []byte(query.Expression)) {
				continue
			}
			if query.Scope == "service" && !selectedBy(membership[query.ServiceKey], filePath) {
				continue
			}
			actual = append(actual, filePath)
		}
		slices.Sort(actual)
		want := slices.Clone(query.Paths)
		slices.Sort(want)
		if !slices.Equal(actual, want) {
			t.Fatalf("%s query %s paths = %v, want %v", snapshot.Revision, query.ID, actual, want)
		}
	}
}

func selectedBy(placements []Placement, filePath string) bool {
	for _, placement := range placements {
		if filePath == placement.Path || strings.HasPrefix(filePath, placement.Path+"/") {
			return true
		}
	}
	return false
}

func hasPlacement(catalog Catalog, serviceKey, path, role string) bool {
	for _, record := range catalog.Records {
		if record.ServiceKey != serviceKey || record.Disposition != "accepted" {
			continue
		}
		for _, placement := range record.Placements {
			if placement.Path == path && placement.Role == role {
				return true
			}
		}
	}
	return false
}

func hasTombstone(oracle Oracle, serviceKey, reason string, successors ...string) bool {
	for _, tombstone := range oracle.Tombstones {
		if tombstone.ServiceKey == serviceKey && tombstone.Reason == reason &&
			slices.Equal(tombstone.Successors, successors) {
			return true
		}
	}
	return false
}

func hasState(oracle Oracle, serviceKey, catalog, search, relationships string) bool {
	for _, state := range oracle.States {
		if state.ServiceKey == serviceKey && state.Catalog == catalog &&
			state.Search == search && state.Relationships == relationships {
			return true
		}
	}
	return false
}
