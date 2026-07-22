// Package grpcgo extracts Go gRPC implementation registrations and typed
// generated-client call sites (T13.1, dark/provisional scope).
//
// It is a syntactic port of the T11.1 spike's validated rules into the
// pure-reader extractor SDK. The spike resolved call sites with go/types over
// hydrated module caches; that fidelity requires capabilities (module
// downloads, build-context execution) the pure-reader invariant forbids, so
// this extractor resolves names against the repository's own generated
// *_grpc.pb.go stubs and reports the honest lower tier: registrations are
// "derived" (name-bound to a same-repo generated stub) and client call sites
// are "heuristic" (method-name unique across the repo's stub index). Typed
// resolution remains the pilot-gate path; no output states or implies
// measured accuracy.
package grpcgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const (
	domain        = "grpc-consumer"
	version       = "1.1.0"
	schemaVersion = "t13-v1"

	// go/parser is in-process and bounded by input size; cap the source so a
	// single candidate cannot hand it a pathological working set. Oversized
	// candidates are read (the worker requires every candidate read) but
	// counted as unresolved rather than parsed.
	maxGoSourceBytes = 4 << 20

	// SHA-256 of the empty adapter configuration (protodecl precedent).
	adapterConfigDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	stubSuffix = "_grpc.pb.go"
)

// New returns the stateless extractor.
func New() sdk.Extractor { return grpcGo{} }

type grpcGo struct{}

func (grpcGo) Domain() string  { return domain }
func (grpcGo) Version() string { return version }

// Candidate covers every regular Go file: generated stubs feed the name
// index and all other Go files are scanned for registrations and call sites.
func (grpcGo) Candidate(filePath string) bool { return strings.HasSuffix(filePath, ".go") }

// Generated names may be package-qualified ("helloworld.Greeter") or a
// single segment ("Greeter") when the protobuf file has no package.
var (
	protoFullNameRe = regexp.MustCompile(`^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*$`)
	// Anchored: the whole literal must be one full method so arbitrary string
	// constants cannot enter the index.
	fullMethodRe = regexp.MustCompile(`^/([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)/([A-Za-z_]\w*)$`)
)

// serviceIndex is the cross-file state retained between the stub pass and the
// scan pass. It holds only derived names and provenance strings, never blob
// content, preserving the SDK's one-blob-at-a-time contract.
type serviceIndex struct {
	// registerFunc maps a generated RegisterXServer function name to every
	// independently anchored service it could register. Resolution succeeds
	// only when the set has exactly one entry.
	registerFunc map[string][]*serviceEntry
	// methods maps a method name to every service declaring it; call sites
	// resolve only when exactly one service matches.
	methods map[string][]*serviceEntry
}

type serviceEntry struct {
	fqn      string // e.g. helloworld.Greeter
	stubPath string // generated stub that anchored this entry
	lineage  string // provisional lineage id derived from stubPath
}

// lineageID reuses the T12.3 provisional recipe: file-scoped, machine-visibly
// provisional until T13.2 promotes cross-repository contract lineage.
func lineageID(repo, stubPath string) string {
	h := sha256.Sum256([]byte(repo + "\x00" + stubPath))
	return "provisional_repo_path_v1_" + hex.EncodeToString(h[:])
}

