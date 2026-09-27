package planner

import (
	"errors"
	"path"
	"slices"
	"strings"
)

// VerifyNeutral checks the independent small oracle for the embedded fixture.
// It is deliberately not a target-repository validator or a driver success
// claim. A T45.1a receipt may say PASS only after this check and custody gates.
func VerifyNeutral(plan Plan) error {
	fixture, err := Fixtures()
	if err != nil {
		return err
	}
	expected := map[string]bool{"lib/lib.go": false, "lib/a.go": false, "lib/b.go": false, "lib/lib_test.go": false, "lib/external_test.go": false, "cgo/cgo.go": false}
	sourceIDs := map[string]string{}
	docs := map[string]Document{}
	external := false
	for _, d := range plan.Documents {
		docs[d.ID] = d
		if d.Kind == "source" && d.Repository == "" {
			if _, ok := expected[d.Path]; !ok {
				return errors.New("extra neutral source document")
			}
			if d.SHA256 != digest(fixture[d.Path]) || d.Bytes != len(fixture[d.Path]) {
				return errors.New("neutral source content mismatch")
			}
			expected[d.Path] = true
			sourceIDs[d.Path] = d.ID
		}
		if d.Kind == "external" && strings.HasPrefix(d.Repository, "neutral_external+") && d.Path == "pkg/pkg.go" {
			if d.SHA256 != digest(fixture["external/pkg/pkg.go"]) {
				return errors.New("external neutral source content mismatch")
			}
			external = true
		}
	}
	for _, found := range expected {
		if !found {
			return errors.New("missing neutral source document")
		}
	}
	if !external {
		return errors.New("missing canonical external-repository document")
	}
	units := map[string]Unit{}
	for _, u := range plan.Units {
		units[u.ID] = u
	}
	paths := func(ids []string) []string {
		names := make([]string, len(ids))
		for i, id := range ids {
			names[i] = docs[id].Path
		}
		slices.Sort(names)
		return names
	}
	sameInputs := func(u Unit) bool {
		declared, compiled := slices.Clone(u.GoFiles), slices.Clone(u.CompiledGoFiles)
		slices.Sort(declared)
		slices.Sort(compiled)
		return slices.Equal(declared, compiled)
	}
	fixtureSources := func(u Unit) bool {
		for _, id := range u.GoFiles {
			if sourceIDs[docs[id].Path] != id {
				return false
			}
		}
		return true
	}
	targets := map[string][]Target{}
	for _, t := range plan.Targets {
		targets[t.Label] = append(targets[t.Label], t)
	}
	root := func(label string) (Target, error) {
		ts := targets[canonicalLabel(label)]
		if len(ts) != 1 {
			return Target{}, errors.New("ambiguous neutral root")
		}
		return ts[0], nil
	}
	a, err := root("//lib:alias")
	if err != nil {
		return err
	}
	b, err := root("//lib:alias_two")
	if err != nil {
		return err
	}
	if len(a.Units) != 1 || !slices.Equal(a.Units, b.Units) {
		return errors.New("aliases do not resolve to one stable package unit")
	}
	ordinary := units[a.Units[0]]
	if ordinary.Owner.Label != "@@//lib:lib" || ordinary.ImportPath != "example.test/neutral/lib" || ordinary.PackageName != "lib" || !fixtureSources(ordinary) || !sameInputs(ordinary) || !slices.Equal(paths(ordinary.GoFiles), []string{"lib/a.go", "lib/lib.go"}) {
		return errors.New("ordinary library source sets disagree")
	}
	split, err := root("//transition:split")
	if err != nil {
		return err
	}
	if len(split.Units) != 2 {
		return errors.New("split transition lost configured package identity")
	}
	var alternatives []string
	for _, id := range split.Units {
		u, ok := units[id]
		if !ok || u.Owner.Label != "@@//lib:lib" || u.ImportPath != "example.test/neutral/lib" || u.PackageName != "lib" || len(u.GoFiles) != 2 || !fixtureSources(u) || !sameInputs(u) {
			return errors.New("invalid configured library projection")
		}
		var names []string
		for _, doc := range u.GoFiles {
			names = append(names, docs[doc].Path)
		}
		if !slices.Contains(names, "lib/lib.go") {
			return errors.New("configured library missing shared source")
		}
		if slices.Contains(names, "lib/a.go") {
			alternatives = append(alternatives, "a")
		}
		if slices.Contains(names, "lib/b.go") {
			alternatives = append(alternatives, "b")
		}
	}
	slices.Sort(alternatives)
	if !slices.Equal(alternatives, []string{"a", "b"}) {
		return errors.New("transition-selected source sets disagree")
	}
	for _, tc := range []struct{ label, name, producer, importPath, packageName string }{
		{"//generated:generated", "generated.go", "@@//generated:source", "example.test/neutral/generated", "generated"},
		{"//proto:message_go", "message.pb.go", "@@//proto:message_proto", "example.test/neutral/message", "message"},
	} {
		t, err := root(tc.label)
		if err != nil {
			return err
		}
		if len(t.Units) != 1 {
			return errors.New("generated target has ambiguous unit")
		}
		u := units[t.Units[0]]
		if u.Owner.Label != canonicalLabel(tc.label) || u.ImportPath != tc.importPath || u.PackageName != tc.packageName || len(u.GoFiles) != 1 || !slices.Equal(u.GoFiles, u.CompiledGoFiles) {
			return errors.New("generated source set disagrees")
		}
		d := docs[u.GoFiles[0]]
		if d.Kind != "generated" || d.Repository != "" || path.Base(d.Path) != tc.name || d.Producer.Label != tc.producer || !checksum(d.Producer.Configuration) {
			return errors.New("generated source lost producer identity")
		}
	}
	cgo, err := root("//cgo:cgo")
	if err != nil {
		return err
	}
	if len(cgo.Units) != 1 {
		return errors.New("ambiguous cgo unit")
	}
	cu := units[cgo.Units[0]]
	if cu.Owner.Label != "@@//cgo:cgo" || cu.ImportPath != "example.test/neutral/cgo" || cu.PackageName != "cgo" || len(cu.GoFiles) != 1 || cu.GoFiles[0] != sourceIDs["cgo/cgo.go"] || len(cu.CompiledGoFiles) != 3 {
		return errors.New("cgo source/compiled sets disagree")
	}
	var cgoNames []string
	for _, id := range cu.CompiledGoFiles {
		d := docs[id]
		if d.Kind != "generated" || d.Repository != "" || d.Producer != cu.Owner {
			return errors.New("raw cgo source entered compile set")
		}
		cgoNames = append(cgoNames, path.Base(d.Path))
	}
	slices.Sort(cgoNames)
	if !slices.Equal(cgoNames, []string{"_cgo_gotypes.go", "_cgo_imports.go", "cgo.cgo1.go"}) {
		return errors.New("cgo generated documents disagree")
	}
	internal, externalTest, main := false, false, false
	var internalID, externalID, mainID string
	testUnits := 0
	for _, u := range plan.Units {
		if u.Owner.Label != "@@//lib:lib_test" {
			continue
		}
		testUnits++
		if !sameInputs(u) {
			return errors.New("test source and compiled sets disagree")
		}
		if u.ImportPath != "testmain" && !fixtureSources(u) {
			return errors.New("test sources do not name canonical fixture documents")
		}
		names := paths(u.CompiledGoFiles)
		if u.ImportPath == "example.test/neutral/lib" {
			internal = u.PackageName == "lib" && slices.Equal(names, []string{"lib/a.go", "lib/lib.go", "lib/lib_test.go"})
			internalID = u.ID
		}
		if u.ImportPath == "example.test/neutral/lib_test" {
			externalTest = u.PackageName == "lib_test" && slices.Equal(names, []string{"lib/external_test.go"})
			externalID = u.ID
		}
		if u.ImportPath == "testmain" {
			main = u.PackageName == "main" && len(names) == 1 && path.Base(names[0]) == "testmain.go" && docs[u.GoFiles[0]].Kind == "generated" && docs[u.GoFiles[0]].Repository == "" && docs[u.GoFiles[0]].Producer == u.Owner
			mainID = u.ID
		}
	}
	if testUnits != 3 || !internal || !externalTest || !main {
		return errors.New("test archive variants were not preserved")
	}
	test, err := root("//lib:lib_test")
	if err != nil || !slices.Equal(test.Units, []string{mainID}) {
		return errors.New("test target does not bind only its main archive")
	}
	// Provider edges intentionally include implicit compiler/test dependencies,
	// not just textual imports. Freeze the pinned rules_go fixture contract.
	for _, u := range plan.Units {
		var want []string
		switch u.Owner.Label {
		case "@@//lib:lib":
			want = []string{"example.test/external/pkg"}
		case "@@//lib:lib_test":
			want = []string{"example.test/external/pkg"}
			if u.ID == externalID || u.ID == mainID {
				want = append(want, "example.test/neutral/lib")
			}
			if u.ID == mainID {
				want = append(want, "example.test/neutral/lib_test", "github.com/bazelbuild/rules_go/go/tools/bzltestutil")
			}
		case "@@//generated:generated", "@@//cgo:cgo":
		case "@@//proto:message_go":
			want = []string{"github.com/golang/protobuf/proto"}
			for _, suffix := range []string{"proto", "reflect/protoreflect", "reflect/protoregistry", "runtime/protoiface", "runtime/protoimpl", "types/descriptorpb", "types/gofeaturespb", "types/known/anypb", "types/known/apipb", "types/known/durationpb", "types/known/emptypb", "types/known/fieldmaskpb", "types/known/sourcecontextpb", "types/known/structpb", "types/known/timestamppb", "types/known/typepb", "types/known/wrapperspb", "types/pluginpb"} {
				want = append(want, "google.golang.org/protobuf/"+suffix)
			}
		default:
			continue
		}
		got := make([]string, 0, len(u.Imports))
		for _, edge := range u.Imports {
			if units[edge.Unit].ImportPath != edge.Path ||
				(u.Owner.Label == "@@//lib:lib_test" && edge.Path == "example.test/neutral/lib" && edge.Unit != internalID) ||
				(u.Owner.Label == "@@//lib:lib_test" && edge.Path == "example.test/neutral/lib_test" && edge.Unit != externalID) {
				return errors.New("neutral import maps to the wrong package unit")
			}
			got = append(got, edge.Path)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			return errors.New("neutral direct package edges disagree")
		}
	}
	return nil
}
