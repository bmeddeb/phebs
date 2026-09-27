package planner

import (
	"path"
	"slices"
	"testing"
)

func neutralOraclePlan(t *testing.T) Plan {
	t.Helper()
	fixtures, err := Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	p := Plan{}
	for _, name := range []string{"lib/lib.go", "lib/a.go", "lib/b.go", "lib/lib_test.go", "lib/external_test.go", "cgo/cgo.go"} {
		p.Documents = append(p.Documents, Document{ID: name, Kind: "source", Path: name, SHA256: digest(fixtures[name]), Bytes: len(fixtures[name])})
	}
	p.Documents = append(p.Documents, Document{ID: "external", Kind: "external", Repository: "neutral_external+", Path: "pkg/pkg.go", SHA256: digest(fixtures["external/pkg/pkg.go"])})
	for _, name := range []string{"generated/generated.go", "proto/message.pb.go", "cgo/_cgo_gotypes.go", "cgo/_cgo_imports.go", "cgo/cgo.cgo1.go", "lib/testmain.go"} {
		owner := "@@//cgo:cgo"
		switch name {
		case "generated/generated.go":
			owner = "@@//generated:source"
		case "proto/message.pb.go":
			owner = "@@//proto:message_proto"
		case "lib/testmain.go":
			owner = "@@//lib:lib_test"
		}
		p.Documents = append(p.Documents, Document{ID: name, Kind: "generated", Path: name, Producer: Configured{Label: owner, Configuration: testHash}})
	}
	add := func(id, owner, importPath string, files ...string) {
		name := path.Base(importPath)
		if importPath == "testmain" {
			name = "main"
		}
		p.Units = append(p.Units, Unit{ID: id, Owner: Configured{Label: owner, Configuration: testHash}, ImportPath: importPath, PackageName: name, GoFiles: files, CompiledGoFiles: slices.Clone(files)})
	}
	add("a", "@@//lib:lib", "example.test/neutral/lib", "lib/a.go", "lib/lib.go")
	add("b", "@@//lib:lib", "example.test/neutral/lib", "lib/b.go", "lib/lib.go")
	add("generated", "@@//generated:generated", "example.test/neutral/generated", "generated/generated.go")
	add("proto", "@@//proto:message_go", "example.test/neutral/message", "proto/message.pb.go")
	add("cgo", "@@//cgo:cgo", "example.test/neutral/cgo", "cgo/cgo.go")
	p.Units[len(p.Units)-1].CompiledGoFiles = []string{"cgo/_cgo_gotypes.go", "cgo/_cgo_imports.go", "cgo/cgo.cgo1.go"}
	add("internal", "@@//lib:lib_test", "example.test/neutral/lib", "lib/a.go", "lib/lib.go", "lib/lib_test.go")
	add("external_test", "@@//lib:lib_test", "example.test/neutral/lib_test", "lib/external_test.go")
	add("main", "@@//lib:lib_test", "testmain", "lib/testmain.go")
	for _, name := range []string{"example.test/external/pkg", "github.com/bazelbuild/rules_go/go/tools/bzltestutil", "github.com/golang/protobuf/proto", "google.golang.org/protobuf/proto", "google.golang.org/protobuf/reflect/protoreflect", "google.golang.org/protobuf/reflect/protoregistry", "google.golang.org/protobuf/runtime/protoiface", "google.golang.org/protobuf/runtime/protoimpl", "google.golang.org/protobuf/types/descriptorpb", "google.golang.org/protobuf/types/gofeaturespb", "google.golang.org/protobuf/types/known/anypb", "google.golang.org/protobuf/types/known/apipb", "google.golang.org/protobuf/types/known/durationpb", "google.golang.org/protobuf/types/known/emptypb", "google.golang.org/protobuf/types/known/fieldmaskpb", "google.golang.org/protobuf/types/known/sourcecontextpb", "google.golang.org/protobuf/types/known/structpb", "google.golang.org/protobuf/types/known/timestamppb", "google.golang.org/protobuf/types/known/typepb", "google.golang.org/protobuf/types/known/wrapperspb", "google.golang.org/protobuf/types/pluginpb"} {
		add(name, "@@dependency//:lib", name)
	}
	for i := range p.Units {
		u := &p.Units[i]
		switch u.ID {
		case "a", "b", "internal", "external_test", "main":
			u.Imports = []UnitImport{{Path: "example.test/external/pkg", Unit: "example.test/external/pkg"}}
			if u.ID == "external_test" || u.ID == "main" {
				u.Imports = append(u.Imports, UnitImport{Path: "example.test/neutral/lib", Unit: "internal"})
			}
			if u.ID == "main" {
				u.Imports = append(u.Imports, UnitImport{Path: "example.test/neutral/lib_test", Unit: "external_test"}, UnitImport{Path: "github.com/bazelbuild/rules_go/go/tools/bzltestutil", Unit: "github.com/bazelbuild/rules_go/go/tools/bzltestutil"})
			}
		case "proto":
			for _, dep := range p.Units[10:] {
				u.Imports = append(u.Imports, UnitImport{Path: dep.ImportPath, Unit: dep.ID})
			}
		}
	}
	for label, ids := range map[string][]string{
		"@@//lib:alias": {"a"}, "@@//lib:alias_two": {"a"}, "@@//transition:split": {"a", "b"},
		"@@//generated:generated": {"generated"}, "@@//proto:message_go": {"proto"}, "@@//cgo:cgo": {"cgo"}, "@@//lib:lib_test": {"main"},
	} {
		p.Targets = append(p.Targets, Target{Configured: Configured{Label: label, Configuration: testHash}, Units: ids})
	}
	return p
}

