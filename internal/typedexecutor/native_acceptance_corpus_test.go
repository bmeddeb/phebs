package typedexecutor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

const acceptanceCorpusRepo = "github.com/bazelbuild/remote-apis-sdks"
const acceptanceCorpusCommit = "d5824b1a2286806b07efd030aa3a139c4f540157"
const acceptanceCorpusArchive = "sha256:c9ecf680cd7bd0d88d8a6d1a0084a09c0a9dc45145fc28fbdcda888586d54bcc"
const acceptanceCorpusOracle = "sha256:0620be2b4c5631e01626f6a27b18621908f045edbf73a00ceffd0b638bb616d1"
const acceptanceCorpusGitMax = 4 << 20
const acceptanceCorpusGitConfig = "[core]\n\trepositoryformatversion = 0\n\tbare = true\n"

var acceptanceCorpusGitPack = regexp.MustCompile(`^objects/pack/pack-[0-9a-f]{40}\.(pack|idx)$`)

func acceptanceCorpusRoots(cohort string) []string {
	switch cohort {
	case "ordinary":
		return []string{"//go/pkg/moreflag:moreflag", "//go/pkg/cache:cache", "//go/pkg/outerr:outerr"}
	case "proto":
		return []string{"//go/api/command:command", "//go/pkg/command:command"}
	case "fanout":
		return []string{"//go/pkg/client:client", "//go/pkg/cas:cas", "//go/pkg/rexec:rexec"}
	}
	return nil
}

type acceptanceOraclePoint struct {
	Cohort string   `json:"cohort"`
	Path   string   `json:"path"`
	Range  [3]int32 `json:"range"`
}
type acceptanceOracleSymbol struct {
	Package       string                  `json:"package"`
	Name          string                  `json:"name"`
	Definition    acceptanceOraclePoint   `json:"definition"`
	References    []acceptanceOraclePoint `json:"references"`
	Signature     string                  `json:"signature"`
	Documentation string                  `json:"documentation"`
}

