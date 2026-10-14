//go:build t581oracle

// Command oracle is the T58.1 independent Go gRPC consumer/registration
// site-coverage oracle. It is written from docs/GRPC_CONSUMER_QUALITY_PROTOCOL.md
// §1 and §3 (the frozen GD1-GD16 rules and the "Row identity used for scoring"
// table) and the phebs.grpc.consumer.go pack card only. It reads no phebs code,
// no extractor source, no test, no fixture and no prior phebs output, and shares
// no code with internal/extract/extractors/grpcgo.
//
// A unit is one repository at one pinned commit (protocol §2). Pass 1 walks
// every *_grpc.pb.go stub in that unit to build the repository-scoped service,
// method and registration index (GD1, GD3-GD6, GD11). Pass 2 walks every other
// .go file in the same unit and reports each frozen call and registration shape
// with its scored state (GD7-GD12), plus whole-file gaps (GD13-GD15).
//
// GD10's out-of-domain candidates are deliberately **not** rows. §6 requires
// them published as a count, and a candidate at a span the oracle did not
// report is spurious whether or not the oracle said so there, so enumerating
// them would cost millions of unscored rows across the locked population and
// change no scored outcome. Each unit therefore ends with exactly one
// `t581-out-of-domain-v1` aggregate: the total, GD10's two causes kept
// separate, and how many files hold at least one candidate.
//
// The oracle deliberately does not classify code roles: GD16 fixes the five role
// names and their precedence but not their detection, so roles stay the
// exporter's business and the scorer groups by what it reports.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	unitSchema = "t581-unit-v1"
	siteSchema = "t581-site-v1"

	// outOfDomainSchema is the per-unit GD10 aggregate §6 requires to be
	// published. It is a separate schema, not a site row, so no consumer can
	// mistake an unscored count for a scored state.
	outOfDomainSchema = "t581-out-of-domain-v1"

	// GD13: a candidate file whose content exceeds 4 MiB is a whole-file gap.
	// "Exceeds" is read strictly, so a file of exactly 4 MiB is still scanned.
	maxFileSize = 4 * 1024 * 1024

	// GD1: stub identity is this filename suffix and nothing else.
	stubSuffix = "_grpc.pb.go"
)

// The complete t581-site-v1 state vocabulary (protocol §3, "Scored state
// vocabulary", plus the two whole-file gap states GD13 and GD14 name).
// out_of_domain is **not** a member: GD10 gives such a candidate no row, and
// this oracle publishes the class per unit as an aggregate instead.
const (
	stateResolvedCall          = "resolved_call"
	stateAmbiguousCall         = "ambiguous_call"
	stateResolvedRegistration  = "resolved_registration"
	stateAmbiguousRegistration = "ambiguous_registration"
	stateGapTooLarge           = "gap_source_too_large"
	stateGapParseError         = "gap_parse_error"
)

// GD10's two causes. The protocol freezes no vocabulary for them. When they
// lived in the unscored object column that left the spellings free; as keys of
// a published aggregate they are now pinned by this pair, by outOfDomainReasons
// and by TestOutOfDomainAggregate. A future third cause must be added as its
// own key here rather than folded into one of these two.
const (
	reasonUnindexedSelector = "unindexed_selector"
	reasonZeroArguments     = "zero_arguments"
)

// outOfDomainReasons is the closed cause set, in the order GD10's checks run:
// an unindexed selector name is decided before the argument count, so a
// zero-argument call to an unindexed name counts once, as unindexed_selector.
// Every aggregate carries every reason, including the ones with no candidate,
// so a zero count is never indistinguishable from an absent key.
var outOfDomainReasons = []string{reasonUnindexedSelector, reasonZeroArguments}

var (
	// GD3: a service binding value. Its short name is the substring after the
	// last dot, which for a package-less name is the whole string.
	serviceNamePattern = regexp.MustCompile(`^[A-Za-z_]\w*(\.[A-Za-z_]\w*)*$`)

	// GD4: the anchored full-method pattern. Group 1 is the service full name.
	// AMBIGUITY: §3 calls the bare method name "the second capture", but the
	// frozen pattern has three capture groups and its second is the inner
	// repeated `(\.[A-Za-z_]\w*)*` dot segment, which cannot be a method name.
	// The row-identity table's object `/<service FQN>/<method>` and §1's
	// `/package.Service/Method` both make the intent unambiguous, so this
	// oracle takes the method name from group 3 - the segment after the last
	// slash.
	fullMethodPattern = regexp.MustCompile(`^/([A-Za-z_]\w*(\.[A-Za-z_]\w*)*)/([A-Za-z_]\w*)$`)

	// GD11: `Register<Short>Server` with a non-empty <Short>. The capture is
	// greedy, so a name containing "Server" before the final suffix yields the
	// longest <Short>; generated stubs do not produce such a name.
	registerPattern = regexp.MustCompile(`^Register(.+)Server$`)
)

