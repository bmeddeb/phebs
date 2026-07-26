// T22.1 gates G1–G9. The corpus-bound gates skip without pinned clones;
// set T221_FETCH=1 to clone and pin on demand (or point T221_CORPUS_ROOT at
// existing pinned clones). TestIndexLockDigests and TestVocabularyDiscipline
// always run offline against committed bytes only.
package t221

import (
	"crypto/sha1" //nolint:gosec // verifying thriftrw's own SHA1 descriptor, not a security boundary
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func corpusDirs(t *testing.T) map[string]string {
	t.Helper()
	entries, err := LoadCorpusLock("corpus.lock.json")
	if err != nil {
		t.Fatalf("load corpus lock: %v", err)
	}
	root := CorpusRoot(".")
	dirs := make(map[string]string, len(entries))
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name)
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
			if os.Getenv("T221_FETCH") == "" {
				t.Skipf("corpus %s not fetched; run with T221_FETCH=1", entry.Name)
			}
			if _, err := EnsureCorpus(root, entry); err != nil {
				t.Fatalf("ensure corpus %s: %v", entry.Name, err)
			}
		} else if err := VerifyCorpusCheckout(dir, entry); err != nil {
			t.Fatalf("verify corpus %s: %v", entry.Name, err)
		}
		dirs[entry.Name] = dir
	}
	return dirs
}

func cadenceModules(t *testing.T, dirs map[string]string) []ThriftModuleDecl {
	t.Helper()
	modules, err := DiscoverThriftModules(dirs["cadence"], ".gen/go")
	if err != nil {
		t.Fatalf("discover modules: %v", err)
	}
	return modules
}

// G1 — every embedded ThriftModule in cadence/.gen/go is complete and
// self-consistent: SHA1 equals sha1(Raw) for all modules, no exceptions.
func TestG1ThriftrwModuleIntegrity(t *testing.T) {
	dirs := corpusDirs(t)
	modules := cadenceModules(t, dirs)
	if len(modules) < 10 {
		t.Fatalf("expected at least 10 embedded modules, found %d", len(modules))
	}
	for _, module := range modules {
		if module.Name == "" || module.Package == "" || module.FilePath == "" || module.SHA1 == "" || module.Raw == "" {
			t.Fatalf("module %s has empty descriptor fields: %+v", module.GoFile, module)
		}
		if got := module.RawSHA1(); got != module.SHA1 {
			t.Fatalf("module %s: sha1(Raw)=%s but embedded SHA1=%s", module.GoFile, got, module.SHA1)
		}
	}
	t.Logf("G1: %d modules, all with sha1(Raw)==SHA1", len(modules))
}

// G2 — the parent repo's idls gitlink names exactly the pinned cadence-idl
// commit, and every module's embedded SHA1 equals the SHA1 of
// thrift/<FilePath> in that IDL repo: the digest join is exact-by-
// construction. This is the evidence for D3's module_digest exact tier.
func TestG2GitlinkAlignmentAndDigestJoin(t *testing.T) {
	dirs := corpusDirs(t)
	entries, err := LoadCorpusLock("corpus.lock.json")
	if err != nil {
		t.Fatalf("load corpus lock: %v", err)
	}
	var cadenceEntry, idlEntry CorpusEntry
	for _, entry := range entries {
		switch entry.Name {
		case "cadence":
			cadenceEntry = entry
		case "cadence-idl":
			idlEntry = entry
		}
	}
	gitlink, err := GitlinkCommit(dirs["cadence"], cadenceEntry.GitlinkPath)
	if err != nil {
		t.Fatalf("read gitlink: %v", err)
	}
	if gitlink != cadenceEntry.GitlinkCommit {
		t.Fatalf("gitlink %s does not match locked %s", gitlink, cadenceEntry.GitlinkCommit)
	}
	if gitlink != idlEntry.Commit {
		t.Fatalf("gitlink %s does not match pinned cadence-idl commit %s", gitlink, idlEntry.Commit)
	}
	modules := cadenceModules(t, dirs)
	joined := 0
	for _, module := range modules {
		idlPath := filepath.Join(dirs["cadence-idl"], "thrift", filepath.FromSlash(module.FilePath))
		content, err := os.ReadFile(idlPath)
		if err != nil {
			t.Fatalf("module %s: IDL file thrift/%s unreadable in cadence-idl: %v", module.GoFile, module.FilePath, err)
		}
		sum := sha1.Sum(content) //nolint:gosec // descriptor integrity check
		if got := hex.EncodeToString(sum[:]); got != module.SHA1 {
			t.Fatalf("module %s: IDL-repo sha1 %s does not match embedded %s", module.GoFile, got, module.SHA1)
		}
		joined++
	}
	t.Logf("G2: gitlink aligned; %d/%d modules digest-join to cadence-idl thrift/<FilePath>", joined, len(modules))
}

