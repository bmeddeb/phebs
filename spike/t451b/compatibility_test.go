package t451b

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Reuse the retained neutral plan as data only. No host tool or Bazel runs.
func retainedPlan(t *testing.T) planner.Plan {
	t.Helper()
	f, err := os.Open("../t451a/review-receipt.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	reader, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, sandbox.OutputBytes+1))
	if err != nil || len(data) > sandbox.OutputBytes {
		t.Fatal("retained fixture byte bound", err)
	}
	var receipt struct {
		NeutralEvidence struct {
			Plan planner.Plan `json:"plan"`
		} `json:"neutral_evidence"`
	}
	if err = json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt.NeutralEvidence.Plan
}

func TestClosedCompatibilityRequest(t *testing.T) {
	plan := retainedPlan(t)
	roots, err := neutralRoots(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range []string{"load", "scip"} {
		t.Run(slot, func(t *testing.T) {
			p, err := launcher.PrepareCompatibility(plan, roots, slot)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(control{"phebs-t451b-client-plan-v1", slot, plan, roots})
			raw = append(raw, '\n')
			hash := t451a.Digest(raw)
			env := callerEnvironment(p, slot, hash)
			argv := append([]string{AdapterPath}, p.Invocation().Arguments...)
			wire := callerRequest(env)
			if len(wire) > maxRequestBytes {
				t.Fatal("frozen caller exceeds request ceiling")
			}
			if _, _, err = inspectCall(raw, hash, argv, env, launcher.Workspace, wire); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct{ name, old, new string }{
				{"mode", "\"mode\":8681", "\"mode\":31"}, {"flags", "\"build_flags\":null", "\"build_flags\":[]"}, {"overlay", "\"overlay\":null", "\"overlay\":{}"}, {"tests", "\"tests\":false", "\"tests\":true"}, {"unknown", "\"mode\":8681", "\"mode\":8681,\"unknown\":0"}, {"duplicate", "\"mode\":8681", "\"mode\":8681,\"mode\":8681"}, {"case alias", "\"mode\":8681", "\"Mode\":8681"}, {"omitted", "\"tests\":false,", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					changed := bytes.Replace(wire, []byte(tc.old), []byte(tc.new), 1)
					if _, _, err := inspectCall(raw, hash, argv, env, launcher.Workspace, changed); err == nil {
						t.Fatal("accepted unsupported wire")
					}
				})
			}
			variations := []struct {
				name       string
				argv, env  []string
				cwd, hash  string
				wire, data []byte
			}{
				{"selector", []string{AdapterPath, "./..."}, env, launcher.Workspace, hash, wire, raw},
				{"subcommand driver", append([]string{"/inputs/t451a", "__gopackagesdriver"}, argv[1:]...), env, launcher.Workspace, hash, wire, raw},
				{"extra env", argv, append(slices.Clone(env), "GOFLAGS=-tags=other"), launcher.Workspace, hash, wire, raw},
				{"missing env", argv, env[:len(env)-1], launcher.Workspace, hash, wire, raw},
				{"working directory", argv, env, "/scratch", hash, wire, raw},
				{"plan hash", argv, env, launcher.Workspace, t451a.Digest(nil), wire, raw},
				{"plan tamper", argv, env, launcher.Workspace, hash, wire, append(slices.Clone(raw), ' ')},
				{"wire whitespace", argv, env, launcher.Workspace, hash, append(slices.Clone(wire), '\n'), raw},
				{"wire bound", argv, env, launcher.Workspace, hash, bytes.Repeat([]byte("x"), maxRequestBytes+1), raw},
			}
			for _, key := range []string{"PATH=", "CGO_ENABLED=", "GOTAGS=", "GOPACKAGESDRIVER=", slotEnv + "=", planEnv + "=", "PWD="} {
				changed := slices.Clone(env)
				for i, value := range changed {
					if strings.HasPrefix(value, key) {
						changed[i] = key + "mutated"
					}
				}
				variations = append(variations, struct {
					name       string
					argv, env  []string
					cwd, hash  string
					wire, data []byte
				}{key, argv, changed, launcher.Workspace, hash, callerRequest(changed), raw})
			}
			for _, tc := range variations {
				t.Run(tc.name, func(t *testing.T) {
					if _, _, err := inspectCall(tc.data, tc.hash, tc.argv, tc.env, tc.cwd, tc.wire); err == nil {
						t.Fatal("accepted unsupported caller")
					}
				})
			}
		})
	}
	if _, _, err = slotPaths("target"); err == nil {
		t.Fatal("admitted extra execution slot")
	}
}

func oracleIndex() *scip.Index {
	symbol := func(pkg, name string) string {
		return "scip-go gomod " + ModulePath + " " + ModuleVersion + " `" + pkg + "`/" + name + "()."
	}
	occurrence := func(line, start, end int32, pkg, name string, definition bool) *scip.Occurrence {
		role := int32(0)
		if definition {
			role = int32(scip.SymbolRole_Definition)
		}
		occ := &scip.Occurrence{Symbol: symbol(pkg, name), SymbolRoles: role}
		occ.SetSourceRange(scip.Range{Start: scip.Position{Line: line, Character: start}, End: scip.Position{Line: line, Character: end}})
		return occ
	}
	hover := func(name string) *scip.SymbolInformation {
		return &scip.SymbolInformation{Symbol: symbol("example.test/neutral/lib", name), SignatureDocumentation: &scip.Signature{Language: "go", Text: "func " + name + "() int"}}
	}
	return &scip.Index{Metadata: &scip.Metadata{ProjectRoot: "file:///scratch/workspace", TextDocumentEncoding: scip.TextEncoding_UTF8, ToolInfo: &scip.ToolInfo{Name: "scip-go", Version: "0.2.7", Arguments: scipArguments([]string{"example.test/neutral/lib"})}}, Documents: []*scip.Document{
		{RelativePath: "lib/a.go", Language: "go", Occurrences: []*scip.Occurrence{occurrence(2, 5, 12, "example.test/neutral/lib", "variant", true)}, Symbols: []*scip.SymbolInformation{hover("variant")}},
		{RelativePath: "lib/lib.go", Language: "go", Occurrences: []*scip.Occurrence{occurrence(4, 5, 10, "example.test/neutral/lib", "Value", true), occurrence(4, 40, 47, "example.test/neutral/lib", "variant", false), occurrence(4, 30, 35, "example.test/external/pkg", "Value", false)}, Symbols: []*scip.SymbolInformation{hover("Value")}},
	}}
}
func TestNeutralSCIPOracle(t *testing.T) {
	data, err := proto.Marshal(oracleIndex())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := VerifySCIP(data)
	if err != nil || facts.Definitions != 2 || facts.Hovers != 2 || facts.CrossFileReferences != 1 || facts.ExternalReferences != 1 {
		t.Fatal("neutral oracle", facts, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*scip.Index)
	}{
		{"missing document", func(i *scip.Index) { i.Documents = i.Documents[:1] }},
		{"extra document", func(i *scip.Index) {
			i.Documents = append(i.Documents, &scip.Document{RelativePath: "lib/b.go", Language: "go"})
		}},
		{"outside document", func(i *scip.Index) { i.Documents[0].RelativePath = "../external/pkg.go" }},
		{"duplicate document", func(i *scip.Index) { i.Documents[1] = i.Documents[0] }},
		{"missing reference", func(i *scip.Index) { i.Documents[1].Occurrences = i.Documents[1].Occurrences[:1] }},
		{"wrong role", func(i *scip.Index) { i.Documents[1].Occurrences[1].SymbolRoles = int32(scip.SymbolRole_Definition) }},
		{"wrong symbol version", func(i *scip.Index) {
			i.Documents[1].Occurrences[1].Symbol = strings.ReplaceAll(i.Documents[1].Occurrences[1].Symbol, ModuleVersion, "other")
		}},
		{"wrong source range", func(i *scip.Index) {
			i.Documents[0].Occurrences[0].SetSourceRange(scip.Range{Start: scip.Position{Line: 2, Character: 0}, End: scip.Position{Line: 2, Character: 7}})
		}},
		{"missing hover", func(i *scip.Index) { i.Documents[0].Symbols = nil }},
		{"wrong hover", func(i *scip.Index) { i.Documents[0].Symbols[0].SignatureDocumentation.Text = "func variant() string" }},
		{"wrong invocation", func(i *scip.Index) { i.Metadata.ToolInfo.Arguments = nil }},
		{"wrong tool version", func(i *scip.Index) { i.Metadata.ToolInfo.Version = "0.2.8" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := oracleIndex()
			tc.mutate(i)
			data, _ := proto.Marshal(i)
			if _, err := VerifySCIP(data); err == nil {
				t.Fatal("accepted failed SCIP oracle")
			}
		})
	}
	if _, err = VerifySCIP(nil); err == nil {
		t.Fatal("accepted empty SCIP")
	}
}

func TestSCIPLegacyWireCompatibility(t *testing.T) {
	index := oracleIndex()
	for _, doc := range index.Documents {
		for _, occ := range doc.Occurrences {
			r, _ := occ.SourceRange()
			occ.TypedRange = nil
			// The pinned v0.7 producer encodes range as packed field 1.
			packed := protowire.AppendVarint(nil, uint64(r.Start.Line))
			packed = protowire.AppendVarint(packed, uint64(r.Start.Character))
			packed = protowire.AppendVarint(packed, uint64(r.End.Character))
			wire := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), packed)
			if err := (proto.UnmarshalOptions{Merge: true}).Unmarshal(wire, occ); err != nil {
				t.Fatal(err)
			}
		}
		for _, info := range doc.Symbols {
			// v0.7 used Document; v0.9's Signature retains its language/text tags.
			wire, err := proto.Marshal(&scip.Document{Language: "go", Text: info.SignatureDocumentation.Text})
			if err != nil {
				t.Fatal(err)
			}
			info.SignatureDocumentation = new(scip.Signature)
			if err := proto.Unmarshal(wire, info.SignatureDocumentation); err != nil {
				t.Fatal(err)
			}
		}
	}
	wire, err := proto.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySCIP(wire); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibilityBudget(t *testing.T) {
	cancelled := false
	b := clientBudget{remaining: 4, cancel: func() { cancelled = true }}
	a, z := clientOutput{budget: &b}, clientOutput{budget: &b}
	if _, err := a.Write([]byte("123")); err != nil {
		t.Fatal(err)
	}
	if _, err := z.Write([]byte("45")); err == nil || !cancelled || z.data.Len() != 0 {
		t.Fatal("stdout/stderr did not share a bounded cancellation budget")
	}
}