// row is one output record. Its field order and JSON names are fixed by the
// round's t581-site-v1 schema.
type row struct {
	Schema         string `json:"schema"`
	Unit           string `json:"unit"`
	Path           string `json:"path"`
	StartByte      int    `json:"start_byte"`
	EndByte        int    `json:"end_byte"`
	StartLine      int    `json:"start_line"`
	EndLine        int    `json:"end_line"`
	State          string `json:"state"`
	Object         string `json:"object"`
	CandidateCount int    `json:"candidate_count"`
}

// outOfDomainAggregate is the one t581-out-of-domain-v1 record a unit emits,
// after all of that unit's site rows. It carries GD10's silence as a count
// because §6 asks for a published count and not an enumeration, and because no
// scored check consumes a candidate the oracle reported nothing for: an
// extractor row at such a span is spurious either way.
//
// Its field order and JSON names are fixed by this round's schema.
type outOfDomainAggregate struct {
	Schema string `json:"schema"`
	Unit   string `json:"unit"`
	// Total is the number of out-of-domain candidates in the unit. It always
	// equals the sum of ByReason.
	Total int `json:"total"`
	// ByReason holds every known GD10 cause, including the causes with no
	// candidate in this unit, so the record's shape is stable across units.
	ByReason map[string]int `json:"by_reason"`
	// FilesWithCandidates is the count of distinct non-stub files holding at
	// least one out-of-domain candidate. It says how spread out GD10's silence
	// is, which a total alone cannot: one file with a thousand unindexed
	// selectors and a thousand files with one each are different blind spots.
	FilesWithCandidates int `json:"files_with_candidates"`
}

// zeroedReasons returns a ByReason map holding every known GD10 cause at zero,
// so an aggregate's shape never depends on which causes a unit happened to hit.
func zeroedReasons() map[string]int {
	m := make(map[string]int, len(outOfDomainReasons))
	for _, reason := range outOfDomainReasons {
		m[reason] = 0
	}
	return m
}

// merge folds one unit's aggregate into a cross-unit total. FilesWithCandidates
// is a sum of per-unit distinct-file counts, not a corpus-wide distinct count:
// paths are repository-relative, so the same path in two units is two files.
func (a *outOfDomainAggregate) merge(other outOfDomainAggregate) {
	a.Total += other.Total
	a.FilesWithCandidates += other.FilesWithCandidates
	for reason, n := range other.ByReason {
		a.ByReason[reason] += n
	}
}

// outOfDomainTally accumulates one unit's GD10 candidates while pass 2 runs. It
// holds no span, so its cost per candidate is one increment plus one set insert
// on the first candidate in a file, and its memory is bounded by the number of
// files with candidates rather than by the number of candidates.
type outOfDomainTally struct {
	total    int
	byReason map[string]int
	files    map[string]bool
}

func newOutOfDomainTally() *outOfDomainTally {
	return &outOfDomainTally{byReason: zeroedReasons(), files: map[string]bool{}}
}

// add records one out-of-domain candidate in path for the given GD10 cause.
func (t *outOfDomainTally) add(path, reason string) {
	t.total++
	t.byReason[reason]++
	t.files[path] = true
}

// aggregate renders the tally as the unit's published record. The map is copied
// so the emitted record cannot be mutated by a later add, and every known
// reason is present even at zero.
func (t *outOfDomainTally) aggregate(unit string) outOfDomainAggregate {
	byReason := make(map[string]int, len(t.byReason))
	for reason, n := range t.byReason {
		byReason[reason] = n
	}
	return outOfDomainAggregate{
		Schema:              outOfDomainSchema,
		Unit:                unit,
		Total:               t.total,
		ByReason:            byReason,
		FilesWithCandidates: len(t.files),
	}
}