// These coordinates are the unchanged public-source oracle, not generated
// output or a caller-provided authority. The portable test binds the full file.
var acceptanceCorpusSymbols = []acceptanceOracleSymbol{
	{Package: acceptanceCorpusRepo + "/go/pkg/outerr", Name: "NewOutWriter", Definition: acceptanceOraclePoint{"ordinary", "go/pkg/outerr/outerr.go", [3]int32{74, 5, 17}}, References: []acceptanceOraclePoint{{"fanout", "go/pkg/rexec/rexec.go", [3]int32{413, 72, 84}}}, Signature: "func NewOutWriter(oe OutErr) io.Writer", Documentation: "NewOutWriter provides an io.Writer implementation for writing to the out stream of an OutErr."},
	{Package: acceptanceCorpusRepo + "/go/pkg/outerr", Name: "NewErrWriter", Definition: acceptanceOraclePoint{"ordinary", "go/pkg/outerr/outerr.go", [3]int32{91, 5, 17}}, References: []acceptanceOraclePoint{{"fanout", "go/pkg/rexec/rexec.go", [3]int32{429, 72, 84}}}, Signature: "func NewErrWriter(oe OutErr) io.Writer", Documentation: "NewErrWriter provides an io.Writer implementation for writing to the err stream of an OutErr."},
	{Package: acceptanceCorpusRepo + "/go/pkg/command", Name: "NewRemoteErrorResult", Definition: acceptanceOraclePoint{"proto", "go/pkg/command/command.go", [3]int32{460, 5, 25}}, References: []acceptanceOraclePoint{
		{"fanout", "go/pkg/rexec/rexec.go", [3]int32{147, 17, 37}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{150, 17, 37}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{163, 43, 63}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{281, 23, 43}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{343, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{359, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{379, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{445, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{451, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{457, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{475, 25, 45}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{480, 25, 45}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{501, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{505, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{539, 22, 42}}, {"fanout", "go/pkg/rexec/rexec.go", [3]int32{556, 22, 42}},
	}, Signature: "func NewRemoteErrorResult(err error) *Result", Documentation: "NewRemoteErrorResult constructs a Result from a remote error."},
}

// Check the exact frozen archive independently of the preparation receipt.
// Only its 128 regular source files may enter the acceptance inventory.
func acceptanceCorpusFiles(ctx context.Context, archive []byte) ([]typedindex.BundleFile, error) {
	if len(archive) != 249496 || acceptanceDigest(archive) != acceptanceCorpusArchive {
		return nil, typedindex.Invalid
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	r := tar.NewReader(io.LimitReader(gz, 2<<20))
	prefix := "remote-apis-sdks-" + acceptanceCorpusCommit
	files, seen := []typedindex.BundleFile{}, map[string]bool{}
	var total int64
	for records := 0; ; records++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		h, next := r.Next()
		if next == io.EOF {
			break
		}
		if next != nil {
			return nil, next
		}
		if records == 0 && h.Typeflag == tar.TypeXGlobalHeader && h.Name == "pax_global_header" && len(h.PAXRecords) == 1 && h.PAXRecords["comment"] == acceptanceCorpusCommit {
			continue
		}
		if records >= 168 || h.Name != prefix && !strings.HasPrefix(h.Name, prefix+"/") {
			return nil, typedindex.Invalid
		}
		name := strings.TrimPrefix(h.Name, prefix+"/")
		if h.Typeflag == tar.TypeDir {
			if h.Size != 0 {
				return nil, typedindex.Invalid
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || name == "." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00\r\n") || seen[name] || h.Size < 0 || h.Size > 1079184-total {
			return nil, typedindex.Invalid
		}
		b, e := io.ReadAll(io.LimitReader(r, h.Size+1))
		if e != nil || int64(len(b)) != h.Size {
			return nil, typedindex.Invalid
		}
		seen[name], total = true, total+h.Size
		files = append(files, typedindex.BundleFile{Path: "source/" + name, Bytes: h.Size, Digest: acceptanceDigest(b), Executable: h.Mode&0111 != 0})
	}
	if len(files) != 128 || total != 1079184 {
		return nil, typedindex.Invalid
	}
	slices.SortFunc(files, func(a, b typedindex.BundleFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// A trusted source-object input permits no repository configuration, hooks,
// alternates or loose objects. Git independently checks its exact commit/tree.
func acceptanceCorpusGitFiles(raw []byte) (map[string][]byte, error) {
	if len(raw) == 0 || len(raw) > acceptanceCorpusGitMax {
		return nil, typedindex.Invalid
	}
	input := bytes.NewReader(raw)
	r, files := tar.NewReader(input), map[string][]byte{}
	pack := ""
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil || h.Typeflag != tar.TypeReg || h.Format != tar.FormatUSTAR || len(files) >= 5 || files[h.Name] != nil || h.Size < 1 || h.Size > acceptanceCorpusGitMax {
			return nil, typedindex.Invalid
		}
		switch h.Name {
		case "HEAD", "config", "shallow":
		default:
			if !acceptanceCorpusGitPack.MatchString(h.Name) {
				return nil, typedindex.Invalid
			}
			stem := strings.TrimSuffix(strings.TrimSuffix(h.Name, ".idx"), ".pack")
			if pack != "" && pack != stem {
				return nil, typedindex.Invalid
			}
			pack = stem
		}
		b, err := io.ReadAll(io.LimitReader(r, h.Size+1))
		if err != nil || int64(len(b)) != h.Size {
			return nil, typedindex.Invalid
		}
		files[h.Name] = b
	}
	if len(raw)%512 != 0 || len(raw) < 1024 || !bytes.Equal(raw[len(raw)-1024:], make([]byte, 1024)) || input.Len() > 10240 || bytes.IndexFunc(raw[len(raw)-input.Len():], func(r rune) bool { return r != 0 }) >= 0 {
		return nil, typedindex.Invalid
	}
	if len(files) != 5 || string(files["HEAD"]) != acceptanceCorpusCommit+"\n" || string(files["shallow"]) != acceptanceCorpusCommit+"\n" || string(files["config"]) != acceptanceCorpusGitConfig || pack == "" || len(files[pack+".pack"]) == 0 || len(files[pack+".idx"]) == 0 {
		return nil, typedindex.Invalid
	}
	return files, nil
}

func acceptanceCorpusVerifyGit(ctx context.Context, repo string, expected []typedindex.BundleFile) (err error) {
	if err = gitobj.EnsureCommit(ctx, repo, acceptanceCorpusCommit); err != nil {
		return err
	}
	tree, err := gitobj.Output(ctx, repo, 128<<10, "ls-tree", "-r", "-z", "--full-tree", acceptanceCorpusCommit)
	if err != nil || len(tree) == 0 || tree[len(tree)-1] != 0 {
		return errors.Join(err, errors.New("source Git tree"))
	}
	rows := strings.Split(string(tree[:len(tree)-1]), "\x00")
	if len(rows) != len(expected) {
		return errors.New("source Git tree cardinality")
	}
	reader, err := gitobj.NewBatchBlobReader(ctx, repo)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	for i, row := range rows {
		meta, name, found := strings.Cut(row, "\t")
		fields := strings.Fields(meta)
		f := expected[i]
		mode := "100644"
		if f.Executable {
			mode = "100755"
		}
		if !found || name != strings.TrimPrefix(f.Path, "source/") || len(fields) != 3 || fields[0] != mode || fields[1] != "blob" || !gitobj.IsObjectID(fields[2]) {
			return errors.New("source Git tree differs from frozen archive")
		}
		b, e := reader.ReadBlob(ctx, fields[2], f.Bytes)
		if e != nil || acceptanceDigest(b) != f.Digest {
			return errors.Join(e, errors.New("source Git blob differs from frozen archive"))
		}
	}
	return nil
}

var acceptanceCorpusGitProof = flag.String("typed-native-corpus-git-proof", "", "opt-in read-only reviewed frozen public original Git object tar")
var acceptanceCorpusArchiveProof = flag.String("typed-native-corpus-archive-proof", "", "opt-in exact frozen public corpus archive for original Git proof")

func TestNativeAcceptanceCorpusOriginalGit(t *testing.T) {
	if *acceptanceCorpusGitProof == "" && *acceptanceCorpusArchiveProof == "" {
		t.Skip("requires reviewed original Git objects and frozen public archive")
	}
	if *acceptanceCorpusGitProof == "" || *acceptanceCorpusArchiveProof == "" {
		t.Fatal("partial original Git proof")
	}
	raw, err := acceptanceCorpusProofRead(*acceptanceCorpusGitProof, acceptanceCorpusGitMax)
	if err != nil || acceptanceDigest(raw) != "sha256:6b37262ebca797aa6ffb086d54dd5f8faa698745a399054ca9759441e4cf4a90" {
		t.Fatal("reviewed original Git input", err)
	}
	files, err := acceptanceCorpusGitFiles(raw)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := acceptanceCorpusProofRead(*acceptanceCorpusArchiveProof, 249496)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := acceptanceCorpusFiles(t.Context(), archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"good", "corrupt-pack", "tree-count", "tree-mode", "blob-digest"} {
		t.Run(fault, func(t *testing.T) {
			dir := t.TempDir()
			want := slices.Clone(expected)
			if err := os.MkdirAll(filepath.Join(dir, "objects/pack"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "refs"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, body := range files {
				b := slices.Clone(body)
				if fault == "corrupt-pack" && strings.HasSuffix(name, ".pack") {
					b[0] ^= 1
				}
				if err := os.WriteFile(filepath.Join(dir, name), b, 0400); err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "tree-count":
				want = want[:127]
			case "tree-mode":
				want[0].Executable = !want[0].Executable
			case "blob-digest":
				want[0].Digest = acceptanceDigest([]byte("changed source"))
			}
			if err := acceptanceCorpusVerifyGit(t.Context(), dir, want); (err == nil) != (fault == "good") {
				t.Fatal(fault, err)
			}
		})
	}
}

func acceptanceCorpusProofRead(name string, bound int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, bound+1))
	if err != nil || int64(len(b)) > bound {
		return nil, typedindex.Invalid
	}
	return b, nil
}

func TestNativeAcceptanceCorpusOracle(t *testing.T) {
	raw, err := os.ReadFile("../../spike/t451b/public-oracle.json")
	if err != nil || acceptanceDigest(raw) != acceptanceCorpusOracle {
		t.Fatal("frozen public oracle identity", err)
	}
	var oracle struct {
		Symbols []acceptanceOracleSymbol `json:"symbols"`
	}
	if json.Unmarshal(raw, &oracle) != nil || !bytes.Equal(acceptanceJSON(oracle.Symbols), acceptanceJSON(acceptanceCorpusSymbols)) {
		t.Fatal("compiled coordinates differ from frozen public oracle")
	}
	if _, err = acceptanceCorpusFiles(t.Context(), []byte("unapproved source")); !errors.Is(err, typedindex.Invalid) {
		t.Fatal("unfrozen archive admitted")
	}
}

func TestNativeAcceptanceCorpusConfig(t *testing.T) {
	h := acceptanceDigest([]byte("fixed"))
	c := nativeAcceptanceConfig{Schema: acceptanceCorpusSchema, ID: "corpus-1", SourceCommit: strings.Repeat("a", 40), Source: typedindex.Source{Repository: acceptanceCorpusRepo, Incarnation: "observed", Generation: h, Commit: acceptanceCorpusCommit}, TestSHA256: h, HelperSHA256: h, EngineSHA256: h, SeedSHA256: h, InventorySHA256: h, ProfileSHA256: h, SelectionSHA256: h, ImageSHA256: h, MkfsSHA256: h, UniverseSHA256: h, ProfileEpoch: 1, DeploymentSHA256: h, Policy: typedindex.MeasuredPolicy(), Cohort: "ordinary", SourceGitSHA256: h}
	for _, cohort := range []string{"ordinary", "proto", "fanout"} {
		c.Cohort = cohort
		if _, err := parseAcceptance(acceptanceJSON(c)); err != nil || !c.caseValid("success") || c.caseValid("canary") || c.caseValid("cancel") || len(c.roots()) < 2 {
			t.Fatal("closed corpus selector", cohort, err)
		}
	}
	for _, mutate := range []func(*nativeAcceptanceConfig){func(c *nativeAcceptanceConfig) { c.Cohort = "all" }, func(c *nativeAcceptanceConfig) { c.Source.Commit = strings.Repeat("b", 40) }, func(c *nativeAcceptanceConfig) { c.Source.Repository = acceptanceRepo }, func(c *nativeAcceptanceConfig) { c.Schema = acceptanceSchema }, func(c *nativeAcceptanceConfig) { c.Policy.WallSeconds++ }} {
		v := c
		mutate(&v)
		if _, err := parseAcceptance(acceptanceJSON(v)); err == nil {
			t.Fatal("widened corpus selector")
		}
	}
}

func TestNativeAcceptanceCorpusProvisionOrdering(t *testing.T) {
	c := nativeAcceptanceConfig{Schema: acceptanceCorpusSchema, Cohort: "ordinary"}
	frozen := c.roots()
	// This is the actual canonical root order from trusted BuildSelection for
	// the fresh frozen ordinary preparation, independent of declared order.
	provisioned := []string{"//go/pkg/cache:cache", "//go/pkg/moreflag:moreflag", "//go/pkg/outerr:outerr"}
	if !slices.Equal(c.selectionRoots(), provisioned) || !slices.Equal(c.roots(), frozen) || slices.Equal(frozen, provisioned) {
		t.Fatal("provision order rejected or frozen order changed")
	}
	for _, bad := range [][]string{provisioned[:2], append(slices.Clone(provisioned), "//..."), {provisioned[0], provisioned[0], provisioned[2]}} {
		if slices.Equal(c.selectionRoots(), bad) {
			t.Fatal("selection root membership widened")
		}
	}
}

func TestNativeAcceptanceCorpusGitShape(t *testing.T) {
	for _, fault := range []string{"good", "config", "commit", "hook", "second-pack", "alias"} {
		files := map[string]string{"HEAD": acceptanceCorpusCommit + "\n", "shallow": acceptanceCorpusCommit + "\n", "config": acceptanceCorpusGitConfig, "objects/pack/pack-" + strings.Repeat("a", 40) + ".pack": "pack", "objects/pack/pack-" + strings.Repeat("a", 40) + ".idx": "index"}
		switch fault {
		case "config":
			files["config"] += "[include]\npath=/private/authority\n"
		case "commit":
			files["HEAD"] = strings.Repeat("b", 40) + "\n"
		case "hook":
			files["hooks/post-checkout"] = "sh"
		case "second-pack":
			files["objects/pack/pack-"+strings.Repeat("b", 40)+".idx"] = "index"
		}
		var raw bytes.Buffer
		w := tar.NewWriter(&raw)
		for name, body := range files {
			h := &tar.Header{Name: name, Size: int64(len(body)), Mode: 0400, Format: tar.FormatUSTAR}
			if fault == "alias" && name == "HEAD" {
				h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, "elsewhere", 0
			}
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if h.Size > 0 {
				if _, err := w.Write([]byte(body)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := acceptanceCorpusGitFiles(raw.Bytes()); (err == nil) != (fault == "good") {
			t.Fatal(fault, err)
		}
		if fault == "good" {
			for _, bad := range [][]byte{raw.Bytes()[:raw.Len()-1024], append(slices.Clone(raw.Bytes()), []byte("trailing")...)} {
				if _, err := acceptanceCorpusGitFiles(bad); err == nil {
					t.Fatal("unbounded or truncated source Git transport")
				}
			}
		}
	}
}
