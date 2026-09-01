package relationshippublication

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
)

func TestArchiveV3IndependentCompositeRoundTrip(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	legacyArchive := filepath.Join(t.TempDir(), "legacy.tar")
	legacyReport, err := CreateArchive(t.Context(), fixture.dataDir, legacyArchive)
	if err != nil {
		t.Fatal(err)
	}
	if legacyReport.Publications != 1 || legacyReport.Omitted != 0 {
		t.Fatalf("legacy report = %+v", legacyReport)
	}
	legacyRaw, err := os.ReadFile(legacyArchive)
	if err != nil {
		t.Fatal(err)
	}

	shadow := fixture.publishV3(t)
	shadowFiles, err := GenerationFilesV3(shadow.Root())
	if err != nil {
		t.Fatal(err)
	}
	bothArchive := filepath.Join(t.TempDir(), "both.tar")
	bothReport, err := CreateArchive(t.Context(), fixture.dataDir, bothArchive)
	if err != nil {
		t.Fatal(err)
	}
	if bothReport.Publications != 2 || bothReport.Omitted != 0 ||
		bothReport.Files != legacyReport.Files+len(shadowFiles)+1 {
		t.Fatalf(
			"composite report = %+v, legacy %+v, shadow files %d",
			bothReport, legacyReport, len(shadowFiles),
		)
	}
	restoredBoth := t.TempDir()
	if err := RestoreArchive(t.Context(), bothArchive, restoredBoth); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCurrent(
		t.Context(), filepath.Join(restoredBoth, "relationships"), fixture.repository,
	); err != nil {
		t.Fatalf("open restored legacy relationship: %v", err)
	}
	assertArchiveV3Current(t, restoredBoth, fixture.repository, shadow.Root())

	root := filepath.Join(fixture.dataDir, "relationships")
	shadowBase, err := RepositoryRootV3(root, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	shadowPointerPath := filepath.Join(shadowBase, "current.json")
	shadowDirectory, err := GenerationPathV3(
		root, fixture.repository, shadow.Root().GenerationDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []struct {
		name string
		path string
	}{
		{name: "pointer", path: shadowPointerPath},
		{name: "member", path: filepath.Join(shadowDirectory, shadowFiles[len(shadowFiles)-1])},
	} {
		t.Run("corrupt-shadow-"+corrupt.name, func(t *testing.T) {
			original, err := os.ReadFile(corrupt.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(corrupt.path, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			legacyOnlyArchive := filepath.Join(t.TempDir(), "legacy-only.tar")
			legacyOnlyReport, err := CreateArchive(
				t.Context(), fixture.dataDir, legacyOnlyArchive,
			)
			if err != nil {
				t.Fatal(err)
			}
			legacyOnlyRaw, err := os.ReadFile(legacyOnlyArchive)
			if err != nil {
				t.Fatal(err)
			}
			if legacyOnlyReport.Publications != 1 || legacyOnlyReport.Omitted != 1 ||
				!bytes.Equal(legacyOnlyRaw, legacyRaw) {
				t.Fatalf(
					"corrupt-shadow legacy preservation = %+v, bytes equal %t",
					legacyOnlyReport, bytes.Equal(legacyOnlyRaw, legacyRaw),
				)
			}
			if err := os.WriteFile(corrupt.path, original, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}

	legacyPointerPath := filepath.Join(
		root, "relationship-publications", repositoryHash(fixture.repository), "current.json",
	)
	legacyPointerRaw, err := os.ReadFile(legacyPointerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPointerPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	shadowOnlyArchive := filepath.Join(t.TempDir(), "shadow-only.tar")
	shadowOnlyReport, err := CreateArchive(
		t.Context(), fixture.dataDir, shadowOnlyArchive,
	)
	if err != nil {
		t.Fatal(err)
	}
	if shadowOnlyReport.Publications != 1 || shadowOnlyReport.Omitted != 1 {
		t.Fatalf("corrupt-legacy shadow preservation = %+v", shadowOnlyReport)
	}
	names := archiveV3EntryNames(t, shadowOnlyArchive)
	legacyPrefix := "relationships/relationship-publications/"
	resolverPointer := filepath.ToSlash(filepath.Join(
		"relationship-resolver-namespaces", "resolver-namespaces",
		repositoryHash(fixture.repository), "current.json",
	))
	if slices.ContainsFunc(names, func(name string) bool {
		return strings.HasPrefix(name, legacyPrefix)
	}) || slices.Contains(names, resolverPointer) {
		t.Fatalf("shadow-only archive retained mutable legacy controls: %q", names)
	}
	if !slices.ContainsFunc(names, func(name string) bool {
		return strings.HasPrefix(
			name, "relationships/"+RelationshipPublicationsV3Shadow+"/",
		)
	}) {
		t.Fatalf("shadow namespace missing from archive: %q", names)
	}
	restoredShadow := t.TempDir()
	if err := RestoreArchive(t.Context(), shadowOnlyArchive, restoredShadow); err != nil {
		t.Fatal(err)
	}
	assertArchiveV3Current(t, restoredShadow, fixture.repository, shadow.Root())
	if err := os.WriteFile(legacyPointerPath, legacyPointerRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	legacyNext, resolverNext := fixture.publishLegacyWithNewResolver(t)
	divergentArchive := filepath.Join(t.TempDir(), "divergent-resolver.tar")
	divergentReport, err := CreateArchive(
		t.Context(), fixture.dataDir, divergentArchive,
	)
	if err != nil {
		t.Fatal(err)
	}
	if divergentReport.Publications != 2 || divergentReport.Omitted != 0 {
		t.Fatalf("divergent-resolver report = %+v", divergentReport)
	}
	divergentNames := archiveV3EntryNames(t, divergentArchive)
	for _, resolver := range []*resolvernamespace.Publication{fixture.resolver, resolverNext} {
		name := filepath.ToSlash(filepath.Join(
			"relationship-resolver-namespaces", "resolver-namespaces",
			repositoryHash(fixture.repository),
			"generation-"+strings.TrimPrefix(resolver.Root().GenerationDigest, "sha256:"),
			"root.json",
		))
		if !slices.Contains(divergentNames, name) {
			t.Fatalf("resolver generation %q missing from %q", name, divergentNames)
		}
	}
	restoredDivergent := t.TempDir()
	if err := RestoreArchive(t.Context(), divergentArchive, restoredDivergent); err != nil {
		t.Fatal(err)
	}
	legacyRestored, err := OpenCurrent(
		t.Context(), filepath.Join(restoredDivergent, "relationships"), fixture.repository,
	)
	if err != nil || legacyRestored.Root().Digest != legacyNext.Root().Digest {
		t.Fatalf("restored divergent legacy relationship = %+v, %v", legacyRestored, err)
	}
	assertArchiveV3Current(t, restoredDivergent, fixture.repository, shadow.Root())
}

func TestArchiveAdmissionBoundsAggregateUniqueFiles(t *testing.T) {
	files := make(map[string]archiveFile)
	first := archiveFile{path: "/source/a", name: "a", size: 3}
	total, err := admitArchiveFile(files, first, 0, 2, 5)
	if err != nil || total != 3 || len(files) != 1 {
		t.Fatalf("first archive admission = %d/%d, %v", total, len(files), err)
	}
	if total, err = admitArchiveFile(files, first, total, 2, 5); err != nil || total != 3 {
		t.Fatalf("duplicate archive admission = %d, %v", total, err)
	}
	second := archiveFile{path: "/source/b", name: "b", size: 2}
	if total, err = admitArchiveFile(files, second, total, 2, 5); err != nil || total != 5 {
		t.Fatalf("second archive admission = %d, %v", total, err)
	}
	before := len(files)
	if got, err := admitArchiveFile(
		files, archiveFile{path: "/source/c", name: "c", size: 0}, total, 2, 5,
	); !errors.Is(err, ErrLimit) || got != total || len(files) != before {
		t.Fatalf("entry overflow = %d/%d, %v", got, len(files), err)
	}
	if got, err := admitArchiveFile(
		files, archiveFile{path: "/other/a", name: "a", size: 3}, total, 2, 5,
	); !errors.Is(err, ErrInvalid) || got != total || len(files) != before {
		t.Fatalf("path collision = %d/%d, %v", got, len(files), err)
	}
	empty := make(map[string]archiveFile)
	if got, err := admitArchiveFile(
		empty, archiveFile{path: "/source/large", name: "large", size: 6}, 0, 2, 5,
	); !errors.Is(err, ErrLimit) || got != 0 || len(empty) != 0 {
		t.Fatalf("byte overflow = %d/%d, %v", got, len(empty), err)
	}
}

type archiveV3Fixture struct {
	dataDir     string
	repository  string
	catalog     servicecatalog.Publication
	states      []servicecatalog.ServiceState
	upstream    downstreamauthority.Authority
	resolver    *resolvernamespace.Publication
	rpc         *rpccallerposting.Publication
	kafka       *kafkatopicposting.Publication
	observation observationpublication.DownstreamAuthority
}

func newArchiveV3Fixture(t *testing.T) archiveV3Fixture {
	t.Helper()
	dataDir := t.TempDir()
	catalog, states := relationshipCatalog(t)
	repository := catalog.Repository
	for _, name := range []string{
		"relationships", "relationship-resolver-namespaces",
		"relationship-rpc-postings", "relationship-kafka-postings",
	} {
		if err := os.Mkdir(filepath.Join(dataDir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: repository,
		SourceGenerationDigest: fixedDigest("1"), SourceRootDigest: fixedDigest("2"),
		ObservationGenerationDigest: fixedDigest("3"), ObservationRootDigest: fixedDigest("4"),
		PartitionPolicyDigest: fixedDigest("5"), ObservationPolicyDigest: fixedDigest("6"),
		InventoryPolicyDigest: fixedDigest("7"),
	}
	domain := candidate.DownstreamDomainAuthority{
		Domain: "proto-contract", Version: "v1", PlanDigest: fixedDigest("8"),
		RootDigest: fixedDigest("9"), RunID: "partition-run",
		Disposition:             candidate.PartitionResultEmpty,
		CandidateManifestDigest: fixedDigest("a"), CandidatePartitionRootDigest: fixedDigest("b"),
		CandidatePolicyDigest: fixedDigest("c"), SourceGenerationDigest: observation.SourceGenerationDigest,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ExtractionPolicyDigest:      fixedDigest("d"), DomainIndexDigest: fixedDigest("e"),
		DomainScheduleDigest: fixedDigest("f"),
	}
	upstream, err := downstreamauthority.Build(
		observation, []candidate.DownstreamDomainAuthority{domain},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverStage, err := resolvernamespace.BuildV2(t.Context(), resolvernamespace.BuildRequestV2{
		BuildRequest: resolvernamespace.BuildRequest{
			Root:       filepath.Join(dataDir, "relationship-resolver-namespaces"),
			Repository: repository, Commit: strings.Repeat("a", 40),
			ResolverGenerationDigest: fixedDigest("1"), ResolverManifestDigest: fixedDigest("2"),
			Descriptors: []gocaller.DirectDescriptor{},
		},
		Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := resolverStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source := fakeDownstreamSource{authority: observation}
	rpcStage, err := rpccallerposting.BuildV2(t.Context(), rpccallerposting.BuildRequestV2{
		Root: filepath.Join(dataDir, "relationship-rpc-postings"), Observations: source,
		Resolver: resolver, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	rpc, err := rpcStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	kafkaStage, err := kafkatopicposting.BuildV2(t.Context(), kafkatopicposting.BuildRequestV2{
		Root:         filepath.Join(dataDir, "relationship-kafka-postings"),
		Observations: source, Upstream: upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	kafka, err := kafkaStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	legacyStage, err := BuildV2(t.Context(), BuildRequestV2{
		BuildRequest: BuildRequest{
			Root: filepath.Join(dataDir, "relationships"), Catalog: catalog, States: states,
			Resolver: resolver, RPC: rpc, Kafka: kafka,
		},
		Upstream: upstream, ServiceSummary: relationshipSummary(t, catalog, states),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyStage.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	return archiveV3Fixture{
		dataDir: dataDir, repository: repository, catalog: catalog, states: states,
		upstream: upstream, resolver: resolver, rpc: rpc, kafka: kafka,
		observation: observation,
	}
}

func (fixture archiveV3Fixture) publishV3(t *testing.T) *PublicationV3 {
	t.Helper()
	generation, states, summary := archiveV3CatalogState(t, fixture.catalog)
	prepared, err := BuildV3(t.Context(), BuildRequestV3{
		Root: filepath.Join(fixture.dataDir, "relationships"), Catalog: generation,
		States: states, ServiceSummary: summary, Resolver: fixture.resolver,
		RPC: fixture.rpc, Kafka: fixture.kafka, Upstream: fixture.upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := PublishV3(t.Context(), prepared, archiveV3Pins{})
	if err != nil {
		t.Fatal(err)
	}
	return publication
}

func (fixture archiveV3Fixture) publishLegacyWithNewResolver(
	t *testing.T,
) (*Publication, *resolvernamespace.Publication) {
	t.Helper()
	resolverStage, err := resolvernamespace.BuildV2(t.Context(), resolvernamespace.BuildRequestV2{
		BuildRequest: resolvernamespace.BuildRequest{
			Root:       filepath.Join(fixture.dataDir, "relationship-resolver-namespaces"),
			Repository: fixture.repository, Commit: strings.Repeat("a", 40),
			ResolverGenerationDigest: fixedDigest("0"), ResolverManifestDigest: fixedDigest("d"),
			Descriptors: []gocaller.DirectDescriptor{},
		},
		Upstream: fixture.upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := resolverStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rpcStage, err := rpccallerposting.BuildV2(t.Context(), rpccallerposting.BuildRequestV2{
		Root:         filepath.Join(fixture.dataDir, "relationship-rpc-postings"),
		Observations: fakeDownstreamSource{authority: fixture.observation},
		Resolver:     resolver, Upstream: fixture.upstream,
	})
	if err != nil {
		t.Fatal(err)
	}
	rpc, err := rpcStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stage, err := BuildV2(t.Context(), BuildRequestV2{
		BuildRequest: BuildRequest{
			Root:    filepath.Join(fixture.dataDir, "relationships"),
			Catalog: fixture.catalog, States: fixture.states,
			Resolver: resolver, RPC: rpc, Kafka: fixture.kafka,
		},
		Upstream:       fixture.upstream,
		ServiceSummary: relationshipSummary(t, fixture.catalog, fixture.states),
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := stage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return publication, resolver
}

func archiveV3CatalogState(
	t *testing.T,
	publication servicecatalog.Publication,
) (servicecatalogv3.Generation, []servicecatalog.ServiceState, servicecatalog.RepositoryState) {
	t.Helper()
	catalog, err := servicecatalog.Decode(publication.Canonical)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := servicecatalogv3.FromV2(publication, catalog)
	if err != nil {
		t.Fatal(err)
	}
	states := make([]servicecatalog.ServiceState, 0, generation.Root.Services)
	for index, descriptor := range generation.Root.ServiceMembers {
		projections, err := servicecatalogv3.ProjectServiceMember(
			generation.Root, descriptor, generation.Members[index].Content,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, projection := range projections {
			if projection.Service.Disposition != servicecatalog.DispositionAccepted {
				continue
			}
			desired, err := servicecatalogv3.ServiceDesiredGeneration(projection, 1)
			if err != nil {
				t.Fatal(err)
			}
			state := servicecatalog.ServiceState{
				Schema:     servicecatalogv3.ServiceStateSchema,
				Repository: generation.Root.Binding.Repository,
				ServiceKey: projection.Service.Key, DisplayName: projection.Service.DisplayName,
				Disposition: projection.Service.Disposition, Origin: projection.Service.Origin,
				Successors: slices.Clone(projection.Service.Successors), Incarnation: 1,
				DesiredGeneration: desired, DesiredSourceGeneration: projection.SourceGeneration,
				DesiredCatalogGeneration: projection.CatalogGeneration,
				Status:                   servicecatalog.StatusUnavailable, ControlRevision: 1,
				ChangedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
			}
			if err := servicecatalogv3.SetServiceStateDigest(&state); err != nil {
				t.Fatal(err)
			}
			states = append(states, state)
		}
	}
	summary := servicecatalog.RepositoryState{
		Schema:            servicecatalogv3.RepositoryStateSchema,
		Repository:        generation.Root.Binding.Repository,
		CatalogGeneration: generation.Root.Digest, CatalogControlRevision: 1,
		CatalogServiceCount: generation.Root.Services, LiveServiceCount: len(states),
		UnavailableCount: len(states), TombstoneCount: generation.Root.Services - len(states),
		ControlRevision: 1, UpdatedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
	}
	if err := servicecatalogv3.SetRepositoryStateDigest(&summary); err != nil {
		t.Fatal(err)
	}
	return generation, states, summary
}

func assertArchiveV3Current(
	t *testing.T,
	dataDir, repository string,
	want RootV3,
) {
	t.Helper()
	root := filepath.Join(dataDir, "relationships")
	pointer, err := ReadPointerV3(t.Context(), root, repository)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := ValidateGenerationV3(
		t.Context(), root, repository, pointer.GenerationDigest, pointer.RootDigest,
	)
	if err != nil || publication.Root().Digest != want.Digest {
		t.Fatalf("restored v3 relationship = %+v, %v", publication, err)
	}
}

func archiveV3EntryNames(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	reader := tar.NewReader(file)
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
}

type archiveV3Pins struct{}

func (archiveV3Pins) PinRelationshipPublicationV3(
	context.Context, string, string, string, string, uint64, uint64, string,
) error {
	return nil
}

func (archiveV3Pins) UnpinRelationshipPublicationV3(
	context.Context, string, string, string, string, uint64, uint64, string,
) error {
	return nil
}

func (archiveV3Pins) PinPartitionedExtractionRun(context.Context, string, string) error {
	return nil
}

func (archiveV3Pins) UnpinPartitionedExtractionRun(context.Context, string, string) error {
	return nil
}
