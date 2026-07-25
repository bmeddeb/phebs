package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// corpusDir returns the pinned checkout for one lock entry. Tests skip when
// the corpus has not been fetched so the default offline suite stays green;
// set T191_FETCH=1 to clone and pin on demand.
func corpusDir(t *testing.T, name string) string {
	t.Helper()
	entries, err := loadCorpusLock("corpus.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name != name {
			continue
		}
		dir := filepath.Join("corpus", name)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			if os.Getenv("T191_FETCH") == "" {
				t.Skipf("corpus %s not fetched; run with T191_FETCH=1", name)
			}
		} else if os.Getenv("T191_FETCH") == "" {
			return dir // present; trust the pin established at fetch time
		}
		pinned, err := ensureCorpus("corpus", entry)
		if err != nil {
			t.Fatalf("ensure corpus %s: %v", name, err)
		}
		return pinned
	}
	t.Fatalf("corpus %s not in lock", name)
	return ""
}

func readCorpusFile(t *testing.T, dir, rel string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// Gate 1 — declaration rules parse the whole IDL corpus and recover the
// expected construct inventory.
func TestDeclarationRulesOnJaegerIDL(t *testing.T) {
	dir := corpusDir(t, "jaeger-idl")
	paths, err := walkSuffix(dir, ".thrift")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 {
		t.Fatalf("expected 4 .thrift files, got %v", paths)
	}
	byScope := map[string]*declFile{}
	oneway, implicit := 0, 0
	var operations []string
	for _, rel := range paths {
		file, err := parseDeclFile(rel, readCorpusFile(t, dir, rel))
		if err != nil {
			t.Fatalf("parse rate below 100%%: %v", err)
		}
		byScope[file.Scope] = file
		implicit += file.ImplicitIDs
		for _, service := range file.Services {
			for _, function := range service.Functions {
				if function.OneWay {
					oneway++
				}
			}
		}
		operations = append(operations, file.operationObjects()...)
	}
	if implicit != 0 {
		t.Fatalf("corpus unexpectedly contains %d implicit field IDs", implicit)
	}
	if oneway != 2 {
		t.Fatalf("expected the 2 agent.thrift oneway functions, got %d", oneway)
	}
	agent := byScope["agent"]
	if agent == nil || len(agent.Includes) != 2 {
		t.Fatalf("agent.thrift should include jaeger + zipkincore: %+v", agent)
	}
	sort.Strings(operations)
	for _, want := range []string{
		"agent.Agent/emitBatch",
		"agent.Agent/emitZipkinBatch",
		"jaeger.Collector/submitBatches",
		"sampling.SamplingManager/getSamplingStrategy",
		"zipkincore.ZipkinCollector/submitZipkinBatch",
	} {
		if index := sort.SearchStrings(operations, want); index == len(operations) || operations[index] != want {
			t.Fatalf("missing operation object %q in %v", want, operations)
		}
	}
}

// Gate 2 — the scope rule (namespace go last segment, else file basename) is
// reproducible from the consumer side: every declared scope names a real
// generated package directory in the same repository.
func TestScopePrecedenceMatchesGeneratedPackages(t *testing.T) {
	dir := corpusDir(t, "jaeger-idl")
	paths, err := walkSuffix(dir, ".thrift")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range paths {
		file, err := parseDeclFile(rel, readCorpusFile(t, dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if len(file.Services) == 0 {
			continue
		}
		generated := filepath.Join(dir, "thrift-gen", file.Scope)
		if _, err := os.Stat(generated); err != nil {
			t.Fatalf("scope %q (from %s) has no generated package at %s", file.Scope, rel, generated)
		}
	}
}

// Gate 3 — consumer rules on the hand-labeled jaeger-client-go sample: the
// index resolves every labeled site exactly, with zero unlabeled emissions.
// The labeled set deliberately includes the NewAgentClientUDP wrapper (an
// exact-match negative) and a handler-side GetSamplingStrategy invocation
// (inherent heuristic-tier noise, counted here and in the decision table).
func TestConsumerRulesOnLabeledSample(t *testing.T) {
	dir := corpusDir(t, "jaeger-client-go")
	index := buildIndex(t, dir)
	agent, sampling := index.Services["Agent"], index.Services["SamplingManager"]
	if agent == nil || sampling == nil {
		t.Fatalf("index missing expected services: %v", index.Services)
	}
	if agent.WireMethods["EmitBatch"] != "emitBatch" ||
		sampling.WireMethods["GetSamplingStrategy"] != "getSamplingStrategy" {
		t.Fatalf("go-name to wire-name mapping wrong: %+v %+v", agent, sampling)
	}
	labeled := map[string][]consumerFinding{
		"transport_udp.go": {
			{Kind: "call", Object: "/agent.Agent/emitBatch"},
		},
		"utils/udp_client.go": {
			{Kind: "call", Object: "/agent.Agent/emitBatch"},
		},
		"testutils/mock_agent.go": {
			// Handler-side invocation, semantically not a wire call: kept as
			// measured heuristic-tier noise (1 of 4 labeled emissions).
			{Kind: "call", Object: "/sampling.SamplingManager/getSamplingStrategy"},
			{Kind: "registration", Object: "agent.Agent"},
		},
	}
	for rel, want := range labeled {
		got, err := scanConsumerFile(readCorpusFile(t, dir, rel), index)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s findings = %+v, want %+v", rel, got, want)
		}
	}
}

// Gate 4 — cross-corpus join: declaration operation objects from jaeger-idl
// intersect the consumer call objects from jaeger-client-go, proving the
// scope rule yields live name-match joins for the Atlas relationship map.
func TestDeclarationConsumerObjectsJoin(t *testing.T) {
	declDir := corpusDir(t, "jaeger-idl")
	consumerDir := corpusDir(t, "jaeger-client-go")
	declared := map[string]bool{}
	paths, err := walkSuffix(declDir, ".thrift")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range paths {
		file, err := parseDeclFile(rel, readCorpusFile(t, declDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, object := range file.operationObjects() {
			declared[object] = true
		}
	}
	index := buildIndex(t, consumerDir)
	joins := map[string]bool{}
	for _, rel := range []string{"transport_udp.go", "utils/udp_client.go", "testutils/mock_agent.go"} {
		findings, err := scanConsumerFile(readCorpusFile(t, consumerDir, rel), index)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range findings {
			if finding.Kind == "call" && declared[finding.Object[1:]] {
				joins[finding.Object] = true
			}
		}
	}
	for _, want := range []string{"/agent.Agent/emitBatch", "/sampling.SamplingManager/getSamplingStrategy"} {
		if !joins[want] {
			t.Fatalf("expected cross-corpus join on %s, got %v", want, joins)
		}
	}
}

// Gate 5 — jaeger at HEAD vendors no generated Thrift code, so the per-repo
// index is empty and the consumer rules abstain entirely: the honest-outcome
// posture for cross-module stub imports.
func TestJaegerHeadAbstains(t *testing.T) {
	dir := corpusDir(t, "jaeger")
	goFiles, err := walkSuffix(dir, ".go")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range goFiles {
		if isGeneratedThrift(readCorpusFile(t, dir, rel)) {
			t.Fatalf("unexpected in-repo generated thrift file: %s", rel)
		}
	}
}

// The legacy generated-header marker has no HEAD-stable public corpus, so a
// synthetic fixture pins the detector's coverage of both marker forms.
func TestGeneratedHeaderMarkers(t *testing.T) {
	modern := []byte("// Code generated by Thrift Compiler (0.19.0). DO NOT EDIT.\npackage x\n")
	legacy := []byte("// Autogenerated by Thrift Compiler (0.9.3)\npackage x\n")
	hand := []byte("// Package x implements things.\npackage x\n")
	if !isGeneratedThrift(modern) || !isGeneratedThrift(legacy) || isGeneratedThrift(hand) {
		t.Fatal("generated-header detection wrong")
	}
}

// Implicit field IDs parse (thriftrw flags them via IDUnset) and are counted,
// backing the decided fail-closed file-gap posture in production.
func TestImplicitFieldIDsAreDetected(t *testing.T) {
	source := []byte("struct Loose {\n  string name\n  2: i32 ok\n}\n")
	file, err := parseDeclFile("loose.thrift", source)
	if err != nil {
		t.Fatal(err)
	}
	if file.ImplicitIDs != 1 {
		t.Fatalf("ImplicitIDs = %d, want 1", file.ImplicitIDs)
	}
}

func buildIndex(t *testing.T, dir string) *stubIndex {
	t.Helper()
	index := newStubIndex()
	goFiles, err := walkSuffix(dir, ".go")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range goFiles {
		content := readCorpusFile(t, dir, rel)
		if !isGeneratedThrift(content) {
			continue
		}
		if err := index.indexGeneratedFile(filepath.Base(filepath.Dir(rel)), content); err != nil {
			t.Fatalf("index %s: %v", rel, err)
		}
	}
	return index
}
