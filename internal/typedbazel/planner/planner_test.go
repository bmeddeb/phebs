package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func pbBytes(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
func pbString(n protowire.Number, s string) []byte { return pbBytes(n, []byte(s)) }
func pbInt(n protowire.Number, v uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, n, protowire.VarintType), v)
}
func concat(bs ...[]byte) []byte { return bytes.Join(bs, nil) }
func frame(b []byte) []byte      { return protowire.AppendBytes(nil, b) }

var testHash = strings.Repeat("a", 64)

func cqConfig() []byte { return frame(pbBytes(2, concat(pbInt(1, 1), pbString(4, testHash)))) }
func cqTarget(name, kind string, inputs ...string) []byte {
	rule := concat(pbString(1, name), pbString(2, kind))
	for _, in := range inputs {
		rule = append(rule, pbBytes(15, concat(pbString(1, in), pbString(2, testHash), pbInt(3, 1)))...)
	}
	return frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 1), pbBytes(2, rule))), pbInt(3, 1))))
}

func cqSource(name string) []byte {
	return frame(pbBytes(1, pbBytes(1, concat(pbInt(1, 2), pbBytes(3, pbString(1, name))))))
}

func syntheticPlan(t *testing.T) ([]byte, []byte, map[string][]byte) {
	t.Helper()
	cq := cqConfig()
	var aq []byte
	projections := map[string][]byte{}
	aq = append(aq, pbBytes(5, concat(pbInt(1, 1), pbString(4, testHash)))...)
	aq = append(aq, pbBytes(6, concat(pbInt(1, 1), pbString(2, "@@//phebs_plan:aspect.bzl%phebs_plan")))...)
	// The roots share one package. This exercises aliases and many-target edges
	// without requiring external Bazel execution in these parser boundary tests.
	common := "@@//common:lib"
	rule := concat(pbString(1, common), pbString(2, "go_library"), pbBytes(15, pbString(1, "@@//common:lib.go")))
	cq = append(cq, frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 1), pbBytes(2, rule))), pbInt(3, 1))))...)
	cq = append(cq, cqSource("@@//common:lib.go")...)
	for _, root := range Roots() {
		cq = append(cq, cqTarget(canonicalLabel(root), "alias", common)...)
	}
	aq = append(aq, pbBytes(3, concat(pbInt(1, 1), pbString(2, common)))...)
	parts := []string{"bazel-out", "cfg", "bin", "common", "lib.phebs-plan.json"}
	p := ""
	for i, s := range parts {
		parent := uint64(i)
		aq = append(aq, pbBytes(8, concat(pbInt(1, uint64(i+1)), pbString(2, s), pbInt(3, parent)))...)
		if p != "" {
			p += "/"
		}
		p += s
	}
	aq = append(aq, pbBytes(1, concat(pbInt(1, 1), pbInt(2, uint64(len(parts)))))...)
	aq = append(aq, pbBytes(2, concat(pbInt(1, 1), pbInt(2, 1), pbString(4, "PhebsPlan"), pbInt(5, 1), pbInt(9, 1)))...)
	a := projectedArchive{Name: "lib", Label: common, Export: "bazel-out/cfg/bin/common/lib.x", ImportPath: "example.test/lib", ImportMap: "example.test/lib", GoFiles: []File{{Artifact: Artifact{Path: "common/lib.go", ShortPath: "common/lib.go", Owner: "@@//common:lib.go", Source: true}, SHA256: digest([]byte("package lib\n")), Bytes: 12}}, Imports: []Import{}}
	a.CompiledGoFiles = append([]File{}, a.GoFiles...)
	b, err := json.Marshal(projection{Version: "phebs-t451a-projection-v1", Owner: common, Roots: []string{a.Export}, Embeds: []string{}, Archives: []projectedArchive{a}})
	if err != nil {
		t.Fatal(err)
	}
	projections[p] = b
	return cq, aq, projections
}

func TestPlannerAuthorityBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*[]byte, *[]byte, map[string][]byte)
	}{
		{"truncated cquery", func(cq, aq *[]byte, p map[string][]byte) { *cq = (*cq)[:len(*cq)-1] }},
		{"duplicate configured target", func(cq, aq *[]byte, p map[string][]byte) {
			*cq = append(*cq, cqTarget("@@//common:lib", "go_library")...)
		}},
		{"extra disconnected target", func(cq, aq *[]byte, p map[string][]byte) {
			*cq = append(*cq, cqTarget("@@//extra:lib", "go_library")...)
		}},
		{"missing package", func(cq, aq *[]byte, p map[string][]byte) {
			for k := range p {
				delete(p, k)
			}
		}},
		{"extra projection", func(cq, aq *[]byte, p map[string][]byte) { p["extra.json"] = []byte("{}") }},
		{"duplicate action", func(cq, aq *[]byte, p map[string][]byte) {
			*aq = append(*aq, pbBytes(2, concat(pbInt(1, 1), pbString(4, "PhebsPlan"), pbInt(5, 1), pbInt(9, 1)))...)
		}},
		{"wrong action owner", func(cq, aq *[]byte, p map[string][]byte) {
			*aq = bytes.Replace(*aq, []byte("@@//common:lib"), []byte("@@//hidden:lib"), 1)
		}},
		{"duplicate JSON key", func(cq, aq *[]byte, p map[string][]byte) {
			for k, v := range p {
				p[k] = append([]byte(`{"version":"x",`), v[1:]...)
			}
		}},
		{"corrupt document digest", func(cq, aq *[]byte, p map[string][]byte) {
			for k, v := range p {
				p[k] = bytes.ReplaceAll(v, []byte(digest([]byte("package lib\n"))), []byte("bad"))
			}
		}},
		{"missing direct package edge", func(cq, aq *[]byte, p map[string][]byte) {
			for k, v := range p {
				var x projection
				if err := json.Unmarshal(v, &x); err != nil {
					t.Fatal(err)
				}
				x.Archives[0].Imports = []Import{{Path: "example.test/missing", Owner: "@@//missing:lib", Export: "bazel-out/cfg/bin/missing.x"}}
				p[k], _ = json.Marshal(x)
			}
		}},
		{"ambiguous document", func(cq, aq *[]byte, p map[string][]byte) {
			for k, v := range p {
				var x projection
				if err := json.Unmarshal(v, &x); err != nil {
					t.Fatal(err)
				}
				x.Archives[0].GoFiles = append(x.Archives[0].GoFiles, x.Archives[0].GoFiles[0])
				p[k], _ = json.Marshal(x)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cq, aq, p := syntheticPlan(t)
			tc.mutate(&cq, &aq, p)
			if _, err := Assemble(cq, aq, p); err == nil {
				t.Fatal("accepted broken authority")
			}
		})
	}
	cq, aq, p := syntheticPlan(t)
	got, err := Assemble(cq, aq, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Units) != 1 || len(got.Documents) != 1 {
		t.Fatalf("unexpected plan size: %d units %d documents", len(got.Units), len(got.Documents))
	}
	for i := 0; i < 20; i++ {
		next, err := Assemble(cq, aq, p)
		if err != nil {
			t.Fatal(err)
		}
		if jsonDigest(next) != jsonDigest(got) {
			t.Fatal("nondeterministic plan")
		}
	}
	for _, target := range got.Targets {
		if target.Kind == "file:2" {
			continue
		}
		if len(target.Units) != 1 || target.Units[0] != got.Units[0].ID {
			t.Fatal("lost many-target package edge")
		}
	}
}

func TestCqueryFullConfigurationIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"short configuration", concat(frame(pbBytes(2, concat(pbInt(1, 1), pbString(4, "abc123")))), cqTarget("@@//x:x", "go_library"))},
		{"missing configuration", cqTarget("@@//x:x", "go_library")},
		{"duplicate singular", concat(cqConfig(), frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 1), pbBytes(2, concat(pbString(1, "@@//x:x"), pbString(2, "go_library"))))), pbInt(3, 1), pbInt(3, 1)))))},
		{"missing dependency", concat(cqConfig(), cqTarget("@@//x:x", "go_library", "@@//y:y"))},
		{"oversized frame", protowire.AppendVarint(nil, MaxProtoBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCquery(tc.data); err == nil {
				t.Fatal("accepted invalid cquery")
			}
		})
	}
}

func TestCqueryGeneratedFileRetainsProducerEdge(t *testing.T) {
	generated := frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 3), pbBytes(4, concat(pbString(1, "@@//gen:out.go"), pbString(2, "@@//gen:source"))))), pbInt(3, 1))))
	data := concat(cqConfig(), cqTarget("@@//gen:lib", "go_library", "@@//gen:out.go"), generated, cqTarget("@@//gen:source", "genrule"))
	targets, err := DecodeCquery(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if target.Label == "@@//gen:out.go" {
			if len(target.Inputs) != 1 || target.Inputs[0].Label != "@@//gen:source" || target.Inputs[0].Configuration != testHash {
				t.Fatal("lost generating-rule edge")
			}
			return
		}
	}
	t.Fatal("missing generated artifact")
}