// sink receives every record of one unit in emission order: that unit's site
// rows first, in sorted-path then source order, and then its single
// out-of-domain aggregate. Only row and outOfDomainAggregate are ever passed;
// each encodes to exactly one JSON line.
type sink func(any)

// unitRecord is one line of units.jsonl.gz. The enumerator owns this schema.
type unitRecord struct {
	Schema     string   `json:"schema"`
	Unit       string   `json:"unit"`
	Repository string   `json:"repository"`
	Commit     string   `json:"commit"`
	Dir        string   `json:"dir"`
	GoFiles    []string `json:"go_files"`
	StubFiles  []string `json:"stub_files"`
}

// blobReader yields the bytes of a fixed, ordered list of paths at one commit.
type blobReader interface {
	// next returns the content of path, which must be the next unread path.
	next(path string) ([]byte, error)
	// close releases the underlying process or state.
	close() error
}

// corpus opens ordered blob reads for one unit.
type corpus interface {
	open(dir, commit string, paths []string) (blobReader, error)
}

// ---------------------------------------------------------------------------
// Index (GD3-GD6, GD11)
// ---------------------------------------------------------------------------

// serviceBinding is one GD3 `ServiceName: "<full name>"` literal in one stub.
type serviceBinding struct {
	full  string
	short string
	stub  string
}

// methodEntry is one GD4 full-method literal attributed under GD5.
type methodEntry struct {
	service string // full name, GD4 group 1
	method  string // bare name, GD4 group 3
	full    string // "/<service>/<method>"
	stub    string
	lineage string // "binding" or "synthetic" (GD5)
}

// registrationEntry is one GD11 registration name bound to one service binding.
type registrationEntry struct {
	name    string // RegisterXServer
	service string // full name
	stub    string
}

// index is the repository-scoped aggregate GD6 requires. Nothing here ever
// crosses a unit, a repository or a commit boundary.
type index struct {
	// methods maps a bare method name to every entry bearing it (GD8).
	methods map[string][]methodEntry
	// registrations maps a Register…Server name to every matching binding
	// (GD12). A name with no matching binding is absent, not empty: GD11
	// defines a registration name as one "bound to" that file's bindings, so
	// an unbound FuncDecl enters nothing and GD9's precedence does not fire
	// for it. That reading is this oracle's choice; the frozen text does not
	// say what a Register…Server declaration with no same-file binding means.
	registrations map[string][]registrationEntry

	seenMethod map[methodEntry]bool
	seenReg    map[registrationEntry]bool
}

func newIndex() *index {
	return &index{
		methods:       map[string][]methodEntry{},
		registrations: map[string][]registrationEntry{},
		seenMethod:    map[methodEntry]bool{},
		seenReg:       map[registrationEntry]bool{},
	}
}

// addMethod records one GD5 attribution.
//
// AMBIGUITY: GD6 deduplicates "on (full name, stub path, lineage)". §3 uses
// "full name" for a service full name in GD3 and GD5, but deduplicating method
// entries on the service full name would collapse every method of one service
// into a single entry and make GD8's "exactly one method entry bears its
// selector name" unanswerable. This oracle therefore reads the method entry's
// "full name" as the full-method name it was created from, so repeated
// occurrences of one literal in one stub collapse while distinct methods and
// distinct stubs stay distinct. Lineage is carried in the key even though, for
// a given (service, method, stub), GD5 makes it a function of the other three.
func (ix *index) addMethod(e methodEntry) {
	if ix.seenMethod[e] {
		return
	}
	ix.seenMethod[e] = true
	ix.methods[e.method] = append(ix.methods[e.method], e)
}

// addRegistration records one GD11 binding, deduplicated on (name, service
// full name, stub path). Two stubs declaring the same Register…Server for the
// same service therefore stay two competing entries, which is exactly §1's
// "one service full name reached through two stubs" ambiguous registration.
func (ix *index) addRegistration(e registrationEntry) {
	if ix.seenReg[e] {
		return
	}
	ix.seenReg[e] = true
	ix.registrations[e.name] = append(ix.registrations[e.name], e)
}

