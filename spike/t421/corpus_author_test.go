package t421

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t401"
)

// These three tiny inputs exercise the real author algorithm, not the frozen
// corpus constructor. Expected objects are independently derived from bytes;
// only actual Git rev-parse/ls-tree outputs can issue an authored result.
func corpusAuthorTestSource(t *testing.T) *corpusAuthorSource {
	t.Helper()
	paths := []string{".phebs/profile.txt", "go.mod", "structural/first.go"}
	contents := [][]byte{[]byte("neutral tiny author\n"), []byte("module example.invalid/tiny\n\ngo 1.26\n"), []byte("package neutral\nconst Revision = \"a\"\n")}
	content := func(revision string, index int) []byte {
		if revision == "b" && index == 2 {
			return []byte("package neutral\nconst Revision = \"b\"\n")
		}
		return contents[index]
	}
	source := &corpusAuthorSource{recipe: SourceRecipe{GitObjectFormat: "sha1", AuthorName: "Neutral", AuthorEmail: "fixture@example.invalid",
		CommitterName: "Neutral", CommitterEmail: "fixture@example.invalid", Timestamp: 1_788_307_200, Timezone: "+0000",
		AuthoredManifestSchema: "t422-authored-source-manifest-v1"}, profile: CombinedProfile{Physical: PhysicalProfile{CombinedRegularFiles: 3}}}
	source.walkRecords = func(ctx context.Context, revision string, visit func(sourceTreeRecord) error) error {
		for index, path := range paths {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			raw := content(revision, index)
			if err := visit(sourceTreeRecord{Path: path, Bytes: uint64(len(raw)), BlobOID: gitSHA1ObjectID("blob", raw)}); err != nil {
				return err
			}
		}
		return nil
	}
	source.walkBlobs = func(ctx context.Context, revision string, visit func(string, []byte) error) error {
		for index, path := range paths {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if revision != "a" && index != 2 {
				continue
			}
			if err := visit(path, content(revision, index)); err != nil {
				return err
			}
		}
		return nil
	}
	parent := ""
	for _, name := range []string{"a", "b", "a-return"} {
		accumulator := newTreeInventoryAccumulator(nil)
		if err := source.walkRecords(t.Context(), name, accumulator.add); err != nil {
			t.Fatal(err)
		}
		identity, err := accumulator.finish()
		if err != nil {
			t.Fatal(err)
		}
		message := "neutral current revision " + name
		raw, err := canonicalGitCommitBytesFor(identity.TreeOID, parent, message, source.recipe)
		if err != nil {
			t.Fatal(err)
		}
		physical := PhysicalRevision{Name: name, CommitMessage: message, ExpectedTree: identity.TreeOID,
			ExpectedTreeInventory: identity.Inventory, ExpectedCommit: gitSHA1ObjectID("commit", raw)}
		source.revisions = append(source.revisions, physical)
		parent = physical.ExpectedCommit
	}
	return source
}

func TestCorpusAuthorFrozenBlobReuse(t *testing.T) {
	profile, err := frozenStructuralProfile()
	if err != nil {
		t.Fatal(err)
	}
	first := make(map[string]string, 3)
	for _, revision := range []string{"a", "b", "a-return"} {
		count := 0
		err := t401.WalkFrozenStructuralRevisionBlobs(profile, revision, func(path string, raw []byte) error {
			count++
			if path == "structural/cells/b000/c00000/f000.go" {
				first[revision] = gitSHA1ObjectID("blob", raw)
			}
			return nil
		})
		want := 1
		if revision == "a" {
			want = 514
		}
		if err != nil || count != want {
			t.Fatalf("revision %s blob count=%d want=%d: %v", revision, count, want, err)
		}
		stop := errors.New("bounded first record")
		err = t401.WalkFrozenTreeRecords(profile, revision, func(record t401.FrozenTreeRecord) error {
			if record.Path == "structural/cells/b000/c00000/f000.go" {
				if record.BlobOID != first[revision] {
					t.Fatal("exported bytes differ from the independently frozen tree record")
				}
				return stop
			}
			return nil
		})
		if !errors.Is(err, stop) {
			t.Fatal(err)
		}
	}
	if first["a"] != first["a-return"] || first["a"] == first["b"] {
		t.Fatal("blob stream lost exact A-B-A content continuity")
	}
	stop := errors.New("visitor refusal")
	if err := t401.WalkFrozenStructuralRevisionBlobs(profile, "a", func(string, []byte) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("blob visitor failure was lost")
	}
	if err := t401.WalkFrozenStructuralRevisionBlobs(profile, "future", func(string, []byte) error { return nil }); err == nil {
		t.Fatal("unknown revision was accepted")
	}
}