func TestPlanRetainsDeclaredGeneratedLocator(t *testing.T) {
	cq, aq, projections := syntheticPlan(t)
	const declared = "bazel-out/exact-cfg/bin/common/lib.go"
	for name, data := range projections {
		var p projection
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		f := &p.Archives[0].GoFiles[0]
		f.Path, f.Owner, f.Source = declared, "@@//common:lib", false
		p.Archives[0].CompiledGoFiles[0] = *f
		projections[name], _ = json.Marshal(p)
	}
	plan, err := Assemble(cq, aq, projections)
	if err != nil {
		t.Fatal(err)
	}
	doc := plan.Documents[0]
	if doc.ExecPath != declared || doc.Path != "common/lib.go" || doc.Producer.Configuration != testHash {
		t.Fatalf("declared locator or canonical identity lost: %+v", doc)
	}
}

func TestHelperPinnedSDKAndDeclaredCgo(t *testing.T) {
	sources := map[string][]byte{
		"pkg/pkg.go": []byte("package pkg\n"),
		"pkg/new.go": []byte("//go:build go1.26\n\npackage pkg\n"),
		"pkg/arm.go": []byte("//go:build arm64.v8.0\n\npackage pkg\n"),
		"pkg/cgo.go": []byte("package pkg\nimport \"C\"\n"),
		"bazel-out/cfg/bin/pkg/cgo/_cgo_gotypes.go": []byte("package pkg\n"),
		"bazel-out/cfg/bin/pkg/cgo/_cgo_imports.go": []byte("package pkg\n"),
		"bazel-out/cfg/bin/pkg/cgo/cgo.cgo1.go":     []byte("package pkg\n"),
	}
	owner := "@@//pkg:pkg"
	a := Archive{Name: "pkg", Label: owner, Export: "bazel-out/cfg/bin/pkg/pkg.x", ImportPath: "example.test/pkg", GOOS: "linux", GOARCH: "arm64", Cgo: true, Imports: []Import{}, Tags: []string{}}
	for _, name := range []string{"pkg/pkg.go", "pkg/new.go", "pkg/arm.go", "pkg/cgo.go"} {
		a.Sources = append(a.Sources, Artifact{Path: name, ShortPath: name, Owner: "@@//pkg:" + strings.TrimPrefix(name, "pkg/"), Source: true})
	}
	a.CgoDirectory = &Artifact{Path: "bazel-out/cfg/bin/pkg/cgo", ShortPath: "pkg/cgo", Owner: owner, Tree: true}
	in := helperInput{Version: "phebs-t451a-projection-input-v1", Owner: owner, Roots: []string{a.Export}, Archives: []Archive{a}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	reader := func(name string, limit int) ([]byte, error) {
		reads++
		v, ok := sources[name]
		if !ok {
			return nil, os.ErrNotExist
		}
		if len(v) > limit {
			return nil, errors.New("limit")
		}
		return v, nil
	}
	b, err := project(data, reader)
	if err != nil {
		t.Fatal(err)
	}
	var p projection
	if err = json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Archives[0].GoFiles) != 4 || len(p.Archives[0].CompiledGoFiles) != 5 {
		t.Fatalf("wrong source selection: %+v", p.Archives[0])
	}
	for _, f := range p.Archives[0].CompiledGoFiles {
		if f.Path == "pkg/new.go" || f.Path == "pkg/cgo.go" {
			t.Fatal("host SDK or raw cgo leaked into compile input")
		}
	}
	if reads != 7 {
		t.Fatalf("unexpected reads: %d", reads)
	}
	delete(sources, "bazel-out/cfg/bin/pkg/cgo/cgo.cgo1.go")
	if _, err = project(data, reader); err == nil {
		t.Fatal("accepted missing required cgo output")
	}
}

func TestFixtureRequestIsClosed(t *testing.T) {
	f, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	if len(f) == 0 || len(f["broken/BUILD.bazel"]) == 0 {
		t.Fatal("missing broken-target exclusion fixture")
	}
	for _, cmd := range Commands() {
		s := strings.Join(cmd, " ")
		if strings.Contains(s, "//...") || strings.Contains(s, "//broken") || strings.Contains(s, "keep_going") {
			t.Fatalf("open command: %s", s)
		}
	}
}