// shortName is GD3's "substring after the last dot". A package-less full name
// has no dot, so its short name is the whole string.
func shortName(full string) string {
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

// ---------------------------------------------------------------------------
// Unit scan
// ---------------------------------------------------------------------------

// counters holds per-state site-row counts for stderr diagnostics and the
// summary. GD10's out-of-domain candidates are not rows, so they are not
// counted here: outOfDomainTally owns them.
type counters map[string]int

func (c counters) add(state string) { c[state]++ }

// scanUnit applies GD1-GD16 to one unit, calling emit for every record.
//
// Records are emitted in a total, deterministic order: files in sorted
// repository-relative path order and, within one file, in parser traversal
// (source) order, with the unit's single out-of-domain aggregate last. Two runs
// over one corpus are therefore byte-identical without any global sort, which
// keeps memory bounded to one file's rows plus one per-unit tally.
//
// A unit that fails to scan emits no aggregate: its partial output is discarded
// by the driver, and a count for a unit whose files were not all read would be
// a lie.
func scanUnit(u unitRecord, src corpus, emit sink) (counters, error) {
	counts := counters{}
	ood := newOutOfDomainTally()
	out := func(path, state, object string, startByte, endByte, startLine, endLine, candidates int) {
		counts.add(state)
		emit(row{
			Schema: siteSchema, Unit: u.Unit, Path: path,
			StartByte: startByte, EndByte: endByte,
			StartLine: startLine, EndLine: endLine,
			State: state, Object: object, CandidateCount: candidates,
		})
	}

	files := sortedUnique(u.GoFiles)
	stubs, nonStubs := splitStubs(files)

	// The enumerator supplies stub_files, but GD1 - not the input record - is
	// the frozen notion of stub identity. Classify by suffix and report any
	// disagreement rather than trusting it silently.
	if diff := stubDisagreement(stubs, u.StubFiles); diff != "" {
		fmt.Fprintf(os.Stderr, "warning: %s: stub_files disagrees with the GD1 suffix rule: %s\n", u.Unit, diff)
	}

	// Pass 1: every stub feeds the index and is never scanned for sites (GD2).
	// Stub gap rows are held so pass 2 can emit them in sorted path order.
	ix := newIndex()
	stubRows := map[string][]row{}
	if len(stubs) > 0 {
		reader, err := src.open(u.Dir, u.Commit, stubs)
		if err != nil {
			return counts, fmt.Errorf("%s: open stub blobs: %w", u.Unit, err)
		}
		for _, path := range stubs {
			content, err := reader.next(path)
			if err != nil {
				reader.close()
				return counts, fmt.Errorf("%s: read stub %s: %w", u.Unit, path, err)
			}
			gap, ok := wholeFileGap(path, content)
			if ok {
				counts.add(gap.State)
				gap.Schema, gap.Unit = siteSchema, u.Unit
				stubRows[path] = []row{gap}
				continue
			}
			if len(content) == 0 {
				continue // GD15
			}
			_, file, err := parseSource(path, content)
			if err != nil {
				counts.add(stateGapParseError)
				g := gapRow(path, content, stateGapParseError, "parse_error")
				g.Schema, g.Unit = siteSchema, u.Unit
				stubRows[path] = []row{g}
				continue // GD14: no index entries, so its services stay unresolvable
			}
			indexStub(ix, path, file)
		}
		if err := reader.close(); err != nil {
			return counts, fmt.Errorf("%s: close stub blob reader: %w", u.Unit, err)
		}
	}

	// Pass 2: every non-stub .go file is scanned against the repository index.
	var reader blobReader = nopReader{}
	if len(nonStubs) > 0 {
		opened, err := src.open(u.Dir, u.Commit, nonStubs)
		if err != nil {
			return counts, fmt.Errorf("%s: open source blobs: %w", u.Unit, err)
		}
		reader = opened
	}
	scanErr := func() error {
		next := 0
		for _, path := range files {
			if isStubPath(path) {
				for _, r := range stubRows[path] {
					emit(r)
				}
				continue
			}
			if next >= len(nonStubs) || nonStubs[next] != path {
				return fmt.Errorf("%s: internal error: pass-2 order lost at %s", u.Unit, path)
			}
			next++
			content, err := reader.next(path)
			if err != nil {
				return fmt.Errorf("%s: read %s: %w", u.Unit, path, err)
			}
			if gap, ok := wholeFileGap(path, content); ok {
				counts.add(gap.State)
				gap.Schema, gap.Unit = siteSchema, u.Unit
				emit(gap)
				continue
			}
			if len(content) == 0 {
				continue // GD15: no gap row and no sites
			}
			fset, file, err := parseSource(path, content)
			if err != nil {
				counts.add(stateGapParseError)
				g := gapRow(path, content, stateGapParseError, "parse_error")
				g.Schema, g.Unit = siteSchema, u.Unit
				emit(g)
				continue // GD14: no sites
			}
			scanSites(ix, path, fset, file, out, ood)
		}
		return nil
	}()
	if cerr := reader.close(); cerr != nil && scanErr == nil {
		scanErr = fmt.Errorf("%s: close source blob reader: %w", u.Unit, cerr)
	}
	if scanErr != nil {
		return counts, scanErr
	}
	// Every unit publishes exactly one aggregate, including a unit with no
	// out-of-domain candidate at all, so a zero is distinguishable from a
	// missing record. It comes after every site row of this unit.
	emit(ood.aggregate(u.Unit))
	return counts, nil
}

// isStubPath is GD1: the filename suffix and nothing else.
func isStubPath(path string) bool { return strings.HasSuffix(path, stubSuffix) }

func sortedUnique(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	deduped := out[:0]
	for i, v := range out {
		if i == 0 || v != out[i-1] {
			deduped = append(deduped, v)
		}
	}
	return deduped
}

func splitStubs(files []string) (stubs, nonStubs []string) {
	for _, f := range files {
		if isStubPath(f) {
			stubs = append(stubs, f)
		} else {
			nonStubs = append(nonStubs, f)
		}
	}
	return stubs, nonStubs
}

func stubDisagreement(suffixStubs, declared []string) string {
	a := sortedUnique(suffixStubs)
	b := sortedUnique(declared)
	if strings.Join(a, "\x00") == strings.Join(b, "\x00") {
		return ""
	}
	var onlySuffix, onlyDeclared []string
	inB := map[string]bool{}
	for _, s := range b {
		inB[s] = true
	}
	inA := map[string]bool{}
	for _, s := range a {
		inA[s] = true
		if !inB[s] {
			onlySuffix = append(onlySuffix, s)
		}
	}
	for _, s := range b {
		if !inA[s] {
			onlyDeclared = append(onlyDeclared, s)
		}
	}
	return fmt.Sprintf("suffix-only=%v declared-only=%v", onlySuffix, onlyDeclared)
}

// parseSource parses one file and returns the file set that positions its AST,
// so a call span's byte offsets come from the parser's own token file (protocol
// §3, "Row identity used for scoring"). A parse error is GD14's whole-file gap;
// the partial AST the parser returns alongside the error is discarded, because
// GD14 allows no sites and no index entries from a file that fails to parse.
func parseSource(path string, content []byte) (*token.FileSet, *ast.File, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	return fset, file, err
}

// wholeFileGap applies GD13. It is checked before parsing, because a file over
// the bound must not be parsed at all: it yields exactly one gap and no sites,
// for stubs and non-stubs alike.
func wholeFileGap(path string, content []byte) (row, bool) {
	if len(content) > maxFileSize {
		return gapRow(path, content, stateGapTooLarge, "source_too_large"), true
	}
	return row{}, false
}

// gapRow spans the whole file, byte 0 to the file length (protocol §3, "Row
// identity used for scoring"). Line numbers are informational - the scorer
// joins on (unit, path, start_byte, end_byte) - so end_line is the count of
// newline-terminated lines plus one rather than a parser position, which a
// source_too_large file has none of.
func gapRow(path string, content []byte, state, object string) row {
	return row{
		Path:      path,
		StartByte: 0,
		EndByte:   len(content),
		StartLine: 1,
		EndLine:   bytes.Count(content, []byte("\n")) + 1,
		State:     state,
		Object:    object,
	}
}

// indexStub collects GD3 bindings, GD4/GD5 method entries and GD11
// registration names from one parsed stub.
func indexStub(ix *index, path string, file *ast.File) {
	var bindings []serviceBinding
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "ServiceName" {
			return true
		}
		value, ok := stringLiteral(kv.Value)
		if !ok || !serviceNamePattern.MatchString(value) {
			return true
		}
		bindings = append(bindings, serviceBinding{full: value, short: shortName(value), stub: path})
		return true
	})

	ast.Inspect(file, func(n ast.Node) bool {
		value, ok := stringLiteral(n)
		if !ok {
			return true
		}
		m := fullMethodPattern.FindStringSubmatch(value)
		if m == nil {
			return true
		}
		service, method := m[1], m[3]
		matched := false
		for _, b := range bindings {
			if b.full != service {
				continue
			}
			matched = true
			ix.addMethod(methodEntry{service: service, method: method, full: value, stub: path, lineage: "binding"})
		}
		if !matched {
			// GD5: still indexed, attributed to a synthetic entry carrying that
			// full name and the stub path.
			ix.addMethod(methodEntry{service: service, method: method, full: value, stub: path, lineage: "synthetic"})
		}
		return true
	})

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name == nil {
			continue
		}
		m := registerPattern.FindStringSubmatch(fn.Name.Name)
		if m == nil {
			continue
		}
		for _, b := range bindings {
			if b.short == m[1] {
				ix.addRegistration(registrationEntry{name: fn.Name.Name, service: b.full, stub: path})
			}
		}
	}
}

