package planner

import "testing"

func TestMatchGoFile(t *testing.T) {
	for _, tt := range []struct {
		name, source string
		mode         GoMode
		match, fail  bool
	}{
		{"a.go", "package p\n", GoMode{GOOS: "linux", GOARCH: "arm64"}, true, false},
		{"a_darwin.go", "package p\n", GoMode{GOOS: "linux", GOARCH: "arm64"}, false, false},
		{"a_arm64.go", "package p\n", GoMode{GOOS: "linux", GOARCH: "arm64"}, true, false},
		{"a.go", "//go:build go1.26\n\npackage p\n", GoMode{GOOS: "linux", GOARCH: "arm64"}, false, false},
		{"a.go", "//go:build enabled && go1.25 && arm64.v8.0\n\npackage p\n", GoMode{GOOS: "linux", GOARCH: "arm64", Tags: []string{"enabled"}}, true, false},
		{"a.go", "//go:build cgo\n\npackage p\n", GoMode{GOOS: "linux", GOARCH: "arm64"}, false, false},
		{"a.go", "//go:build cgo\n\npackage p\n", GoMode{GOOS: "linux", GOARCH: "arm64", Cgo: true}, true, false},
		{"a.go", "package p\nimport \"C\"\n", GoMode{GOOS: "linux", GOARCH: "arm64"}, true, false},
		{"a.go", "package p\n", GoMode{GOOS: "darwin", GOARCH: "arm64"}, false, true},
		{"a_amd64.go", "package p\n", GoMode{GOOS: "linux", GOARCH: "amd64"}, true, false},
		{"a_arm64.go", "package p\n", GoMode{GOOS: "linux", GOARCH: "amd64"}, false, false},
		{"a.go", "//go:build amd64.v1 && go1.25 && !go1.26\n\npackage p\n", GoMode{GOOS: "linux", GOARCH: "amd64"}, true, false},
		{"a.go", "//go:build amd64.v2 || arm64.v8.0\n\npackage p\n", GoMode{GOOS: "linux", GOARCH: "amd64"}, false, false},
		{"a.go", "package p\n", GoMode{GOOS: "linux", GOARCH: "386"}, false, true},
		{"./a.go", "package p\n", GoMode{GOOS: "linux", GOARCH: "arm64"}, false, true},
	} {
		got, err := MatchGoFile(tt.name, []byte(tt.source), tt.mode)
		if got != tt.match || (err != nil) != tt.fail {
			t.Errorf("MatchGoFile(%q, %q, %+v) = %v, %v", tt.name, tt.source, tt.mode, got, err)
		}
	}
}
