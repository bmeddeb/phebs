package main

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
)

const (
	goTestsInclude = "include"
	goTestsExclude = "exclude"
)

// goBuildContext is the complete package-selection policy consumed by
// hydration and both semantic readers. The producer's host platform remains
// independently bound by the exact-toolchain manifest; this target selects
// the corpus source variants to analyze.
type goBuildContext struct {
	GOOS         string
	GOARCH       string
	IncludeTests bool
	BuildTags    []string
}

var (
	knownGoOS = map[string]bool{
		"aix": true, "android": true, "darwin": true, "dragonfly": true,
		"freebsd": true, "illumos": true, "ios": true, "js": true,
		"linux": true, "netbsd": true, "openbsd": true, "plan9": true,
		"solaris": true, "wasip1": true, "windows": true,
	}
	knownGoArch = map[string]bool{
		"386": true, "amd64": true, "arm": true, "arm64": true,
		"loong64": true, "mips": true, "mips64": true, "mips64le": true,
		"mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true,
		"s390x": true, "wasm": true,
	}
)

func validateGoBuildPolicy(goos, goarch, tests string) error {
	if !knownGoOS[goos] {
		return fmt.Errorf("unsupported Go analysis GOOS %q", goos)
	}
	if !knownGoArch[goarch] {
		return fmt.Errorf("unsupported Go analysis GOARCH %q", goarch)
	}
	if tests != goTestsInclude && tests != goTestsExclude {
		return fmt.Errorf("Go analysis test policy must be %q or %q, got %q", goTestsInclude, goTestsExclude, tests)
	}
	return nil
}

func corpusBuildContext(entry CorpusEntry) (goBuildContext, error) {
	if err := validateGoBuildPolicy(entry.GoOS, entry.GoArch, entry.GoTests); err != nil {
		return goBuildContext{}, err
	}
	tags := append([]string(nil), entry.GoBuildTags...)
	sort.Strings(tags)
	return goBuildContext{
		GOOS: entry.GoOS, GOARCH: entry.GoArch,
		IncludeTests: entry.GoTests == goTestsInclude,
		BuildTags:    tags,
	}, nil
}

// runtimeBuildContext keeps package-level fixtures concise. Real corpus reads
// always use corpusBuildContext after loadCorpus has required an explicit
// policy in corpus.lock.json.
func runtimeBuildContext(tags []string) goBuildContext {
	ordered := append([]string(nil), tags...)
	sort.Strings(ordered)
	return goBuildContext{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		IncludeTests: true, BuildTags: ordered,
	}
}

func (context goBuildContext) descriptor() string {
	return strings.Join([]string{
		"go-build-context", context.GOOS, context.GOARCH,
		fmt.Sprintf("tests=%t", context.IncludeTests),
		"tags=" + strings.Join(context.BuildTags, ","),
	}, "\x00")
}
