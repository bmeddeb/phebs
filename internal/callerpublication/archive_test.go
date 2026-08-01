package callerpublication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
)

func TestCallerPublicationArchiveExactRestore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "caller-leaves")
	manifest := installArchivePublication(
		t, root, "github.com/acme/archive", func(context.Context, State) error { return nil },
	)
	archive := filepath.Join(t.TempDir(), "caller-publication.tar")
	report, err := CreateArchiveWithReport(root, archive)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, ArchiveReport{Publications: 1}) {
		t.Fatalf("archive report = %+v", report)
	}
	verified, err := VerifyArchiveWithReport(archive)
	if err != nil || !reflect.DeepEqual(verified, ArchiveReport{Publications: 1}) {
		t.Fatalf("verify report = %+v, %v", verified, err)
	}
	restored := filepath.Join(t.TempDir(), "caller-leaves")
	if err := RestoreArchive(archive, restored); err != nil {
		t.Fatal(err)
	}
	assertCallerArchiveFilesEqual(t, root, restored)
	publication, err := Open(t.Context(), restored, manifest.State())
	if err != nil || !publication.Current() {
		t.Fatalf("restored caller publication = current:%v, %v", publication != nil && publication.Current(), err)
	}
	reconcileState := &reconcileTestStore{
		repositories: []string{manifest.Generation.Repository},
		pointers:     make(map[string]State),
	}
	reconcileReport, err := Reconcile(
		t.Context(), restored, reconcileState,
		Expected{Extractors: manifest.Generation.Extractors}, NewRegistry(restored),
	)
	if err != nil || reconcileReport.ReplacementsQueued != 1 ||
		reconcileReport.OrphansObserved != 1 || len(reconcileState.forced) != 1 ||
		reconcileState.forced[0] != manifest.Generation.Repository ||
		len(reconcileState.pointers) != 0 {
		t.Fatalf(
			"restored pointerless reconcile = report:%+v forced:%v pointers:%v err:%v",
			reconcileReport, reconcileState.forced, reconcileState.pointers, err,
		)
	}

	second := filepath.Join(t.TempDir(), "caller-publication.tar")
	if _, err := CreateArchiveWithReport(root, second); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("equivalent caller publications produced different archives")
	}
}

func TestCallerPublicationArchiveOmitsIncompleteAndUnreferencedState(t *testing.T) {
	t.Run("publication marker", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "caller-leaves")
		installArchivePublication(
			t, root, "github.com/acme/marked",
			func(context.Context, State) error { return errors.New("store unavailable") },
		)
		archive := filepath.Join(t.TempDir(), "caller-publication.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 0 || report.OmittedPublications != 1 ||
			report.OmittedArtifacts != 1 || report.StaleMarkers != 1 {
			t.Fatalf("marker archive report = %+v", report)
		}
		assertCallerArchiveEmpty(t, archive)
	})

	t.Run("invalid publication", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "caller-leaves")
		manifest := installArchivePublication(
			t, root, "github.com/acme/invalid", func(context.Context, State) error { return nil },
		)
		artifact := ArtifactRefs(manifest)[1]
		path := filepath.Join(root, artifact.RepositoryDirectory, artifact.Name)
		if err := os.WriteFile(path, []byte("corrupt\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(t.TempDir(), "caller-publication.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 0 || report.OmittedPublications != 1 ||
			report.OmittedArtifacts != 1 {
			t.Fatalf("invalid archive report = %+v", report)
		}
		assertCallerArchiveEmpty(t, archive)
	})

	t.Run("ambiguous manifests", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "caller-leaves")
		manifest := installArchivePublication(
			t, root, "github.com/acme/ambiguous", func(context.Context, State) error { return nil },
		)
		state := manifest.State()
		directory := callerpublicationid.RepositoryDirectory(manifest.Generation.Repository)
		raw, err := os.ReadFile(filepath.Join(root, directory, state.Manifest))
		if err != nil {
			t.Fatal(err)
		}
		other := callerpublicationid.ManifestName(
			"sha256:"+strings.Repeat("1", 64), "sha256:"+strings.Repeat("2", 64),
		)
		if err := os.WriteFile(filepath.Join(root, directory, other), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(t.TempDir(), "caller-publication.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.Publications != 0 || report.OmittedPublications != 2 ||
			report.OmittedArtifacts != 1 || len(report.Details) != 3 ||
			report.Details[0].Reason != "ambiguous_publication" {
			t.Fatalf("ambiguous archive report = %+v", report)
		}
		assertCallerArchiveEmpty(t, archive)
	})

	t.Run("bounded omission receipt", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "caller-leaves")
		directory := filepath.Join(
			root, callerpublicationid.RepositoryDirectory("github.com/acme/orphans"),
		)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		for index := range MaxOmissionDetails + 6 {
			name := fmt.Sprintf(
				"leaf-v1-%064x-%064x.ndjson", index+1, index+1000,
			)
			if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		archive := filepath.Join(t.TempDir(), "caller-publication.tar")
		report, err := CreateArchiveWithReport(root, archive)
		if err != nil {
			t.Fatal(err)
		}
		if report.OmittedArtifacts != MaxOmissionDetails+6 ||
			len(report.Details) != MaxOmissionDetails || report.TruncatedDetails != 6 {
			t.Fatalf("bounded archive report = %+v", report)
		}
		assertCallerArchiveEmpty(t, archive)
	})
}