func retainedCall(t *testing.T) (planner.Plan, []planner.Configured, CallEvidence) {
	t.Helper()
	plan := retainedPlan(t)
	roots, err := neutralRoots(plan)
	if err != nil {
		t.Fatal(err)
	}
	p, err := launcher.PrepareCompatibility(plan, roots, "load")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../t451a/review-receipt.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	reader, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, sandbox.OutputBytes+1))
	if err != nil || len(data) > sandbox.OutputBytes {
		t.Fatal("retained fixture byte bound", err)
	}
	var receipt struct {
		NeutralEvidence struct {
			Response []byte `json:"response_wire"`
		} `json:"neutral_evidence"`
	}
	if err = json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	raw := receipt.NeutralEvidence.Response
	var response map[string]json.RawMessage
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	response["Compiler"], response["Arch"], response["GoVersion"] = json.RawMessage(`"gc"`), json.RawMessage(`"arm64"`), json.RawMessage(`25`)
	adapted, _ := json.Marshal(response)
	var parsed struct{ Packages []struct{ ExportFile string } }
	if err = json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, pkg := range parsed.Packages {
		if pkg.ExportFile != "" {
			paths[pkg.ExportFile] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for name := range paths {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	exports := make([]launcher.ExportArtifact, 0, len(ordered))
	for _, name := range ordered {
		exports = append(exports, launcher.ExportArtifact{Path: name, Bytes: 1, SHA256: strings.Repeat("a", 64)})
	}
	controlBytes, _ := json.Marshal(control{"phebs-t451b-client-plan-v1", "load", plan, roots})
	controlBytes = append(controlBytes, '\n')
	hash := t451a.Digest(controlBytes)
	env := callerEnvironment(p, "load", hash)
	request := callerRequest(env)
	call := CallEvidence{Slot: "load", PlanSHA256: hash, Argv: append([]string{AdapterPath}, p.Invocation().Arguments...), Environment: env, Directory: launcher.Workspace, Request: request, RequestSHA256: t451a.Digest(request), Launcher: p.Invocation(), LauncherSHA256: p.Digest(), Result: launcher.CompatibilityResult{DriverResponse: raw, Response: adapted, Exports: exports}, DriverResponseSHA256: t451a.Digest(raw), ResponseSHA256: t451a.Digest(adapted)}
	return plan, roots, call
}

func TestCompatibilityEvidence(t *testing.T) {
	plan, roots, call := retainedCall(t)
	if err := verifyCall(plan, roots, "load", call); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(call)
	for _, tc := range []struct {
		name   string
		mutate func(*CallEvidence)
	}{
		{"wrong slot", func(c *CallEvidence) { c.Slot = "scip" }},
		{"wrong wire hash", func(c *CallEvidence) { c.RequestSHA256 = t451a.Digest(nil) }},
		{"wrong invocation", func(c *CallEvidence) { c.Launcher.Environment = append(c.Launcher.Environment, "GOFLAGS=-x") }},
		{"wrong transform", func(c *CallEvidence) {
			c.Result.Response = bytes.Replace(c.Result.Response, []byte(`"arm64"`), []byte(`"amd64"`), 1)
			c.ResponseSHA256 = t451a.Digest(c.Result.Response)
		}},
		{"duplicate transform key", func(c *CallEvidence) {
			c.Result.Response = append([]byte(`{"Arch":"arm64",`), c.Result.Response[1:]...)
			c.ResponseSHA256 = t451a.Digest(c.Result.Response)
		}},
		{"changed raw graph", func(c *CallEvidence) {
			c.Result.DriverResponse = bytes.Replace(c.Result.DriverResponse, []byte(`"lib"`), []byte(`"different"`), 1)
			c.DriverResponseSHA256 = t451a.Digest(c.Result.DriverResponse)
		}},
		{"extra export", func(c *CallEvidence) {
			c.Result.Exports = append(c.Result.Exports, launcher.ExportArtifact{Path: "/scratch/extra", Bytes: 1, SHA256: strings.Repeat("a", 64)})
		}},
		{"missing export", func(c *CallEvidence) { c.Result.Exports = nil }},
		{"export digest", func(c *CallEvidence) { c.Result.Exports[0].SHA256 = strings.Repeat("A", 64) }},
		{"export bound", func(c *CallEvidence) { c.Result.Exports[0].Bytes = planner.MaxFileBytes + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed CallEvidence
			if err := json.Unmarshal(encoded, &changed); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&changed)
			if err := verifyCall(plan, roots, "load", changed); err == nil {
				t.Fatal("accepted mutated evidence")
			}
		})
	}
}

func TestTypedProbeRefusals(t *testing.T) {
	root := loadFact{ID: "lib", Path: "example.test/neutral/lib", Types: true, Syntax: 2, TypeInfo: true}
	report := loadReport{Mode: 8681, Roots: []loadFact{root}, Packages: []loadFact{{ID: "external", Path: "example.test/external/pkg", Types: true}, root}}
	response := []byte(`{"Roots":["lib"],"Packages":[{"ID":"external","PkgPath":"example.test/external/pkg"},{"ID":"lib","PkgPath":"example.test/neutral/lib"}]}`)
	good, _ := json.Marshal(report)
	good = append(good, '\n')
	if err := verifyProbe(good, response); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*loadReport)
	}{
		{"nil types", func(r *loadReport) { r.Packages[0].Types = false }},
		{"package errors", func(r *loadReport) { r.Packages[0].Errors = 1 }},
		{"ill typed", func(r *loadReport) { r.Roots[0].IllTyped = true }},
		{"syntax absent", func(r *loadReport) { r.Roots[0].Syntax = 0 }},
		{"typeinfo absent", func(r *loadReport) { r.Roots[0].TypeInfo = false }},
		{"missing dependency", func(r *loadReport) { r.Packages = r.Packages[:1] }},
		{"extra dependency", func(r *loadReport) { r.Packages = append(r.Packages, loadFact{ID: "unknown", Types: true}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed loadReport
			if err := json.Unmarshal(good, &changed); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&changed)
			wire, _ := json.Marshal(changed)
			if err := verifyProbe(append(wire, '\n'), response); err == nil {
				t.Fatal("accepted incomplete typed load")
			}
		})
	}
}