// stringLiteral reports the unquoted value of a Go string literal node.
func stringLiteral(n ast.Node) (string, bool) {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// scanSites reports every GD7-GD12 candidate in one parsed non-stub file, and
// tallies its GD10 out-of-domain candidates into ood rather than emitting them.
//
// Spans are the whole CallExpr, from the start of its function expression to
// its closing parenthesis, with byte offsets taken from the parser's token file
// (protocol §3, "Row identity used for scoring").
func scanSites(ix *index, path string, fset *token.FileSet, file *ast.File, out func(path, state, object string, startByte, endByte, startLine, endLine, candidates int), ood *outOfDomainTally) {
	tf := fset.File(file.Pos())
	if tf == nil {
		// Unreachable for a parser-produced AST, but a nil token file must
		// never panic the round.
		return
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		start := tf.Offset(call.Pos())
		end := tf.Offset(call.End())
		startLine := tf.Line(call.Pos())
		endLine := tf.Line(call.End())
		span := func(state, object string, candidates int) {
			out(path, state, object, start, end, startLine, endLine, candidates)
		}

		switch fun := call.Fun.(type) {
		case *ast.Ident:
			// GD7: a plain Ident call is never a client site, so it can only
			// ever be a registration. GD12: a candidate matching no
			// registration name emits nothing and is not counted as
			// out-of-domain either, because GD10's class belongs to client
			// candidates.
			entries := ix.registrations[fun.Name]
			if len(entries) == 0 {
				return true
			}
			emitRegistration(span, fun.Name, entries)
		case *ast.SelectorExpr:
			name := fun.Sel.Name
			// GD9: registration is tested first for a selector expression, so a
			// name that is both a registration name and a method name is a
			// registration and never falls through to the call check.
			if entries := ix.registrations[name]; len(entries) > 0 {
				emitRegistration(span, name, entries)
				return true
			}
			entries := ix.methods[name]
			switch {
			case len(entries) == 0:
				// GD10: no row and no denominator. The cause order is frozen -
				// an unindexed selector is decided before the argument count, so
				// a zero-argument call to an unindexed name counts once, here.
				ood.add(path, reasonUnindexedSelector)
			case len(call.Args) == 0:
				ood.add(path, reasonZeroArguments) // GD10
			case len(entries) == 1:
				span(stateResolvedCall, entries[0].full, 0) // GD8
			default:
				span(stateAmbiguousCall, name, len(entries)) // GD8
			}
		default:
			// GD7 admits only a SelectorExpr Fun. A generic instantiation
			// (IndexExpr), a parenthesized selector, a method value or a func
			// literal is therefore not a candidate at all: it emits nothing and
			// is not an out-of-domain candidate either, since GD10 excludes
			// candidates from the domain rather than admitting shapes into it.
			// AMBIGUITY: §3 never says whether an IndexExpr wrapping a
			// SelectorExpr counts; this oracle reads the frozen shape strictly
			// and does not unwrap, so `client.Method[T](ctx, req)` is silent.
			return true
		}
		return true
	})
}

