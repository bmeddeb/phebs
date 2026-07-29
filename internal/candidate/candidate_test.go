package candidate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
		flags["other/out.go"] != [2]bool{false, false} {
		t.Fatalf("unit flags = %+v", flags)
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

func artifactDigest(raw []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-candidate-artifact-v1\x00"))
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
		Domains: []string{"caller"}, Hash: hashText,
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
		Domains:       []string{"local"},
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
		OID: strings.Repeat("a", 40), Domains: []string{"local"},
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