func TestCorpusAuthorStreamBoundsAndCurrentRevision(t *testing.T) {
	for _, name := range []string{"baseline", "delta", "return", "parent mismatch", "writer error", "canceled", "extra delta blob"} {
		t.Run(name, func(t *testing.T) {
			source := corpusAuthorTestSource(t)
			index, parent := 0, ""
			var output bytes.Buffer
			var target io.Writer = &output
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			valid := name == "baseline" || name == "delta" || name == "return"
			switch name {
			case "delta", "extra delta blob":
				index, parent = 1, source.revisions[0].ExpectedCommit
			case "return":
				index, parent = 2, source.revisions[1].ExpectedCommit
			case "parent mismatch":
				index, parent = 1, source.revisions[1].ExpectedCommit
			case "writer error":
				target = corpusAuthorFailWriter{}
			case "canceled":
				cancel()
			}
			if name == "extra delta blob" {
				walk := source.walkBlobs
				source.walkBlobs = func(ctx context.Context, _ string, visit func(string, []byte) error) error {
					return walk(ctx, "a", visit)
				}
			}
			writer := bufio.NewWriterSize(target, 64)
			err := source.writeRevision(ctx, writer, index, parent)
			if err == nil {
				err = writer.Flush()
			}
			if (err == nil) != valid {
				t.Fatalf("stream validity=%t: %v", valid, err)
			}
			if valid && (strings.Count(output.String(), "commit refs/heads/main\n") != 1 ||
				strings.Contains(output.String(), "refs/heads/t422") || strings.Contains(output.String(), "refs/heads/a")) {
				t.Fatal("stream authored extra revision refs")
			}
			if valid && index > 0 && (strings.Count(output.String(), "\nM 100644 ") != 1 || strings.Contains(output.String(), "deleteall")) {
				t.Fatal("later stream is not a one-file delta")
			}
		})
	}
}

type corpusAuthorFailWriter struct{}

func (corpusAuthorFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCorpusAuthorInventoryRefusesIncompleteAndMalformed(t *testing.T) {
	for _, name := range []string{"exact", "missing", "extra", "wrong oid", "wrong mode", "long record", "wrong inventory", "canceled"} {
		t.Run(name, func(t *testing.T) {
			source := corpusAuthorTestSource(t)
			var output bytes.Buffer
			if err := source.walkRecords(t.Context(), "a", func(record sourceTreeRecord) error {
				_, err := fmt.Fprintf(&output, "100644 blob %s\t%s\x00", record.BlobOID, record.Path)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			raw := output.String()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch name {
			case "missing":
				raw = raw[:len(raw)-1]
			case "extra":
				raw += "x"
			case "wrong oid":
				raw = raw[:12] + "z" + raw[13:]
			case "wrong mode":
				raw = "120000" + raw[6:]
			case "long record":
				raw = strings.Repeat("x", maxCorpusAuthorPath+100)
			case "wrong inventory":
				source.revisions[0].ExpectedTreeInventory.Records++
			case "canceled":
				cancel()
			}
			_, err := source.verifyInventory(ctx, strings.NewReader(raw), 0)
			if (err == nil) != (name == "exact") {
				t.Fatalf("inventory %s: %v", name, err)
			}
		})
	}
}