func TestNeutralOracleCompleteSourceSets(t *testing.T) {
	if err := VerifyNeutral(neutralOraclePlan(t)); err != nil {
		t.Fatal(err)
	}
	for _, unit := range []string{"a", "b", "generated", "proto", "cgo", "internal", "external_test", "main"} {
		for _, field := range []string{"import path", "package name"} {
			t.Run(unit+" "+field, func(t *testing.T) {
				p := neutralOraclePlan(t)
				for i := range p.Units {
					if p.Units[i].ID == unit {
						if field == "import path" {
							p.Units[i].ImportPath = "example.test/wrong"
						} else {
							p.Units[i].PackageName = "wrong"
						}
					}
				}
				if err := VerifyNeutral(p); err == nil {
					t.Fatal("accepted wrong fixture package identity")
				}
			})
		}
	}
	for _, tc := range []struct {
		name, unit string
		mutate     func(*Unit)
	}{
		{"ordinary compiled omission", "a", func(u *Unit) { u.CompiledGoFiles = u.CompiledGoFiles[:1] }},
		{"split compiled substitution", "b", func(u *Unit) { u.CompiledGoFiles = []string{"lib/a.go", "lib/lib.go"} }},
		{"generated compiled substitution", "generated", func(u *Unit) { u.CompiledGoFiles = []string{"proto/message.pb.go"} }},
		{"proto compiled substitution", "proto", func(u *Unit) { u.CompiledGoFiles = []string{"generated/generated.go"} }},
		{"internal embedded omission", "internal", func(u *Unit) { u.GoFiles, u.CompiledGoFiles = []string{"lib/lib_test.go"}, []string{"lib/lib_test.go"} }},
		{"internal compiled omission", "internal", func(u *Unit) { u.CompiledGoFiles = []string{"lib/lib_test.go"} }},
		{"missing package edge", "a", func(u *Unit) { u.Imports = nil }},
		{"extra package edge", "generated", func(u *Unit) { u.Imports = []UnitImport{{Path: "example.test/neutral/lib", Unit: "a"}} }},
		{"wrong configured test variant", "external_test", func(u *Unit) { u.Imports[1].Unit = "a" }},
		{"external extra source", "external_test", func(u *Unit) {
			u.GoFiles, u.CompiledGoFiles = []string{"lib/external_test.go", "lib/a.go"}, []string{"lib/external_test.go", "lib/a.go"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := neutralOraclePlan(t)
			for i := range p.Units {
				if p.Units[i].ID == tc.unit {
					tc.mutate(&p.Units[i])
				}
			}
			if err := VerifyNeutral(p); err == nil {
				t.Fatal("accepted incomplete or substituted fixture mapping")
			}
		})
	}
	p := neutralOraclePlan(t)
	p.Units = append(p.Units, p.Units[7])
	if err := VerifyNeutral(p); err == nil {
		t.Fatal("accepted extra test archive")
	}
	p = neutralOraclePlan(t)
	for i := range p.Targets {
		if p.Targets[i].Label == "@@//lib:lib_test" {
			p.Targets[i].Units = []string{"internal"}
		}
	}
	if err := VerifyNeutral(p); err == nil {
		t.Fatal("accepted wrong test root archive")
	}
	p = neutralOraclePlan(t)
	for i := range p.Documents {
		if p.Documents[i].ID == "proto/message.pb.go" {
			p.Documents[i].Producer.Label = "@@//proto:message_go"
		}
	}
	if err := VerifyNeutral(p); err == nil {
		t.Fatal("accepted the embedding library as proto source producer")
	}
	for _, both := range []bool{false, true} {
		p = neutralOraclePlan(t)
		p.Documents = append(p.Documents, Document{ID: "substitute", Kind: "generated", Path: "lib/b.go", Producer: Configured{Configuration: testHash}})
		p.Units[1].CompiledGoFiles[0] = "substitute"
		if both {
			p.Units[1].GoFiles[0] = "substitute"
		}
		if err := VerifyNeutral(p); err == nil {
			t.Fatal("accepted different canonical document at the same relative path")
		}
	}
}