func testRequest() Request {
	return Request{Schema: "phebs-t451b-request-v1", Profile: Profile, BundleSHA256: t451a.Digest([]byte("bundle")), HelperSHA256: t451a.Digest([]byte("helper")), ProbeSHA256: t451a.Digest([]byte("probe")), ProbeSourceSHA256: t451a.Digest(ProbeSource), SCIPGoSHA256: SCIPDigest, GoSHA256: GoDigest, ImageID: t451a.Digest([]byte("image"))}
}
func testTools(r Request) []ToolIdentity {
	var result []ToolIdentity
	for _, t := range toolProfiles(r) {
		info := &debug.BuildInfo{Path: t.main, GoVersion: "go1.25.0", Settings: []debug.BuildSetting{{Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "arm64"}, {Key: "CGO_ENABLED", Value: "0"}, {Key: "GOARM64", Value: "v8.0"}}}
		if t.tools {
			info.Deps = []*debug.Module{{Path: "golang.org/x/tools", Version: ToolsVersion, Sum: ToolsSum}}
		}
		result = append(result, ToolIdentity{t.path, 1, t.sha, info})
	}
	return result
}
func TestToolEvidenceAdmission(t *testing.T) {
	r := testRequest()
	tools := testTools(r)
	if err := validateTools(r, tools); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(tools)
	for _, tc := range []struct {
		name   string
		mutate func(*[]ToolIdentity)
	}{
		{"missing tool", func(s *[]ToolIdentity) { *s = (*s)[:4] }},
		{"wrong path", func(s *[]ToolIdentity) { (*s)[0].Path = "/other" }},
		{"wrong helper", func(s *[]ToolIdentity) { (*s)[1].SHA256 = t451a.Digest(nil) }},
		{"wrong probe", func(s *[]ToolIdentity) { (*s)[2].SHA256 = t451a.Digest(nil) }},
		{"wrong go", func(s *[]ToolIdentity) { (*s)[4].SHA256 = t451a.Digest(nil) }},
		{"missing build", func(s *[]ToolIdentity) { (*s)[0].Build = nil }},
		{"wrong module", func(s *[]ToolIdentity) { (*s)[2].Build.Path = "other" }},
		{"wrong tools version", func(s *[]ToolIdentity) { (*s)[2].Build.Deps[0].Version = "v0.48.0" }},
		{"replaced tools", func(s *[]ToolIdentity) { (*s)[2].Build.Deps[0].Replace = &debug.Module{Path: "local"} }},
		{"wrong build release", func(s *[]ToolIdentity) { (*s)[3].Build.GoVersion = "go1.26.5" }},
		{"wrong architecture", func(s *[]ToolIdentity) { (*s)[3].Build.Settings[1].Value = "amd64" }},
		{"duplicate setting", func(s *[]ToolIdentity) {
			(*s)[3].Build.Settings = append((*s)[3].Build.Settings, debug.BuildSetting{Key: "GOARCH", Value: "arm64"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed []ToolIdentity
			if err := json.Unmarshal(encoded, &changed); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&changed)
			if err := validateTools(r, changed); err == nil {
				t.Fatal("accepted substituted tool evidence")
			}
		})
	}
	raw, _ := json.MarshalIndent(r, "", "  ")
	raw = append(raw, '\n')
	if _, err := DecodeRequest(raw); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Request)
	}{
		{"wrong Go digest", func(r *Request) { r.GoSHA256 = t451a.Digest(nil) }},
		{"wrong SCIP digest", func(r *Request) { r.SCIPGoSHA256 = t451a.Digest(nil) }},
		{"wrong probe source", func(r *Request) { r.ProbeSourceSHA256 = t451a.Digest(nil) }},
		{"wrong profile", func(r *Request) { r.Profile = "target" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := r
			tc.mutate(&changed)
			wire, _ := json.MarshalIndent(changed, "", "  ")
			if _, err := DecodeRequest(append(wire, '\n')); err == nil {
				t.Fatal("accepted substituted request identity")
			}
		})
	}
}

