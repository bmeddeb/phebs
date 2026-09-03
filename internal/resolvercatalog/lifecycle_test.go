package resolvercatalog

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
)

func testDigest(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

func testGenerationDigest(fill byte) string {
	return "extraction_generation_v1_" + strings.Repeat(string(fill), 64)
}

func testIdentity(t *testing.T, repository string) Identity {
	t.Helper()
	identity, err := NewIdentity(
		repository, strings.Repeat("1", 40), "", testDigest('a'),
		nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestRunIDIsProvenanceOutsideV2Authority(t *testing.T) {
	t.Parallel()
	declaration := DeclarationPublication{
		Domain: "proto-contract", RunID: "run-a",
		GenerationDigest: testDigest('4'),
		AuthoritySchema:  store.PartitionedExtractionDomainSchema,
		PlanDigest:       testDigest('3'), RootDigest: testDigest('4'),
	}
	identityA, err := NewIdentity(
		"github.com/acme/run-id-authority", strings.Repeat("1", 40), "",
		testDigest('2'), []DeclarationPublication{declaration}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	declaration.RunID = "run-b"
	identityB, err := NewIdentity(
		identityA.Repository, identityA.Commit, identityA.UnitDigest,
		identityA.CandidateManifestDigest,
		[]DeclarationPublication{declaration}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if identityA.DeclarationSetDigest != identityB.DeclarationSetDigest ||
		identityA.GenerationDigest != identityB.GenerationDigest {
		t.Fatalf("run ID changed semantic identity: A=%+v B=%+v", identityA, identityB)
	}

	seal := func(identity Identity) Manifest {
		stage, stageErr := NewStage(t.TempDir(), identity)
		if stageErr != nil {
			t.Fatal(stageErr)
		}
		prepared, sealErr := stage.Seal(t.Context())
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		return prepared.manifest
	}
	manifestA := seal(identityA)
	manifestB := seal(identityB)
	if manifestA.AuthorityDigest != manifestB.AuthorityDigest {
		t.Fatalf("run ID changed manifest authority: %q != %q", manifestA.AuthorityDigest, manifestB.AuthorityDigest)
	}
	if manifestA.Digest == manifestB.Digest {
		t.Fatal("exact manifest integrity digest ignored run ID provenance")
	}
	sharedRoot := t.TempDir()
	install := func(identity Identity) State {
		stage, stageErr := NewStage(sharedRoot, identity)
		if stageErr != nil {
			t.Fatal(stageErr)
		}
		prepared, sealErr := stage.Seal(t.Context())
		if sealErr != nil {
			t.Fatal(sealErr)
		}
		state, installErr := prepared.Install(t.Context())
		if installErr != nil {
			t.Fatal(installErr)
		}
		if clearErr := ClearPublishing(sharedRoot, identity.Repository); clearErr != nil {
			t.Fatal(clearErr)
		}
		return state
	}
	installedA := install(identityA)
	installedB := install(identityB)
	if installedA.GenerationDigest != installedB.GenerationDigest ||
		installedA.AuthorityDigest != installedB.AuthorityDigest ||
		installedA.ManifestDigest == installedB.ManifestDigest {
		t.Fatalf("shared-root run replay = %+v / %+v", installedA, installedB)
	}

	changed := declaration
	changed.RootDigest = testDigest('5')
	changed.GenerationDigest = changed.RootDigest
	identityChanged, err := NewIdentity(
		identityA.Repository, identityA.Commit, identityA.UnitDigest,
		identityA.CandidateManifestDigest,
		[]DeclarationPublication{changed}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if identityChanged.GenerationDigest == identityA.GenerationDigest {
		t.Fatal("semantic declaration change preserved generation authority")
	}
}

func TestHistoricalV1IdentityAndManifestRemainValid(t *testing.T) {
	t.Parallel()
	declaration := DeclarationPublication{
		Domain: "proto-contract", RunID: "historical-run",
		GenerationDigest: testDigest('4'),
		AuthoritySchema:  store.PartitionedExtractionDomainSchema,
		PlanDigest:       testDigest('3'), RootDigest: testDigest('4'),
	}
	identity := Identity{
		Repository: "github.com/acme/historical-resolver", Commit: strings.Repeat("1", 40),
		Declarations:            []DeclarationPublication{declaration},
		CandidateManifestDigest: testDigest('2'), SourceLanePolicy: SourceLanePolicy,
		ResolverPacks: []ResolverPack{}, CatalogPolicy: frozenPolicyV1(),
	}
	var err error
	identity.DeclarationSetDigest, err = digestCanonical(identity.Declarations)
	if err != nil {
		t.Fatal(err)
	}
	identity.ResolverPackSetDigest, err = digestCanonical(identity.ResolverPacks)
	if err != nil {
		t.Fatal(err)
	}
	identity.CatalogPolicyDigest, err = digestCanonical(identity.CatalogPolicy)
	if err != nil {
		t.Fatal(err)
	}
	generation := identity
	identity.GenerationDigest, err = digestCanonical(generation)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Schema: ManifestSchemaV1, Identity: identity, Members: []MemberReceipt{}}
	manifest.Digest, err = manifestIntegrityDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateManifest(manifest); err != nil {
		t.Fatalf("historical v1 manifest rejected: %v", err)
	}
	manifest.AuthorityDigest = testDigest('9')
	if err := validateManifest(manifest); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("v1 manifest accepted v2 authority digest: %v", err)
	}
}

func testPrepared(
	t *testing.T,
	root, repository string,
	withMember bool,
) *Prepared {
	t.Helper()
	stage, err := NewStage(root, testIdentity(t, repository))
	if err != nil {
		t.Fatal(err)
	}
	if withMember {
		err = stage.AddMember(
			t.Context(), "go.ndjson", json.RawMessage(`{"pack":"neutral"}`),
			func(write func(json.RawMessage) error) error {
				for _, raw := range []string{
					`{"name":"a","schema":"phebs-resolver-catalog-record-v1"}`,
					`{"name":"b","schema":"phebs-resolver-catalog-record-v1"}`,
				} {
					if err := write(json.RawMessage(raw)); err != nil {
						return err
					}
				}
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := stage.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func testInstall(
	t *testing.T,
	root, repository string,
	withMember bool,
) State {
	t.Helper()
	state, err := testPrepared(t, root, repository, withMember).Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func testInstallWithMembers(
	t *testing.T,
	root, repository string,
	packs []ResolverPack,
	members ...string,
) State {
	t.Helper()
	identity, err := NewIdentity(
		repository, strings.Repeat("1", 40), "", testDigest('a'), nil, packs,
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := NewStage(root, identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range members {
		if err := stage.AddMember(
			t.Context(), name, json.RawMessage(`{}`),
			func(write func(json.RawMessage) error) error {
				return write(json.RawMessage(
					`{"schema":"phebs-resolver-catalog-record-v1"}`,
				))
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := stage.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestNeutralCatalogPublicationAndWarmCurrent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/neutral"
	state := testInstall(t, root, repository, false)
	if _, err := Open(t.Context(), root, state); !errors.Is(err, ErrPublishing) {
		t.Fatalf("Open while marked = %v, want ErrPublishing", err)
	}
	publishing, err := OpenPublishing(t.Context(), root, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(publishing.Manifest().Members) != 0 {
		t.Fatalf("neutral members = %+v, want empty", publishing.Manifest().Members)
	}
	if err := ClearPublishing(root, repository); err != nil {
		t.Fatal(err)
	}
	publication, err := Open(t.Context(), root, state)
	if err != nil {
		t.Fatal(err)
	}
	if !publication.Current() {
		t.Fatal("fresh publication is not current")
	}
	manifestPath := filepath.Join(root, state.Manifest)
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(
		manifestPath, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if publication.Current() {
		t.Fatal("warm check accepted changed file identity")
	}
}

func TestOpenWithVisitorProjectsColdValidationRecords(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/visitor"
	state := testInstall(t, root, repository, true)
	if err := ClearPublishing(root, repository); err != nil {
		t.Fatal(err)
	}
	var visited []string
	publication, err := OpenWithVisitor(
		t.Context(), root, state,
		func(member MemberReceipt, index int, raw json.RawMessage) error {
			visited = append(visited, member.Name+":"+strconv.Itoa(index)+":"+string(raw))
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if publication == nil || len(visited) != 2 ||
		!strings.Contains(visited[0], `go.ndjson:0:{"name":"a"`) ||
		!strings.Contains(visited[1], `go.ndjson:1:{"name":"b"`) {
		t.Fatalf("visited = %q", visited)
	}

	want := errors.New("stop projection")
	_, err = OpenWithVisitor(
		t.Context(), root, state,
		func(MemberReceipt, int, json.RawMessage) error { return want },
	)
	if !errors.Is(err, want) {
		t.Fatalf("visitor error = %v, want %v", err, want)
	}
}

func TestPublicationCurrentFailsClosedOnMarkerIO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/current-marker-io"
	state := testInstall(t, root, repository, false)
	if err := ClearPublishing(root, repository); err != nil {
		t.Fatal(err)
	}
	publication, err := Open(t.Context(), root, state)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, resolvercatalogid.PublishingName(repository))
	prior := testCatalogPathError
	testCatalogPathError = func(opened string) error {
		if opened == marker {
			return &os.PathError{Op: "lstat", Path: opened, Err: syscall.EMFILE}
		}
		return nil
	}
	t.Cleanup(func() { testCatalogPathError = prior })
	if publication.Current() {
		t.Fatal("publication remained current when marker authority could not be checked")
	}
}

func TestPublishCommitsStoreBeforeMarkerClear(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/publish-order"
	prepared := testPrepared(t, root, repository, false)
	committed := false
	state, err := prepared.Publish(
		t.Context(),
		func(ctx context.Context, state State) error {
			if !IsPublishing(root, repository) {
				t.Fatal("marker cleared before store commit")
			}
			if _, err := OpenPublishing(ctx, root, state); err != nil {
				t.Fatalf("manifest not durable before store commit: %v", err)
			}
			committed = true
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !committed || IsPublishing(root, repository) {
		t.Fatalf("committed=%v marker=%v", committed, IsPublishing(root, repository))
	}
	if _, err := Open(t.Context(), root, state); err != nil {
		t.Fatal(err)
	}
}

func TestPublishCleansOnlyRetiredLocalMembers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/cleanup"
	firstState := testInstall(t, root, repository, true)
	if err := ClearPublishing(root, repository); err != nil {
		t.Fatal(err)
	}
	firstManifest := readTestManifest(t, root, firstState)
	oldMember := filepath.Join(
		root, memberArtifactName(firstManifest.Identity, firstManifest.Members[0].Name),
	)
	foreign := filepath.Join(
		root, resolvercatalogid.ArtifactBase("github.com/other/repository")+"-v1-keep",
	)
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	nextIdentity, err := NewIdentity(
		repository, strings.Repeat("1", 40), "", testDigest('a'), nil,
		[]ResolverPack{{Name: "neutral", Version: "1.0.0"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := NewStage(root, nextIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.AddMember(
		t.Context(), "go.ndjson", json.RawMessage(`{"pack":"neutral"}`),
		func(write func(json.RawMessage) error) error {
			return write(json.RawMessage(
				`{"schema":"phebs-resolver-catalog-record-v1"}`,
			))
		},
	); err != nil {
		t.Fatal(err)
	}
	prepared, err := stage.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	nextState, err := prepared.Publish(
		t.Context(), func(context.Context, State) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldMember); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired member remains: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign member removed: %v", err)
	}
	if _, err := Open(t.Context(), root, nextState); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogMemberNamesRejectPortableAliases(t *testing.T) {
	tests := []struct {
		name   string
		first  string
		second string
	}{
		{"case fold", "A.ndjson", "a.ndjson"},
		{"unicode composition", "cafe\u0301.ndjson", "caf\u00e9.ndjson"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stage, err := NewStage(
				filepath.Join(t.TempDir(), "catalogs"),
				testIdentity(t, "github.com/acme/member-alias"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.AddMember(
				t.Context(), test.first, json.RawMessage(`{}`), nil,
			); err != nil {
				t.Fatal(err)
			}
			if err := stage.AddMember(
				t.Context(), test.second, json.RawMessage(`{}`), nil,
			); err == nil || !strings.Contains(err.Error(), "portable filesystem") {
				t.Fatalf("portable alias AddMember = %v, want collision refusal", err)
			}
		})
	}

	stage, err := NewStage(
		filepath.Join(t.TempDir(), "catalogs"),
		testIdentity(t, "github.com/acme/forged-member-alias"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A.ndjson", "b.ndjson"} {
		if err := stage.AddMember(
			t.Context(), name, json.RawMessage(`{}`), nil,
		); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := stage.Seal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	forged := cloneManifest(prepared.manifest)
	forged.Members[1].Name = "a.ndjson"
	unsigned := forged
	unsigned.Digest = ""
	forged.Digest, err = digestCanonical(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateManifest(forged); !errors.Is(err, ErrInvalidManifest) ||
		!strings.Contains(err.Error(), "portable filesystem") {
		t.Fatalf("forged portable aliases = %v, want ErrInvalidManifest", err)
	}
}

func TestInstallMarkerPreservesActivePublisherFence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "catalogs")
	repository := "github.com/acme/active-publisher"
	if err := ensureRealDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := installMarker(root, repository); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, resolvercatalogid.PublishingName(repository))
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := installMarker(root, repository); !errors.Is(err, ErrPublishing) {
		t.Fatalf("second installMarker = %v, want ErrPublishing", err)
	}
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, after) {
		t.Fatal("active publisher marker was overwritten")
	}
}

func TestRemoveRepositoryRejectsSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	repository := "github.com/acme/symlink-root"
	external := filepath.Join(parent, "external")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(
		external, resolvercatalogid.ArtifactBase(repository)+"-v1-member.ndjson",
	)
	if err := os.WriteFile(owned, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "resolver-catalogs")
	if err := os.Symlink(external, root); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRepository(t.Context(), root, repository); err == nil {
		t.Fatal("RemoveRepository accepted a symlink catalog root")
	}
	if raw, err := os.ReadFile(owned); err != nil || string(raw) != "keep" {
		t.Fatalf("symlink-root refusal changed external artifact: %q, %v", raw, err)
	}
}

func TestCleanupRetiredRejectsSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	repository := "github.com/acme/symlink-cleanup"
	prepared := testPrepared(
		t, filepath.Join(parent, "source"), repository, true,
	)
	external := filepath.Join(parent, "external")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(
		external, resolvercatalogid.OwnedArtifactPrefix(repository)+"stale.ndjson",
	)
	if err := os.WriteFile(stale, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "resolver-catalogs")
	if err := os.Symlink(external, root); err != nil {
		t.Fatal(err)
	}
	if err := CleanupRetired(root, prepared.manifest); err == nil {
		t.Fatal("CleanupRetired accepted a symlink catalog root")
	}
	if raw, err := os.ReadFile(stale); err != nil || string(raw) != "keep" {
		t.Fatalf("symlink-root refusal changed external artifact: %q, %v", raw, err)
	}
}

func TestCleanupRetiredContextHonorsCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "resolver-catalogs")
	repository := "github.com/acme/canceled-cleanup"
	prepared := testPrepared(t, root, repository, true)
	stale := filepath.Join(
		root, resolvercatalogid.OwnedArtifactPrefix(repository)+"stale.ndjson",
	)
	if err := os.WriteFile(stale, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CleanupRetiredContext(ctx, root, prepared.manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("CleanupRetiredContext = %v, want context.Canceled", err)
	}
	if raw, err := os.ReadFile(stale); err != nil || string(raw) != "keep" {
		t.Fatalf("canceled cleanup changed retired artifact: %q, %v", raw, err)
	}
}

func TestCatalogColdValidationRejectsTamperSymlinkAndDescriptorSwap(t *testing.T) {
	t.Run("tamper", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/tamper", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		memberPath := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		if err := os.WriteFile(memberPath, []byte(
			`{"name":"x","schema":"phebs-resolver-catalog-record-v1"}`+"\n",
		), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(t.Context(), root, state); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("tampered Open = %v, want ErrInvalidManifest", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/symlink", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		memberPath := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		realPath := filepath.Join(root, "outside")
		raw, err := os.ReadFile(memberPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(realPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(memberPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realPath, memberPath); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(t.Context(), root, state); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("symlink Open = %v, want ErrInvalidManifest", err)
		}
	})
	t.Run("descriptor swap", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/swap", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		memberPath := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		replacement := filepath.Join(root, "replacement")
		raw, err := os.ReadFile(memberPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(replacement, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		swapped := false
		testAfterStableOpen = func(opened string) {
			if opened == memberPath && !swapped {
				swapped = true
				if err := os.Rename(replacement, memberPath); err != nil {
					t.Fatalf("swap descriptor path: %v", err)
				}
			}
		}
		t.Cleanup(func() { testAfterStableOpen = nil })
		if _, err := Open(t.Context(), root, state); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("descriptor-swap Open = %v, want ErrInvalidManifest", err)
		}
	})
}

func TestCatalogBoundsAtCapAndCapPlusOne(t *testing.T) {
	policy := FrozenPolicy()
	if got := policy.MaxDirectoryEntries; got != MaxDirectoryEntries {
		t.Fatalf(
			"policy directory entries = %d, want %d",
			got, MaxDirectoryEntries,
		)
	}
	if policy.PublicationMemoryDesignBytes != PublicationMemoryDesignBytes {
		t.Fatalf("per-publication memory design budget = %+v", policy)
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(policyJSON, []byte(`"max_memory_bytes":67108864`)) ||
		bytes.Contains(policyJSON, []byte("max_per_publication_memory_bytes")) {
		t.Fatalf("policy v1 memory identity changed: %s", policyJSON)
	}
	if MaxPublicationDiskBytes != MaxStagingDiskBytes+
		MaxCatalogContentBytes+2*int64(MaxManifestBytes) {
		t.Fatalf(
			"publication disk peak = %d, want exact stage+live+manifests bound",
			MaxPublicationDiskBytes,
		)
	}
	if policy.MaxOpenFiles != 2 {
		t.Fatalf("lifecycle open descriptors = %d, want structural bound 2", policy.MaxOpenFiles)
	}
	declarations := make([]DeclarationPublication, MaxDeclarationPublications)
	for index := range declarations {
		declarations[index] = DeclarationPublication{
			Domain:           strings.Repeat("a", 4) + string(rune('a'+index)),
			RunID:            "run-" + string(rune('a'+index)),
			GenerationDigest: testGenerationDigest('b'),
		}
	}
	if _, err := NewIdentity(
		"github.com/acme/cap", strings.Repeat("1", 40), "", testDigest('a'),
		declarations, nil,
	); err != nil {
		t.Fatalf("identity at cap: %v", err)
	}
	over := append(slices.Clone(declarations), DeclarationPublication{
		Domain: "zzzz", RunID: "run-z",
		GenerationDigest: testGenerationDigest('b'),
	})
	if _, err := NewIdentity(
		"github.com/acme/over", strings.Repeat("1", 40), "", testDigest('a'),
		over, nil,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("identity cap+1 = %v, want ErrLimit", err)
	}

	root := filepath.Join(t.TempDir(), "catalogs")
	stage, err := NewStage(root, testIdentity(t, "github.com/acme/records"))
	if err != nil {
		t.Fatal(err)
	}
	err = stage.AddMember(
		t.Context(), "cap.ndjson", json.RawMessage(`{}`),
		func(write func(json.RawMessage) error) error {
			for range MaxRecordsPerMember {
				if err := write(json.RawMessage(
					`{"schema":"phebs-resolver-catalog-record-v1"}`,
				)); err != nil {
					return err
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("records at cap: %v", err)
	}

	directory := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(
			filepath.Join(directory, name), nil, 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readDirectoryUpTo(
		directory, 2,
	); !errors.Is(err, ErrLimit) {
		t.Fatalf("directory cap+1 = %v, want ErrLimit", err)
	}
	if err := os.Remove(filepath.Join(directory, "c")); err != nil {
		t.Fatal(err)
	}
	if entries, err := readDirectoryUpTo(
		directory, 2,
	); err != nil || len(entries) != 2 {
		t.Fatalf("directory at cap = %d, %v; want 2", len(entries), err)
	}
}

func TestCleanupStagesAndReconcileCrashBoundaries(t *testing.T) {
	t.Run("prior stage", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		if _, err := NewStage(root, testIdentity(t, "github.com/acme/stage")); err != nil {
			t.Fatal(err)
		}
		removed, err := CleanupStages(t.Context(), root)
		if err != nil || removed != 1 {
			t.Fatalf("CleanupStages = %d, %v; want 1", removed, err)
		}
	})
	t.Run("nested stage is never recursively removed", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		stage := filepath.Join(root, stageDirectoryPrefix+"nested")
		nested := filepath.Join(stage, "nested")
		if err := os.MkdirAll(nested, 0o700); err != nil {
			t.Fatal(err)
		}
		witness := filepath.Join(nested, "witness")
		if err := os.WriteFile(witness, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if removed, err := CleanupStages(t.Context(), root); err == nil {
			t.Fatalf("CleanupStages = %d, nil; want nested-stage refusal", removed)
		}
		if raw, err := os.ReadFile(witness); err != nil || string(raw) != "keep" {
			t.Fatalf("nested-stage refusal changed witness: %q, %v", raw, err)
		}
	})
	t.Run("manifest durable before store", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/recover"
		state := testInstall(t, root, repository, true)
		fake := newFakeReconcileStore()
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.MarkersRecovered != 1 || IsPublishing(root, repository) {
			t.Fatalf("report = %+v, marker=%v", report, IsPublishing(root, repository))
		}
		if got := fake.publications[repository]; got.ManifestDigest != state.ManifestDigest {
			t.Fatalf("recovered pointer = %+v, want %s", got, state.ManifestDigest)
		}
	})
	t.Run("store committed before marker clear", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/store-first"
		state := testInstall(t, root, repository, false)
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(state)
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.MarkersRecovered != 1 || report.ReplacementsQueued != 0 {
			t.Fatalf("report = %+v", report)
		}
	})
	t.Run("historical pointerless marker queues v2 replacement", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/historical-pointerless"
		if err := ensureRealDirectory(root); err != nil {
			t.Fatal(err)
		}
		identity := Identity{
			Repository: repository, Commit: strings.Repeat("1", 40),
			Declarations:            []DeclarationPublication{},
			CandidateManifestDigest: testDigest('2'), SourceLanePolicy: SourceLanePolicy,
			ResolverPacks: []ResolverPack{}, CatalogPolicy: frozenPolicyV1(),
		}
		var err error
		identity.DeclarationSetDigest, err = digestCanonical(identity.Declarations)
		if err != nil {
			t.Fatal(err)
		}
		identity.ResolverPackSetDigest, err = digestCanonical(identity.ResolverPacks)
		if err != nil {
			t.Fatal(err)
		}
		identity.CatalogPolicyDigest, err = digestCanonical(identity.CatalogPolicy)
		if err != nil {
			t.Fatal(err)
		}
		generation := identity
		identity.GenerationDigest, err = digestCanonical(generation)
		if err != nil {
			t.Fatal(err)
		}
		manifest := Manifest{
			Schema: ManifestSchemaV1, Identity: identity, Members: []MemberReceipt{},
		}
		manifest.Digest, err = manifestIntegrityDigest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, '\n')
		if err := os.WriteFile(
			filepath.Join(root, resolvercatalogid.ManifestName(repository)), raw, 0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := installMarker(root, repository); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.ReplacementsQueued != 1 || report.OrphansObserved != 1 ||
			!slices.Equal(fake.operations, []string{"queue:" + repository}) {
			t.Fatalf("historical reconcile = %+v operations=%v", report, fake.operations)
		}
		if IsPublishing(root, repository) {
			t.Fatal("historical publication marker survived v2 replacement queue")
		}
	})
	t.Run("replacement manifest durable before store", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/replacement-recover"
		oldState := testInstall(t, root, repository, false)
		if err := ClearPublishing(root, repository); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(oldState)

		identity, err := NewIdentity(
			repository, strings.Repeat("1", 40), "", testDigest('a'), nil,
			[]ResolverPack{{Name: "neutral", Version: "1.0.0"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		stage, err := NewStage(root, identity)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		replacementState, err := prepared.Install(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		report, err := Reconcile(
			t.Context(), root, fake,
			[]ResolverPack{{Name: "neutral", Version: "1.0.0"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		if report.MarkersRecovered != 1 ||
			report.ReplacementsQueued != 0 ||
			IsPublishing(root, repository) {
			t.Fatalf("report = %+v, marker=%v", report, IsPublishing(root, repository))
		}
		if got := fake.publications[repository]; got.ManifestDigest !=
			replacementState.ManifestDigest {
			t.Fatalf(
				"recovered replacement = %+v, want %s",
				got, replacementState.ManifestDigest,
			)
		}
		if !slices.Equal(fake.operations, []string{"publish:" + repository}) {
			t.Fatalf("operations = %v", fake.operations)
		}
	})
	t.Run("marker before manifest queues before cleanup", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/marker-only"
		if err := ensureRealDirectory(root); err != nil {
			t.Fatal(err)
		}
		if err := installMarker(root, repository); err != nil {
			t.Fatal(err)
		}
		owned := filepath.Join(root, resolvercatalogid.ArtifactBase(repository)+"-partial")
		if err := os.WriteFile(owned, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(root, resolvercatalogid.ArtifactBase("github.com/other/repo")+"-keep")
		if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.ReplacementsQueued != 1 || !slices.Equal(fake.operations, []string{"queue:" + repository}) {
			t.Fatalf("report=%+v operations=%v", report, fake.operations)
		}
		if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned partial remains: %v", err)
		}
		if _, err := os.Stat(other); err != nil {
			t.Fatalf("other repository artifact removed: %v", err)
		}
	})
	t.Run("invalid pointer queues before clear", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		if err := ensureRealDirectory(root); err != nil {
			t.Fatal(err)
		}
		repository := "github.com/acme/invalid-pointer"
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(
			testIdentityManifest(t, repository).State(),
		)
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.PointersCleared != 1 || !slices.Equal(fake.operations, []string{
			"queue:" + repository, "clear:" + repository,
		}) {
			t.Fatalf("report=%+v operations=%v", report, fake.operations)
		}
	})
	t.Run("maximum repository marker", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := strings.Repeat("a", reponame.MaxBytes)
		if err := ensureRealDirectory(root); err != nil {
			t.Fatal(err)
		}
		if err := installMarker(root, repository); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.ReplacementsQueued != 1 || !slices.Equal(
			fake.operations, []string{"queue:" + repository},
		) {
			t.Fatalf("report=%+v operations=%v", report, fake.operations)
		}
		if IsPublishing(root, repository) {
			t.Fatal("maximum-length marker survived recovery cleanup")
		}
	})
	t.Run("known pack mismatch reads no members", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/pack-mismatch"
		identity, err := NewIdentity(
			repository, strings.Repeat("1", 40), "", testDigest('a'), nil,
			[]ResolverPack{{Name: "neutral", Version: "1.0.0"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		stage, err := NewStage(root, identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.AddMember(
			t.Context(), "go.ndjson", json.RawMessage(`{}`),
			func(write func(json.RawMessage) error) error {
				return write(json.RawMessage(
					`{"schema":"phebs-resolver-catalog-record-v1"}`,
				))
			},
		); err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		state, err := prepared.Install(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(state)
		memberOpens := 0
		testAfterStableOpen = func(path string) {
			if strings.HasSuffix(path, ".ndjson") {
				memberOpens++
			}
		}
		t.Cleanup(func() { testAfterStableOpen = nil })
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if memberOpens != 0 || report.ReplacementsQueued != 1 ||
			report.PointersCleared != 1 {
			t.Fatalf("member opens=%d report=%+v", memberOpens, report)
		}
	})
	t.Run("current pointer sweeps undeclared owned debris", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/owned-debris"
		state := testInstall(t, root, repository, true)
		if err := ClearPublishing(root, repository); err != nil {
			t.Fatal(err)
		}
		owned := filepath.Join(
			root, resolvercatalogid.OwnedArtifactPrefix(repository)+"undeclared",
		)
		foreign := filepath.Join(
			root,
			resolvercatalogid.OwnedArtifactPrefix("github.com/acme/foreign")+"keep",
		)
		if err := os.WriteFile(owned, []byte("debris"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(state)
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.PublicationsCurrent != 1 || report.ReplacementsQueued != 0 {
			t.Fatalf("report = %+v", report)
		}
		if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("undeclared owned debris survived: %v", err)
		}
		if _, err := os.Stat(foreign); err != nil {
			t.Fatalf("foreign debris was removed: %v", err)
		}
	})
}

func TestReconcileMarkedSameGenerationConflictFailsClosed(t *testing.T) {
	newFixture := func(t *testing.T, repository string) (
		string, State, State, *fakeReconcileStore,
	) {
		t.Helper()
		root := filepath.Join(t.TempDir(), "catalogs")
		current := testInstallWithMembers(
			t, root, repository, nil, "go.ndjson",
		)
		if err := ClearPublishing(root, repository); err != nil {
			t.Fatal(err)
		}
		currentManifest := readTestManifest(t, root, current)
		stage, err := NewStage(root, currentManifest.Identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.AddMember(
			t.Context(), "go.ndjson", json.RawMessage(`{}`),
			func(write func(json.RawMessage) error) error {
				return write(json.RawMessage(
					`{"schema":"phebs-resolver-catalog-record-v1","variant":"challenger"}`,
				))
			},
		); err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		challenger, err := prepared.Install(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if challenger.GenerationDigest != current.GenerationDigest ||
			challenger.ManifestDigest == current.ManifestDigest {
			t.Fatalf(
				"fixture states do not conflict within one generation: current=%+v challenger=%+v",
				current, challenger,
			)
		}
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(current)
		fake.publishErr = store.ErrConflict
		return root, current, challenger, fake
	}

	t.Run("current authority retains pointer and marker across attempts", func(t *testing.T) {
		repository := "github.com/acme/marked-nondeterminism"
		root, current, challenger, fake := newFixture(t, repository)
		for attempt := 1; attempt <= 2; attempt++ {
			report, err := Reconcile(t.Context(), root, fake, nil)
			if !errors.Is(err, ErrNondeterministicPublication) {
				t.Fatalf(
					"attempt %d error = %v, want ErrNondeterministicPublication",
					attempt, err,
				)
			}
			if report.ReplacementsQueued != 0 || report.PointersCleared != 0 ||
				len(fake.operations) != 0 {
				t.Fatalf(
					"attempt %d mutated recovery state: report=%+v operations=%v",
					attempt, report, fake.operations,
				)
			}
			if got := fake.publications[repository]; got.ManifestDigest != current.ManifestDigest {
				t.Fatalf("attempt %d pointer = %+v, want current", attempt, got)
			}
			if !IsPublishing(root, repository) {
				t.Fatalf("attempt %d removed the nondeterminism marker", attempt)
			}
			if got := readTestManifest(t, root, challenger); got.Digest != challenger.ManifestDigest {
				t.Fatalf("attempt %d changed challenger manifest: %+v", attempt, got)
			}
		}
	})

	t.Run("stale authority keeps queue before clear recovery", func(t *testing.T) {
		repository := "github.com/acme/marked-stale-authority"
		root, _, _, fake := newFixture(t, repository)
		fake.stale[repository] = true
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if report.ReplacementsQueued != 1 || report.PointersCleared != 1 ||
			!slices.Equal(fake.operations, []string{
				"queue:" + repository, "clear:" + repository,
			}) {
			t.Fatalf("report=%+v operations=%v", report, fake.operations)
		}
		if IsPublishing(root, repository) {
			t.Fatal("stale marked publication remained fenced")
		}
	})
}

func TestReconcileResolverPack111Cutover(t *testing.T) {
	currentPacks := []ResolverPack{
		{Name: "go-module", Version: "1.1.1"},
		{Name: "grpc-generated-attribution", Version: "1.1.1"},
		{Name: "thrift-generated-attribution", Version: "1.1.1"},
	}
	for _, test := range []struct {
		name       string
		marked     bool
		queueFails bool
	}{
		{name: "current pointer"},
		{name: "marked publication", marked: true},
		{name: "queue failure preserves pointer", queueFails: true},
		{name: "queue failure preserves marker", marked: true, queueFails: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "catalogs")
			repository := "example.invalid/resolver-pack-upgrade"
			oldPacks := slices.Clone(currentPacks)
			for index := range oldPacks {
				oldPacks[index].Version = "1.1.0"
			}
			state := testInstallWithMembers(t, root, repository, oldPacks,
				"go-module-v2.ndjson", "grpc-generated-attribution-v2.ndjson", "thrift-generated-attribution-v2.ndjson")
			if !test.marked {
				if err := ClearPublishing(root, repository); err != nil {
					t.Fatal(err)
				}
			}
			fake := newFakeReconcileStore()
			fake.publications[repository] = storeFromState(state)
			if test.queueFails {
				fake.queueErr = errors.New("replacement queue unavailable")
			}
			memberOpens := 0
			priorOpen := testAfterStableOpen
			testAfterStableOpen = func(path string) {
				if strings.HasSuffix(path, ".ndjson") {
					memberOpens++
				}
			}
			t.Cleanup(func() { testAfterStableOpen = priorOpen })
			report, err := Reconcile(t.Context(), root, fake, currentPacks)
			if memberOpens != 0 {
				t.Fatalf("obsolete pack opened %d members before rejection", memberOpens)
			}
			if test.queueFails {
				if !errors.Is(err, fake.queueErr) || report.ReplacementsQueued != 0 || report.PointersCleared != 0 ||
					!slices.Equal(fake.operations, []string{"queue:" + repository}) || len(fake.jobs) != 0 {
					t.Fatalf("queue refusal = %+v, %v; operations=%v jobs=%v", report, err, fake.operations, fake.jobs)
				}
				if !statesEqual(stateFromStore(fake.publications[repository]), state) ||
					IsPublishing(root, repository) != test.marked ||
					readTestManifest(t, root, state).Digest != state.ManifestDigest {
					t.Fatal("queue failure changed prior pointer, manifest, or marker")
				}
				return
			}
			if err != nil || report.ReplacementsQueued != 1 || report.PointersCleared != 1 ||
				!slices.Equal(fake.operations, []string{"queue:" + repository, "clear:" + repository}) ||
				len(fake.jobs) != 1 || !fake.jobs[0].Force || fake.jobs[0].Kind != store.JobResolverCatalog {
				t.Fatalf("upgrade retirement = %+v, %v; operations=%v jobs=%v", report, err, fake.operations, fake.jobs)
			}
			if _, exists := fake.publications[repository]; exists || IsPublishing(root, repository) {
				t.Fatal("obsolete pack retained current authority after queued replacement")
			}
		})
	}
}

func TestReconcilePreservesAuthorityOnCatalogIO(t *testing.T) {
	inject := func(t *testing.T, target string) {
		t.Helper()
		prior := testCatalogPathError
		testCatalogPathError = func(opened string) error {
			if opened == target {
				return &os.PathError{Op: "open", Path: opened, Err: syscall.EMFILE}
			}
			return nil
		}
		t.Cleanup(func() { testCatalogPathError = prior })
	}
	assertPreserved := func(
		t *testing.T,
		root, repository string,
		fake *fakeReconcileStore,
		wantPointer bool,
	) {
		t.Helper()
		report, err := Reconcile(t.Context(), root, fake, nil)
		if !errors.Is(err, ErrCatalogIO) || !errors.Is(err, syscall.EMFILE) {
			t.Fatalf("Reconcile error = %v, want ErrCatalogIO/EMFILE", err)
		}
		if report.ReplacementsQueued != 0 || report.PointersCleared != 0 ||
			len(fake.operations) != 0 {
			t.Fatalf("I/O failure mutated recovery: report=%+v operations=%v", report, fake.operations)
		}
		_, pointerPresent := fake.publications[repository]
		if pointerPresent != wantPointer {
			t.Fatalf("pointer present = %v, want %v", pointerPresent, wantPointer)
		}
	}

	t.Run("marked current manifest read", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/marked-current-io"
		state := testInstallWithMembers(t, root, repository, nil, "go.ndjson")
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(state)
		inject(t, filepath.Join(root, state.Manifest))
		assertPreserved(t, root, repository, fake, true)
		if !IsPublishing(root, repository) {
			t.Fatal("marked current I/O failure removed marker")
		}
	})

	t.Run("unmarked current member read", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/unmarked-current-io"
		state := testInstallWithMembers(t, root, repository, nil, "go.ndjson")
		if err := ClearPublishing(root, repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		member := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(state)
		inject(t, member)
		assertPreserved(t, root, repository, fake, true)
	})

	t.Run("pointerless marked member read", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/pointerless-marked-io"
		state := testInstallWithMembers(t, root, repository, nil, "go.ndjson")
		manifest := readTestManifest(t, root, state)
		member := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		fake := newFakeReconcileStore()
		inject(t, member)
		assertPreserved(t, root, repository, fake, false)
		if !IsPublishing(root, repository) {
			t.Fatal("pointerless marked I/O failure removed marker")
		}
	})
}

func TestReconcilePreservesMarkedBytesOnStorePublishFailure(t *testing.T) {
	t.Run("pointer present", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/marked-store-io-current"
		current := testInstall(t, root, repository, false)
		if err := ClearPublishing(root, repository); err != nil {
			t.Fatal(err)
		}
		replacementIdentity, err := NewIdentity(
			repository, strings.Repeat("2", 40), "", testDigest('a'), nil, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		stage, err := NewStage(root, replacementIdentity)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := stage.Seal(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepared.Install(t.Context()); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(current)
		storeFailure := errors.New("resolver catalog store unavailable")
		fake.publishErr = storeFailure

		report, err := Reconcile(t.Context(), root, fake, nil)
		if !errors.Is(err, storeFailure) {
			t.Fatalf("Reconcile = %v, want store failure", err)
		}
		if report.ReplacementsQueued != 0 || report.PointersCleared != 0 ||
			len(fake.operations) != 0 ||
			fake.publications[repository].ManifestDigest != current.ManifestDigest {
			t.Fatalf(
				"store failure mutated current recovery: report=%+v operations=%v pointer=%+v",
				report, fake.operations, fake.publications[repository],
			)
		}
		if !IsPublishing(root, repository) {
			t.Fatal("store failure removed current recovery marker")
		}
	})

	t.Run("pointerless", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/marked-store-io-pointerless"
		_ = testInstall(t, root, repository, false)
		fake := newFakeReconcileStore()
		storeFailure := errors.New("resolver catalog store unavailable")
		fake.publishErr = storeFailure

		report, err := Reconcile(t.Context(), root, fake, nil)
		if !errors.Is(err, storeFailure) {
			t.Fatalf("Reconcile = %v, want store failure", err)
		}
		if report.ReplacementsQueued != 0 || report.PointersCleared != 0 ||
			len(fake.operations) != 0 || len(fake.publications) != 0 {
			t.Fatalf(
				"store failure mutated pointerless recovery: report=%+v operations=%v pointers=%v",
				report, fake.operations, fake.publications,
			)
		}
		if !IsPublishing(root, repository) {
			t.Fatal("store failure removed pointerless recovery marker")
		}
	})
}

func TestReconcileMarkedCatalogsStreamOnceAndUnfenceFailures(t *testing.T) {
	t.Run("corrupt current pointer streams each member once", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/corrupt-marked"
		state := testInstallWithMembers(
			t, root, repository, nil, "a.ndjson", "b.ndjson",
		)
		manifest := readTestManifest(t, root, state)
		corrupt := filepath.Join(
			root,
			memberArtifactName(manifest.Identity, manifest.Members[1].Name),
		)
		raw, err := os.ReadFile(corrupt)
		if err != nil {
			t.Fatal(err)
		}
		raw[0] = '['
		if err := os.WriteFile(corrupt, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		fake := newFakeReconcileStore()
		fake.publications[repository] = storeFromState(state)
		memberOpens := 0
		testAfterStableOpen = func(path string) {
			if strings.HasSuffix(path, ".ndjson") {
				memberOpens++
			}
		}
		t.Cleanup(func() { testAfterStableOpen = nil })
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if memberOpens != 2 || report.ReplacementsQueued != 1 ||
			report.PointersCleared != 1 {
			t.Fatalf("member opens=%d report=%+v", memberOpens, report)
		}
		if IsPublishing(root, repository) {
			t.Fatal("corrupt prior-process marker survived queued replacement")
		}
	})

	t.Run("pointerless pack mismatch opens no member and removes marker", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/pointerless-pack-mismatch"
		state := testInstallWithMembers(
			t, root, repository,
			[]ResolverPack{{Name: "neutral", Version: "1.0.0"}},
			"go.ndjson",
		)
		foreign := filepath.Join(
			root,
			resolvercatalogid.ArtifactBase("github.com/acme/foreign")+"-v1-keep",
		)
		if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		memberOpens := 0
		testAfterStableOpen = func(path string) {
			if strings.HasSuffix(path, ".ndjson") {
				memberOpens++
			}
		}
		t.Cleanup(func() { testAfterStableOpen = nil })
		fake := newFakeReconcileStore()
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if memberOpens != 0 || report.ReplacementsQueued != 1 ||
			report.OrphansObserved != 1 ||
			!slices.Equal(fake.operations, []string{"queue:" + repository}) {
			t.Fatalf(
				"member opens=%d report=%+v operations=%v",
				memberOpens, report, fake.operations,
			)
		}
		if IsPublishing(root, repository) {
			t.Fatal("pack-mismatched marker survived queued replacement")
		}
		if _, err := os.Stat(filepath.Join(root, state.Manifest)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pack-mismatched manifest survived: %v", err)
		}
		if _, err := os.Stat(foreign); err != nil {
			t.Fatalf("foreign namespace removed: %v", err)
		}
	})

	t.Run("guarded recovery rejection removes marker after queue", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		repository := "github.com/acme/rejected-marked-recovery"
		_ = testInstallWithMembers(t, root, repository, nil, "go.ndjson")
		fake := newFakeReconcileStore()
		fake.publishErr = store.ErrConflict
		memberOpens := 0
		testAfterStableOpen = func(path string) {
			if strings.HasSuffix(path, ".ndjson") {
				memberOpens++
			}
		}
		t.Cleanup(func() { testAfterStableOpen = nil })
		report, err := Reconcile(t.Context(), root, fake, nil)
		if err != nil {
			t.Fatal(err)
		}
		if memberOpens != 1 || report.ReplacementsQueued != 1 ||
			!slices.Equal(fake.operations, []string{"queue:" + repository}) {
			t.Fatalf(
				"member opens=%d report=%+v operations=%v",
				memberOpens, report, fake.operations,
			)
		}
		if IsPublishing(root, repository) {
			t.Fatal("store-rejected marker survived queued replacement")
		}
	})
}

func TestCatalogArchiveExactAndBoundedOmissions(t *testing.T) {
	t.Run("exact restore", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/archive", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(t.TempDir(), "catalog.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 1 || report.OmittedPublications != 0 {
			t.Fatalf("archive report = %+v", report)
		}
		restored := filepath.Join(t.TempDir(), "restored")
		if err := RestoreArchive(archive, restored); err != nil {
			t.Fatal(err)
		}
		assertDirectoryBytesEqual(t, root, restored)
	})
	t.Run("marker omission", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		_ = testInstall(t, root, "github.com/acme/marked", false)
		archive := filepath.Join(t.TempDir(), "catalog.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 0 || report.OmittedPublications != 1 ||
			report.StaleMarkers != 1 {
			t.Fatalf("archive report = %+v", report)
		}
		assertEmptyTar(t, archive)
	})
	t.Run("invalid publication omission", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		state := testInstall(t, root, "github.com/acme/invalid-archive", true)
		if err := ClearPublishing(root, state.Repository); err != nil {
			t.Fatal(err)
		}
		manifest := readTestManifest(t, root, state)
		memberPath := filepath.Join(
			root, memberArtifactName(manifest.Identity, manifest.Members[0].Name),
		)
		if err := os.WriteFile(memberPath, []byte("damage\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(t.TempDir(), "catalog.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 0 || report.OmittedPublications != 1 ||
			report.OmittedArtifacts < 2 {
			t.Fatalf("archive report = %+v", report)
		}
		assertEmptyTar(t, archive)
	})
	t.Run("bounded report", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "catalogs")
		if err := ensureRealDirectory(root); err != nil {
			t.Fatal(err)
		}
		for index := range MaxOmissionDetails + 6 {
			name := filepath.Join(root, "phebs-resolver-catalog-orphan-"+string(rune('a'+index%26))+
				strings.Repeat("x", index/26))
			if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		archive := filepath.Join(t.TempDir(), "catalog.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.OmittedArtifacts != MaxOmissionDetails+6 ||
			len(report.Details) != MaxOmissionDetails ||
			report.TruncatedDetails != 6 {
			t.Fatalf("archive report = %+v", report)
		}
	})
}

func readTestManifest(t *testing.T, root string, state State) Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, state.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func testIdentityManifest(t *testing.T, repository string) Manifest {
	t.Helper()
	manifest := Manifest{Schema: ManifestSchema, Identity: testIdentity(t, repository)}
	authorityDigest, err := manifestAuthorityDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.AuthorityDigest = authorityDigest
	digest, err := manifestIntegrityDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = digest
	return manifest
}

func assertDirectoryBytesEqual(t *testing.T, left, right string) {
	t.Helper()
	leftEntries, err := os.ReadDir(left)
	if err != nil {
		t.Fatal(err)
	}
	rightEntries, err := os.ReadDir(right)
	if err != nil {
		t.Fatal(err)
	}
	leftNames := make([]string, len(leftEntries))
	rightNames := make([]string, len(rightEntries))
	for index := range leftEntries {
		leftNames[index] = leftEntries[index].Name()
	}
	for index := range rightEntries {
		rightNames[index] = rightEntries[index].Name()
	}
	if !slices.Equal(leftNames, rightNames) {
		t.Fatalf("entry names differ: %v != %v", leftNames, rightNames)
	}
	for _, name := range leftNames {
		leftRaw, err := os.ReadFile(filepath.Join(left, name))
		if err != nil {
			t.Fatal(err)
		}
		rightRaw, err := os.ReadFile(filepath.Join(right, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(leftRaw) != string(rightRaw) {
			t.Fatalf("artifact %q differs", name)
		}
	}
}

func assertEmptyTar(t *testing.T, archive string) {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := tar.NewReader(file).Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty archive first entry = %v", err)
	}
}

type fakeReconcileStore struct {
	publications map[string]store.ResolverCatalogPublication
	jobs         []store.Job
	operations   []string
	publishErr   error
	queueErr     error
	stale        map[string]bool
}

func newFakeReconcileStore() *fakeReconcileStore {
	return &fakeReconcileStore{
		publications: make(map[string]store.ResolverCatalogPublication),
		stale:        make(map[string]bool),
	}
}

func (fake *fakeReconcileStore) ListResolverCatalogPublications(
	context.Context,
) ([]store.ResolverCatalogPublication, error) {
	values := make([]store.ResolverCatalogPublication, 0, len(fake.publications))
	for _, publication := range fake.publications {
		values = append(values, publication)
	}
	slices.SortFunc(values, func(left, right store.ResolverCatalogPublication) int {
		return strings.Compare(left.Repository, right.Repository)
	})
	return values, nil
}

func (fake *fakeReconcileStore) GetResolverCatalogPublication(
	_ context.Context,
	repository string,
) (*store.ResolverCatalogPublication, error) {
	publication, ok := fake.publications[repository]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &publication, nil
}

func (fake *fakeReconcileStore) ResolverCatalogPublicationCurrent(
	_ context.Context,
	publication store.ResolverCatalogPublication,
) (bool, error) {
	return !fake.stale[publication.Repository], nil
}

func (fake *fakeReconcileStore) PublishResolverCatalog(
	_ context.Context,
	publication store.ResolverCatalogPublication,
) error {
	if fake.publishErr != nil {
		return fake.publishErr
	}
	publication.ControlRevision = 1
	publication.WriterSchema = resolvercatalogid.WriterSchema
	publication.PublishedAt = time.Now().UTC()
	fake.publications[publication.Repository] = publication
	fake.operations = append(fake.operations, "publish:"+publication.Repository)
	return nil
}

func (fake *fakeReconcileStore) ClearResolverCatalogPublication(
	_ context.Context,
	repository string,
) error {
	delete(fake.publications, repository)
	fake.operations = append(fake.operations, "clear:"+repository)
	return nil
}

func (fake *fakeReconcileStore) EnqueuePending(
	_ context.Context,
	kind store.JobKind,
	target string,
	force bool,
) (*store.Job, error) {
	if kind != store.JobResolverCatalog || !force {
		return nil, errors.New("unexpected replacement request")
	}
	fake.operations = append(fake.operations, "queue:"+target)
	if fake.queueErr != nil {
		return nil, fake.queueErr
	}
	job := store.Job{ID: "job", Kind: kind, Target: target, Status: store.StatusPending, Force: force}
	fake.jobs = append(fake.jobs, job)
	return &job, nil
}
