package planner

import (
	"embed"
	"errors"
	"io/fs"
	"regexp"
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
	return commands(Roots())
}

var nativeRoot = regexp.MustCompile(`^//[A-Za-z0-9._+/-]*:[A-Za-z0-9._+/-]+$`)

func validateRoots(roots []string) error {
	if len(roots) == 0 || len(roots) > 64 {
		return errors.New("native root count")
	}
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		if !label(root) || !nativeRoot.MatchString(root) || seen[root] || strings.HasSuffix(root, ":all") {
			return errors.New("native roots require unique exact main-repository labels")
		}
		seen[root] = true
	}
	return nil
}

// NativeCommands preserves the planner command shapes and adds only the public
// target's explicit proto source-info flag. The caller owns its fixed roots.
func NativeCommands(roots []string) ([][]string, error) {
	if err := validateRoots(roots); err != nil {
		return nil, err
	}
	result := commands(roots)
	for i := range result {
		result[i] = append(result[i], "--experimental_proto_descriptor_sets_include_source_info")
	}
	return result, nil
}

func commands(roots []string) [][]string {
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