// emitRegistration applies GD12's resolved/ambiguous split.
func emitRegistration(span func(state, object string, candidates int), name string, entries []registrationEntry) {
	if len(entries) == 1 {
		span(stateResolvedRegistration, entries[0].service, 0)
		return
	}
	span(stateAmbiguousRegistration, name, len(entries))
}

// ---------------------------------------------------------------------------
// Corpus readers
// ---------------------------------------------------------------------------

// gitCorpus reads each unit's blobs from its clone under a root directory, at
// the pinned commit, through one `git cat-file --batch` process per pass. The
// working tree is never read.
type gitCorpus struct{ root string }

func (g gitCorpus) open(dir, commit string, paths []string) (blobReader, error) {
	if len(paths) == 0 {
		return nopReader{}, nil
	}
	if dir == "" {
		return nil, fmt.Errorf("unit has no dir")
	}
	if commit == "" {
		return nil, fmt.Errorf("%s: unit has no commit", dir)
	}
	cmd := exec.Command("git", "-C", filepath.Join(g.root, dir), "cat-file", "--batch")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("git cat-file stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("git cat-file stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %w", err)
	}
	b := &gitBatch{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, 1<<20), remaining: len(paths)}
	b.written = make(chan error, 1)
	// Requests are written from their own goroutine so a full stdin pipe and a
	// full stdout pipe can never deadlock against each other: this goroutine
	// drains stdout while that one fills stdin.
	go func() {
		w := bufio.NewWriterSize(stdin, 1<<16)
		for _, p := range paths {
			if _, err := fmt.Fprintf(w, "%s:%s\n", commit, p); err != nil {
				_ = stdin.Close()
				b.written <- err
				return
			}
		}
		flushErr := w.Flush()
		closeErr := stdin.Close()
		if flushErr != nil {
			b.written <- flushErr
			return
		}
		b.written <- closeErr
	}()
	return b, nil
}

