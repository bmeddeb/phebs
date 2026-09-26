package planner

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func sdkFixture(t *testing.T) (SDKInput, map[string][]byte) {
	t.Helper()
	const root = "external/go_sdk+"
	const owner = "@@rules_go+//:stdlib"
	const cache = "bazel-out/exact/bin/external/rules_go+/stdlib/gocache"
	in := SDKInput{Version: "1.25.0", Root: root, Prefix: "@@rules_go+//stdlib:", List: Artifact{Path: "bazel-out/exact/bin/external/rules_go+/stdlib/stdlib.pkg.json", Owner: owner}, Caches: []Artifact{{Path: cache, Owner: owner, Tree: true}}, Sources: []string{root + "/src/unsafe/unsafe.go", root + "/src/demo/demo.go"}}
	pkgs := []sdkListPackage{
		{ID: in.Prefix + "unsafe", Name: "unsafe", PkgPath: "unsafe", Standard: true, GoFiles: []string{"__BAZEL_OUTPUT_BASE__/" + in.Sources[0]}},
		{ID: in.Prefix + "demo", Name: "demo", PkgPath: "demo", Standard: true, GoFiles: []string{"__BAZEL_OUTPUT_BASE__/" + in.Sources[1]}, CompiledGoFiles: []string{"__BAZEL_EXECROOT__/" + cache + "/ab/cgo-d"}, Imports: map[string]string{"unsafe": in.Prefix + "unsafe"}},
	}
	in.Mode = GoMode{GOOS: "linux", GOARCH: "arm64", Tags: []string{}}
	files := map[string][]byte{in.Sources[0]: []byte("package unsafe\n"), in.Sources[1]: []byte("package demo\nimport \"unsafe\"\n"), cache + "/ab/cgo-d": []byte("package demo\nimport \"unsafe\"\n")}
	for _, p := range pkgs {
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		files[in.List.Path] = append(files[in.List.Path], append(data, '\n')...)
	}
	return in, files
}

func TestSDKProjectionUsesOnlyDeclaredArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*SDKInput, map[string][]byte)
	}{
		{"missing source", func(in *SDKInput, files map[string][]byte) { delete(files, in.Sources[0]) }},
		{"undeclared source", func(in *SDKInput, files map[string][]byte) { in.Sources = in.Sources[1:] }},
		{"escaped cache", func(in *SDKInput, files map[string][]byte) {
			files[in.List.Path] = []byte(strings.ReplaceAll(string(files[in.List.Path]), "/ab/cgo-d", "/../outside/cgo-d"))
		}},
		{"missing cache document", func(in *SDKInput, files map[string][]byte) { delete(files, in.Caches[0].Path+"/ab/cgo-d") }},
		{"missing package", func(in *SDKInput, files map[string][]byte) {
			files[in.List.Path] = []byte(strings.SplitN(string(files[in.List.Path]), "\n", 2)[1])
		}},
		{"duplicate package", func(in *SDKInput, files map[string][]byte) {
			files[in.List.Path] = append(files[in.List.Path], files[in.List.Path]...)
		}},
		{"duplicate member", func(in *SDKInput, files map[string][]byte) {
			files[in.List.Path] = append([]byte(`{"ID":"duplicate",`), files[in.List.Path][1:]...)
		}},
		{"case-aliased member", func(in *SDKInput, files map[string][]byte) {
			files[in.List.Path] = []byte(strings.ReplaceAll(string(files[in.List.Path]), `"Name":"demo"`, `"Name":"demo","name":"other"`))
		}},
		{"wrong SDK", func(in *SDKInput, files map[string][]byte) { in.Version = "1.26.0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, files := sdkFixture(t)
			tc.mutate(&in, files)
			if _, err := projectSDK(in, func(name string, limit int) ([]byte, error) {
				b, ok := files[name]
				if !ok || len(b) > limit {
					return nil, errors.New("missing/bounded")
				}
				return b, nil
			}); err == nil {
				t.Fatal("accepted mutated SDK authority")
			}
		})
	}
	in, files := sdkFixture(t)
	reads := map[string]int{}
	sdk, err := projectSDK(in, func(name string, limit int) ([]byte, error) {
		reads[name]++
		b, ok := files[name]
		if !ok || len(b) > limit {
			return nil, errors.New("undeclared read")
		}
		return b, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sdk.Packages) != 2 || len(sdk.Documents) != 3 || len(reads) != 4 {
		t.Fatal("SDK package/document/read set differs")
	}
	for _, count := range reads {
		if count != 1 {
			t.Fatal("repeated SDK file read")
		}
	}
	sdk.Documents = append(sdk.Documents, SDKDocument{ExecPath: in.Root + "/src/extra/extra.go", Path: "src/extra/extra.go", SHA256: testHash})
	if err := validateSDK(sdk); err == nil {
		t.Fatal("accepted extra SDK document")
	}
}

func TestStrictStructFieldsPreserveImportCase(t *testing.T) {
	var value struct{ Imports map[string]string }
	data := []byte(`{"Imports":{"example.test/Pkg":"upper","example.test/pkg":"lower"}}`)
	if err := strictJSON(data, &value); err != nil || len(value.Imports) != 2 {
		t.Fatalf("folded import keys: %v", err)
	}
	if err := strictJSON([]byte(`{"Imports":{},"imports":{"hidden":"hidden"}}`), &value); err == nil {
		t.Fatal("accepted struct field alias")
	}
}

func TestSDKExcludedHelperRequiresExplicitSameRootFile(t *testing.T) {
	in, files := sdkFixture(t)
	name := in.Root + "/src/internal/obscuretestdata/obscuretestdata.go"
	pkg := sdkListPackage{ID: in.Prefix + "internal/obscuretestdata", Name: "obscuretestdata", PkgPath: "internal/obscuretestdata", Standard: true, GoFiles: []string{"__BAZEL_OUTPUT_BASE__/" + name}}
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	files[in.List.Path] = append(files[in.List.Path], append(data, '\n')...)
	files[name] = []byte("package obscuretestdata\n")
	reads := map[string]bool{}
	read := func(name string, limit int) ([]byte, error) {
		reads[name] = true
		data, ok := files[name]
		if !ok || len(data) > limit {
			return nil, errors.New("undeclared read")
		}
		return data, nil
	}
	if _, err := projectSDK(in, read); err == nil || reads[name] {
		t.Fatal("read an excluded helper without its explicit File declaration")
	}
	in.Sources = append(in.Sources, name)
	sdk, err := projectSDK(in, read)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range sdk.Documents {
		if d.ExecPath == name {
			found = d.Path == "src/internal/obscuretestdata/obscuretestdata.go" && d.SHA256 == digest(files[name]) && !d.Generated
		}
	}
	if !found {
		t.Fatal("explicit SDK helper lost its canonical identity")
	}
	in.Sources[len(in.Sources)-1] = "external/another_sdk/src/internal/obscuretestdata/obscuretestdata.go"
	if _, err := projectSDK(in, read); err == nil {
		t.Fatal("accepted helper declaration from a different SDK root")
	}
}
