package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestNonGoProjectionV2(t *testing.T) {
	fixtures, err := FixturesV2()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range legacy {
		if !bytes.Equal(data, fixtures[name]) {
			t.Fatal("historical fixture changed")
		}
	}
	if _, ok := legacy["assembly/answer_arm64.s"]; ok {
		t.Fatal("v2 fixture leaked into v1")
	}
	files := map[string][]byte{
		"assembly/assembly.go":       fixtures["assembly/assembly.go"],
		"assembly/answer_arm64.s":    fixtures["assembly/answer_arm64.s"],
		"assembly/ignored_windows.s": []byte("//go:build windows\n\nassembly bytes\n"),
		"assembly/source.c":          []byte("int answer(void) { return 42; }\n"),
		"assembly/header.h":          []byte("int answer(void);\n"),
		"assembly/object.syso":       {0, 1, 2, 255},
	}
	a := Archive{Name: "assembly", Label: "@@//assembly:assembly", Export: "bazel-out/cfg/bin/assembly/assembly.x", ImportPath: "example.test/assembly", GOOS: "linux", GOARCH: "arm64"}
	for _, name := range []string{"assembly/assembly.go", "assembly/answer_arm64.s", "assembly/ignored_windows.s", "assembly/source.c", "assembly/header.h", "assembly/object.syso"} {
		a.Sources = append(a.Sources, Artifact{Path: name, ShortPath: name, Owner: "@@//assembly:" + strings.TrimPrefix(name, "assembly/"), Source: true})
	}
	in := helperInput{Version: ProjectionInputV2, Owner: a.Label, Roots: []string{a.Export}, Archives: []Archive{a}}
	data, _ := json.Marshal(in)
	reads := 0
	read := func(name string, limit int) ([]byte, error) {
		reads++
		b, ok := files[name]
		if !ok {
			return nil, os.ErrNotExist
		}
		if len(b) > limit {
			return nil, errors.New("file bound")
		}
		return b, nil
	}
	projected, err := project(data, read)
	if err != nil {
		t.Fatal(err)
	}
	var p projection
	if err = json.Unmarshal(projected, &p); err != nil {
		t.Fatal(err)
	}
	if p.Version != ProjectionV2 || len(p.Archives) != 1 || len(p.Archives[0].OtherFiles) != 5 || reads != 6 {
		t.Fatalf("non-Go selection/read count: %d/%d", len(p.Archives[0].OtherFiles), reads)
	}
	for _, f := range p.Archives[0].OtherFiles {
		if f.SHA256 != digest(files[f.Path]) || f.Bytes != len(files[f.Path]) {
			t.Fatal("unsealed non-Go file")
		}
	}
	t.Run("missing declared bytes", func(t *testing.T) {
		delete(files, "assembly/answer_arm64.s")
		if _, err := project(data, read); err == nil {
			t.Fatal("missing assembly accepted")
		}
		files["assembly/answer_arm64.s"] = fixtures["assembly/answer_arm64.s"]
	})
	t.Run("changed bytes change seal", func(t *testing.T) {
		files["assembly/answer_arm64.s"] = []byte("same path, changed assembly")
		got, err := project(data, read)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(projected, got) {
			t.Fatal("mutation did not change seal")
		}
	})
	t.Run("duplicate declaration", func(t *testing.T) {
		duplicate := in
		duplicate.Archives = append([]Archive{}, in.Archives...)
		duplicate.Archives[0].Sources = append(append([]Artifact{}, a.Sources...), a.Sources[1])
		b, _ := json.Marshal(duplicate)
		if _, err := project(b, read); err == nil {
			t.Fatal("duplicate non-Go file accepted")
		}
	})
	t.Run("legacy omits non-Go", func(t *testing.T) {
		in.Version = "phebs-t451a-projection-input-v1"
		b, _ := json.Marshal(in)
		b, err := project(b, read)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("other_files")) {
			t.Fatal("legacy schema changed")
		}
	})
}

func TestNonGoPlanV2Authority(t *testing.T) {
	cq, aq, projections := syntheticPlan(t)
	// A generated non-Go artifact belongs to the already configured producer;
	// the same checks used for generated Go files bind that producer and path.
	for name, b := range projections {
		var p projection
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		p.Version = ProjectionV2
		p.Archives[0].OtherFiles = []File{{Artifact: Artifact{Path: "bazel-out/cfg/bin/common/assembly.s", ShortPath: "common/assembly.s", Owner: "@@//common:lib"}, SHA256: testHash, Bytes: 17}}
		projections[name], _ = json.Marshal(p)
	}
	plan, err := AssembleRootsV2(cq, aq, projections, Roots())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != PlanV2 || len(plan.Units[0].OtherFiles) != 1 || len(plan.Documents) != 2 {
		t.Fatal("non-Go authority missing")
	}
	if _, err = Assemble(cq, aq, projections); err == nil {
		t.Fatal("legacy accepts v2")
	}
	cq1, aq1, p1 := syntheticPlan(t)
	if _, err = AssembleRootsV2(cq1, aq1, p1, Roots()); err == nil {
		t.Fatal("v2 accepts legacy projection")
	}
	for _, tc := range []struct {
		name   string
		change func(*File)
	}{
		{"path escape", func(f *File) { f.Path = "../outside.s" }},
		{"substituted owner", func(f *File) { f.Owner = "@@//outside:owner" }},
		{"Go in non-Go", func(f *File) { f.Path = "bazel-out/cfg/bin/common/assembly.go" }},
		{"oversize", func(f *File) { f.Bytes = MaxFileBytes + 1 }},
		{"bad digest", func(f *File) { f.SHA256 = "bad" }},
		{"tree", func(f *File) { f.Tree = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := map[string][]byte{}
			for name, b := range projections {
				var p projection
				_ = json.Unmarshal(b, &p)
				tc.change(&p.Archives[0].OtherFiles[0])
				changed[name], _ = json.Marshal(p)
			}
			if _, err := AssembleRootsV2(cq, aq, changed, Roots()); err == nil {
				t.Fatal("invalid non-Go authority accepted")
			}
		})
	}
}

func TestLegacyProjectionRefusesNonGoField(t *testing.T) {
	for _, value := range []string{"null", "[]"} {
		t.Run(value, func(t *testing.T) {
			cq, aq, projections := syntheticPlan(t)
			for name, b := range projections {
				var p map[string]json.RawMessage
				if err := json.Unmarshal(b, &p); err != nil {
					t.Fatal(err)
				}
				var archives []map[string]json.RawMessage
				if err := json.Unmarshal(p["archives"], &archives); err != nil {
					t.Fatal(err)
				}
				archives[0]["other_files"] = json.RawMessage(value)
				p["archives"], _ = json.Marshal(archives)
				projections[name], _ = json.Marshal(p)
			}
			if _, err := Assemble(cq, aq, projections); err == nil {
				t.Fatal("legacy accepts new field")
			}
		})
	}
}
