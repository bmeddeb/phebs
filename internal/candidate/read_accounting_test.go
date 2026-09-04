package candidate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
)

func TestReadAccountingManifestAttempts(t *testing.T) {
	for _, name := range []string{"missing", "special", "invalid"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			switch name {
			case "special":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "invalid":
				if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, ledger := candidateReadScope(t, readaccounting.Counts{ControlFileReads: 1})
			if _, err := readManifestContext(ctx, path); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
			counts, err := ledger.Finish()
			if err != nil || counts != (readaccounting.Counts{ControlFileReads: 1}) {
				t.Fatalf("manifest attempt = %+v, %v", counts, err)
			}
		})
	}
	t.Run("refuse_before_io", func(t *testing.T) {
		ctx, ledger := candidateReadScope(t, readaccounting.Counts{})
		if _, err := readManifestContext(ctx, filepath.Join(t.TempDir(), "missing")); !errors.Is(err, readaccounting.ErrLimit) {
			t.Fatalf("zero-budget read = %v, want accounting refusal before missing-file I/O", err)
		}
		if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts.ControlFileReads != 1 {
			t.Fatalf("failed scope = %+v, %v", counts, err)
		}
	})
}

func TestReadAccountingArtifactDecodeFailures(t *testing.T) {
	raw, err := json.Marshal(Record{})
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	for _, test := range []struct {
		name   string
		raw    []byte
		visits uint64
	}{
		{name: "missing", visits: 0},
		{name: "malformed", raw: []byte("{\n"), visits: 0},
		{name: "trailing_json", raw: []byte("{} {}\n"), visits: 1},
		{name: "noncanonical", raw: append([]byte(" "), raw...), visits: 1},
		{name: "semantic", raw: raw, visits: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "member.ndjson")
			if test.raw != nil {
				if err := os.WriteFile(path, test.raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, ledger := candidateReadScope(t, readaccounting.Counts{MemberVisits: 1})
			member := Artifact{Name: filepath.Base(path), RecordCount: 1, ContentBytes: int64(len(test.raw))}
			err := validateArtifactFile(ctx, path, member, func(record Record) error {
				return validateRecord(record, PlaneRepository, nil, &analysisUnitView{whole: true})
			})
			if err == nil {
				t.Fatal("invalid member was accepted")
			}
			counts, finishErr := ledger.Finish()
			if finishErr != nil || counts != (readaccounting.Counts{MemberVisits: test.visits}) {
				t.Fatalf("decoded visits = %+v, %v; want %d", counts, finishErr, test.visits)
			}
		})
	}
	t.Run("refuse_before_visitor", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "member.ndjson")
		if err := os.WriteFile(path, append(bytes.Clone(raw), raw...), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, ledger := candidateReadScope(t, readaccounting.Counts{MemberVisits: 1})
		visits := 0
		err := validateArtifactFile(ctx, path, Artifact{ContentBytes: int64(2 * len(raw))}, func(Record) error {
			visits++
			return nil
		})
		if !errors.Is(err, readaccounting.ErrLimit) || visits != 1 {
			t.Fatalf("member limit = %v, visitor calls=%d", err, visits)
		}
		if counts, err := ledger.Finish(); !errors.Is(err, readaccounting.ErrLimit) || counts.MemberVisits != 2 {
			t.Fatalf("failed member scope = %+v, %v", counts, err)
		}
	})
}

func TestReadAccountingProjectionRereads(t *testing.T) {
	// Exact decoded visits from the existing 512-record binary-carry merge
	// and final scan; spool creation and metadata operations are not reads.
	for _, test := range []struct {
		records int
		visits  uint64
	}{
		{0, 0}, {1, 1}, {511, 511}, {512, 512}, {513, 1026},
		{1024, 2048}, {1025, 3074}, {2048, 6144}, {2049, 8194},
	} {
		t.Run(fmt.Sprint(test.records), func(t *testing.T) {
			ctx, ledger := candidateReadScope(t, readaccounting.Counts{MemberVisits: test.visits})
			sorter := newProjectionSorter(ctx, t.TempDir())
			for index := 0; index < test.records; index++ {
				if err := sorter.add(candidateProjection{
					Path: fmt.Sprintf("src/%06d.go", index), OID: strings.Repeat("a", 40),
					DeclaredBytes: 1, Plane: PlaneRepository,
				}); err != nil {
					t.Fatal(err)
				}
			}
			path, err := sorter.finish()
			if err != nil {
				t.Fatal(err)
			}
			summary := newCorpusAccumulator().summary()
			summary.RegularCount, summary.RegularDeclaredBytes = test.records, int64(test.records)
			if err := validateCorpusSummary(ctx, summary, path); err != nil {
				t.Fatal(err)
			}
			if counts, err := ledger.Finish(); err != nil || counts != (readaccounting.Counts{MemberVisits: test.visits}) {
				t.Fatalf("projection visits = %+v, %v; want %d", counts, err, test.visits)
			}
		})
	}
}