func TestClosedToolLayout(t *testing.T) {
	r := testRequest()
	bundle := t451a.Bundle{Schema: "phebs-t451a-offline-v1", Files: []t451a.BundleFile{
		{Path: "tools/bin/bazel", Bytes: 1, SHA256: t451a.Digest([]byte("bazel")), Executable: true},
		{Path: "tools/bin/gopackagesdriver", Bytes: 1, SHA256: "sha256:" + launcher.DriverSHA256, Executable: true},
		{Path: "tools/bin/phebs-t451b-driver", Bytes: 1, SHA256: r.HelperSHA256, Executable: true},
		{Path: "tools/bin/scip-go", Bytes: 1, SHA256: SCIPDigest, Executable: true},
		{Path: "tools/bin/t451b-load-probe", Bytes: 1, SHA256: r.ProbeSHA256, Executable: true},
		{Path: "tools/cc-sysroot.zip", Bytes: 1, SHA256: t451a.Digest([]byte("compiler"))},
		{Path: "tools/go/bin/go", Bytes: 1, SHA256: GoDigest, Executable: true},
	}}
	if err := validateLayout(bundle, r); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*t451a.Bundle)
	}{
		{"missing probe", func(b *t451a.Bundle) { b.Files = slices.Delete(b.Files, 4, 5) }},
		{"unbound adapter", func(b *t451a.Bundle) { b.Files[2].SHA256 = t451a.Digest(nil) }},
		{"other Go", func(b *t451a.Bundle) { b.Files[6].SHA256 = t451a.Digest(nil) }},
		{"caller Git", func(b *t451a.Bundle) {
			b.Files = append(b.Files, t451a.BundleFile{Path: "tools/bin/git", Bytes: 1, Executable: true})
		}},
		{"SDK Git", func(b *t451a.Bundle) {
			b.Files = append(b.Files, t451a.BundleFile{Path: "tools/go/bin/git", Bytes: 1, Executable: true})
		}},
		{"workspace input", func(b *t451a.Bundle) {
			b.Files = append(b.Files, t451a.BundleFile{Path: "workspace/BUILD.bazel", Bytes: 1})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := bundle
			changed.Files = slices.Clone(bundle.Files)
			tc.mutate(&changed)
			if err := validateLayout(changed, r); err == nil {
				t.Fatal("accepted unclosed tool layout")
			}
		})
	}
}
