package planner

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed all:testdata/fixture
var fixtureFS embed.FS

// Fixtures contains only the owned neutral fixture and planner Starlark. The
// caller additionally installs its pinned executable as phebs_plan/t451a.
func Fixtures() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(fixtureFS, "testdata/fixture", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := fixtureFS.ReadFile(p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, "testdata/fixture/")] = b
		return nil
	})
	return out, err
}

func Roots() []string {
	return []string{"//lib:alias", "//lib:alias_two", "//lib:lib_test", "//generated:generated", "//proto:message_go", "//cgo:cgo", "//transition:split"}
}

// Commands returns command suffixes in execution order. The caller prepends
// the pinned Bazel binary/startup arguments and appends the same closed
// resource/cache/profile options to each. Stdout is bounded before buffering.
func Commands() [][]string {
	roots := Roots()
	expr := "deps(set(" + strings.Join(roots, " ") + "))"
	common := []string{"--aspects=//phebs_plan:aspect.bzl%phebs_plan", "--@protobuf//bazel/toolchains:prefer_prebuilt_protoc"}
	// Rule attributes are not authority: Bazel 9.2 emits configured_rule_input
	// from configured prerequisites independently of this attribute allowlist.
	cq := []string{"cquery", expr, "--output=streamed_proto", "--proto:output_rule_attrs=", "--proto:include_configurations", "--transitions=lite", "--consistent_labels", "--universe_scope=" + strings.Join(roots, ",")}
	// aquery needs artifact IDs and paths for the exact configured-owner join.
	aq := []string{"aquery", "mnemonic(\"PhebsPlan\", " + expr + ")", "--output=proto", "--include_aspects", "--noinclude_commandline", "--include_artifacts"}
	b := append([]string{"build", "--output_groups=phebs_plan"}, roots...)
	return [][]string{append(cq, common...), append(aq, common...), append(b, common...)}
}