func TestProjectionMergeMemberVisitFormula(t *testing.T) {
	for _, test := range []struct {
		records uint64
		visits  uint64
	}{
		{0, 0}, {512, 0}, {513, 513}, {1024, 1024}, {1025, 2049},
		{53_204, 364_324}, {20_000_000, 313_296_640},
	} {
		if got := projectionMergeMemberVisits(test.records); got != test.visits {
			t.Fatalf("projection merge visits for %d records = %d, want %d", test.records, got, test.visits)
		}
	}
	if got := MaxWholeRepositoryStrictOpenMemberVisits(); got != 353_296_640 {
		t.Fatalf("whole-repository strict-open maximum = %d", got)
	}
}

func TestReadAccountingCandidateScopes(t *testing.T) {
	fixture := newGitFixture(t)
	fixture.write("src/main.go", "package main\n")
	fixture.write("src/other.go", "package main\n")
	commit := fixture.commit("read accounting")
	root := t.TempDir()
	manifest, expected := buildFixture(t, fixture, commit, nil, root)
	var repository, caller, local uint64
	for _, member := range manifest.RepositoryMembers {
		repository += uint64(member.RecordCount)
	}
	for _, leaf := range manifest.CallerLeaves {
		caller += uint64(leaf.RecordCount)
	}
	for _, projection := range manifest.LocalProjections {
		for _, member := range projection.Members {
			local += uint64(member.RecordCount)
		}
	}
	if repository+caller == 0 || repository+caller > projectionChunkRecords {
		t.Fatal("fixture must exercise a single nonempty projection chunk")
	}
	want := readaccounting.Counts{ControlFileReads: 1, MemberVisits: 2*(repository+caller) + local}
	ctx, ledger := candidateReadScope(t, want)
	publication, err := OpenContext(ctx, root, expected)
	if err != nil {
		t.Fatal(err)
	}
	if counts, err := ledger.Finish(); err != nil || counts != want {
		t.Fatalf("strict open = %+v, %v; want %+v", counts, err, want)
	}
	fingerprint, err := CaptureControlFingerprintContext(t.Context(), root, manifest.State())
	if err != nil {
		t.Fatal(err)
	}
	warmContext, warm := candidateReadScope(t, readaccounting.Counts{})
	if current, err := fingerprint.MatchesContext(warmContext, root, publication.State()); err != nil || !current {
		t.Fatalf("warm metadata-only match = %v, %v", current, err)
	}
	if counts, err := warm.Finish(); err != nil || counts != (readaccounting.Counts{}) {
		t.Fatalf("warm no-open scope = %+v, %v", counts, err)
	}
	view, err := publication.Domain("go-local", "1")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		replayContext, replay := candidateReadScope(t, readaccounting.Counts{MemberVisits: repository})
		if err := view.ForEachRepositoryRecord(replayContext, func(Record) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if counts, err := replay.Finish(); err != nil || counts != (readaccounting.Counts{MemberVisits: repository}) {
			t.Fatalf("reused publication's current scope = %+v, %v", counts, err)
		}
	}
}

func TestReadAccountingAbsentMemberScopeAddsNoAllocations(t *testing.T) {
	raw := []byte("{}")
	plain := testing.AllocsPerRun(10, func() {
		var record Record
		if err := strictDecode(bytes.NewReader(raw), int64(len(raw)), &record); err != nil {
			panic(err)
		}
	})
	withoutScope := testing.AllocsPerRun(10, func() {
		var record Record
		if err := strictDecodeMember(context.Background(), bytes.NewReader(raw), int64(len(raw)), &record); err != nil {
			panic(err)
		}
	})
	if withoutScope != plain {
		t.Fatalf("unobserved member decode allocations=%g, plain=%g", withoutScope, plain)
	}
}

func candidateReadScope(t *testing.T, limits readaccounting.Counts) (context.Context, *readaccounting.Ledger) {
	t.Helper()
	ctx, ledger, err := readaccounting.Start(t.Context(), limits)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, ledger
}
