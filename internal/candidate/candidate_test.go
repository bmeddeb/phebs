package candidate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/gitobj"
)

type gitFixture struct {
	t          *testing.T
	repository string
	directory  string
}

type cancelAfterErrChecks struct {
	context.Context
	remaining int
}

type syntheticUntrustedReader struct {
	remaining int64
	read      int64
	maxRead   int
}

func TestManifestReadSeparatesIOFailureFromIntegrityRefusal(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	if _, err := readManifest(missing); !errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("missing manifest error = %v", err)
	}
	ioErr := &os.PathError{Op: "read", Path: missing, Err: syscall.EIO}
	if err := classifyManifestReadError(ioErr); !errors.Is(err, syscall.EIO) ||
		errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("transient manifest I/O classification = %v", err)
	}

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(invalid); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("invalid manifest error = %v, want ErrInvalidManifest", err)
	}
}

func (reader *syntheticUntrustedReader) Read(buffer []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	count := len(buffer)
	if int64(count) > reader.remaining {
		count = int(reader.remaining)
	}
	for index := range buffer[:count] {
		buffer[index] = 'x'
	}
	reader.remaining -= int64(count)
	reader.read += int64(count)
	if count > reader.maxRead {
		reader.maxRead = count
	}
	return count, nil
}

func (ctx *cancelAfterErrChecks) Err() error {
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	ctx.remaining--
	return nil
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	fixture := &gitFixture{
		t: t, repository: "example.invalid/mono", directory: t.TempDir(),
	}
	fixture.git("init", "-q")
	fixture.git("config", "user.email", "candidate@example.invalid")
	fixture.git("config", "user.name", "Candidate Test")
	return fixture
}