func installArchivePublication(
	t *testing.T,
	root, repository string,
	commit func(context.Context, State) error,
) Manifest {
	t.Helper()
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	generation, err := callerleaf.NewGenerationIdentity(callerleaf.GenerationIdentity{
		Repository: repository, HeadCommit: strings.Repeat("1", 40),
		DeclarationSetDigest: digestA, CandidateManifestDigest: digestB,
		CandidatePolicyDigest: digestA, ResolverGenerationDigest: digestB,
		ResolverManifestDigest: digestA,
		Extractors: []callerleaf.ExtractorIdentity{{
			Domain: "grpc-caller", Version: "1.5.0",
			LeafAdapterVersion: callerleaf.LeafAdapterV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := callerleaf.NewPairIdentity(
		generation, "grpc-caller", "1.5.0", callerleaf.LeafDescriptor{
			Name: "candidate-caller-000000.ndjson", Ordinal: 0,
			Prefix: "00", PrefixBits: 2, RecordCount: 1,
			DeclaredBytes: 32, ContentBytes: 64, ContentDigest: digestB,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := callerleaf.NewStage(root, generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	preparedLeaf, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := preparedLeaf.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildManifest(generation, []PairReceipt{{
		Pair: pair, Receipt: leaf.Receipt(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(t.Context(), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	var commitErr error
	publication, err := prepared.Publish(t.Context(), func(ctx context.Context, state State) error {
		commitErr = commit(ctx, state)
		return commitErr
	})
	if commitErr != nil {
		if err == nil {
			t.Fatal("caller publication ignored the commit failure")
		}
		return manifest
	}
	if err != nil {
		t.Fatalf("publish caller publication: %v", err)
	}
	if publication == nil || !publication.Current() {
		t.Fatal("installed caller publication is not current")
	}
	return manifest
}

func assertCallerArchiveEmpty(t *testing.T, archive string) {
	t.Helper()
	verified, err := VerifyArchiveWithReport(archive)
	if err != nil || !reflect.DeepEqual(verified, ArchiveReport{}) {
		t.Fatalf("empty archive verify = %+v, %v", verified, err)
	}
}

func assertCallerArchiveFilesEqual(t *testing.T, left, right string) {
	t.Helper()
	files := func(root string) map[string][]byte {
		result := make(map[string][]byte)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			result[relative], err = os.ReadFile(path)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	want := files(left)
	got := files(right)
	if len(got) != len(want) {
		t.Fatalf("restored files = %d, want %d", len(got), len(want))
	}
	for name, expected := range want {
		if !bytes.Equal(got[name], expected) {
			t.Fatalf("restored caller artifact %q differs", name)
		}
	}
}
