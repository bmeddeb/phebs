package planner

import (
	"embed"
	"errors"
	"io/fs"
	"strings"
)

//go:embed all:testdata/fixture
var fixtureFS embed.FS

// The new fixture is separate so historical embedded fixture bytes stay exact.
//
//go:embed all:testdata/fixture-v2
var fixtureV2FS embed.FS

// FixturesV2 adds an assembly package without changing the V1 fixture map.
func FixturesV2() (map[string][]byte, error) {
	out, err := Fixtures()
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(fixtureV2FS, "testdata/fixture-v2", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := fixtureV2FS.ReadFile(p)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(p, "testdata/fixture-v2/")
		if _, exists := out[name]; exists {
			return errors.New("v2 fixture overlaps historical fixture")
		}
		out[name] = b
		return nil
	})
	return out, err
}

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

// Assemble never creates completeness from a package driver's output. The
// cquery universe and owned aspect projections must agree through aquery's
// exact declared-artifact locator. Extra bytes, objects and edges refuse.
func Assemble(cqueryProto, aqueryProto []byte, projections map[string][]byte) (Plan, error) {
	return assemble(cqueryProto, aqueryProto, projections, Roots(), false)
}
