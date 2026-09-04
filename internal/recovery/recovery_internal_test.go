package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/store"
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

func TestReadArchiveTransitionManifestProjectsOneStrictRead(t *testing.T) {
	if ArchiveTransitionReportCalls != 1 {
		t.Fatalf("archive transition report calls = %d", ArchiveTransitionReportCalls)
	}
	backup := t.TempDir()
	manifest := archiveTransitionManifestFixture(t)
	if err := writeManifest(filepath.Join(backup, ManifestName), manifest); err != nil {
		t.Fatal(err)
	}
	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
		ControlFileReads: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := ReadArchiveTransitionManifest(
		ctx, backup, manifest.ManifestSHA256, manifest.ManifestSHA256,
	)
	counts, accountingErr := ledger.Finish()
	if readErr != nil || accountingErr != nil {
		t.Fatalf("read archive transition manifest: %v", errors.Join(readErr, accountingErr))
	}
	if counts != (readaccounting.Counts{ControlFileReads: 1}) {
		t.Fatalf("archive transition read counts = %+v", counts)
	}
	if got.ManifestSchema != ManifestSchema || got.ManifestSHA256 != manifest.ManifestSHA256 ||
		len(got.Components) != 6 || len(got.Reports) != 5 {
		t.Fatalf("archive transition projection = %+v", got)
	}
	for index, component := range got.Components {
		artifact := manifest.Inventory[index]
		if component != (ArchiveTransitionComponent{
			Name: artifact.Path, Classification: artifact.Classification,
			MediaType: artifact.MediaType, Bytes: uint64(artifact.Size), SHA256: artifact.SHA256,
		}) {
			t.Fatalf("archive transition component %d = %+v", index, component)
		}
	}
	wantReports := []ArchiveTransitionReport{
		{Name: "focused_index", Schema: FocusedIndexArchiveReportSchema, Publications: 1},
		{Name: "resolver_catalog", Schema: ResolverCatalogArchiveReportSchema, Publications: 1},
		{Name: "caller_publication", Schema: CallerPublicationArchiveReportSchema, Publications: 1},
		{Name: "observation", Schema: ObservationArchiveReportSchema, Publications: 1, V2Publications: 1, Files: 1, Bytes: 1},
		{Name: "relationship", Schema: RelationshipArchiveReportSchema, Publications: 1, Files: 1, Bytes: 1},
	}
	for index, want := range wantReports {
		if got.Reports[index] != want {
			t.Fatalf("archive transition report %d = %+v, want %+v", index, got.Reports[index], want)
		}
	}
}

func TestReadArchiveTransitionManifestRejectsReportGaps(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"focused zero publications", func(value *Manifest) { value.FocusedIndex.Publications = 0 }},
		{"focused omitted publications", func(value *Manifest) { value.FocusedIndex.OmittedPublications = 1 }},
		{"focused omitted artifacts", func(value *Manifest) { value.FocusedIndex.OmittedArtifacts = 1 }},
		{"focused stale markers", func(value *Manifest) { value.FocusedIndex.StaleMarkers = 1 }},
		{"resolver zero publications", func(value *Manifest) { value.ResolverCatalog.Publications = 0 }},
		{"resolver omitted publications", func(value *Manifest) { value.ResolverCatalog.OmittedPublications = 1 }},
		{"resolver omitted artifacts", func(value *Manifest) { value.ResolverCatalog.OmittedArtifacts = 1 }},
		{"resolver stale markers", func(value *Manifest) { value.ResolverCatalog.StaleMarkers = 1 }},
		{"resolver omission detail", func(value *Manifest) {
			value.ResolverCatalog.Details = []resolvercatalog.Omission{{Name: "x", Reason: "invalid_manifest"}}
		}},
		{"resolver truncated details", func(value *Manifest) { value.ResolverCatalog.TruncatedDetails = 1 }},
		{"caller zero publications", func(value *Manifest) { value.CallerPublication.Publications = 0 }},
		{"caller omitted publications", func(value *Manifest) { value.CallerPublication.OmittedPublications = 1 }},
		{"caller omitted artifacts", func(value *Manifest) { value.CallerPublication.OmittedArtifacts = 1 }},
		{"caller stale markers", func(value *Manifest) { value.CallerPublication.StaleMarkers = 1 }},
		{"caller omission detail", func(value *Manifest) {
			value.CallerPublication.Details = []callerpublication.Omission{{Name: "x", Reason: "invalid_manifest"}}
		}},
		{"caller truncated details", func(value *Manifest) { value.CallerPublication.TruncatedDetails = 1 }},
		{"observation zero publications", func(value *Manifest) {
			value.Observation.Publications, value.Observation.V2Publications = 0, 0
		}},
		{"observation zero files", func(value *Manifest) { value.Observation.Files = 0 }},
		{"observation zero bytes", func(value *Manifest) { value.Observation.Bytes = 0 }},
		{"observation omitted publications", func(value *Manifest) {
			value.Observation.Omitted, value.Observation.OmittedPublications = 1, 1
		}},
		{"observation omitted artifacts", func(value *Manifest) {
			value.Observation.Omitted, value.Observation.OmittedArtifacts = 1, 1
		}},
		{"observation stale markers", func(value *Manifest) {
			value.Observation.Omitted, value.Observation.OmittedArtifacts, value.Observation.StaleMarkers = 1, 1, 1
		}},
		{"relationship zero publications", func(value *Manifest) { value.Relationship.Publications = 0 }},
		{"relationship zero files", func(value *Manifest) { value.Relationship.Files = 0 }},
		{"relationship zero bytes", func(value *Manifest) { value.Relationship.Bytes = 0 }},
		{"relationship omitted", func(value *Manifest) { value.Relationship.Omitted = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backup := t.TempDir()
			manifest := archiveTransitionManifestFixture(t)
			test.mutate(&manifest)
			manifest.ManifestSHA256 = ""
			var err error
			manifest.ManifestSHA256, err = manifestDigest(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeManifest(filepath.Join(backup, ManifestName), manifest); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadArchiveTransitionManifest(
				t.Context(), backup, manifest.ManifestSHA256, manifest.ManifestSHA256,
			); err == nil {
				t.Fatal("invalid archive report was accepted")
			}
		})
	}
}