func (grpcGo) Extract(ctx context.Context, corpus sdk.Corpus, emit sdk.Emit) (sdk.Coverage, error) {
	coverage := sdk.Coverage{Protocols: []string{
		"grpc-go", "lineage-provisional-repo-path-v1", "resolution-syntactic-v1",
	}}
	index := &serviceIndex{
		registerFunc: map[string][]*serviceEntry{},
		methods:      map[string][]*serviceEntry{},
	}

	// Pass 1: generated stubs build the name index. Stubs declare no facts of
	// their own in this domain — declared operations belong to protodecl.
	err := corpus.WalkFiles(ctx, func(relPath string) error {
		if !strings.HasSuffix(relPath, stubSuffix) {
			return nil
		}
		blob, err := corpus.Read(ctx, relPath)
		if err != nil {
			return err
		}
		if len(blob.Content) > maxGoSourceBytes {
			unresolved, err := emitFileGap(relPath, blob, "source_too_large", emit)
			coverage.UnresolvedCount += unresolved
			return err
		}
		if err := indexStub(corpus.RepoName(), relPath, blob.Content, index); err != nil {
			// A malformed generated stub is an honest abstention, not a run
			// failure: its services simply stay unresolvable.
			unresolved, emitErr := emitFileGap(relPath, blob, "parse_error", emit)
			coverage.UnresolvedCount += unresolved
			return emitErr
		}
		return nil
	})
	if err != nil {
		return coverage, err
	}

	// Pass 2: every other Go file is scanned for registrations and call sites.
	err = corpus.WalkFiles(ctx, func(relPath string) error {
		if !strings.HasSuffix(relPath, ".go") || strings.HasSuffix(relPath, stubSuffix) {
			return nil
		}
		blob, err := corpus.Read(ctx, relPath)
		if err != nil {
			return err
		}
		if len(blob.Content) > maxGoSourceBytes {
			unresolved, err := emitFileGap(relPath, blob, "source_too_large", emit)
			coverage.UnresolvedCount += unresolved
			return err
		}
		unresolved, err := scanFile(relPath, blob, index, emit)
		if err != nil {
			return err
		}
		coverage.UnresolvedCount += unresolved
		return nil
	})
	return coverage, err
}

// indexStub extracts service identities from one generated stub: ServiceDesc
// ServiceName literals bind short names to protobuf FQNs, RegisterXServer
// declarations bind registration entry points, and anchored full-method
// constants bind the method universe.
func indexStub(repo, relPath, content string, index *serviceIndex) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, content, parser.SkipObjectResolution)
	if err != nil {
		return err
	}

	// Collect service FQNs from ServiceDesc composite literals:
	// ServiceName: "pkg.Service".
	byShort := map[string][]*serviceEntry{}
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "ServiceName" {
			return true
		}
		lit, ok := kv.Value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		fqn, err := strconv.Unquote(lit.Value)
		if err != nil || !protoFullNameRe.MatchString(fqn) {
			return true
		}
		short := fqn[strings.LastIndex(fqn, ".")+1:]
		entry := &serviceEntry{fqn: fqn, stubPath: relPath, lineage: lineageID(repo, relPath)}
		byShort[short] = appendEntryUnique(byShort[short], entry)
		return true
	})

	// Full-method constants populate the method index. Each constant names its
	// service FQN directly, so no short-name inference is needed.
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		m := fullMethodRe.FindStringSubmatch(value)
		if m == nil {
			return true
		}
		fqn, method := m[1], m[2]
		var entries []*serviceEntry
		for _, entry := range byShort[fqn[strings.LastIndex(fqn, ".")+1:]] {
			if entry.fqn == fqn {
				entries = append(entries, entry)
			}
		}
		if len(entries) == 0 {
			entries = []*serviceEntry{{
				fqn: fqn, stubPath: relPath, lineage: lineageID(repo, relPath),
			}}
		}
		for _, entry := range entries {
			index.methods[method] = appendEntryUnique(index.methods[method], entry)
		}
		return true
	})

	// RegisterXServer declarations: bind by generated naming convention
	// Register<Short>Server against the ServiceDesc short names in this file.
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		short, ok := strings.CutPrefix(name, "Register")
		if !ok {
			continue
		}
		short, ok = strings.CutSuffix(short, "Server")
		if !ok || short == "" {
			continue
		}
		for _, entry := range byShort[short] {
			index.registerFunc[name] = appendEntryUnique(index.registerFunc[name], entry)
		}
	}
	return nil
}

func appendEntryUnique(entries []*serviceEntry, candidate *serviceEntry) []*serviceEntry {
	for _, entry := range entries {
		if entry.fqn == candidate.fqn && entry.stubPath == candidate.stubPath &&
			entry.lineage == candidate.lineage {
			return entries
		}
	}
	return append(entries, candidate)
}