func (fixture *gitFixture) write(name, content string) {
	fixture.t.Helper()
	fullPath := filepath.Join(fixture.directory, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *gitFixture) symlink(name, target string) {
	fixture.t.Helper()
	fullPath := filepath.Join(fixture.directory, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		fixture.t.Fatal(err)
	}
	if err := os.Symlink(target, fullPath); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *gitFixture) commit(message string) string {
	fixture.t.Helper()
	fixture.git("add", "-A")
	fixture.git("commit", "-q", "-m", message)
	return strings.TrimSpace(fixture.git("rev-parse", "HEAD"))
}

func (fixture *gitFixture) git(arguments ...string) string {
	fixture.t.Helper()
	command := exec.Command("git", append([]string{"-C", fixture.directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		fixture.t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func testPolicies() []Policy {
	return []Policy{
		{
			Domain: "go-local", Version: "1",
			EnumerationPolicy: "go-local-paths-v1", Plane: PlaneLocal,
			Enumerate: func(value string) bool {
				return strings.HasSuffix(value, ".go")
			},
			Required: func(value string) bool {
				return value == "src/main.go"
			},
		},
		{
			Domain: "go-caller", Version: "1",
			EnumerationPolicy: "go-caller-paths-v1", Plane: PlaneCaller,
			Enumerate: func(value string) bool {
				return strings.HasSuffix(value, ".go")
			},
		},
	}
}

func buildFixture(
	t *testing.T,
	fixture *gitFixture,
	commit string,
	unit *analysisunit.State,
	output string,
) (Manifest, Expected) {
	t.Helper()
	policies := testPolicies()
	identities, err := PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	expected := Expected{
		Repository: fixture.repository, Commit: commit,
		Unit: unit, Policies: identities,
	}
	manifest, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: output,
		Repository: fixture.repository, Commit: commit,
		Unit: unit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manifest, expected
}

func TestBuildIsDeterministicBoundedAndNilUnitSafe(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	fixture.write("src/other.go", "package main\n")
	fixture.write("README.md", "not retained\n")
	fixture.write("unsafe\nnoncandidate.txt", "still in the census\n")
	commit := fixture.commit("initial")

	firstDir := t.TempDir()
	first, expected := buildFixture(t, fixture, commit, nil, firstDir)
	secondDir := t.TempDir()
	second, _ := buildFixture(t, fixture, commit, nil, secondDir)
	if first.Digest != second.Digest ||
		first.GenerationDigest != second.GenerationDigest ||
		first.UnitDigest != "" {
		t.Fatalf("nondeterministic or scoped manifests:\n%+v\n%+v", first, second)
	}
	if first.Corpus.RegularCount != 4 ||
		first.Corpus.RegularDeclaredBytes <= 0 ||
		len(first.RepositoryMembers) != 1 ||
		first.LocalProjections == nil ||
		len(first.LocalProjections) != 0 ||
		len(first.CallerLeaves) == 0 {
		t.Fatalf("unexpected census/partition: %+v", first)
	}
	for _, leaf := range first.CallerLeaves {
		if leaf.RecordCount > MaxRecordsPerArtifact ||
			leaf.DeclaredBytes > MaxDeclaredBytesPerArtifact {
			t.Fatalf("unbounded leaf: %+v", leaf)
		}
	}
	publication, err := Open(firstDir, expected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(firstDir, first.State()); err != nil {
		t.Fatalf("nil-unit state open: %v", err)
	}
	local, err := publication.Domain("go-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	var paths, required []string
	if err := local.ForEachRepositoryRecord(
		t.Context(), func(record Record) error {
			paths = append(paths, record.Path)
			if record.Required {
				required = append(required, record.Path)
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(paths, []string{"src/main.go", "src/other.go"}) ||
		!slices.Equal(required, []string{"src/main.go"}) {
		t.Fatalf("paths/required = %v/%v", paths, required)
	}
	caller, err := publication.Domain("go-caller", "1")
	if err != nil {
		t.Fatal(err)
	}
	callerCounts := make(map[string]int)
	if err := caller.ForEachRepositoryRecord(
		t.Context(), func(record Record) error {
			callerCounts[record.Path]++
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(callerCounts, map[string]int{
		"src/main.go": 1, "src/other.go": 1,
	}) {
		t.Fatalf("caller candidate multiplicity = %v", callerCounts)
	}

	firstPrefixes := callerPrefixes(first)
	firstAssignments := callerAssignments(t, firstDir, first)
	fixture.write("src/main.go", "package main\n// content only\n")
	changedCommit := fixture.commit("content")
	changedDir := t.TempDir()
	changed, _ := buildFixture(t, fixture, changedCommit, nil, changedDir)
	if changed.Digest == first.Digest ||
		!slices.Equal(firstPrefixes, callerPrefixes(changed)) ||
		!maps.Equal(
			firstAssignments, callerAssignments(t, changedDir, changed),
		) {
		t.Fatalf(
			"content-only change digest/prefixes = %q/%q %v/%v",
			first.Digest, changed.Digest, firstPrefixes, callerPrefixes(changed),
		)
	}
}

func TestSourceLaneClassificationAndStrictRecomputation(t *testing.T) {
	fixture := newGitFixture(t)
	for path := range map[string]SourceLane{
		"src/main.go":                       SourceLaneBase,
		"src/main_test.go":                  SourceLaneGoTest,
		"generated/mock/fixture_test.go":    SourceLaneGoTest,
		"testdata/generated/client_test.go": SourceLaneGoTest,
		"src/generated_test.go.go":          SourceLaneBase,
		"src/test.go":                       SourceLaneBase,
	} {
		fixture.write(path, "package fixture\n")
	}
	commit := fixture.commit("source lanes")
	unit, err := (analysisunit.Scope{
		Repository: fixture.repository,
		Name:       "source-lane-fixture",
		Primary:    []string{"src"},
		Supporting: []string{"generated", "testdata"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manifest, expected := buildFixture(
		t, fixture, commit, unit, directory,
	)
	publication, err := Open(directory, expected)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]SourceLane{
		"src/main.go":                       SourceLaneBase,
		"src/main_test.go":                  SourceLaneGoTest,
		"generated/mock/fixture_test.go":    SourceLaneGoTest,
		"testdata/generated/client_test.go": SourceLaneGoTest,
		"src/generated_test.go.go":          SourceLaneBase,
		"src/test.go":                       SourceLaneBase,
	}
	for _, domain := range []string{"go-local", "go-caller"} {
		view, err := publication.Domain(domain, "1")
		if err != nil {
			t.Fatal(err)
		}
		got := make(map[string]SourceLane, len(want))
		if err := view.ForEachRepositoryRecord(
			t.Context(), func(record Record) error {
				got[record.Path] = record.SourceLane
				return nil
			},
		); err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(got, want) {
			t.Fatalf("%s source lanes = %v, want %v", domain, got, want)
		}
	}

	member := &manifest.RepositoryMembers[0]
	if !strings.Contains(member.Name, "-v4-") {
		t.Fatalf("candidate-v4 member namespace = %q", member.Name)
	}
	memberPath := filepath.Join(directory, member.Name)
	raw, err := os.ReadFile(memberPath)
	if err != nil {
		t.Fatal(err)
	}
	newline := bytes.IndexByte(raw, '\n')
	if newline < 0 {
		t.Fatal("repository member has no record")
	}
	var forged Record
	if err := strictCanonicalJSONLine(raw[:newline+1], &forged); err != nil {
		t.Fatal(err)
	}
	if forged.SourceLane == SourceLaneGoTest {
		forged.SourceLane = SourceLaneBase
	} else {
		forged.SourceLane = SourceLaneGoTest
	}
	line, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	line = append(line, '\n')
	replaced := append(slices.Clone(line), raw[newline+1:]...)
	if err := os.WriteFile(memberPath, replaced, 0o600); err != nil {
		t.Fatal(err)
	}
	member.ContentBytes = int64(len(replaced))
	member.ContentDigest = artifactDigest(replaced)
	rewriteManifest(t, directory, &manifest)
	if _, err := Open(directory, expected); err == nil ||
		!strings.Contains(err.Error(), "malformed candidate record") {
		t.Fatalf("forged source lane error = %v", err)
	}
}

func callerAssignments(
	t *testing.T,
	directory string,
	manifest Manifest,
) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, leaf := range manifest.CallerLeaves {
		if err := forEachCanonicalRecord(
			filepath.Join(directory, leaf.Name),
			func(record Record) error {
				if !hashHasPrefix(record.Hash, leaf.Prefix) {
					return errors.New("record is outside test leaf")
				}
				result[record.Path] = leaf.Prefix
				return nil
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func callerPrefixes(manifest Manifest) []string {
	result := make([]string, 0, len(manifest.CallerLeaves))
	for _, leaf := range manifest.CallerLeaves {
		result = append(result, leaf.Prefix)
	}
	return result
}

func TestBuildUnitFlagsAndSelectedSpecialRefusal(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	fixture.write("shared/types.go", "package shared\n")
	fixture.write("other/out.go", "package other\n")
	commit := fixture.commit("unit")
	unit, err := (analysisunit.Scope{
		Repository: fixture.repository, Name: "api",
		Primary: []string{"src"}, Supporting: []string{"shared"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	manifest, expected := buildFixture(t, fixture, commit, unit, stage)
	if _, err := Open(stage, expected); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(stage, manifest.State()); err != nil {
		t.Fatalf("state-only scoped open: %v", err)
	}
	publication, err := Open(stage, expected)
	if err != nil {
		t.Fatal(err)
	}
	view, err := publication.Domain("go-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string][2]bool{}
	if err := view.ForEachRepositoryRecord(
		t.Context(), func(record Record) error {
			flags[record.Path] = [2]bool{record.InUnit, record.Shared}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if flags["src/main.go"] != [2]bool{true, false} ||
		flags["shared/types.go"] != [2]bool{true, true} ||
		len(flags) != 2 {
		t.Fatalf("unit flags = %+v", flags)
	}
	caller, err := publication.Domain("go-caller", "1")
	if err != nil {
		t.Fatal(err)
	}
	var callerPaths []string
	if err := caller.ForEachRepositoryRecord(
		t.Context(), func(record Record) error {
			callerPaths = append(callerPaths, record.Path)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(callerPaths, "other/out.go") {
		t.Fatalf("caller repository view = %v", callerPaths)
	}

	special := newGitFixture(t)
	special.write("target.go", "package target\n")
	special.symlink("src", "target.go")
	specialCommit := special.commit("selected symlink")
	specialUnit, err := (analysisunit.Scope{
		Repository: special.repository, Name: "api", Primary: []string{"src"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(t.Context(), Request{
		RepoDir: special.directory, OutputDir: t.TempDir(),
		Repository: special.repository, Commit: specialCommit,
		Unit: specialUnit, Policies: testPolicies(),
	})
	if !errors.Is(err, ErrSelectedPath) {
		t.Fatalf("selected symlink error = %v", err)
	}

	unsafe := newGitFixture(t)
	unsafe.write("src/good.go", "package src\n")
	unsafe.write("src/unsafe\nmember.txt", "unsafe\n")
	unsafeCommit := unsafe.commit("unsafe selected descendant")
	unsafeUnit, err := (analysisunit.Scope{
		Repository: unsafe.repository, Name: "api", Primary: []string{"src"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(t.Context(), Request{
		RepoDir: unsafe.directory, OutputDir: t.TempDir(),
		Repository: unsafe.repository, Commit: unsafeCommit,
		Unit: unsafeUnit, Policies: testPolicies(),
	})
	if !errors.Is(err, ErrSelectedPath) {
		t.Fatalf("unsafe selected descendant error = %v", err)
	}
}

func TestTypedInputEnvelopeAndScopedDomainReplay(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("service/main.go", "package service\n")
	fixture.write("other/out.go", "package other\n")
	fixture.write("service/index.scip", "scoped typed input")
	fixture.write("service/alternate.scip", "alternate typed input")
	fixture.write("index.scip", "legacy root must not be selected")
	commit := fixture.commit("typed")
	unitScope := analysisunit.Scope{
		Repository: fixture.repository, Name: "service",
		Primary: []string{"service/main.go"},
		Supporting: []string{
			"service/index.scip", "service/alternate.scip",
		},
		TypedIndex: &analysisunit.TypedIndex{
			Kind: analysisunit.TypedIndexKindSCIP,
			Path: "service/index.scip",
		},
	}
	unit, err := unitScope.State()
	if err != nil {
		t.Fatal(err)
	}
	policies := []Policy{
		{
			Domain: "local", Version: "1",
			EnumerationPolicy: "typed-local-v1", Plane: PlaneLocal,
			TypedInputs: []string{analysisunit.TypedIndexKindSCIP},
			Enumerate: func(value string) bool {
				return strings.HasSuffix(value, ".go")
			},
			Required: func(value string) bool {
				return value == "service/main.go"
			},
		},
		{
			Domain: "caller", Version: "1",
			EnumerationPolicy: "typed-caller-v1", Plane: PlaneCaller,
			TypedInputs: []string{analysisunit.TypedIndexKindSCIP},
			Enumerate: func(value string) bool {
				return strings.HasSuffix(value, ".go")
			},
		},
	}
	identities, err := PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	manifest, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: stage,
		Repository: fixture.repository, Commit: commit,
		Unit: unit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TypedIndex == nil ||
		manifest.TypedIndex.Path != "service/index.scip" ||
		len(manifest.TypedInputs) != 1 ||
		!manifest.TypedInputs[0].Present ||
		manifest.TypedInputs[0].Path != "service/index.scip" ||
		!manifest.TypedInputs[0].InUnit ||
		!manifest.TypedInputs[0].Shared ||
		!slices.Equal(manifest.TypedInputs[0].Domains, []string{"caller", "local"}) {
		t.Fatalf("typed manifest = %+v / %+v", manifest.TypedIndex, manifest.TypedInputs)
	}
	if manifest.UnitCorpus.RegularCount != 3 ||
		manifest.UnitCorpus.RegularCount >= manifest.Corpus.RegularCount {
		t.Fatalf(
			"unit/repository corpus = %+v / %+v",
			manifest.UnitCorpus, manifest.Corpus,
		)
	}
	publication, err := Open(stage, Expected{
		Repository: fixture.repository, Commit: commit, Unit: unit,
		Policies: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	local, err := publication.Domain("local", "1")
	if err != nil {
		t.Fatal(err)
	}
	var localPaths []string
	if err := local.ForEachRepositoryRecord(
		t.Context(), func(record Record) error {
			localPaths = append(localPaths, record.Path)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(localPaths, []string{"service/main.go"}) {
		t.Fatalf("local replay = %v", localPaths)
	}
	caller, err := publication.Domain("caller", "1")
	if err != nil {
		t.Fatal(err)
	}
	var callerPaths []string
	if err := caller.ForEachRepositoryRecord(
		t.Context(), func(record Record) error {
			callerPaths = append(callerPaths, record.Path)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(callerPaths, []string{"other/out.go", "service/main.go"}) {
		t.Fatalf("caller replay = %v", callerPaths)
	}
	typed, err := local.TypedInput(analysisunit.TypedIndexKindSCIP)
	if err != nil {
		t.Fatal(err)
	}
	if typed == nil || typed.Path != "service/index.scip" ||
		!typed.Present || !gitobj.IsObjectID(typed.OID) {
		t.Fatalf("local typed input = %+v", typed)
	}
	for filePath, want := range map[string]bool{
		"service/main.go":    true,
		"service/index.scip": true,
		"other/out.go":       false,
	} {
		if got := publication.PathInScope(filePath); got != want {
			t.Fatalf("PathInScope(%q) = %t, want %t", filePath, got, want)
		}
	}

	alternateScope := unitScope
	alternateScope.TypedIndex = &analysisunit.TypedIndex{
		Kind: analysisunit.TypedIndexKindSCIP,
		Path: "service/alternate.scip",
	}
	alternate, err := alternateScope.State()
	if err != nil {
		t.Fatal(err)
	}
	if alternate.Digest != unit.Digest {
		t.Fatal("typed designation changed stable unit digest")
	}
	firstGeneration, err := GenerationDigest(
		fixture.repository, commit, unit, identities,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondGeneration, err := GenerationDigest(
		fixture.repository, commit, alternate, identities,
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstGeneration == secondGeneration {
		t.Fatal("typed designation reused candidate generation identity")
	}
}

func TestTypedInputLegacyDefaultAndConfiguredNoFallback(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("service/main.go", "package service\n")
	fixture.write("index.scip", "legacy root")
	commit := fixture.commit("legacy typed")
	policies := []Policy{
		{
			Domain: "local", Version: "1",
			EnumerationPolicy: "typed-local-v1", Plane: PlaneLocal,
			TypedInputs: []string{analysisunit.TypedIndexKindSCIP},
			Enumerate: func(value string) bool {
				return strings.HasSuffix(value, ".go")
			},
		},
		{
			Domain: "plain", Version: "1",
			EnumerationPolicy: "plain-local-v1", Plane: PlaneLocal,
			Enumerate: func(value string) bool {
				return strings.HasSuffix(value, ".go")
			},
		},
	}
	whole, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: t.TempDir(),
		Repository: fixture.repository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if whole.TypedIndex == nil || whole.TypedIndex.Path != "index.scip" ||
		len(whole.TypedInputs) != 1 || !whole.TypedInputs[0].Present ||
		whole.UnitCorpus != whole.Corpus {
		t.Fatalf("whole typed input = %+v / %+v", whole.TypedIndex, whole.TypedInputs)
	}

	unit, err := (analysisunit.Scope{
		Repository: fixture.repository, Name: "service",
		Primary: []string{"service"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	scopedDir := t.TempDir()
	scoped, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: scopedDir,
		Repository: fixture.repository, Commit: commit,
		Unit: unit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.TypedIndex != nil || len(scoped.TypedInputs) != 0 {
		t.Fatalf(
			"configured unit silently fell back to root: %+v / %+v",
			scoped.TypedIndex, scoped.TypedInputs,
		)
	}
	identities, err := PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := Open(scopedDir, Expected{
		Repository: fixture.repository,
		Commit:     commit,
		Unit:       unit,
		Policies:   identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	typedDomain, err := publication.Domain("local", "1")
	if err != nil {
		t.Fatal(err)
	}
	typed, err := typedDomain.TypedInput(analysisunit.TypedIndexKindSCIP)
	if err != nil || typed == nil || typed.Kind != analysisunit.TypedIndexKindSCIP ||
		typed.Present || typed.Path != "" {
		t.Fatalf("applicable absent typed input = %+v, %v", typed, err)
	}
	plainDomain, err := publication.Domain("plain", "1")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := plainDomain.TypedInput(analysisunit.TypedIndexKindSCIP)
	if err != nil || plain != nil {
		t.Fatalf("non-applicable typed input = %+v, %v", plain, err)
	}
}

func TestCandidateSymlinkLiteralLookupAndEscape(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write(":(glob)target.go", "package target\n")
	fixture.symlink("alias.go", ":(glob)target.go")
	commit := fixture.commit("literal")
	manifest, _ := buildFixture(t, fixture, commit, nil, t.TempDir())
	if manifest.Corpus.SymlinkCount != 1 ||
		manifest.Corpus.RegularCount != 1 {
		t.Fatalf("symlink census = %+v", manifest.Corpus)
	}

	escape := newGitFixture(t)
	escape.symlink("src/main.go", "../../../outside.go")
	escapeCommit := escape.commit("escape")
	_, err := Build(t.Context(), Request{
		RepoDir: escape.directory, OutputDir: t.TempDir(),
		Repository: escape.repository, Commit: escapeCommit,
		Policies: testPolicies(),
	})
	if err == nil || !strings.Contains(err.Error(), "escapes the repository") {
		t.Fatalf("escaping symlink error = %v", err)
	}

	nearLimit := strings.Repeat("a", maxCandidatePathBytes-4) + "/x"
	if _, err := resolveSymlinkTarget(
		nearLimit, "target",
	); err == nil {
		t.Fatalf("overlong resolved symlink target = %v", err)
	}
}

func TestEnumerationOnlySymlinkIsNotResolved(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.symlink("unrelated.go", "../missing.go")
	fixture.symlink("-generated.go", "missing.go")
	commit := fixture.commit("enumeration-only broken symlink")
	manifest, _ := buildFixture(t, fixture, commit, nil, t.TempDir())
	if manifest.Corpus.SymlinkCount != 2 ||
		len(manifest.RepositoryMembers) != 0 ||
		len(manifest.CallerLeaves) != 0 {
		t.Fatalf("enumeration-only symlink plan = %+v", manifest)
	}
}

func TestCandidateSymlinkDepthContract(t *testing.T) {
	t.Parallel()
	policies := []Policy{{
		Domain: "go", Version: "1",
		EnumerationPolicy: "all-go-v1", Plane: PlaneLocal,
		Enumerate: func(value string) bool {
			return strings.HasSuffix(value, ".go")
		},
		Required: func(value string) bool {
			return strings.HasSuffix(value, ".go")
		},
	}}
	for _, testCase := range []struct {
		links int
		ok    bool
	}{
		{links: maxSymlinkDepth, ok: true},
		{links: maxSymlinkDepth + 1},
	} {
		t.Run(fmt.Sprintf("%d", testCase.links), func(t *testing.T) {
			fixture := newGitFixture(t)
			fixture.write("src/target.go", "package target\n")
			for index := testCase.links - 1; index >= 0; index-- {
				name := fmt.Sprintf("src/link-%02d.go", index)
				target := "target.go"
				if index+1 < testCase.links {
					target = fmt.Sprintf("link-%02d.go", index+1)
				}
				fixture.symlink(name, target)
			}
			commit := fixture.commit("symlink depth")
			_, err := Build(t.Context(), Request{
				RepoDir: fixture.directory, OutputDir: t.TempDir(),
				Repository: fixture.repository, Commit: commit,
				Policies: policies,
			})
			if testCase.ok && err != nil {
				t.Fatalf("%d-link candidate = %v", testCase.links, err)
			}
			if !testCase.ok && (err == nil ||
				!strings.Contains(err.Error(), "exceeds 16-link depth")) {
				t.Fatalf("%d-link candidate = %v", testCase.links, err)
			}
		})
	}
}

func TestBuildRejectsCandidatePathThatCorpusCannotRead(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("-leading.go", "package leading\n")
	commit := fixture.commit("leading dash")
	_, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: t.TempDir(),
		Repository: fixture.repository, Commit: commit,
		Policies: testPolicies(),
	})
	if err == nil || !strings.Contains(
		err.Error(), "candidate Git path is not canonical and bounded",
	) {
		t.Fatalf("leading-dash candidate error = %v", err)
	}
}

func TestBuildTreatsPolicyShapedGitlinkAsCensusOnly(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("README.md", "fixture\n")
	gitlinkCommit := fixture.commit("base")
	fixture.git(
		"update-index", "--add", "--cacheinfo",
		"160000,"+gitlinkCommit+",vendor/schema.go",
	)
	fixture.git("commit", "-q", "-m", "policy-shaped gitlink")
	commit := strings.TrimSpace(fixture.git("rev-parse", "HEAD"))

	manifest, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: t.TempDir(),
		Repository: fixture.repository, Commit: commit,
		Policies: testPolicies(),
	})
	if err != nil {
		t.Fatalf("policy-shaped gitlink: %v", err)
	}
	if manifest.Corpus.GitlinkCount != 1 ||
		manifest.Corpus.RegularCount != 1 ||
		len(manifest.RepositoryMembers) != 0 ||
		len(manifest.CallerLeaves) != 0 {
		t.Fatalf("policy-shaped gitlink manifest = %+v", manifest)
	}
}

func TestCorpusEntryLimitIsEnforcedAtBuildAndOpen(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		record treeRecord
		fill   func(*corpusAccumulator)
		mutate func(*CorpusSummary)
	}{
		{
			name: "regular",
			record: treeRecord{
				mode: "100644", kind: "blob", oid: strings.Repeat("a", 40),
				size: 1, path: "a.go", rawPath: []byte("a.go"),
			},
			fill: func(corpus *corpusAccumulator) {
				corpus.regularCount = MaxCorpusEntries
			},
			mutate: func(summary *CorpusSummary) {
				summary.RegularCount = MaxCorpusEntries + 1
			},
		},
		{
			name: "gitlink",
			record: treeRecord{
				mode: "160000", kind: "commit", oid: strings.Repeat("b", 40),
				path: "vendor/module", rawPath: []byte("vendor/module"),
			},
			fill: func(corpus *corpusAccumulator) {
				corpus.gitlinkCount = MaxCorpusEntries
			},
			mutate: func(summary *CorpusSummary) {
				summary.GitlinkCount = MaxCorpusEntries + 1
			},
		},
		{
			name: "symlink",
			record: treeRecord{
				mode: "120000", kind: "blob", oid: strings.Repeat("c", 40),
				size: 1, path: "alias", rawPath: []byte("alias"),
			},
			fill: func(corpus *corpusAccumulator) {
				corpus.symlinkCount = MaxCorpusEntries
			},
			mutate: func(summary *CorpusSummary) {
				summary.SymlinkCount = MaxCorpusEntries + 1
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			corpus := newCorpusAccumulator()
			testCase.fill(corpus)
			if err := corpus.add(testCase.record); !errors.Is(
				err, ErrCorpusTooLarge,
			) {
				t.Fatalf("producer entry limit = %v", err)
			}

			summary := newCorpusAccumulator().summary()
			testCase.mutate(&summary)
			if err := validateCorpusSummary(
				t.Context(), summary, "",
			); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("manifest entry limit = %v", err)
			}
		})
	}
}

func TestBuildRejectsUntrustedObjectStoreAndNonCommit(t *testing.T) {
	t.Parallel()
	t.Run("external alternate", func(t *testing.T) {
		fixture := newGitFixture(t)
		fixture.write("source.go", "package source\n")
		commit := fixture.commit("alternate")
		alternates := filepath.Join(
			fixture.directory, ".git", "objects", "info", "alternates",
		)
		if err := os.WriteFile(
			alternates, []byte("/external/objects\n"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		_, err := Build(t.Context(), Request{
			RepoDir: fixture.directory, OutputDir: t.TempDir(),
			Repository: fixture.repository, Commit: commit,
			Policies: testPolicies(),
		})
		if !errors.Is(err, gitobj.ErrExternalAlternate) {
			t.Fatalf("external alternate build = %v", err)
		}
	})

	t.Run("tree object", func(t *testing.T) {
		fixture := newGitFixture(t)
		fixture.write("source.go", "package source\n")
		_ = fixture.commit("tree")
		tree := strings.TrimSpace(fixture.git("rev-parse", "HEAD^{tree}"))
		_, err := Build(t.Context(), Request{
			RepoDir: fixture.directory, OutputDir: t.TempDir(),
			Repository: fixture.repository, Commit: tree,
			Policies: testPolicies(),
		})
		if err == nil || !strings.Contains(err.Error(), "is not a commit") {
			t.Fatalf("tree object build = %v", err)
		}
	})
}

func TestCorpusCensusRejectsEveryUnsupportedLeaf(t *testing.T) {
	t.Parallel()
	for _, current := range []treeRecord{
		{
			mode: "100664", kind: "blob", oid: strings.Repeat("a", 40),
			path: "unrelated.txt", rawPath: []byte("unrelated.txt"),
		},
		{
			mode: "100644", kind: "commit", oid: strings.Repeat("a", 40),
			path: "unrelated.txt", rawPath: []byte("unrelated.txt"),
		},
		{
			mode: "160000", kind: "blob", oid: strings.Repeat("a", 40),
			path: "unrelated", rawPath: []byte("unrelated"),
		},
	} {
		if err := newCorpusAccumulator().add(current); err == nil ||
			!strings.Contains(err.Error(), "unsupported Git tree entry") {
			t.Fatalf("unsupported census record %+v = %v", current, err)
		}
	}
}

func TestRequiredMustBeEnumerated(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("required.txt", "required\n")
	commit := fixture.commit("required")
	_, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: t.TempDir(),
		Repository: fixture.repository, Commit: commit,
		Policies: []Policy{{
			Domain: "bad", Version: "1", EnumerationPolicy: "bad-v1",
			Plane:     PlaneLocal,
			Enumerate: func(string) bool { return false },
			Required:  func(string) bool { return true },
		}},
	})
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("required-outside-enumeration error = %v", err)
	}
}

func TestCanonicalLineReaderEnforcesBoundBeforeMaterialization(t *testing.T) {
	limit := maxTreeRecordBytes
	exact := bytes.Repeat([]byte{'x'}, limit)
	exact[len(exact)-1] = '\n'
	overflowWithNewline := bytes.Repeat([]byte{'x'}, limit+1)
	overflowWithNewline[len(overflowWithNewline)-1] = '\n'
	tests := []struct {
		name      string
		input     []byte
		wantBytes int
		wantError string
	}{
		{
			name: "exact bound with newline", input: exact,
			wantBytes: limit,
		},
		{
			name:  "bound plus one with newline",
			input: overflowWithNewline, wantError: "exceeds its byte limit",
		},
		{
			name:      "newline-free overflow",
			input:     bytes.Repeat([]byte{'x'}, limit+1),
			wantError: "exceeds its byte limit",
		},
		{
			name:      "newline-free exact bound",
			input:     bytes.Repeat([]byte{'x'}, limit),
			wantError: "partial trailing record",
		},
		{
			name:  "short partial",
			input: []byte("partial"), wantError: "partial trailing record",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			reader := bufio.NewReaderSize(
				bytes.NewReader(testCase.input), 64<<10,
			)
			line, err := readCanonicalLine(reader, limit)
			if testCase.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				if len(line) != testCase.wantBytes || cap(line) > limit {
					t.Fatalf(
						"line len/cap = %d/%d, want %d/cap<=%d",
						len(line), cap(line), testCase.wantBytes, limit,
					)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("line error = %v, want %q", err, testCase.wantError)
			}
			if line != nil {
				t.Fatalf("refused line retained %d bytes", len(line))
			}
		})
	}

	empty := bufio.NewReader(bytes.NewReader(nil))
	if line, err := readCanonicalLine(
		empty, limit,
	); line != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("empty line read = %d bytes / %v", len(line), err)
	}

	const untrustedMemberBytes = int64(256 << 20)
	source := &syntheticUntrustedReader{remaining: untrustedMemberBytes}
	reader := bufio.NewReaderSize(source, 64<<10)
	line, err := readCanonicalLine(reader, limit)
	if err == nil || !strings.Contains(err.Error(), "exceeds its byte limit") {
		t.Fatalf("virtual oversized line error = %v", err)
	}
	if line != nil {
		t.Fatalf("virtual oversized line retained %d bytes", len(line))
	}
	const readerBufferBytes = 64 << 10
	if source.read > int64(limit+readerBufferBytes) ||
		source.read >= untrustedMemberBytes ||
		source.maxRead > readerBufferBytes {
		t.Fatalf(
			"virtual oversized line read/max request = %d/%d, want <=%d/%d",
			source.read, source.maxRead, limit+readerBufferBytes,
			readerBufferBytes,
		)
	}
}

func TestCanonicalLineReaderFastPathReturnsOwnedBytes(t *testing.T) {
	reader := bufio.NewReaderSize(
		strings.NewReader("first\n"+strings.Repeat("x", 64)+"\n"),
		16,
	)
	first, err := readCanonicalLine(reader, maxTreeRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	expected := bytes.Clone(first)
	if _, err := readCanonicalLine(reader, maxTreeRecordBytes); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, expected) {
		t.Fatalf(
			"first canonical line changed after refill: %q, want %q",
			first, expected,
		)
	}
}

func TestControlFingerprintDetectsIdentityChangeWithoutClaimingContentProof(
	t *testing.T,
) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	commit := fixture.commit("fingerprint")
	root := t.TempDir()
	manifest, _ := buildFixture(t, fixture, commit, nil, root)
	if len(manifest.RepositoryMembers) == 0 {
		t.Fatal("fixture has no repository member")
	}
	state := manifest.State()
	fingerprint, err := CaptureControlFingerprintContext(
		t.Context(), root, state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged, err := fingerprint.MatchesContext(
		t.Context(), root, state,
	); err != nil || !unchanged {
		t.Fatalf("initial fingerprint match = %v, %v", unchanged, err)
	}

	memberPath := filepath.Join(root, manifest.RepositoryMembers[0].Name)
	before, err := os.Lstat(memberPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(memberPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("fixture member is empty")
	}
	raw[0] ^= 0x01
	if err := os.WriteFile(memberPath, raw, before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	changedTime := before.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(memberPath, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	if unchanged, err := fingerprint.MatchesContext(
		t.Context(), root, state,
	); err != nil || unchanged {
		t.Fatalf("changed fingerprint match = %v, %v", unchanged, err)
	}

	// A control fingerprint is intentionally metadata-only. If an adversary
	// restores every observed identity attribute, strict consumption—not this
	// cheap steady-state signal—must still reject the changed content.
	if err := os.Chtimes(
		memberPath, before.ModTime(), before.ModTime(),
	); err != nil {
		t.Fatal(err)
	}
	restored, err := os.Lstat(memberPath)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.ModTime().Equal(before.ModTime()) {
		t.Fatalf(
			"restored modification time = %v, want %v",
			restored.ModTime(), before.ModTime(),
		)
	}
	if unchanged, err := fingerprint.MatchesContext(
		t.Context(), root, state,
	); err != nil || !unchanged {
		t.Fatalf("same-stat fingerprint match = %v, %v", unchanged, err)
	}
	if _, err := CaptureControlFingerprintContext(
		t.Context(), root, state,
	); err != nil {
		t.Fatalf("same-stat cold fingerprint = %v", err)
	}
	if _, err := OpenStateContext(
		t.Context(), root, state,
	); err == nil {
		t.Fatal("strict consumption accepted same-stat member corruption")
	}
}

func TestStableRegularFileRejectsSymlinkAndPathReplacement(t *testing.T) {
	t.Run("initial symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target.json")
		if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "manifest.json")
		if err := os.Symlink(filepath.Base(target), link); err != nil {
			t.Fatal(err)
		}
		if _, err := readManifest(link); err == nil {
			t.Fatal("symlink manifest unexpectedly opened")
		}
	})

	for _, testCase := range []struct {
		name    string
		replace func(*testing.T, string, string, []byte)
	}{
		{
			name: "same-size regular replacement",
			replace: func(t *testing.T, path, _ string, raw []byte) {
				t.Helper()
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink replacement",
			replace: func(t *testing.T, path, displaced string, _ []byte) {
				t.Helper()
				if err := os.Symlink(filepath.Base(displaced), path); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "member.ndjson")
			raw := []byte("{\"stable\":true}\n")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			source, err := openStableRegularFile(path, before)
			if err != nil {
				t.Fatal(err)
			}
			read, readErr := io.ReadAll(source.file)
			if readErr != nil {
				_ = source.file.Close()
				t.Fatal(readErr)
			}
			displaced := filepath.Join(directory, "member.original")
			if err := os.Rename(path, displaced); err != nil {
				_ = source.file.Close()
				t.Fatal(err)
			}
			testCase.replace(t, path, displaced, raw)
			verifyErr := source.verifyAfterRead(int64(len(read)))
			closeErr := source.file.Close()
			if verifyErr == nil {
				t.Fatal("path replacement retained a stable identity")
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
}

func TestStrictValidationRejectsTamperingAndStaleness(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	fixture.write("src/other.go", "package main\n")
	commit := fixture.commit("strict")
	source := t.TempDir()
	manifest, expected := buildFixture(t, fixture, commit, nil, source)

	tests := []struct {
		name   string
		tamper func(*testing.T, string, *Manifest, *Expected)
	}{
		{
			name: "noncanonical manifest",
			tamper: func(t *testing.T, directory string, manifest *Manifest, _ *Expected) {
				filePath := filepath.Join(directory, ManifestName(manifest.Repository))
				raw, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filePath, append([]byte(" "), raw...), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "trailing manifest JSON",
			tamper: func(t *testing.T, directory string, manifest *Manifest, _ *Expected) {
				filePath := filepath.Join(directory, ManifestName(manifest.Repository))
				file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("{}\n"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unknown manifest field",
			tamper: func(t *testing.T, directory string, manifest *Manifest, _ *Expected) {
				filePath := filepath.Join(directory, ManifestName(manifest.Repository))
				raw, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatal(err)
				}
				raw = bytes.Replace(
					raw, []byte(`{"schema":`),
					[]byte(`{"unknown":true,"schema":`), 1,
				)
				if err := os.WriteFile(filePath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "partial member",
			tamper: func(t *testing.T, directory string, manifest *Manifest, _ *Expected) {
				filePath := filepath.Join(directory, manifest.RepositoryMembers[0].Name)
				file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("{"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "duplicate row with internally consistent digests",
			tamper: func(t *testing.T, directory string, manifest *Manifest, _ *Expected) {
				member := &manifest.RepositoryMembers[0]
				filePath := filepath.Join(directory, member.Name)
				raw, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatal(err)
				}
				line := raw[:bytes.IndexByte(raw, '\n')+1]
				if err := os.WriteFile(filePath, append(raw, line...), 0o600); err != nil {
					t.Fatal(err)
				}
				var duplicate Record
				if err := strictCanonicalJSONLine(line, &duplicate); err != nil {
					t.Fatal(err)
				}
				member.RecordCount++
				member.DeclaredBytes += duplicate.DeclaredBytes
				member.ContentBytes += int64(len(line))
				member.ContentDigest = artifactDigest(append(raw, line...))
				rewriteManifest(t, directory, manifest)
			},
		},
		{
			name: "extra generation member",
			tamper: func(t *testing.T, directory string, manifest *Manifest, _ *Expected) {
				name := ArtifactPrefix(
					manifest.Repository, manifest.GenerationDigest,
				) + "caller-999999.ndjson"
				if err := os.WriteFile(filepath.Join(directory, name), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "stale commit",
			tamper: func(_ *testing.T, _ string, _ *Manifest, expected *Expected) {
				expected.Commit = strings.Repeat("0", 40)
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			copyDirectory(t, source, directory)
			currentManifest := cloneManifest(manifest)
			currentExpected := expected
			currentExpected.Policies = slices.Clone(expected.Policies)
			testCase.tamper(t, directory, &currentManifest, &currentExpected)
			if _, err := Open(directory, currentExpected); err == nil {
				t.Fatal("tampered publication unexpectedly opened")
			}
		})
	}
}

func TestStrictValidationRejectsForgedCallerPartitionCoverage(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	commit := fixture.commit("caller partition")
	directory := t.TempDir()
	manifest, expected := buildFixture(
		t, fixture, commit, nil, directory,
	)
	if len(manifest.CallerLeaves) != 1 {
		t.Fatalf("caller leaves = %d, want one", len(manifest.CallerLeaves))
	}
	leaf := &manifest.CallerLeaves[0]
	for _, forged := range []string{"00", "01", "10", "11"} {
		if forged != leaf.Prefix {
			leaf.Prefix = forged
			leaf.PrefixBits = len(forged)
			break
		}
	}
	rewriteManifest(t, directory, &manifest)
	if _, err := Open(
		directory, expected,
	); err == nil || !strings.Contains(err.Error(), "outside its leaf") {
		t.Fatalf("forged caller partition error = %v", err)
	}
}

func TestStrictValidationRejectsDigestConsistentMissingCallerLeaf(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	commit := fixture.commit("missing caller partition")
	directory := t.TempDir()
	manifest, expected := buildFixture(
		t, fixture, commit, nil, directory,
	)
	if len(manifest.CallerLeaves) != 1 {
		t.Fatalf("caller leaves = %d, want one", len(manifest.CallerLeaves))
	}
	missing := manifest.CallerLeaves[0].Name
	if err := os.Remove(filepath.Join(directory, missing)); err != nil {
		t.Fatal(err)
	}
	// Preserve the independently committed domain summary, remove only the
	// leaf descriptor/file, and recompute the outer manifest digest. Every
	// remaining file and envelope digest is therefore internally consistent,
	// but the declared caller-domain coverage is incomplete.
	manifest.CallerLeaves = nil
	rewriteManifest(t, directory, &manifest)
	if _, err := Open(
		directory, expected,
	); err == nil || !strings.Contains(err.Error(), "domain summaries do not match") {
		t.Fatalf("missing caller partition error = %v", err)
	}
}

func artifactDigest(raw []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-candidate-artifact-v2\x00"))
	_, _ = hash.Write(raw)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func rewriteManifest(t *testing.T, directory string, manifest *Manifest) {
	t.Helper()
	var err error
	manifest.Digest, err = ManifestDigest(*manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(
		filepath.Join(directory, ManifestName(manifest.Repository)), raw, 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func copyDirectory(t *testing.T, source, destination string) {
	t.Helper()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(destination, entry.Name()), raw, 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPartitionBoundsAndCollisionRefusal(t *testing.T) {
	tests := []struct {
		count      int
		wantLeaves int
	}{
		{count: MaxRecordsPerArtifact, wantLeaves: 1},
		{count: MaxRecordsPerArtifact + 1, wantLeaves: 2},
	}
	for _, testCase := range tests {
		t.Run(fmt.Sprintf("%d", testCase.count), func(t *testing.T) {
			spoolDir := t.TempDir()
			root := &spool{
				path:   filepath.Join(spoolDir, "root.ndjson"),
				prefix: "00", bits: InitialCallerPrefixBits,
			}
			for index := 0; index < testCase.count; index++ {
				leading := byte('0')
				if index%2 == 1 {
					leading = '2'
				}
				record := partitionTestRecord(
					fmt.Sprintf("%c%063x", leading, index),
					fmt.Sprintf("p/%06d.go", index),
				)
				if err := appendSpoolRecord(root, record); err != nil {
					t.Fatal(err)
				}
			}
			var leaves []*spool
			if err := splitSpool(t.Context(), spoolDir, root, &leaves); err != nil {
				t.Fatal(err)
			}
			if len(leaves) != testCase.wantLeaves {
				t.Fatalf("leaves=%d, want %d", len(leaves), testCase.wantLeaves)
			}
			for _, leaf := range leaves {
				if leaf.count > MaxRecordsPerArtifact ||
					leaf.declaredBytes > MaxDeclaredBytesPerArtifact {
					t.Fatalf("unbounded leaf: %+v", leaf)
				}
			}
		})
	}

	byteTests := []struct {
		name       string
		secondSize int64
		wantLeaves int
	}{
		{
			name:       "exact declared-byte boundary",
			secondSize: MaxDeclaredBytesPerArtifact / 2,
			wantLeaves: 1,
		},
		{
			name:       "declared-byte boundary plus one",
			secondSize: MaxDeclaredBytesPerArtifact/2 + 1,
			wantLeaves: 2,
		},
	}
	for _, testCase := range byteTests {
		t.Run(testCase.name, func(t *testing.T) {
			spoolDir := t.TempDir()
			root := &spool{
				path:   filepath.Join(spoolDir, "bytes.ndjson"),
				prefix: "00", bits: InitialCallerPrefixBits,
			}
			first := partitionTestRecord(
				"0"+strings.Repeat("0", 63), "p/first.go",
			)
			first.DeclaredBytes = MaxDeclaredBytesPerArtifact / 2
			second := partitionTestRecord(
				"2"+strings.Repeat("0", 63), "p/second.go",
			)
			second.DeclaredBytes = testCase.secondSize
			if err := appendSpoolRecord(root, first); err != nil {
				t.Fatal(err)
			}
			if err := appendSpoolRecord(root, second); err != nil {
				t.Fatal(err)
			}
			var leaves []*spool
			if err := splitSpool(
				t.Context(), spoolDir, root, &leaves,
			); err != nil {
				t.Fatal(err)
			}
			if len(leaves) != testCase.wantLeaves {
				t.Fatalf("leaves=%d, want %d", len(leaves), testCase.wantLeaves)
			}
		})
	}

	spoolDir := t.TempDir()
	root := &spool{
		path:   filepath.Join(spoolDir, "collision.ndjson"),
		prefix: "00", bits: InitialCallerPrefixBits,
	}
	collision := strings.Repeat("0", sha256.Size*2)
	for index := 0; index < MaxRecordsPerArtifact+1; index++ {
		if err := appendSpoolRecord(
			root, partitionTestRecord(collision, fmt.Sprintf("p/%06d.go", index)),
		); err != nil {
			t.Fatal(err)
		}
	}
	var leaves []*spool
	if err := splitSpool(
		t.Context(), spoolDir, root, &leaves,
	); !errors.Is(err, ErrHashCollision) {
		t.Fatalf("collision error = %v", err)
	}
}

func TestCallerSpoolScanHonorsCancellation(t *testing.T) {
	spoolDir := t.TempDir()
	root := &spool{
		path:   filepath.Join(spoolDir, "cancel.ndjson"),
		prefix: "00", bits: InitialCallerPrefixBits,
	}
	for index := 0; index < 100; index++ {
		if err := appendSpoolRecord(
			root,
			partitionTestRecord(
				fmt.Sprintf("%064x", index),
				fmt.Sprintf("p/%06d.go", index),
			),
		); err != nil {
			t.Fatal(err)
		}
	}
	// Force a split scan without constructing 4,097 records. Cancellation
	// must be observed inside that scan, not only at the recursive boundary.
	root.count = MaxRecordsPerArtifact + 1
	ctx := &cancelAfterErrChecks{
		Context:   context.Background(),
		remaining: 20,
	}
	var leaves []*spool
	if err := splitSpool(
		ctx, spoolDir, root, &leaves,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller spool scan = %v", err)
	}
}

func partitionTestRecord(hashText, filePath string) Record {
	return Record{
		Schema: RecordSchema, Path: filePath, OID: strings.Repeat("a", 40),
		SourceLane: sourceLane(filePath),
		Domains:    []string{"caller"}, Hash: hashText,
	}
}

func TestOversizedSingletonRefused(t *testing.T) {
	packer := newArtifactPacker(
		t.TempDir(), "example.invalid/repo",
		"sha256:"+strings.Repeat("a", 64), "repository",
	)
	err := packer.add(Record{
		Schema: RecordSchema, Path: "big.go", OID: strings.Repeat("a", 40),
		DeclaredBytes: MaxDeclaredBytesPerArtifact + 1,
		SourceLane:    SourceLaneBase, Domains: []string{"local"},
	})
	if !errors.Is(err, ErrCandidateTooLarge) {
		t.Fatalf("oversized singleton error = %v", err)
	}
}

func TestArtifactPackerAbortClosesPartialMemberAndIsIdempotent(t *testing.T) {
	packer := newArtifactPacker(
		t.TempDir(), "example.invalid/repo",
		"sha256:"+strings.Repeat("a", 64), "repository",
	)
	if err := packer.add(Record{
		Schema: RecordSchema, Path: "partial.go",
		OID: strings.Repeat("a", 40), SourceLane: SourceLaneBase,
		Domains: []string{"local"},
	}); err != nil {
		t.Fatal(err)
	}
	opened := packer.file
	if opened == nil {
		t.Fatal("packer did not open a partial member")
	}
	if err := packer.abort(); err != nil {
		t.Fatal(err)
	}
	if packer.file != nil {
		t.Fatal("abort retained the partial file")
	}
	if _, err := opened.WriteString("{}\n"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after abort = %v, want os.ErrClosed", err)
	}
	if err := packer.abort(); err != nil {
		t.Fatalf("second abort = %v", err)
	}
}

func TestProjectionValidationUsesBoundedExternalMerge(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	sorter := newProjectionSorter(ctx, t.TempDir())
	const uniquePaths = projectionChunkRecords*8 + 17
	for index := 0; index < uniquePaths; index++ {
		current := candidateProjection{
			Path: fmt.Sprintf("src/%06d.go", index),
			OID:  strings.Repeat("a", 40), DeclaredBytes: 1,
			Plane: PlaneRepository,
		}
		if err := sorter.add(current); err != nil {
			t.Fatal(err)
		}
		current.Plane = PlaneCaller
		if err := sorter.add(current); err != nil {
			t.Fatal(err)
		}
	}
	run, err := sorter.finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(sorter.chunk) != 0 ||
		len(sorter.levels) > 2+bits.Len(uint(uniquePaths*2)) {
		t.Fatalf(
			"sorter retained chunk/levels = %d/%d",
			len(sorter.chunk), len(sorter.levels),
		)
	}
	summary := newCorpusAccumulator().summary()
	summary.RegularCount = uniquePaths
	summary.RegularDeclaredBytes = uniquePaths
	if err := validateCorpusSummary(ctx, summary, run); err != nil {
		t.Fatal(err)
	}

	t.Run("cross-plane mismatch", func(t *testing.T) {
		mismatch := newProjectionSorter(t.Context(), t.TempDir())
		left := candidateProjection{
			Path: "same.go", OID: strings.Repeat("a", 40),
			DeclaredBytes: 1, Plane: PlaneRepository,
		}
		right := left
		right.OID = strings.Repeat("b", 40)
		right.Plane = PlaneCaller
		if err := mismatch.add(left); err != nil {
			t.Fatal(err)
		}
		if err := mismatch.add(right); err != nil {
			t.Fatal(err)
		}
		path, err := mismatch.finish()
		if err != nil {
			t.Fatal(err)
		}
		current := summary
		current.RegularCount = 1
		current.RegularDeclaredBytes = 1
		if err := validateCorpusSummary(
			t.Context(), current, path,
		); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("cross-plane mismatch = %v", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		cancelContext, cancel := context.WithCancel(context.Background())
		canceled := newProjectionSorter(cancelContext, t.TempDir())
		if err := canceled.add(candidateProjection{
			Path: "a.go", OID: strings.Repeat("a", 40),
			Plane: PlaneRepository,
		}); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err := canceled.finish(); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled projection sort = %v", err)
		}
	})
}

func TestExternalMergeRejectsCrossPlaneSourceLaneMismatch(t *testing.T) {
	directory := t.TempDir()
	sorter := newProjectionSorter(t.Context(), directory)
	base := candidateProjection{
		Path: "src/main_test.go", OID: strings.Repeat("a", 40),
		DeclaredBytes: 10, SourceLane: SourceLaneGoTest,
		InUnit: true, Plane: PlaneRepository,
	}
	caller := base
	caller.Plane = PlaneCaller
	caller.SourceLane = SourceLaneBase
	if err := sorter.add(base); err != nil {
		t.Fatal(err)
	}
	if err := sorter.add(caller); err != nil {
		t.Fatal(err)
	}
	run, err := sorter.finish()
	if err != nil {
		t.Fatal(err)
	}
	summary := newCorpusAccumulator().summary()
	summary.RegularCount = 1
	summary.RegularDeclaredBytes = base.DeclaredBytes
	if err := validateCorpusSummary(
		t.Context(), summary, run,
	); err == nil || !strings.Contains(err.Error(), "cross-plane candidate mismatch") {
		t.Fatalf("cross-plane lane mismatch error = %v", err)
	}
}

func TestPublicationLifecycleAndStageCleanup(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	firstCommit := fixture.commit("first")
	root := t.TempDir()
	firstStage, err := NewStage(root)
	if err != nil {
		t.Fatal(err)
	}
	first, firstExpected := buildFixture(
		t, fixture, firstCommit, nil, firstStage,
	)
	firstState, err := Publish(root, firstStage, firstExpected)
	if err != nil {
		t.Fatal(err)
	}
	if !IsPublishing(root, fixture.repository) {
		t.Fatal("publication marker is absent")
	}
	if _, err := Open(root, firstExpected); !errors.Is(err, ErrPublishing) {
		t.Fatalf("open during publication = %v", err)
	}
	if err := FinishPublication(root, fixture.repository); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(root, firstState); err != nil {
		t.Fatal(err)
	}

	unrelatedFile := filepath.Join(root, "unrelated.txt")
	if err := os.WriteFile(unrelatedFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelatedDirectory := filepath.Join(root, "unrelated")
	if err := os.Mkdir(unrelatedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.write("src/main.go", "package main\n// changed\n")
	secondCommit := fixture.commit("second")
	secondStage, err := NewStage(root)
	if err != nil {
		t.Fatal(err)
	}
	second, secondExpected := buildFixture(
		t, fixture, secondCommit, nil, secondStage,
	)
	oldManifestPath := filepath.Join(root, ManifestName(first.Repository))
	oldManifest, err := os.ReadFile(oldManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.CallerLeaves) == 0 {
		t.Fatal("second generation has no caller leaf for failure injection")
	}
	blockedDestination := filepath.Join(root, second.CallerLeaves[0].Name)
	if err := os.Mkdir(blockedDestination, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(root, secondStage, secondExpected); err == nil {
		t.Fatal("publication unexpectedly replaced a blocking directory")
	}
	if !IsPublishing(root, fixture.repository) {
		t.Fatal("failed publication did not retain its marker")
	}
	stillOldManifest, err := os.ReadFile(oldManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldManifest, stillOldManifest) {
		t.Fatal("failed member move switched the manifest")
	}
	if err := os.Remove(blockedDestination); err != nil {
		t.Fatal(err)
	}
	// The failed attempt moved at least one member. Rebuild the immutable
	// stage and retry; stale partial bytes are replaced under the marker.
	secondStage, err = NewStage(root)
	if err != nil {
		t.Fatal(err)
	}
	second, secondExpected = buildFixture(
		t, fixture, secondCommit, nil, secondStage,
	)
	if _, err := Publish(root, secondStage, secondExpected); err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(root, fixture.repository); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, secondExpected); err != nil {
		t.Fatal(err)
	}
	secondNames := make(map[string]bool)
	for _, member := range append(
		slices.Clone(second.RepositoryMembers), callerArtifacts(second)...,
	) {
		secondNames[member.Name] = true
	}
	for _, member := range append(
		slices.Clone(first.RepositoryMembers), callerArtifacts(first)...,
	) {
		if secondNames[member.Name] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, member.Name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale member %q remains: %v", member.Name, err)
		}
	}
	if _, err := os.Stat(unrelatedFile); err != nil {
		t.Fatal("unrelated file was removed")
	}
	if _, err := os.Stat(unrelatedDirectory); err != nil {
		t.Fatal("unrelated directory was removed")
	}

	stageOne, err := NewStage(root)
	if err != nil {
		t.Fatal(err)
	}
	stageTwo, err := NewStage(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = stageOne
	_ = stageTwo
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, stageDirectoryPrefix+"symlink")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	validationCrash := filepath.Join(
		root, validationDirectoryPrefix+"crash",
	)
	if err := os.Mkdir(validationCrash, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(validationCrash, "projection.ndjson"),
		[]byte("{}\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	removed, err := CleanupStages(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 7 {
		t.Fatalf("removed %d startup artifacts, want 7", removed)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("stage cleanup followed a symlink")
	}
	if _, err := os.Stat(unrelatedDirectory); err != nil {
		t.Fatal("stage cleanup removed an unrelated directory")
	}
	if _, err := os.Lstat(validationCrash); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("validation crash spool remains: %v", err)
	}
	names, err := ManagedArtifactNames(root)
	if err != nil || len(names) == 0 {
		t.Fatalf("managed names = %v, %v", names, err)
	}
	if err := Remove(t.Context(), root, fixture.repository); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unrelatedFile); err != nil {
		t.Fatal("repository removal touched unrelated file")
	}
}

func callerArtifacts(manifest Manifest) []Artifact {
	result := make([]Artifact, 0, len(manifest.CallerLeaves))
	for _, leaf := range manifest.CallerLeaves {
		result = append(result, leaf.Artifact)
	}
	return result
}

func TestCleanupStagesHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CleanupStages(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cleanup cancellation = %v", err)
	}
}

func TestPublicationMarkerTemporaryName(t *testing.T) {
	repository := "example.invalid/temporary-name"
	validPrefix := "." + PublishingName(repository) + "."
	for _, testCase := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "zero", value: validPrefix + "0", want: true},
		{name: "uint32 maximum", value: validPrefix + "4294967295", want: true},
		{name: "stable marker", value: PublishingName(repository)},
		{name: "missing random suffix", value: validPrefix},
		{name: "leading zero", value: validPrefix + "01"},
		{name: "uint32 overflow", value: validPrefix + "4294967296"},
		{name: "nondecimal suffix", value: validPrefix + "123x"},
		{
			name: "uppercase hash",
			value: ".phebs-candidate-" + strings.Repeat("A", 64) +
				".publishing.1",
		},
		{
			name: "short hash",
			value: ".phebs-candidate-" + strings.Repeat("a", 63) +
				".publishing.1",
		},
		{
			name: "manifest temporary",
			value: ".phebs-candidate-" + strings.Repeat("a", 64) +
				".manifest.json.1",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isPublicationMarkerTemporary(testCase.value); got != testCase.want {
				t.Fatalf(
					"isPublicationMarkerTemporary(%q) = %t, want %t",
					testCase.value, got, testCase.want,
				)
			}
		})
	}
}

func TestCleanupStagesUnlinksPublicationMarkerTemporaries(t *testing.T) {
	root := t.TempDir()
	temporary, err := os.CreateTemp(
		root, "."+PublishingName("example.invalid/regular")+".",
	)
	if err != nil {
		t.Fatal(err)
	}
	regularPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(
		root, "."+PublishingName("example.invalid/symlink")+".17",
	)
	if err := os.Symlink(sentinel, symlinkPath); err != nil {
		t.Fatal(err)
	}

	directoryPath := filepath.Join(
		root, "."+PublishingName("example.invalid/directory")+".23",
	)
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	directorySentinel := filepath.Join(directoryPath, "sentinel")
	if err := os.WriteFile(directorySentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	lookalikes := []string{
		"." + PublishingName("example.invalid/missing-random") + ".",
		"." + PublishingName("example.invalid/nondigit") + ".17x",
		"." + PublishingName("example.invalid/leading-zero") + ".017",
		PublishingName("example.invalid/stable"),
		".phebs-candidate-" + strings.Repeat("a", 63) + ".publishing.17",
	}
	for _, name := range lookalikes {
		if err := os.WriteFile(
			filepath.Join(root, name), []byte("keep"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := CleanupStages(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed %d startup artifacts, want 2", removed)
	}
	for _, path := range []string{regularPath, symlinkPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary marker %q remains: %v", path, err)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("temporary-marker cleanup followed a symlink")
	}
	if _, err := os.Stat(directorySentinel); err != nil {
		t.Fatal("temporary-marker cleanup removed a directory")
	}
	for _, name := range lookalikes {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("lookalike %q was removed: %v", name, err)
		}
	}
}

func TestDomainSummaryOverflowRefused(t *testing.T) {
	accumulators := makeDomainAccumulators([]PolicyIdentity{{
		Domain: "local", Version: "1",
		EnumerationPolicy: "local-v1", SymlinkPolicy: "none",
		Plane: PlaneLocal,
	}})
	accumulators["local"].summary.RepositoryCandidateCount = int(^uint(0) >> 1)
	err := addDomainRecords(
		accumulators,
		Record{DeclaredBytes: 1},
		[]string{"local"},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("domain overflow error = %v", err)
	}
}

func TestFocusedLocalProjectionReadsRepositoryMembersOnce(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("unit/a.go", "package unit\n")
	fixture.write("unit/b.go", "package unit\n")
	for index := 0; index < 24; index++ {
		fixture.write(
			fmt.Sprintf("outside/file-%02d.go", index),
			"package outside\n",
		)
	}
	commit := fixture.commit("focused projections")
	unit, err := (analysisunit.Scope{
		Repository: fixture.repository,
		Name:       "unit",
		Primary:    []string{"unit"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	goFiles := func(value string) bool {
		return strings.HasSuffix(value, ".go")
	}
	policies := []Policy{
		{
			Domain: "empty-local", Version: "1",
			EnumerationPolicy: "proto-v1", Plane: PlaneLocal,
			Enumerate: func(value string) bool {
				return strings.HasSuffix(value, ".proto")
			},
		},
		{
			Domain: "local-a", Version: "1",
			EnumerationPolicy: "go-a-v1", Plane: PlaneLocal,
			Enumerate: goFiles,
		},
		{
			Domain: "local-b", Version: "1",
			EnumerationPolicy: "go-b-v1", Plane: PlaneLocal,
			Enumerate: goFiles,
		},
		{
			Domain: "repository-all", Version: "1",
			EnumerationPolicy: "go-repository-v1", Plane: PlaneRepository,
			Enumerate: goFiles,
		},
		{
			Domain: "caller-all", Version: "1",
			EnumerationPolicy: "go-caller-v1", Plane: PlaneCaller,
			Enumerate: goFiles,
		},
	}
	identities, err := PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manifest, err := Build(t.Context(), Request{
		RepoDir: fixture.directory, OutputDir: root,
		Repository: fixture.repository, Commit: commit,
		Unit: unit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.LocalProjections) != 3 {
		t.Fatalf(
			"local projection count = %d, want 3",
			len(manifest.LocalProjections),
		)
	}
	for _, projection := range manifest.LocalProjections {
		if projection.Members == nil {
			t.Fatalf("projection %q has a nil member commitment", projection.Domain)
		}
		if projection.Domain == "empty-local" &&
			len(projection.Members) != 0 {
			t.Fatalf("empty projection has members: %+v", projection.Members)
		}
	}

	reads := make(map[string]int64)
	observedContext := withArtifactReadObserver(
		t.Context(), func(name string, count int64) {
			reads[name] += count
		},
	)
	publication, err := OpenContext(observedContext, root, Expected{
		Repository: fixture.repository, Commit: commit,
		Unit: unit, Policies: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnce := func(member Artifact) {
		t.Helper()
		if reads[member.Name] != member.ContentBytes {
			t.Fatalf(
				"open read %q bytes = %d, want exactly %d",
				member.Name, reads[member.Name], member.ContentBytes,
			)
		}
	}
	for _, member := range manifest.RepositoryMembers {
		assertReadOnce(member)
	}
	for _, projection := range manifest.LocalProjections {
		for _, member := range projection.Members {
			assertReadOnce(member)
		}
	}
	for _, leaf := range manifest.CallerLeaves {
		assertReadOnce(leaf.Artifact)
	}

	reads = make(map[string]int64)
	replayContext := withArtifactReadObserver(
		t.Context(), func(name string, count int64) {
			reads[name] += count
		},
	)
	for _, domain := range []string{"empty-local", "local-a", "local-b"} {
		view, err := publication.Domain(domain, "1")
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		if err := view.ForEachRepositoryRecord(
			replayContext, func(record Record) error {
				if !record.InUnit ||
					!strings.HasPrefix(record.Path, "unit/") {
					t.Fatalf(
						"local domain %q received out-of-unit record %+v",
						domain, record,
					)
				}
				paths = append(paths, record.Path)
				return nil
			},
		); err != nil {
			t.Fatal(err)
		}
		if domain == "empty-local" && len(paths) != 0 {
			t.Fatalf("empty local replay = %v", paths)
		}
		if domain != "empty-local" &&
			!slices.Equal(paths, []string{"unit/a.go", "unit/b.go"}) {
			t.Fatalf("%s local replay = %v", domain, paths)
		}
	}
	for _, member := range manifest.RepositoryMembers {
		if reads[member.Name] != 0 {
			t.Fatalf(
				"local replay re-read repository member %q: %d bytes",
				member.Name, reads[member.Name],
			)
		}
	}
	for _, leaf := range manifest.CallerLeaves {
		if reads[leaf.Name] != 0 {
			t.Fatalf(
				"local replay read caller member %q: %d bytes",
				leaf.Name, reads[leaf.Name],
			)
		}
	}
	for _, projection := range manifest.LocalProjections {
		for _, member := range projection.Members {
			if reads[member.Name] != member.ContentBytes {
				t.Fatalf(
					"local replay read %q bytes = %d, want %d",
					member.Name, reads[member.Name], member.ContentBytes,
				)
			}
		}
	}

	repositoryView, err := publication.Domain("repository-all", "1")
	if err != nil {
		t.Fatal(err)
	}
	var repositoryPaths []string
	if err := repositoryView.ForEachRepositoryRecord(
		t.Context(), func(record Record) error {
			repositoryPaths = append(repositoryPaths, record.Path)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(repositoryPaths) != 26 ||
		!slices.Contains(repositoryPaths, "outside/file-00.go") {
		t.Fatalf("repository replay changed scope: %v", repositoryPaths)
	}
	callerView, err := publication.Domain("caller-all", "1")
	if err != nil {
		t.Fatal(err)
	}
	var callerPaths []string
	if err := callerView.ForEachRepositoryRecord(
		t.Context(), func(record Record) error {
			callerPaths = append(callerPaths, record.Path)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	slices.Sort(repositoryPaths)
	slices.Sort(callerPaths)
	if !slices.Equal(callerPaths, repositoryPaths) {
		t.Fatalf(
			"caller/repository behavior diverged: %v / %v",
			callerPaths, repositoryPaths,
		)
	}

	localView, err := publication.Domain("local-a", "1")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := localView.ForEachRepositoryRecord(
		canceled, func(Record) error { return nil },
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled local replay = %v", err)
	}
	markerPath := filepath.Join(root, PublishingName(fixture.repository))
	if err := os.WriteFile(
		markerPath, []byte(fixture.repository+"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := localView.ForEachRepositoryRecord(
		t.Context(), func(Record) error { return nil },
	); !errors.Is(err, ErrPublishing) {
		t.Fatalf("marked local replay = %v", err)
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	var localMember Artifact
	for _, projection := range manifest.LocalProjections {
		if projection.Domain == "local-a" &&
			len(projection.Members) > 0 {
			localMember = projection.Members[0]
			break
		}
	}
	if localMember.Name == "" {
		t.Fatal("local-a projection has no member")
	}
	fingerprint, err := CaptureControlFingerprintContext(
		t.Context(), root, manifest.State(),
	)
	if err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(root, localMember.Name)
	beforeLocal, err := os.Lstat(localPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0x01
	if err := os.WriteFile(localPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	changedLocalTime := beforeLocal.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(
		localPath, changedLocalTime, changedLocalTime,
	); err != nil {
		t.Fatal(err)
	}
	if unchanged, err := fingerprint.MatchesContext(
		t.Context(), root, manifest.State(),
	); err != nil || unchanged {
		t.Fatalf(
			"tampered local projection fingerprint = %t, %v",
			unchanged, err,
		)
	}
	if err := localView.ForEachRepositoryRecord(
		t.Context(), func(Record) error { return nil },
	); err == nil {
		t.Fatal("tampered local projection replay unexpectedly succeeded")
	}
}

func TestStrictValidationRejectsForgedLocalProjectionCoverage(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("unit/in.go", "package unit\n")
	fixture.write("outside/out.go", "package outside\n")
	commit := fixture.commit("forged local projection")
	unit, err := (analysisunit.Scope{
		Repository: fixture.repository,
		Name:       "unit",
		Primary:    []string{"unit"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manifest, expected := buildFixture(t, fixture, commit, unit, root)
	if len(manifest.LocalProjections) != 1 ||
		len(manifest.LocalProjections[0].Members) != 1 {
		t.Fatalf("local projection fixture = %+v", manifest.LocalProjections)
	}
	var outside Record
	for _, member := range manifest.RepositoryMembers {
		if err := forEachCanonicalRecordContext(
			t.Context(), filepath.Join(root, member.Name),
			func(record Record) error {
				if record.Path == "outside/out.go" {
					outside = record
				}
				return nil
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	if outside.Path == "" || outside.InUnit {
		t.Fatalf("outside fixture record = %+v", outside)
	}
	payload, err := json.Marshal(outside)
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	member := &manifest.LocalProjections[0].Members[0]
	if err := os.WriteFile(
		filepath.Join(root, member.Name), payload, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	member.RecordCount = 1
	member.DeclaredBytes = outside.DeclaredBytes
	member.ContentBytes = int64(len(payload))
	member.ContentDigest = artifactDigest(payload)
	rewriteManifest(t, root, &manifest)
	if _, err := Open(root, expected); err == nil ||
		!strings.Contains(err.Error(), "does not exactly cover") {
		t.Fatalf("forged local projection error = %v", err)
	}
	if _, err := OpenState(root, manifest.State()); err == nil {
		t.Fatal("forged local projection opened through a persisted pointer")
	}
}

func TestFocusedLocalProjectionPublicationLifecycle(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("unit/in.go", "package unit\n")
	fixture.write("outside/out.go", "package outside\n")
	firstCommit := fixture.commit("first focused generation")
	unit, err := (analysisunit.Scope{
		Repository: fixture.repository,
		Name:       "unit",
		Primary:    []string{"unit"},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	firstStage, err := NewStage(root)
	if err != nil {
		t.Fatal(err)
	}
	first, firstExpected := buildFixture(
		t, fixture, firstCommit, unit, firstStage,
	)
	firstState, err := Publish(root, firstStage, firstExpected)
	if err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(root, fixture.repository); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(root, firstState); err != nil {
		t.Fatal(err)
	}
	if len(first.LocalProjections) != 1 ||
		len(first.LocalProjections[0].Members) != 1 {
		t.Fatalf("first local projection = %+v", first.LocalProjections)
	}
	firstLocalName := first.LocalProjections[0].Members[0].Name
	if _, err := os.Stat(filepath.Join(root, firstLocalName)); err != nil {
		t.Fatalf("published local projection: %v", err)
	}

	fixture.write("unit/in.go", "package unit\n// changed\n")
	secondCommit := fixture.commit("second focused generation")
	secondStage, err := NewStage(root)
	if err != nil {
		t.Fatal(err)
	}
	second, secondExpected := buildFixture(
		t, fixture, secondCommit, unit, secondStage,
	)
	secondState, err := Publish(root, secondStage, secondExpected)
	if err != nil {
		t.Fatal(err)
	}
	if err := FinishPublication(root, fixture.repository); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(root, secondState); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(root, firstState); err == nil {
		t.Fatal("stale focused publication pointer unexpectedly opened")
	}
	secondNames := make(map[string]bool)
	for _, projection := range second.LocalProjections {
		for _, member := range projection.Members {
			secondNames[member.Name] = true
		}
	}
	if !secondNames[firstLocalName] {
		if _, err := os.Lstat(
			filepath.Join(root, firstLocalName),
		); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale local projection remains: %v", err)
		}
	}
}

func TestLocalProjectionAggregateAdmissionBounds(t *testing.T) {
	t.Run("build member budget", func(t *testing.T) {
		budget := &artifactBudget{maxMembers: 1, maxBytes: 10}
		if err := budget.reserveMember(); err != nil {
			t.Fatal(err)
		}
		if err := budget.reserveMember(); !errors.Is(err, ErrCandidateTooLarge) {
			t.Fatalf("member budget overflow = %v", err)
		}
	})
	t.Run("build content budget", func(t *testing.T) {
		budget := &artifactBudget{maxMembers: 1, maxBytes: 10}
		if err := budget.reserveBytes(10); err != nil {
			t.Fatal(err)
		}
		if err := budget.reserveBytes(1); !errors.Is(err, ErrCandidateTooLarge) {
			t.Fatalf("content budget overflow = %v", err)
		}
	})
	baseManifest := func(members []Artifact) Manifest {
		return Manifest{
			Repository:       "example.invalid/bounds",
			GenerationDigest: "sha256:" + strings.Repeat("a", 64),
			UnitDigest:       "sha256:" + strings.Repeat("b", 64),
			Policies: []PolicyIdentity{{
				Domain: "local", Version: "1",
				EnumerationPolicy: "local-v1",
				SymlinkPolicy:     "none",
				Plane:             PlaneLocal,
			}},
			LocalProjections: []LocalProjection{{
				Domain: "local", Version: "1",
				PolicyOrdinal: 0, Members: members,
			}},
		}
	}
	t.Run("manifest artifact budget", func(t *testing.T) {
		manifest := baseManifest(
			make([]Artifact, MaxLocalProjectionArtifacts+1),
		)
		if _, _, err := prepareLocalProjectionVerifiers(
			manifest, map[string]bool{},
		); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("manifest artifact overflow = %v", err)
		}
	})
	t.Run("manifest content budget", func(t *testing.T) {
		count := int(MaxLocalProjectionContentBytes/maxArtifactBytes) + 1
		members := make([]Artifact, count)
		prefix := ArtifactPrefix(
			"example.invalid/bounds",
			"sha256:"+strings.Repeat("a", 64),
		)
		for ordinal := range members {
			members[ordinal] = Artifact{
				Name: prefix + fmt.Sprintf(
					"local-000-%06d.ndjson", ordinal,
				),
				Ordinal: ordinal, RecordCount: 1,
				ContentBytes:  maxArtifactBytes,
				ContentDigest: "sha256:" + strings.Repeat("c", 64),
			}
		}
		manifest := baseManifest(members)
		if _, _, err := prepareLocalProjectionVerifiers(
			manifest, map[string]bool{},
		); !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("manifest content overflow = %v", err)
		}
	})
}