func TestReadArchiveTransitionManifestRejectsDigestsAndUnsafeInput(t *testing.T) {
	valid := archiveTransitionManifestFixture(t)
	otherDigest := archiveTransitionDigest("f")
	tests := []struct {
		name          string
		write         func(*testing.T, string)
		backupDigest  string
		restoreDigest string
	}{
		{
			name: "backup command digest", write: writeArchiveTransitionManifest(valid),
			backupDigest: otherDigest, restoreDigest: valid.ManifestSHA256,
		},
		{
			name: "restore command digest", write: writeArchiveTransitionManifest(valid),
			backupDigest: valid.ManifestSHA256, restoreDigest: otherDigest,
		},
		{
			name: "self digest", write: func(t *testing.T, backup string) {
				changed := valid
				changed.ConfigSHA256 = otherDigest
				if err := writeManifest(filepath.Join(backup, ManifestName), changed); err != nil {
					t.Fatal(err)
				}
			}, backupDigest: valid.ManifestSHA256, restoreDigest: valid.ManifestSHA256,
		},
		{
			name: "symlink", write: func(t *testing.T, backup string) {
				target := filepath.Join(t.TempDir(), ManifestName)
				if err := writeManifest(target, valid); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(backup, ManifestName)); err != nil {
					t.Fatal(err)
				}
			}, backupDigest: valid.ManifestSHA256, restoreDigest: valid.ManifestSHA256,
		},
		{
			name: "oversize", write: func(t *testing.T, backup string) {
				file, err := os.Create(filepath.Join(backup, ManifestName))
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(maxManifestBytes + 1); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}, backupDigest: valid.ManifestSHA256, restoreDigest: valid.ManifestSHA256,
		},
		{
			name: "trailing", write: func(t *testing.T, backup string) {
				raw, err := json.Marshal(valid)
				if err != nil {
					t.Fatal(err)
				}
				raw = append(raw, []byte("\n{}\n")...)
				if err := os.WriteFile(filepath.Join(backup, ManifestName), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}, backupDigest: valid.ManifestSHA256, restoreDigest: valid.ManifestSHA256,
		},
		{
			name: "malformed", write: func(t *testing.T, backup string) {
				if err := os.WriteFile(filepath.Join(backup, ManifestName), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}, backupDigest: valid.ManifestSHA256, restoreDigest: valid.ManifestSHA256,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backup := t.TempDir()
			test.write(t, backup)
			ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
				ControlFileReads: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := ReadArchiveTransitionManifest(
				ctx, backup, test.backupDigest, test.restoreDigest,
			)
			counts, accountingErr := ledger.Finish()
			if readErr == nil || accountingErr != nil {
				t.Fatalf("unsafe archive manifest read = %v", errors.Join(readErr, accountingErr))
			}
			if counts != (readaccounting.Counts{ControlFileReads: 1}) {
				t.Fatalf("unsafe archive manifest counts = %+v", counts)
			}
		})
	}
}