// G3 — two independent in-file witnesses agree: the Raw IDL parse and the
// generated ToWire wire.Field{ID:} literals recover identical field-ID sets
// per struct, wire-only structs are exactly the service wrappers absent from
// the IDL, and ID 0 appears only in *_Result wrappers.
func TestG3FieldIDRecoveryAgreement(t *testing.T) {
	dirs := corpusDirs(t)
	modules := cadenceModules(t, dirs)
	checked := 0
	for _, module := range modules {
		if module.Name != "shared" && module.Name != "health" {
			continue
		}
		idlFields, err := ParseIDLStructFields(module.Raw)
		if err != nil {
			t.Fatalf("module %s: %v", module.GoFile, err)
		}
		wireFields, err := WireFieldIDsByStruct(filepath.Join(dirs["cadence"], filepath.FromSlash(module.GoFile)))
		if err != nil {
			t.Fatalf("module %s: %v", module.GoFile, err)
		}
		for structName, fields := range idlFields {
			wire, ok := wireFields[structName]
			if !ok {
				continue // fieldless or unused structs may emit no wire.Field literals
			}
			for name, id := range fields {
				if !wire[id] {
					t.Fatalf("%s.%s: IDL declares field %s=%d but ToWire has no wire.Field{ID: %d}", module.Name, structName, name, id, id)
				}
			}
			if len(wire) != len(fields) {
				t.Fatalf("%s.%s: IDL declares %d fields but ToWire emits %d distinct IDs", module.Name, structName, len(fields), len(wire))
			}
			checked++
		}
		for structName, wire := range wireFields {
			if _, declared := idlFields[structName]; declared {
				continue
			}
			if !strings.Contains(structName, "_") {
				t.Fatalf("module %s: wire-only struct %s is not a service wrapper", module.Name, structName)
			}
			if wire[0] && !strings.HasSuffix(structName, "_Result") {
				t.Fatalf("module %s: field ID 0 outside a _Result wrapper: %s", module.Name, structName)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no structs were cross-checked")
	}
	// Hand-labeled anchor: cadence uses ten-step IDs, so agreement here also
	// proves nothing assumes sequential numbering.
	for _, module := range modules {
		if module.Name != "shared" {
			continue
		}
		idlFields, err := ParseIDLStructFields(module.Raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := idlFields["WorkflowExecution"]["workflowId"]; got != 10 {
			t.Fatalf("WorkflowExecution.workflowId = %d, hand-labeled 10", got)
		}
	}
	t.Logf("G3: %d structs agree between Raw IDL and wire.Field recovery", checked)
}

// G4 — the Apache family's only in-file identity is the thrift struct tag:
// tags parse with valid bounds, agree with the out-of-repo jaeger-idl IDL,
// and no Apache generated file embeds a ThriftModule. That absent identity
// is why Apache rows stay derived (D3).
func TestG4ApacheTagJoin(t *testing.T) {
	dirs := corpusDirs(t)
	genDir := filepath.Join(dirs["jaeger-client-go"], "thrift-gen", "jaeger")
	goFiles, err := filepath.Glob(filepath.Join(genDir, "*.go"))
	if err != nil || len(goFiles) == 0 {
		t.Fatalf("no generated files found: %v", err)
	}
	byStruct := make(map[string]map[string]int)
	for _, goFile := range goFiles {
		if module, err := HasThriftModuleIdent(goFile); err != nil {
			t.Fatal(err)
		} else if module {
			t.Fatalf("Apache generated file %s embeds a ThriftModule", goFile)
		}
		tags, err := ParseThriftTags(goFile)
		if err != nil {
			t.Fatal(err)
		}
		for _, tag := range tags {
			if !ValidThriftFieldID(tag.ID) {
				t.Fatalf("tag %s.%s has out-of-bounds ID %d", tag.Struct, tag.ThriftName, tag.ID)
			}
			if byStruct[tag.Struct] == nil {
				byStruct[tag.Struct] = make(map[string]int)
			}
			byStruct[tag.Struct][tag.ThriftName] = tag.ID
		}
	}
	if got := byStruct["Batch"]; got["process"] != 1 || got["spans"] != 2 {
		t.Fatalf("Batch tags disagree with hand labels: %v", got)
	}
	idlRaw, err := os.ReadFile(filepath.Join(dirs["jaeger-idl"], "thrift", "jaeger.thrift"))
	if err != nil {
		t.Fatalf("read jaeger.thrift: %v", err)
	}
	idlFields, err := ParseIDLStructFields(string(idlRaw))
	if err != nil {
		t.Fatal(err)
	}
	for _, structName := range []string{"Batch", "Process", "Span"} {
		if len(idlFields[structName]) == 0 || len(byStruct[structName]) == 0 {
			t.Fatalf("struct %s missing from IDL or tags", structName)
		}
		for name, id := range idlFields[structName] {
			if byStruct[structName][name] != id {
				t.Fatalf("%s.%s: IDL %d vs tag %d", structName, name, id, byStruct[structName][name])
			}
		}
	}
	t.Logf("G4: %d tagged structs, zero embedded modules, IDL agreement for Batch/Process/Span", len(byStruct))
}

// familyFor is the spike's D5 document-eligibility rule.
func familyFor(corpusDir string) func(rel string) (string, bool) {
	return func(rel string) (string, bool) {
		full := filepath.Join(corpusDir, filepath.FromSlash(rel))
		if _, found, err := ParseThriftModuleFile(full); err == nil && found {
			return "thriftrw", true
		}
		marker, err := HasApacheGeneratedMarker(full)
		if err != nil || !marker {
			return "", false
		}
		tags, err := ParseThriftTags(full)
		if err != nil || len(tags) == 0 {
			return "", false
		}
		return "apache", true
	}
}

// G5 — eligibility sweeps: every eligible file lives in a generated root,
// and the adversarial handwritten directories yield zero eligible files.
func TestG5DocumentEligibility(t *testing.T) {
	dirs := corpusDirs(t)
	cadenceFamily := familyFor(dirs["cadence"])
	eligible := 0
	walkGoFiles(t, dirs["cadence"], ".gen/go", func(rel string) {
		if family, ok := cadenceFamily(rel); ok {
			if family != "thriftrw" {
				t.Fatalf("%s classified %s, want thriftrw", rel, family)
			}
			eligible++
		}
	})
	if eligible < 10 {
		t.Fatalf("expected at least 10 eligible thriftrw files, found %d", eligible)
	}
	// The mapper directory is the adversarial-est handwritten code: it is
	// full of thrift type names yet must produce zero eligible documents.
	walkGoFiles(t, dirs["cadence"], "common/types/mapper/thrift", func(rel string) {
		if family, ok := cadenceFamily(rel); ok {
			t.Fatalf("handwritten %s wrongly eligible as %s", rel, family)
		}
	})
	jaegerFamily := familyFor(dirs["jaeger-client-go"])
	apacheEligible := 0
	walkGoFiles(t, dirs["jaeger-client-go"], "thrift-gen", func(rel string) {
		if family, ok := jaegerFamily(rel); ok {
			if family != "apache" {
				t.Fatalf("%s classified %s, want apache", rel, family)
			}
			apacheEligible++
		}
	})
	if apacheEligible == 0 {
		t.Fatal("no eligible apache files found")
	}
	if family, ok := jaegerFamily("transport_udp.go"); ok {
		t.Fatalf("handwritten transport_udp.go wrongly eligible as %s", family)
	}
	t.Logf("G5: %d thriftrw + %d apache eligible files; adversarial directories clean", eligible, apacheEligible)
}

func walkGoFiles(t *testing.T, root, subdir string, visit func(rel string)) {
	t.Helper()
	base := filepath.Join(root, filepath.FromSlash(subdir))
	err := fs.WalkDir(os.DirFS(base), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			visit(filepath.ToSlash(filepath.Join(subdir, path)))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", subdir, err)
	}
}

type indexLock struct {
	Version string `json:"version"`
	Entries []struct {
		Name   string `json:"name"`
		Corpus string `json:"corpus"`
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"entries"`
}

func loadIndexLock(t *testing.T) indexLock {
	t.Helper()
	raw, err := os.ReadFile("index.lock.json")
	if err != nil {
		t.Fatalf("read index lock: %v", err)
	}
	var lock indexLock
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatalf("decode index lock: %v", err)
	}
	return lock
}

type needleFile struct {
	Version string `json:"version"`
	Entries []struct {
		Name           string `json:"name"`
		Index          string `json:"index"`
		Family         string `json:"family"`
		Symbol         string `json:"symbol"`
		Scope          string `json:"scope"`
		Message        string `json:"message"`
		FieldName      string `json:"field_name"`
		FieldID        int    `json:"field_id"`
		DefinitionPath string `json:"definition_path"`
		Expect         string `json:"expect"`
		Reason         string `json:"reason"`
		References     []struct {
			Path           string `json:"path"`
			Classification string `json:"classification"`
		} `json:"references"`
	} `json:"entries"`
}

// G6 — the authored indexes join against real corpus bytes: every
// hand-labeled needle binds with its expected references and
// classifications, and every adversarial entry abstains for exactly the
// labeled reason.
func TestG6ScipExactSpanJoin(t *testing.T) {
	dirs := corpusDirs(t)
	lock := loadIndexLock(t)
	raw, err := os.ReadFile(filepath.Join("testdata", "needles.json"))
	if err != nil {
		t.Fatalf("read needles: %v", err)
	}
	var needles needleFile
	if err := json.Unmarshal(raw, &needles); err != nil {
		t.Fatalf("decode needles: %v", err)
	}
	results := make(map[string]map[string]*Binding)
	abstained := make(map[string]map[string]string)
	for _, entry := range lock.Entries {
		corpusDir := dirs[entry.Corpus]
		index, err := LoadIndex(entry.Path, entry.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		bindings, abstentions, err := JoinFieldReferences(index, corpusDir, familyFor(corpusDir))
		if err != nil {
			t.Fatal(err)
		}
		results[entry.Name] = bindings
		reasons := make(map[string]string)
		for _, abstention := range abstentions {
			reasons[abstention.Symbol] = abstention.Reason
		}
		abstained[entry.Name] = reasons
	}
	for _, needle := range needles.Entries {
		bindings := results[needle.Index]
		switch needle.Expect {
		case "bound":
			binding, ok := bindings[needle.Symbol]
			if !ok {
				t.Fatalf("needle %s: expected binding, got none", needle.Name)
			}
			if binding.Family != needle.Family || binding.DocPath != needle.DefinitionPath {
				t.Fatalf("needle %s: bound as %s@%s, labeled %s@%s", needle.Name, binding.Family, binding.DocPath, needle.Family, needle.DefinitionPath)
			}
			if !ValidThriftFieldID(needle.FieldID) {
				t.Fatalf("needle %s: labeled field ID %d out of bounds", needle.Name, needle.FieldID)
			}
			if len(binding.References) != len(needle.References) {
				t.Fatalf("needle %s: %d references, labeled %d", needle.Name, len(binding.References), len(needle.References))
			}
			for i, want := range needle.References {
				got := binding.References[i]
				if got.DocPath != want.Path || got.Classification != want.Classification {
					t.Fatalf("needle %s reference %d: got %s/%s, labeled %s/%s", needle.Name, i, got.DocPath, got.Classification, want.Path, want.Classification)
				}
			}
		case "abstain":
			if _, ok := bindings[needle.Symbol]; ok {
				t.Fatalf("adversarial needle %s wrongly bound", needle.Name)
			}
			if reason := abstained[needle.Index][needle.Symbol]; reason != needle.Reason {
				t.Fatalf("adversarial needle %s abstained for %q, labeled %q", needle.Name, reason, needle.Reason)
			}
		default:
			t.Fatalf("needle %s has unknown expectation %q", needle.Name, needle.Expect)
		}
	}
	t.Logf("G6: %d needles reproduced (bound + adversarial)", len(needles.Entries))
}

// G7 — field 0 is a first-class identity: bounds admit it, the spelling is
// well-formed, wire evidence confines it to result wrappers (G3), and the
// scope spelling agrees with thriftdecl's rule (namespace go last segment,
// else file basename — cadence declares no go namespace).
func TestG7FieldZeroIdentity(t *testing.T) {
	dirs := corpusDirs(t)
	if ValidThriftFieldID(-1) || !ValidThriftFieldID(0) || !ValidThriftFieldID(32767) || ValidThriftFieldID(32768) {
		t.Fatal("bounds rule broken")
	}
	if got := FieldIdentity("health", "Meta_Health_Result", 0); got != "health.Meta_Health_Result#0" {
		t.Fatalf("spelling %q", got)
	}
	modules := cadenceModules(t, dirs)
	for _, module := range modules {
		if module.Name != "health" {
			continue
		}
		if _, declared, err := IDLGoNamespace(module.Raw); err != nil {
			t.Fatal(err)
		} else if declared {
			t.Fatal("cadence health.thrift unexpectedly declares namespace go")
		}
		basename := strings.TrimSuffix(filepath.Base(module.FilePath), ".thrift")
		if basename != module.Name {
			t.Fatalf("scope fallback %q does not match module name %q", basename, module.Name)
		}
		wireFields, err := WireFieldIDsByStruct(filepath.Join(dirs["cadence"], filepath.FromSlash(module.GoFile)))
		if err != nil {
			t.Fatal(err)
		}
		if !wireFields["Meta_Health_Result"][0] {
			t.Fatal("Meta_Health_Result has no wire.Field{ID: 0}")
		}
	}
	t.Log("G7: field 0 admitted, spelled, confined to result wrappers, scope aligned with thriftdecl")
}

// G8 — lineage reuses the contract_scip_package_v1 recipe: stable across
// recomputation, shared within a package, distinct across packages, and
// string-prefix-disjoint from thriftdecl's provisional family.
func TestG8LineageStabilityAndDisjointness(t *testing.T) {
	corpusDirs(t)
	cadenceSymbol := "scip-go gomod github.com/uber/cadence 98f3d45 `.gen/go/shared`/WorkflowExecution#GetWorkflowId()."
	jaegerSymbol := "scip-go gomod github.com/uber/jaeger-client-go 8d8e8fc `thrift-gen/jaeger`/Batch#Process."
	first, ok := SymbolLineage(cadenceSymbol)
	if !ok {
		t.Fatal("cadence lineage failed")
	}
	second, _ := SymbolLineage(cadenceSymbol)
	if first != second {
		t.Fatal("lineage unstable across recomputation")
	}
	other, ok := SymbolLineage(jaegerSymbol)
	if !ok || other == first {
		t.Fatalf("cross-package lineage not distinct: %s vs %s", first, other)
	}
	for _, lineage := range []string{first, other} {
		if !strings.HasPrefix(lineage, "contract_scip_package_v1_") {
			t.Fatalf("lineage %s not in the contract_scip_package_v1 family", lineage)
		}
		if strings.HasPrefix(lineage, "provisional_repo_path_v1") {
			t.Fatalf("lineage %s collides with thriftdecl's provisional family", lineage)
		}
	}
	t.Log("G8: lineage stable, package-distinct, disjoint from provisional_repo_path_v1")
}

// G9 — scale probe against the scipfield bounds the production extractor
// would inherit: per-file parse ceiling 4 MiB, symbol length 16 KiB, index
// size 64 MiB. Exceedance is a recorded decision, never a silent raise.
func TestG9ScaleProbe(t *testing.T) {
	dirs := corpusDirs(t)
	const (
		maxGeneratedBytes = 4 << 20
		maxSymbolBytes    = 16 << 10
		maxIndexBytes     = 64 << 20
	)
	largest := int64(0)
	largestPath := ""
	walkGoFiles(t, dirs["cadence"], ".gen/go", func(rel string) {
		info, err := os.Stat(filepath.Join(dirs["cadence"], filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > largest {
			largest, largestPath = info.Size(), rel
		}
	})
	if largest > maxGeneratedBytes {
		t.Fatalf("largest generated file %s is %d bytes, above the inherited %d ceiling: a raise requires its own recorded decision", largestPath, largest, maxGeneratedBytes)
	}
	lock := loadIndexLock(t)
	for _, entry := range lock.Entries {
		info, err := os.Stat(entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > maxIndexBytes {
			t.Fatalf("index %s is %d bytes, above the 64 MiB corpus capability", entry.Path, info.Size())
		}
		index, err := LoadIndex(entry.Path, entry.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range index.GetDocuments() {
			for _, occurrence := range doc.GetOccurrences() {
				if len(occurrence.GetSymbol()) > maxSymbolBytes {
					t.Fatalf("symbol beyond 16 KiB in %s", doc.GetRelativePath())
				}
			}
		}
	}
	t.Logf("G9: largest generated file %s = %d bytes (%.0f%% of the 4 MiB ceiling); all indexes within bounds", largestPath, largest, 100*float64(largest)/float64(maxGeneratedBytes))
}

// Offline: the committed index bytes must match index.lock.json exactly.
func TestIndexLockDigests(t *testing.T) {
	lock := loadIndexLock(t)
	if lock.Version != "t221-scip-input-v1" {
		t.Fatalf("unexpected lock version %q", lock.Version)
	}
	for _, entry := range lock.Entries {
		if _, err := LoadIndex(entry.Path, entry.SHA256); err != nil {
			t.Fatalf("entry %s: %v", entry.Name, err)
		}
	}
}

// Offline: spike prose stays inside the no-accuracy-claim discipline.
func TestVocabularyDiscipline(t *testing.T) {
	banned := []string{"precision", "recall", "f1 score"}
	err := fs.WalkDir(os.DirFS("."), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".json") {
			return nil
		}
		if path == "gates_test.go" { // the file defining the ban list
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(content))
		for _, word := range banned {
			if strings.Contains(lower, word) {
				t.Errorf("%s contains banned vocabulary %q", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("README.md missing: %v", err)
	}
	for _, required := range []string{"not an accuracy claim", "GATE2-V2"} {
		if !strings.Contains(string(readme), required) {
			t.Fatalf("README.md missing required disclaimer %q", required)
		}
	}
}
