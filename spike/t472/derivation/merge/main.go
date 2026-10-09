// Command merge canonicalizes and merges per-module scip-go index streams
// into the single root index.scip consumed by T47.2b section 2.
//
// Inputs: one or more --run REL=<path> flags in run order, each produced by
// the pinned scip-go 0.2.7 recipe (spike/t457/native_tool_pins_amd64.json).
// Exactly one run must carry the empty module rel (the repository root
// module). Every run file is the raw scip-go output: a sequence of protobuf
// LEN-framed scip.Index field messages in the order scip-go v0.2.7 writes
// them (metadata first, then one frame per document, then optional external
// symbols; see internal/index/scip.go and cmd/scip-go/main.go).
//
// Canonicalization (recorded in spike/t472/README.md):
//   - Metadata is rewritten to one fixed canonical form: project root
//     file:///phebs/t472-corpus/<repo> and argv tokens canonicalized to the
//     corpus-root recipe. The original metadata is validated first
//     (tool name scip-go, UTF-8 encoding, absolute file:// project root).
//   - Document relative paths are rebased from module-relative to
//     repository-relative by prefixing the run's module rel (empty for the
//     root module).
//   - Documents are emitted sorted by relative path, duplicates refused;
//     external symbols are dropped and counted (the pinned recipe passes
//     --skip-implementations, so zero are expected).
//
// The output is the same LEN-framed stream, re-marshaled with this module's
// scip bindings. Exit status is nonzero on any refusal.
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

const (
	fieldMetadata        = 1
	fieldDocuments       = 2
	fieldExternalSymbols = 3
	wireTypeLen          = 2
	maxVarintBytes       = 10
	canonicalRootPrefix  = "file:///phebs/t472-corpus/"
	// base2Bytes is the T47.2b section 2 index.scip bound.
	base2Bytes = 64 << 20
)

type runFlag struct {
	rel  string // module dir relative to repo root; "" for the root module
	path string
}

func (r *runFlag) Set(v string) error {
	rel, p, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("run %q: want REL=path", v)
	}
	if err := checkRel(rel); err != nil {
		return err
	}
	r.rel, r.path = rel, p
	return nil
}

func (r *runFlag) String() string { return r.rel + "=" + r.path }

// checkRel refuses anything that is not a clean relative slash path.
func checkRel(rel string) error {
	if rel == "" {
		return nil
	}
	if rel != path.Clean(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("module rel %q is not a clean relative path", rel)
	}
	return nil
}

type runStats struct {
	Rel               string   `json:"rel"`
	Docs              int      `json:"docs"`
	OutOfTreeDropped  int      `json:"out_of_tree_dropped"`
	OutOfTreeSample   []string `json:"out_of_tree_sample,omitempty"`
	Occurrences       int64    `json:"occurrences"`
	Symbols           int64    `json:"symbols"`
	ExternalSymbols   int      `json:"external_symbols"`
	RoundTripStable   int      `json:"round_trip_stable_docs"`
	RoundTripUnstable int      `json:"round_trip_unstable_docs"`
}

type report struct {
	Repo                   string     `json:"repo"`
	Pin                    string     `json:"pin"`
	Docs                   int        `json:"docs"`
	OutOfTreeDropped       int        `json:"out_of_tree_dropped"`
	Occurrences            int64      `json:"occurrences"`
	Symbols                int64      `json:"symbols"`
	ExternalSymbolsDropped int        `json:"external_symbols_dropped"`
	RoundTripUnstableDocs  int        `json:"round_trip_unstable_docs"`
	Bytes                  int64      `json:"bytes"`
	Runs                   []runStats `json:"runs"`
}