func TestReadArchiveTransitionManifestCancellationAndRepeat(t *testing.T) {
	backup := t.TempDir()
	manifest := archiveTransitionManifestFixture(t)
	if err := writeManifest(filepath.Join(backup, ManifestName), manifest); err != nil {
		t.Fatal(err)
	}
	emptyCtx, emptyLedger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
		ControlFileReads: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArchiveTransitionManifest(
		emptyCtx, "", manifest.ManifestSHA256, manifest.ManifestSHA256,
	); err == nil {
		t.Fatal("empty archive transition path was accepted")
	}
	if counts, err := emptyLedger.Finish(); err != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("empty archive transition counts = %+v, %v", counts, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	canceled, canceledLedger, err := readaccounting.Start(canceled, readaccounting.Counts{
		ControlFileReads: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArchiveTransitionManifest(
		canceled, backup, manifest.ManifestSHA256, manifest.ManifestSHA256,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled archive transition read = %v", err)
	}
	if counts, err := canceledLedger.Finish(); err != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("canceled archive transition counts = %+v, %v", counts, err)
	}

	ctx, ledger, err := readaccounting.Start(t.Context(), readaccounting.Counts{
		ControlFileReads: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArchiveTransitionManifest(
		ctx, backup, manifest.ManifestSHA256, manifest.ManifestSHA256,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(backup, ManifestName)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArchiveTransitionManifest(
		ctx, backup, manifest.ManifestSHA256, manifest.ManifestSHA256,
	); !errors.Is(err, readaccounting.ErrLimit) {
		t.Fatalf("repeated archive transition read = %v", err)
	}
	if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) ||
		counts.ControlFileReads != 2 ||
		counts.StoreReadAttempts != 0 || counts.MemberVisits != 0 || counts.StoreWriteAttempts != 0 {
		t.Fatalf("repeated archive transition counts = %+v, %v", counts, err)
	}
}

func archiveTransitionManifestFixture(t *testing.T) Manifest {
	t.Helper()
	digest := archiveTransitionDigest("a")
	manifest := Manifest{
		Schema: ManifestSchema, CreatedAt: time.Unix(1, 0).UTC(),
		Database:      DatabaseIdentity{Namespace: "phebs", Database: "phebs"},
		ConfigSHA256:  digest,
		Phebs:         ToolIdentity{Version: "test", SHA256: digest},
		Surreal:       ToolIdentity{Version: "3.0.0", SHA256: digest},
		Store:         store.CurrentStoreIdentity(),
		ExportCommand: append([]string(nil), exportCommand...),
		Inventory: []Artifact{
			{Path: DatabaseName, Classification: "precious", MediaType: "application/surrealql", Size: 1, SHA256: archiveTransitionDigest("1")},
			{Path: FocusedIndexName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Size: 1, SHA256: archiveTransitionDigest("2")},
			{Path: ResolverCatalogName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Size: 1, SHA256: archiveTransitionDigest("3")},
			{Path: CallerPublicationName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Size: 1, SHA256: archiveTransitionDigest("4")},
			{Path: ObservationPublicationName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Size: 1, SHA256: archiveTransitionDigest("5")},
			{Path: RelationshipPublicationName, Classification: "derived-byte-exact", MediaType: "application/x-tar", Size: 1, SHA256: archiveTransitionDigest("6")},
		},
		FocusedIndex:      FocusedIndexArchiveReport{Schema: FocusedIndexArchiveReportSchema, Publications: 1},
		ResolverCatalog:   ResolverCatalogArchiveReport{Schema: ResolverCatalogArchiveReportSchema, Publications: 1},
		CallerPublication: CallerPublicationArchiveReport{Schema: CallerPublicationArchiveReportSchema, Publications: 1},
		Observation: ObservationArchiveReport{
			Schema: ObservationArchiveReportSchema, Publications: 1, V2Publications: 1,
			Files: 1, Bytes: 1,
		},
		Relationship: RelationshipArchiveReport{
			Schema: RelationshipArchiveReportSchema, Publications: 1, Files: 1, Bytes: 1,
		},
		DerivedExclusions: append([]string(nil), derivedExclusions...),
	}
	var err error
	manifest.ManifestSHA256, err = manifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func archiveTransitionDigest(value string) string {
	return "sha256:" + strings.Repeat(value, 64)
}

func writeArchiveTransitionManifest(manifest Manifest) func(*testing.T, string) {
	return func(t *testing.T, backup string) {
		t.Helper()
		if err := writeManifest(filepath.Join(backup, ManifestName), manifest); err != nil {
			t.Fatal(err)
		}
	}
}