type gitBatch struct {
	cmd       *exec.Cmd
	stdin     io.Closer
	out       *bufio.Reader
	written   chan error
	remaining int
}

func (b *gitBatch) next(path string) ([]byte, error) {
	if b.remaining <= 0 {
		return nil, fmt.Errorf("%s: read past the end of the batch", path)
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("%s: git cat-file header: %w", path, err)
	}
	header = strings.TrimSuffix(header, "\n")
	if strings.HasSuffix(header, " missing") {
		return nil, fmt.Errorf("%s: no such object at the pinned commit", path)
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return nil, fmt.Errorf("%s: unexpected git cat-file header %q", path, header)
	}
	if fields[1] != "blob" {
		return nil, fmt.Errorf("%s: expected a blob, git reported %s", path, fields[1])
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, fmt.Errorf("%s: bad blob size %q: %w", path, fields[2], err)
	}
	if size < 0 {
		return nil, fmt.Errorf("%s: negative blob size %d", path, size)
	}
	content := make([]byte, size)
	if _, err := io.ReadFull(b.out, content); err != nil {
		return nil, fmt.Errorf("%s: short blob read: %w", path, err)
	}
	// git terminates each blob body with a newline.
	if _, err := b.out.Discard(1); err != nil {
		return nil, fmt.Errorf("%s: missing blob terminator: %w", path, err)
	}
	b.remaining--
	return content, nil
}

func (b *gitBatch) close() error {
	abandoned := b.remaining > 0
	if abandoned {
		// Nobody will drain stdout, so kill before Wait or Wait can block.
		_ = b.cmd.Process.Kill()
	}
	_ = b.stdin.Close()
	writeErr := <-b.written
	waitErr := b.cmd.Wait()
	if abandoned {
		return nil // the caller already holds the real error
	}
	if writeErr != nil {
		return fmt.Errorf("git cat-file request write: %w", writeErr)
	}
	if waitErr != nil {
		return fmt.Errorf("git cat-file --batch: %w", waitErr)
	}
	return nil
}

// nopReader is the empty-request reader.
type nopReader struct{}

func (nopReader) next(path string) ([]byte, error) {
	return nil, fmt.Errorf("%s: no blob was requested for this path", path)
}

func (nopReader) close() error { return nil }

// ---------------------------------------------------------------------------
// Driver
// ---------------------------------------------------------------------------