// emitFileGap records a source-backed unresolved assertion so the trusted
// worker can independently reconcile Coverage.UnresolvedCount. An empty file
// contains no possible call or registration site, so reading it is complete
// coverage even though go/parser cannot form a file AST for it.
func emitFileGap(relPath string, blob sdk.Blob, reason string, emit sdk.Emit) (int, error) {
	if len(blob.Content) == 0 {
		return 0, nil
	}
	const predicate = "GRPC_EXTRACTION_GAP"
	endLine := 1 + strings.Count(blob.Content[:len(blob.Content)-1], "\n")
	err := emit(sdk.Fact{
		Atom: sdk.AtomInput{
			SchemaVersion: schemaVersion, BlobDigest: blob.Digest,
			StartByte: 0, EndByte: len(blob.Content), RuleID: "grpcgo-file-gap-v1",
			AdapterConfigDigest: adapterConfigDigest,
			FactFingerprint:     predicate + "|" + reason,
		},
		Path: relPath, StartLine: 1, EndLine: endLine,
		Assertion: sdk.AssertionInput{
			Predicate: predicate, Subject: relPath, Object: reason,
			Tier: "unresolved", CodeRole: classifyRole(relPath, blob.Content),
			Detail: `{"schema":"grpcgo-gap-detail-v1","reason":` + strconv.Quote(reason) + `}`,
		},
	})
	if err != nil {
		return 0, err
	}
	return 1, nil
}

// scanFile emits registration and call-site facts for one non-stub Go file.
// Parse failures are honest abstentions (testdata and templates legitimately
// contain invalid Go); they count as unresolved.
func scanFile(relPath string, blob sdk.Blob, index *serviceIndex, emit sdk.Emit) (int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, blob.Content, parser.SkipObjectResolution)
	if err != nil {
		return emitFileGap(relPath, blob, "parse_error", emit)
	}
	role := classifyRole(relPath, blob.Content)
	tokenFile := fset.File(file.Pos())

	unresolved := map[string]struct{}{}
	var emitErr error
	emitSpan := func(predicate, object, lineage, rule, tier, detail string, node ast.Node) {
		if emitErr != nil {
			return
		}
		start := tokenFile.Offset(node.Pos())
		end := tokenFile.Offset(node.End())
		if start < 0 || end <= start || end > len(blob.Content) {
			emitErr = fmt.Errorf("invalid parser span for %s in %s", object, relPath)
			return
		}
		emitErr = emit(sdk.Fact{
			Atom: sdk.AtomInput{
				SchemaVersion: schemaVersion, BlobDigest: blob.Digest,
				StartByte: start, EndByte: end, RuleID: rule,
				AdapterConfigDigest: adapterConfigDigest,
				FactFingerprint:     predicate + "|" + object,
			},
			Path:      relPath,
			StartLine: fset.Position(node.Pos()).Line,
			EndLine:   fset.Position(node.End()).Line,
			Assertion: sdk.AssertionInput{
				Predicate: predicate, Subject: relPath, Object: object,
				Lineage: lineage,
				Tier:    tier, CodeRole: role, Detail: detail,
			},
		})
	}
	emitUnresolved := func(predicate, object, rule, detail string, node ast.Node) {
		emitSpan(predicate, object, "", rule, "unresolved", detail, node)
		if emitErr == nil {
			// Assertion identity also includes subject (relPath) and repository;
			// both are fixed for this scan. Count distinct unresolved assertions,
			// matching the worker rather than raw source occurrences.
			unresolved[predicate+"\x00"+object] = struct{}{}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if emitErr != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			entries := index.registerFunc[fun.Name]
			if len(entries) == 1 {
				entry := entries[0]
				emitSpan("REGISTERS_GRPC_SERVICE", entry.fqn, entry.lineage,
					"grpcgo-register-v1", "derived", registerDetail(fun.Name), call)
			} else if len(entries) > 1 {
				emitUnresolved("UNRESOLVED_GRPC_REGISTRATION", fun.Name,
					"grpcgo-register-ambiguous-v1", ambiguousRegisterDetail(fun.Name, len(entries)), call)
			}
		case *ast.SelectorExpr:
			name := fun.Sel.Name
			registers := index.registerFunc[name]
			if len(registers) == 1 {
				entry := registers[0]
				emitSpan("REGISTERS_GRPC_SERVICE", entry.fqn, entry.lineage,
					"grpcgo-register-v1", "derived", registerDetail(name), call)
				return true
			}
			if len(registers) > 1 {
				emitUnresolved("UNRESOLVED_GRPC_REGISTRATION", name,
					"grpcgo-register-ambiguous-v1", ambiguousRegisterDetail(name, len(registers)), call)
				return true
			}
			services := index.methods[name]
			if len(services) == 0 || len(call.Args) == 0 {
				return true
			}
			if len(services) > 1 {
				// Ambiguous method name across services: syntactic resolution
				// abstains rather than guesses.
				emitUnresolved("UNRESOLVED_GRPC_CALL", name, "grpcgo-call-ambiguous-v1",
					ambiguousCallDetail(name, len(services)), call)
				return true
			}
			entry := services[0]
			emitSpan("CALLS_OPERATION", "/"+entry.fqn+"/"+name, entry.lineage,
				"grpcgo-call-v1", "heuristic", callDetail(name), call)
		}
		return true
	})
	return len(unresolved), emitErr
}