// readUvarint reads one unsigned varint with a bounded number of bytes.
func readUvarint(br *bufio.Reader) (uint64, error) {
	var x uint64
	var s uint
	for i := 0; i < maxVarintBytes; i++ {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		if b < 0x80 {
			if i == maxVarintBytes-1 && b > 1 {
				return 0, errors.New("varint overflows 64 bits")
			}
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, errors.New("varint longer than 10 bytes")
}

// readFrame reads one LEN-framed message; returns the scip.Index field
// number and the payload. io.EOF on a clean end of stream.
func readFrame(br *bufio.Reader) (int, []byte, error) {
	tag, err := readUvarint(br)
	if err != nil {
		return 0, nil, err
	}
	field, wire := tag>>3, tag&7
	if wire != wireTypeLen {
		return 0, nil, fmt.Errorf("frame tag %#x: wire type %d, want %d", tag, wire, wireTypeLen)
	}
	n, err := readUvarint(br)
	if err != nil {
		return 0, nil, fmt.Errorf("frame length: %w", err)
	}
	if n > uint64(base2Bytes) {
		return 0, nil, fmt.Errorf("frame length %d exceeds the %d-byte section 2 bound", n, base2Bytes)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(br, payload); err != nil {
		return 0, nil, fmt.Errorf("frame payload: %w", err)
	}
	return int(field), payload, nil
}

func writeFrame(w *bufio.Writer, field int, msg proto.Message) error {
	payload, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	var hdr [maxVarintBytes * 2]byte
	n := binary.PutUvarint(hdr[:], uint64(field)<<3|wireTypeLen)
	n += binary.PutUvarint(hdr[n:], uint64(len(payload)))
	if _, err := w.Write(hdr[:n]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

// loadRun parses one scip-go output file. It returns the parsed metadata
// (first frame), the documents with repository-relative paths, the dropped
// external symbol count, and round-trip instability count (documents whose
// original bytes are not reproduced by parse+re-marshal; informational,
// recorded, never silently ignored).
func loadRun(rel, file string) (*scip.Metadata, []*scip.Document, runStats, error) {
	var st runStats
	st.Rel = rel
	f, err := os.Open(file)
	if err != nil {
		return nil, nil, st, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)

	field, payload, err := readFrame(br)
	if err != nil {
		return nil, nil, st, fmt.Errorf("%s: first frame: %w", file, err)
	}
	if field != fieldMetadata {
		return nil, nil, st, fmt.Errorf("%s: first frame is field %d, want metadata (%d)", file, field, fieldMetadata)
	}
	meta := &scip.Metadata{}
	if err := proto.Unmarshal(payload, meta); err != nil {
		return nil, nil, st, fmt.Errorf("%s: metadata: %w", file, err)
	}
	if err := validateMetadata(meta); err != nil {
		return nil, nil, st, fmt.Errorf("%s: %w", file, err)
	}

	var docs []*scip.Document
	for {
		field, payload, err := readFrame(br)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, st, fmt.Errorf("%s: %w", file, err)
		}
		switch field {
		case fieldDocuments:
			doc := &scip.Document{}
			if err := proto.Unmarshal(payload, doc); err != nil {
				return nil, nil, st, fmt.Errorf("%s: document: %w", file, err)
			}
			if re, err := proto.Marshal(doc); err == nil && bytes.Equal(re, payload) {
				st.RoundTripStable++
			} else {
				st.RoundTripUnstable++
			}
			// scip-go emits documents for every file of every project
			// package, including cgo-processed copies that live in the
			// shared Go build cache outside the module. A cgo source file
			// ("import \"C\"") is compiled from its cache copy, so the
			// cache copy is the only document such a file gets; its path
			// escapes the module root ("../..") and is host- and
			// cache-dependent. Those documents are dropped and counted.
			// Any out-of-tree document from another class is refused
			// rather than dropped, so nothing silent can vanish.
			if !cleanRel(doc.RelativePath) {
				if !strings.Contains(doc.RelativePath, "/.cache/go-build/") {
					return nil, nil, st, fmt.Errorf("%s: unrecognized out-of-tree document %q", file, doc.RelativePath)
				}
				st.OutOfTreeDropped++
				if len(st.OutOfTreeSample) < 8 {
					st.OutOfTreeSample = append(st.OutOfTreeSample, doc.RelativePath)
				}
				continue
			}
			doc.RelativePath = joinRel(rel, doc.RelativePath)
			docs = append(docs, doc)
		case fieldExternalSymbols:
			st.ExternalSymbols++
		default:
			return nil, nil, st, fmt.Errorf("%s: unexpected frame field %d", file, field)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].RelativePath < docs[j].RelativePath })
	for i := 1; i < len(docs); i++ {
		if docs[i].RelativePath == docs[i-1].RelativePath {
			return nil, nil, st, fmt.Errorf("%s: duplicate document %q", file, docs[i].RelativePath)
		}
	}
	st.Docs = len(docs)
	for _, d := range docs {
		st.Occurrences += int64(len(d.Occurrences))
		st.Symbols += int64(len(d.Symbols))
	}
	return meta, docs, st, nil
}

func joinRel(rel, p string) string {
	if rel == "" {
		return p
	}
	return rel + "/" + p
}

// cleanRel reports whether p is a clean repo-relative slash path that stays
// inside the repository.
func cleanRel(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "./") &&
		p == path.Clean(p) && !strings.HasPrefix(p, "..")
}

func validateMetadata(m *scip.Metadata) error {
	if m.GetToolInfo().GetName() != "scip-go" {
		return fmt.Errorf("tool info name %q, want scip-go", m.GetToolInfo().GetName())
	}
	if m.GetToolInfo().GetVersion() == "" {
		return errors.New("tool info version is empty")
	}
	if !strings.HasPrefix(m.GetProjectRoot(), "file:///") {
		return fmt.Errorf("project root %q is not an absolute file:// URL", m.GetProjectRoot())
	}
	if m.GetTextDocumentEncoding() != scip.TextEncoding_UTF8 {
		return fmt.Errorf("text document encoding %v, want UTF8", m.GetTextDocumentEncoding())
	}
	return nil
}

// canonicalMetadata returns the fixed merged metadata for a repository.
func canonicalMetadata(version, repo, remote, pin string) *scip.Metadata {
	return &scip.Metadata{
		ToolInfo: &scip.ToolInfo{
			Name:    "scip-go",
			Version: version,
			Arguments: []string{
				"index",
				"--module-root", canonicalRootPrefix + repo,
				"--repository-remote", remote,
				"--module-version", pin,
				"--skip-implementations",
				"--output", "index.scip",
			},
		},
		ProjectRoot:          canonicalRootPrefix + repo,
		TextDocumentEncoding: scip.TextEncoding_UTF8,
	}
}

func checkDocumentPaths(docs []*scip.Document) error {
	for _, d := range docs {
		if !cleanRel(d.RelativePath) {
			return fmt.Errorf("refusing non-clean repository-relative document path %q", d.RelativePath)
		}
	}
	return nil
}

func statsOnly(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)
	var st runStats
	seen := map[string]int{}
	for {
		field, payload, err := readFrame(br)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch field {
		case fieldMetadata:
			meta := &scip.Metadata{}
			if err := proto.Unmarshal(payload, meta); err != nil {
				return err
			}
			fmt.Printf("metadata: tool=%s version=%s root=%s args=%q\n",
				meta.GetToolInfo().GetName(), meta.GetToolInfo().GetVersion(),
				meta.GetProjectRoot(), meta.GetToolInfo().GetArguments())
		case fieldDocuments:
			doc := &scip.Document{}
			if err := proto.Unmarshal(payload, doc); err != nil {
				return err
			}
			if !cleanRel(doc.RelativePath) {
				if !strings.Contains(doc.RelativePath, "/.cache/go-build/") {
					return fmt.Errorf("unrecognized out-of-tree document %q", doc.RelativePath)
				}
				st.OutOfTreeDropped++
				if len(st.OutOfTreeSample) < 8 {
					st.OutOfTreeSample = append(st.OutOfTreeSample, doc.RelativePath)
				}
				continue
			}
			st.Docs++
			st.Occurrences += int64(len(doc.Occurrences))
			st.Symbols += int64(len(doc.Symbols))
			seen[doc.RelativePath]++
		case fieldExternalSymbols:
			st.ExternalSymbols++
		default:
			return fmt.Errorf("unexpected frame field %d", field)
		}
	}
	dupes := 0
	for _, n := range seen {
		if n > 1 {
			dupes++
		}
	}
	vendor, testdoc := 0, 0
	for p := range seen {
		if strings.Contains(p, "vendor/") {
			vendor++
		}
		if strings.HasSuffix(p, "_test.go") {
			testdoc++
		}
	}
	fmt.Printf("docs=%d out_of_tree_dropped=%d occurrences=%d symbols=%d external=%d dupes=%d vendor_docs=%d test_docs=%d\n",
		st.Docs, st.OutOfTreeDropped, st.Occurrences, st.Symbols, st.ExternalSymbols, dupes, vendor, testdoc)
	fmt.Printf("out_of_tree_sample: %q\n", st.OutOfTreeSample)
	return nil
}

func main() {
	var runs multiFlag
	var repo, remote, pin, out string
	var maxBytes int64
	var statsFile string
	flag.StringVar(&repo, "repo", "", "repository name (e.g. containerd)")
	flag.StringVar(&remote, "remote", "", "repository remote (e.g. github.com/containerd/containerd)")
	flag.StringVar(&pin, "pin", "", "pinned commit (full or 12-hex prefix)")
	flag.StringVar(&out, "out", "", "output path for the merged index.scip")
	flag.StringVar(&statsFile, "stats", "", "inspect one scip-go output file and exit")
	flag.Int64Var(&maxBytes, "max-bytes", base2Bytes, "refuse output larger than this many bytes")
	flag.Var(&runs, "run", "RELLPATH=path: one scip-go output per module, in run order; REL is the module dir relative to the repo root (empty for the root module)")
	flag.Parse()

	if statsFile != "" {
		if err := statsOnly(statsFile); err != nil {
			fmt.Fprintln(os.Stderr, "merge:", err)
			os.Exit(1)
		}
		return
	}
	if repo == "" || remote == "" || pin == "" || out == "" || len(runs) == 0 {
		fmt.Fprintln(os.Stderr, "merge: --repo, --remote, --pin, --out, and at least one --run are required")
		os.Exit(2)
	}

	roots := 0
	rels := map[string]bool{}
	for _, r := range runs {
		if rels[r.rel] {
			fmt.Fprintf(os.Stderr, "merge: duplicate module rel %q\n", r.rel)
			os.Exit(1)
		}
		rels[r.rel] = true
		if r.rel == "" {
			roots++
		}
	}
	if roots != 1 {
		fmt.Fprintln(os.Stderr, "merge: exactly one run must carry the empty module rel")
		os.Exit(1)
	}

	var metas []*scip.Metadata
	var docs []*scip.Document
	var rep report
	rep.Repo, rep.Pin = repo, pin
	var version string
	for _, r := range runs {
		meta, d, st, err := loadRun(r.rel, r.path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "merge:", err)
			os.Exit(1)
		}
		if version == "" {
			version = meta.GetToolInfo().GetVersion()
		}
		if meta.GetToolInfo().GetVersion() != version {
			fmt.Fprintf(os.Stderr, "merge: %s: tool version %q differs from %q\n", r.path, meta.GetToolInfo().GetVersion(), version)
			os.Exit(1)
		}
		metas = append(metas, meta)
		docs = append(docs, d...)
		rep.ExternalSymbolsDropped += st.ExternalSymbols
		rep.OutOfTreeDropped += st.OutOfTreeDropped
		rep.RoundTripUnstableDocs += st.RoundTripUnstable
		rep.Runs = append(rep.Runs, st)
	}
	if err := checkDocumentPaths(docs); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].RelativePath < docs[j].RelativePath })
	for i := 1; i < len(docs); i++ {
		if docs[i].RelativePath == docs[i-1].RelativePath {
			fmt.Fprintf(os.Stderr, "merge: duplicate document across runs: %q\n", docs[i].RelativePath)
			os.Exit(1)
		}
	}

	_ = metas // parsed and validated; the merged stream carries the canonical metadata

	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if err := writeFrame(w, fieldMetadata, canonicalMetadata(version, repo, remote, pin)); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
	for _, d := range docs {
		if err := writeFrame(w, fieldDocuments, d); err != nil {
			fmt.Fprintln(os.Stderr, "merge:", err)
			os.Exit(1)
		}
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
	if err := f.Sync(); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}

	fi, err := os.Stat(tmp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
	if fi.Size() > maxBytes {
		fmt.Fprintf(os.Stderr, "merge: merged stream is %d bytes, over the %d-byte bound; refusing to place an over-bound artifact\n", fi.Size(), maxBytes)
		os.Exit(1)
	}
	if err := os.Rename(tmp, out); err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}

	rep.Docs = len(docs)
	for _, d := range docs {
		rep.Occurrences += int64(len(d.Occurrences))
		rep.Symbols += int64(len(d.Symbols))
	}
	rep.Bytes = fi.Size()
	enc, err := json.MarshalIndent(&rep, "", " ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "merge:", err)
		os.Exit(1)
	}
	fmt.Println(string(enc))
}

type multiFlag []runFlag

func (m *multiFlag) Set(v string) error {
	var r runFlag
	if err := r.Set(v); err != nil {
		return err
	}
	*m = append(*m, r)
	return nil
}

func (m *multiFlag) String() string { return fmt.Sprint(*m) }