func main() {
	unitsPath := flag.String("units", "", "gzip-compressed JSON Lines population file (t581-unit-v1)")
	corpusRoot := flag.String("corpus", "", "directory holding one clone per unit, named by each unit's dir")
	flag.Parse()
	if *unitsPath == "" || *corpusRoot == "" {
		fail(fmt.Errorf("both -units and -corpus are required"))
	}
	out := bufio.NewWriterSize(os.Stdout, 1<<20)
	total := counters{}
	grand := outOfDomainAggregate{ByReason: zeroedReasons()}
	enc := json.NewEncoder(out)
	units := 0
	for _, u := range readUnits(*unitsPath) {
		units++
		var unitAggregate outOfDomainAggregate
		sawAggregate := false
		counts, err := scanUnit(u, gitCorpus{root: *corpusRoot}, func(rec any) {
			switch v := rec.(type) {
			case row:
				if err := enc.Encode(v); err != nil {
					fail(fmt.Errorf("encode site row: %w", err))
				}
			case outOfDomainAggregate:
				unitAggregate, sawAggregate = v, true
				if err := enc.Encode(v); err != nil {
					fail(fmt.Errorf("encode out-of-domain aggregate: %w", err))
				}
			default:
				fail(fmt.Errorf("internal error: unknown record type %T", rec))
			}
		})
		if err != nil {
			_ = out.Flush()
			fail(err)
		}
		if !sawAggregate {
			// §6's disclosure is per unit, so a unit with no aggregate would
			// publish no out-of-domain count at all rather than a zero.
			_ = out.Flush()
			fail(fmt.Errorf("%s: scan produced no out-of-domain aggregate", u.Unit))
		}
		grand.merge(unitAggregate)
		for state, n := range counts {
			total[state] += n
		}
		fmt.Fprintf(os.Stderr, "unit=%s commit=%s files=%d stubs=%d %s %s\n",
			u.Unit, u.Commit, len(u.GoFiles), len(u.StubFiles),
			summarize(counts), summarizeOutOfDomain(unitAggregate))
	}
	if err := out.Flush(); err != nil {
		fail(fmt.Errorf("flush stdout: %w", err))
	}
	fmt.Fprintf(os.Stderr, "total units=%d %s %s\n", units, summarize(total), summarizeOutOfDomain(grand))
}

// siteStates is the complete t581-site-v1 state vocabulary, in the order the
// stderr summary reports it. GD10's out-of-domain class is not a member: it is
// published per unit as an aggregate, never as a row.
var siteStates = []string{
	stateResolvedCall, stateAmbiguousCall,
	stateResolvedRegistration, stateAmbiguousRegistration,
	stateGapTooLarge, stateGapParseError,
}

func summarize(c counters) string {
	parts := make([]string, 0, len(siteStates))
	for _, s := range siteStates {
		parts = append(parts, fmt.Sprintf("%s=%d", s, c[s]))
	}
	return strings.Join(parts, " ")
}

// summarizeOutOfDomain renders one aggregate for stderr. Reasons are walked in
// outOfDomainReasons order, never by map iteration, so the line is stable.
func summarizeOutOfDomain(a outOfDomainAggregate) string {
	parts := make([]string, 0, len(outOfDomainReasons)+2)
	parts = append(parts,
		fmt.Sprintf("out_of_domain=%d", a.Total),
		fmt.Sprintf("files_with_candidates=%d", a.FilesWithCandidates))
	for _, reason := range outOfDomainReasons {
		parts = append(parts, fmt.Sprintf("%s=%d", reason, a.ByReason[reason]))
	}
	return strings.Join(parts, " ")
}

func readUnits(path string) []unitRecord {
	raw, err := os.Open(path)
	if err != nil {
		fail(err)
	}
	defer raw.Close()
	zr, err := gzip.NewReader(raw)
	if err != nil {
		fail(fmt.Errorf("%s: not a gzip stream: %w", path, err))
	}
	defer zr.Close()
	var out []unitRecord
	dec := json.NewDecoder(zr)
	for line := 1; dec.More(); line++ {
		var u unitRecord
		if err := dec.Decode(&u); err != nil {
			fail(fmt.Errorf("%s: line %d: %w", path, line, err))
		}
		if u.Schema != unitSchema {
			fail(fmt.Errorf("%s: line %d: schema %q, want %q", path, line, u.Schema, unitSchema))
		}
		if u.Unit == "" || u.Dir == "" || u.Commit == "" {
			fail(fmt.Errorf("%s: line %d: unit, dir and commit are all required", path, line))
		}
		out = append(out, u)
	}
	return out
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "t581 oracle:", err)
	os.Exit(1)
}