func registerDetail(fn string) string {
	return `{"schema":"grpcgo-register-detail-v1","register_func":` + strconv.Quote(fn) + `}`
}

func callDetail(method string) string {
	return `{"schema":"grpcgo-call-detail-v1","method":` + strconv.Quote(method) + `}`
}

func ambiguousCallDetail(method string, candidates int) string {
	return `{"schema":"grpcgo-call-ambiguity-v1","method":` + strconv.Quote(method) +
		`,"candidate_count":` + strconv.Itoa(candidates) + `}`
}

func ambiguousRegisterDetail(fn string, candidates int) string {
	return `{"schema":"grpcgo-register-ambiguity-v1","register_func":` + strconv.Quote(fn) +
		`,"candidate_count":` + strconv.Itoa(candidates) + `}`
}

// ---- code_role classification, ported verbatim from the T11.1 spike ----

const (
	roleProduction = "production"
	roleTest       = "test"
	roleMock       = "mock"
	roleGenerated  = "generated"
	roleVendor     = "vendor"
)

var (
	generatedHeaderRe = regexp.MustCompile(`(?m)^(//|#).*Code generated .* DO NOT EDIT`)
	mockHeaderRe      = regexp.MustCompile(`Code generated by MockGen|Code generated by mockery|Code generated by counterfeiter`)
)

// classifyRole implements the spike's validated precedence:
// vendor > mock > generated > test > production. A generated mock inside
// vendor/ is vendor; a MockGen file outside vendor is mock even though it is
// also generated.
func classifyRole(relPath, content string) string {
	if pathHasSegment(relPath, "vendor") {
		return roleVendor
	}
	head := content
	if len(head) > 4096 {
		head = head[:4096]
	}
	if mockHeaderRe.MatchString(head) || pathHasMockSegment(relPath) {
		return roleMock
	}
	if generatedHeaderRe.MatchString(head) {
		return roleGenerated
	}
	if strings.HasSuffix(relPath, "_test.go") || hasTestSegment(relPath) {
		return roleTest
	}
	return roleProduction
}

// hasTestSegment catches test code without the _test.go suffix — integration
// suites under tests/... and reusable support under common/testing/.
func hasTestSegment(relPath string) bool {
	for _, seg := range strings.Split(relPath, "/") {
		if seg == "tests" || seg == "testing" || seg == "testdata" {
			return true
		}
	}
	return false
}

func pathHasSegment(relPath, want string) bool {
	for _, seg := range strings.Split(relPath, "/") {
		if seg == want {
			return true
		}
	}
	return false
}

func pathHasMockSegment(relPath string) bool {
	parts := strings.Split(relPath, "/")
	for i, seg := range parts {
		isFile := i == len(parts)-1
		if seg == "mock" || seg == "mocks" || seg == "mocksgen" || seg == "fake" || seg == "fakes" {
			return true
		}
		if isFile && (strings.HasPrefix(seg, "mock_") || strings.HasPrefix(seg, "fake_") ||
			strings.HasSuffix(seg, "_mock.go") || strings.HasSuffix(seg, "_mock_test.go") ||
			strings.HasSuffix(seg, "_fake.go") || strings.HasSuffix(seg, "_fake_test.go")) {
			return true
		}
	}
	return false
}
