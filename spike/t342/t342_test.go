// Package t342 retains the production-compiler conformance gate against the
// independently authored T32.3 oracle and a real in-process zoekt reader.
package t342

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sourcegraph/zoekt"
	"github.com/sourcegraph/zoekt/index"
	zoektsearch "github.com/sourcegraph/zoekt/search"

	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t323"
)

func TestProductionCompilerMatchesT323ServiceOracle(t *testing.T) {
	snapshots, err := t323.SmallSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	commits := make([]string, len(snapshots))
	for index, snapshot := range snapshots {
		commits[index] = objectID(snapshot.Revision)
	}

	checked := 0
	for snapshotIndex, snapshot := range snapshots {
		for _, expectation := range snapshot.Oracle.Queries {
			if expectation.Scope != "service" ||
				(expectation.State != "exact" && expectation.State != "stale") {
				continue
			}
			checked++
			t.Run(snapshot.Revision+"/"+expectation.ID, func(t *testing.T) {
				activeIndex := snapshotIndex
				if expectation.State == "stale" {
					activeIndex = snapshotIndex - 1
				}
				currentPublication := publicationFor(
					t, snapshot, expectation.ServiceKey,
					commits[snapshotIndex], uint64(snapshotIndex+1),
				)
				activePublication := publicationFor(
					t, snapshots[activeIndex], expectation.ServiceKey,
					commits[activeIndex], uint64(activeIndex+1),
				)
				state, summary := stateFor(
					t, currentPublication, activePublication,
					expectation.State,
				)
				searchManifest := searchManifestFor(
					t, commits[activeIndex], expectation.ID,
				)
				prepared, err := servicequery.Prepare(servicequery.Scope{
					CurrentCatalog: currentPublication,
					ActiveCatalog:  activePublication,
					Summary:        summary, State: state,
					Search: searchManifest,
				})
				if err != nil {
					t.Fatal(err)
				}
				compiled, err := servicequery.Compile(servicequery.Request{
					Expression:       expectation.Expression,
					RevisionSelector: "HEAD",
					Scopes:           []servicequery.PreparedScope{prepared},
				})
				if err != nil {
					t.Fatal(err)
				}
				reader := buildReader(
					t, snapshots[activeIndex].Files,
					commits[activeIndex],
				)
				defer reader.Close()
				result, err := reader.Search(
					context.Background(), compiled.Query,
					&zoekt.SearchOptions{
						ChunkMatches: true, MaxDocDisplayCount: 500,
						TotalMaxMatchCount: 100_000,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				got := make([]string, 0, len(result.Files))
				for _, file := range result.Files {
					got = append(got, file.FileName)
				}
				sort.Strings(got)
				want := slices.Clone(expectation.Paths)
				sort.Strings(want)
				if !slices.Equal(got, want) {
					t.Fatalf("paths = %v, want %v", got, want)
				}
				wantStatus := servicecatalog.StatusCurrent
				if expectation.State == "stale" {
					wantStatus = servicecatalog.StatusStale
				}
				if compiled.Authority.Status != wantStatus {
					t.Fatalf(
						"authority status = %q, want %q",
						compiled.Authority.Status, wantStatus,
					)
				}
			})
		}
	}
	if checked != 18 {
		t.Fatalf("checked %d exact/stale service oracles, want 18", checked)
	}
}

func TestPathPredicateAppliesBeforeRankingAndTruncation(t *testing.T) {
	files := map[string][]byte{
		"service/a.go": []byte("package service\nconst A = \"T342_TIE\"\n"),
		"service/b.go": []byte("package service\nconst B = \"T342_TIE\"\n"),
	}
	for index := range 100 {
		files[fmt.Sprintf("outside/T342_TIE-%03d.go", index)] =
			[]byte("package outside\nconst Match = \"T342_TIE\"\n")
	}
	snapshot := t323.Snapshot{
		Revision: "ranking", Files: files,
		Catalog: t323.Catalog{
			Records: []t323.AuthorityRecord{{
				ServiceKey: "svc.ranking", DisplayName: "Ranking",
				Disposition: "accepted",
				Placements:  []t323.Placement{{Path: "service", Role: "primary"}},
			}},
		},
	}
	commit := objectID(snapshot.Revision)
	publication := publicationFor(t, snapshot, "svc.ranking", commit, 1)
	state, summary := stateFor(t, publication, publication, "exact")
	prepared, err := servicequery.Prepare(servicequery.Scope{
		CurrentCatalog: publication, ActiveCatalog: publication,
		Summary: summary, State: state,
		Search: searchManifestFor(t, commit, "ranking"),
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := servicequery.Compile(servicequery.Request{
		Expression: "T342_TIE", RevisionSelector: "HEAD",
		Scopes: []servicequery.PreparedScope{prepared},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := buildReader(t, files, commit)
	defer reader.Close()
	result, err := reader.Search(
		context.Background(), compiled.Query,
		&zoekt.SearchOptions{
			ChunkMatches: true, MaxDocDisplayCount: 1,
			TotalMaxMatchCount: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 ||
		!strings.HasPrefix(result.Files[0].FileName, "service/") {
		t.Fatalf("truncated files = %+v", result.Files)
	}
	broad, err := servicequery.Compile(servicequery.Request{
		Expression: "file:.*", RevisionSelector: "HEAD",
		Scopes: []servicequery.PreparedScope{prepared},
	})
	if err != nil {
		t.Fatal(err)
	}
	broadResult, err := reader.Search(
		context.Background(), broad.Query,
		&zoekt.SearchOptions{
			ChunkMatches: true, MaxDocDisplayCount: 500,
			TotalMaxMatchCount: 100_000,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(broadResult.Files) != 2 {
		t.Fatalf("broad service files = %+v, want two", broadResult.Files)
	}
	for _, file := range broadResult.Files {
		if !strings.HasPrefix(file.FileName, "service/") {
			t.Fatalf("broad query returned out-of-service path %q", file.FileName)
		}
	}
}

func TestIndexedTagSelectorUsesExactZoektBranch(t *testing.T) {
	head, tag := objectID("branch-head"), objectID("branch-tag")
	snapshot := t323.Snapshot{
		Revision: "branches",
		Files: map[string][]byte{
			"service/head.go": []byte("package service\nconst Head = \"T342_BRANCH\"\n"),
			"service/tag.go":  []byte("package service\nconst Tag = \"T342_BRANCH\"\n"),
		},
		Catalog: t323.Catalog{Records: []t323.AuthorityRecord{{
			ServiceKey: "svc.branch", DisplayName: "Branch",
			Disposition: "accepted",
			Placements:  []t323.Placement{{Path: "service", Role: "primary"}},
		}}},
	}
	publication := publicationFor(t, snapshot, "svc.branch", head, 1)
	state, summary := stateFor(t, publication, publication, "exact")
	revisions := []store.IndexedRevision{
		{Selector: "HEAD", Branch: "HEAD", Commit: head},
		{Selector: "release", Branch: "refs/tags/v1^{commit}", Commit: tag},
	}
	prepared, err := servicequery.Prepare(servicequery.Scope{
		CurrentCatalog: publication, ActiveCatalog: publication,
		Summary: summary, State: state,
		Search: searchManifestWithRevisions(t, revisions, "branches"),
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := servicequery.Compile(servicequery.Request{
		Expression: "T342_BRANCH", RevisionSelector: "release",
		Scopes: []servicequery.PreparedScope{prepared},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := buildBranchReader(t, head, tag)
	defer reader.Close()
	result, err := reader.Search(
		context.Background(), compiled.Query,
		&zoekt.SearchOptions{MaxDocDisplayCount: 10, TotalMaxMatchCount: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].FileName != "service/tag.go" ||
		compiled.Authority.RevisionCommit != tag ||
		compiled.Authority.RevisionBranch != "refs/tags/v1^{commit}" {
		t.Fatalf("tag result = %+v, authority = %+v", result.Files, compiled.Authority)
	}
}

func publicationFor(
	t *testing.T,
	snapshot t323.Snapshot,
	serviceKey, commit string,
	revision uint64,
) servicecatalog.Publication {
	t.Helper()
	var selected *t323.AuthorityRecord
	for index := range snapshot.Catalog.Records {
		record := &snapshot.Catalog.Records[index]
		if record.ServiceKey == serviceKey && record.Disposition == "accepted" {
			selected = record
			break
		}
	}
	if selected == nil {
		t.Fatalf("accepted service %q is absent", serviceKey)
	}
	memberships := make([]servicecatalog.Membership, 0, len(selected.Placements))
	for _, placement := range selected.Placements {
		memberships = append(memberships, servicecatalog.Membership{
			ServiceKey: serviceKey, Path: placement.Path,
			Role: placement.Role, Origin: servicecatalog.OriginBase,
		})
		// The neutral pre-schema oracle labels the typed artifact by its typed
		// role alone. The production v2 contract deliberately requires the
		// same exact path to carry supporting as well; this adds no query path.
		if placement.Role == servicecatalog.RoleTyped {
			memberships = append(memberships, servicecatalog.Membership{
				ServiceKey: serviceKey, Path: placement.Path,
				Role:   servicecatalog.RoleSupporting,
				Origin: servicecatalog.OriginBase,
			})
		}
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema,
		Authority: servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator,
			ID:   "t323-oracle", Version: snapshot.Revision,
		},
		Services: []servicecatalog.Service{{
			Key: serviceKey, DisplayName: selected.DisplayName,
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: memberships,
		Unowned:     []servicecatalog.UnownedPlacement{},
	}
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema:             servicecatalog.PublicationSchema,
		Repository:         t323.RepositoryName,
		SourceKind:         servicecatalog.SourceOperator,
		SourcePath:         "/t323/" + snapshot.Revision + ".json",
		SourceCommit:       commit,
		SourceCensusDigest: namedDigest("t323-census", snapshot.Revision),
		SourceFileCount:    len(snapshot.Files),
		AcceptedFileCount:  len(snapshot.Files),
		Authority:          catalog.Authority,
		CatalogDigest:      catalogDigest, Canonical: canonical,
	}
	publication.GenerationDigest, err =
		servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	publication.ControlRevision = revision
	publication.PublishedAt = time.Date(
		2026, 8, 5, 14, 0, int(revision), 0, time.UTC,
	)
	if err := servicecatalog.ValidatePublication(publication, true); err != nil {
		t.Fatal(err)
	}
	return publication
}

func stateFor(
	t *testing.T,
	current, active servicecatalog.Publication,
	oracleState string,
) (servicecatalog.ServiceState, servicecatalog.RepositoryState) {
	t.Helper()
	serviceKey := mustServiceKey(t, current)
	desired, found, err := servicecatalog.ProjectService(current, serviceKey)
	if err != nil || !found {
		t.Fatalf("desired projection: found %v, error %v", found, err)
	}
	activeProjection, found, err := servicecatalog.ProjectService(active, serviceKey)
	if err != nil || !found {
		t.Fatalf("active projection: found %v, error %v", found, err)
	}
	desiredGeneration, err := servicecatalog.ServiceDesiredGeneration(desired, 1)
	if err != nil {
		t.Fatal(err)
	}
	activeGeneration, err := servicecatalog.ServiceDesiredGeneration(activeProjection, 1)
	if err != nil {
		t.Fatal(err)
	}
	status := servicecatalog.StatusCurrent
	if oracleState == "stale" {
		status = servicecatalog.StatusStale
	}
	state := servicecatalog.ServiceState{
		Schema:     servicecatalog.ServiceStateSchema,
		Repository: current.Repository, ServiceKey: serviceKey,
		DisplayName: desired.Service.DisplayName,
		Disposition: desired.Service.Disposition, Origin: desired.Service.Origin,
		Successors: []string{}, Incarnation: 1,
		DesiredGeneration:        desiredGeneration,
		DesiredSourceGeneration:  desired.SourceGeneration,
		DesiredCatalogGeneration: desired.CatalogGeneration,
		ActiveDesiredGeneration:  activeGeneration,
		ActiveSourceGeneration:   activeProjection.SourceGeneration,
		ActiveCatalogGeneration:  activeProjection.CatalogGeneration,
		Status:                   status, ControlRevision: 1,
		ChangedAt: time.Date(2026, 8, 5, 15, 0, 0, 0, time.UTC),
	}
	if err := servicecatalog.SetServiceStateDigest(&state); err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidateServiceState(state, true); err != nil {
		t.Fatal(err)
	}
	summary := servicecatalog.RepositoryState{
		Schema:                 servicecatalog.RepositoryStateSchema,
		Repository:             current.Repository,
		CatalogGeneration:      current.GenerationDigest,
		CatalogControlRevision: current.ControlRevision,
		CatalogServiceCount:    1, LiveServiceCount: 1,
		ControlRevision: 1,
		UpdatedAt:       time.Date(2026, 8, 5, 15, 1, 0, 0, time.UTC),
	}
	if status == servicecatalog.StatusCurrent {
		summary.CurrentCount = 1
	} else {
		summary.StaleCount = 1
	}
	if err := servicecatalog.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	if err := servicecatalog.ValidateRepositoryState(summary, true); err != nil {
		t.Fatal(err)
	}
	return state, summary
}

func searchManifestFor(
	t *testing.T,
	commit, salt string,
) repositoryindex.SearchManifest {
	t.Helper()
	return searchManifestWithRevisions(t, []store.IndexedRevision{{
		Selector: "HEAD", Branch: "HEAD", Commit: commit,
	}}, salt)
}

func searchManifestWithRevisions(
	t *testing.T,
	revisions []store.IndexedRevision,
	salt string,
) repositoryindex.SearchManifest {
	t.Helper()
	manifest := repositoryindex.SearchManifest{
		Schema:                 repositoryindex.SearchManifestSchema,
		Repository:             t323.RepositoryName,
		Revisions:              slices.Clone(revisions),
		SourceManifestName:     repositoryindex.SourceManifestName(t323.RepositoryName),
		SourceGenerationDigest: namedDigest("t342-source", salt),
		TopologyPolicy:         repositoryindex.DirectTopologyPolicy,
		PhysicalRoot: repositoryindex.PhysicalRoot{
			Schema:         "phebs-whole-shard-set-v1",
			ManifestName:   "whole.manifest.json",
			ManifestDigest: namedDigest("t342-whole", salt),
			Members: []repositoryindex.PhysicalMember{{
				Ordinal: 0, Count: 1, Name: "t342.zoekt",
				ContentDigest:  namedDigest("t342-content", salt),
				ContentBytes:   1,
				MetadataDigest: namedDigest("t342-metadata", salt),
			}},
		},
	}
	var err error
	manifest.Digest, err = repositoryindex.SearchManifestDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositoryindex.ValidateSearchManifest(manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func buildBranchReader(t *testing.T, head, tag string) zoekt.Streamer {
	t.Helper()
	directory := t.TempDir()
	tagBranch := "refs/tags/v1^{commit}"
	builder, err := index.NewBuilder(index.Options{
		IndexDir: directory, ShardPrefixOverride: "t342-branches",
		Parallelism: 1, DisableCTags: true,
		RepositoryDescription: zoekt.Repository{
			Name: t323.RepositoryName,
			Branches: []zoekt.RepositoryBranch{
				{Name: "HEAD", Version: head},
				{Name: tagBranch, Version: tag},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []index.Document{
		{Name: "service/head.go", Content: []byte("package service\nconst Head = \"T342_BRANCH\"\n"), Branches: []string{"HEAD"}},
		{Name: "service/tag.go", Content: []byte("package service\nconst Tag = \"T342_BRANCH\"\n"), Branches: []string{tagBranch}},
	} {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.Finish(); err != nil {
		t.Fatal(err)
	}
	reader, err := zoektsearch.NewDirectorySearcher(directory)
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func buildReader(
	t *testing.T,
	files map[string][]byte,
	commit string,
) zoekt.Streamer {
	t.Helper()
	directory := t.TempDir()
	builder, err := index.NewBuilder(index.Options{
		IndexDir: directory, ShardPrefixOverride: "t342",
		ShardMax: 100 << 20, Parallelism: 1, DisableCTags: true,
		RepositoryDescription: zoekt.Repository{
			Name:     t323.RepositoryName,
			Branches: []zoekt.RepositoryBranch{{Name: "HEAD", Version: commit}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := builder.Add(index.Document{
			Name: path, Content: files[path], Branches: []string{"HEAD"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.Finish(); err != nil {
		t.Fatal(err)
	}
	reader, err := zoektsearch.NewDirectorySearcher(filepath.Clean(directory))
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func mustServiceKey(t *testing.T, publication servicecatalog.Publication) string {
	t.Helper()
	catalog, err := servicecatalog.Decode(publication.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Services) != 1 {
		t.Fatalf("catalog has %d services", len(catalog.Services))
	}
	return catalog.Services[0].Key
}

func objectID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:40]
}

func namedDigest(domain, value string) string {
	sum := sha256.Sum256([]byte(domain + "\x00" + value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
