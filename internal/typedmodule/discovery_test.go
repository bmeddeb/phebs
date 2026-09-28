package typedmodule

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

// fakeSource is a synthetic ControlSource: it returns exact bytes for known
// repo-relative regular control files and refuses absent or over-bound paths,
// mirroring the run worker's inventory-reader contract.
type fakeSource struct{ files map[string][]byte }

func (f fakeSource) Read(_ context.Context, p string, maxBytes int) ([]byte, error) {
	b, ok := f.files[p]
	if !ok {
		return nil, typedindex.Unprepared
	}
	if len(b) > maxBytes {
		return nil, typedindex.Invalid
	}
	return b, nil
}

func goMod(module, goVer string) []byte {
	return []byte("module " + module + "\n\ngo " + goVer + "\n")
}

func TestDiscoverSingleModule(t *testing.T) {
	ctx := context.Background()
	src := fakeSource{files: map[string][]byte{"go.mod": goMod("example.com/root", "1.25")}}
	sel, err := Discover(ctx, src, ModeSingle, "go.mod", []string{"example.com/root/cmd"})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Mode != ModeSingle || sel.Entry != "go.mod" || len(sel.Roots) != 1 {
		t.Fatalf("unexpected selection: %#v", sel)
	}
	r := sel.Roots[0]
	if r.Path != "." || r.Module != "example.com/root" || r.GoMod != "go.mod" || !isDigest(r.Digest) {
		t.Fatalf("unexpected root: %#v", r)
	}
	if len(sel.Controls) != 1 || !isDigest(sel.Controls["go.mod"]) {
		t.Fatalf("unexpected controls: %#v", sel.Controls)
	}
	if strings.Join(sel.Packages, ",") != "example.com/root/cmd" {
		t.Fatalf("unexpected packages: %#v", sel.Packages)
	}
}

func TestDiscoverSingleModuleIgnoresSiblingGoWork(t *testing.T) {
	ctx := context.Background()
	src := fakeSource{files: map[string][]byte{
		"go.mod":  goMod("example.com/root", "1.25"),
		"go.work": []byte("go 1.25\n\nuse (\n\t./other\n)\n"),
	}}
	sel, err := Discover(ctx, src, ModeSingle, "go.mod", []string{"example.com/root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sel.Controls["go.work"]; ok {
		t.Fatalf("single-module mode must ignore a sibling go.work: %#v", sel.Controls)
	}
	if len(sel.Controls) != 1 {
		t.Fatalf("expected exactly one control, got %#v", sel.Controls)
	}
}

func TestDiscoverWorkspace(t *testing.T) {
	ctx := context.Background()
	src := fakeSource{files: map[string][]byte{
		"go.work":  []byte("go 1.25\n\nuse (\n\t./b\n\t./a\n)\n"),
		"a/go.mod": goMod("example.com/a", "1.25"),
		"b/go.mod": goMod("example.com/b", "1.25"),
	}}
	sel, err := Discover(ctx, src, ModeWorkspace, "go.work", []string{"example.com/a", "example.com/b"})
	if err != nil {
		t.Fatal(err)
	}
	if sel.Mode != ModeWorkspace || len(sel.Roots) != 2 {
		t.Fatalf("unexpected selection: %#v", sel)
	}
	// Roots are sorted ascending by path even though the workfile listed b first.
	if sel.Roots[0].Path != "a" || sel.Roots[1].Path != "b" {
		t.Fatalf("roots not sorted ascending: %#v", sel.Roots)
	}
	if len(sel.Controls) != 3 {
		t.Fatalf("expected go.work + two go.mod controls, got %#v", sel.Controls)
	}
	for _, p := range []string{"go.work", "a/go.mod", "b/go.mod"} {
		if !isDigest(sel.Controls[p]) {
			t.Fatalf("missing control digest for %q: %#v", p, sel.Controls)
		}
	}
}

func TestDiscoverRefusals(t *testing.T) {
	ctx := context.Background()
	workN := func(n int) []byte {
		var b strings.Builder
		b.WriteString("go 1.25\n\nuse (\n")
		for i := 0; i < n; i++ {
			b.WriteString("\t./m" + string(rune('a'+i)) + "\n")
		}
		b.WriteString(")\n")
		return []byte(b.String())
	}
	fourMods := map[string][]byte{"go.work": workN(4)}
	for i := 0; i < 4; i++ {
		d := "m" + string(rune('a'+i))
		fourMods[d+"/go.mod"] = goMod("example.com/"+d, "1.25")
	}

	cases := []struct {
		name  string
		src   fakeSource
		mode  Mode
		entry string
		pkgs  []string
		want  error
	}{
		{"nil source", fakeSource{}, ModeSingle, "go.mod", nil, nil}, // handled below
		{"bad mode", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, Mode("x"), "go.mod", []string{"m"}, typedindex.Invalid},
		{"single entry not go.mod", fakeSource{files: map[string][]byte{"go.work": []byte("go 1.25\n\nuse ./a\n")}}, ModeSingle, "go.work", []string{"m"}, typedindex.Invalid},
		{"workspace entry not go.work", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeWorkspace, "go.mod", []string{"m"}, typedindex.Invalid},
		{"absolute entry", fakeSource{files: map[string][]byte{}}, ModeSingle, "/go.mod", []string{"m"}, typedindex.Invalid},
		{"missing go.mod", fakeSource{files: map[string][]byte{}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Unprepared},
		{"malformed go.mod", fakeSource{files: map[string][]byte{"go.mod": []byte("this is not a go.mod\n")}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Invalid},
		{"unknown directive", fakeSource{files: map[string][]byte{"go.mod": []byte("module m\n\nfoobar 1\n")}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Invalid},
		{"no module directive", fakeSource{files: map[string][]byte{"go.mod": []byte("go 1.25\n")}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Invalid},
		{"newer go", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.26")}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Unsupported},
		{"newer toolchain", fakeSource{files: map[string][]byte{"go.mod": []byte("module m\n\ngo 1.25\n\ntoolchain go1.26.0\n")}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Unsupported},
		{"replace", fakeSource{files: map[string][]byte{"go.mod": []byte("module m\n\ngo 1.25\n\nreplace example.com/y => ../y\n")}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Unsupported},
		{"exclude", fakeSource{files: map[string][]byte{"go.mod": []byte("module m\n\ngo 1.25\n\nexclude example.com/y v1.0.0\n")}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Unsupported},
		{"retract", fakeSource{files: map[string][]byte{"go.mod": []byte("module m\n\ngo 1.25\n\nretract v1.0.0\n")}}, ModeSingle, "go.mod", []string{"m"}, typedindex.Unsupported},
		{"empty packages", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", nil, typedindex.Invalid},
		{"wildcard package", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", []string{"m/..."}, typedindex.Invalid},
		{"dot-slash-ellipsis", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", []string{"./..."}, typedindex.Invalid},
		{"all default", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", []string{"all"}, typedindex.Invalid},
		{"std default", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", []string{"std"}, typedindex.Invalid},
		{"empty selector", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", []string{""}, typedindex.Invalid},
		{"star selector", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", []string{"m/*"}, typedindex.Invalid},
		{"absolute selector", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", []string{"/m"}, typedindex.Invalid},
		{"duplicate selector", fakeSource{files: map[string][]byte{"go.mod": goMod("m", "1.25")}}, ModeSingle, "go.mod", []string{"m", "m"}, typedindex.Invalid},
		{"workspace duplicate use", fakeSource{files: map[string][]byte{"go.work": []byte("go 1.25\n\nuse (\n\t./a\n\t./a\n)\n"), "a/go.mod": goMod("example.com/a", "1.25")}}, ModeWorkspace, "go.work", []string{"example.com/a"}, typedindex.Invalid},
		{"workspace missing use go.mod", fakeSource{files: map[string][]byte{"go.work": []byte("go 1.25\n\nuse ./a\n")}}, ModeWorkspace, "go.work", []string{"example.com/a"}, typedindex.Unprepared},
		{"workspace absolute use", fakeSource{files: map[string][]byte{"go.work": []byte("go 1.25\n\nuse /a\n")}}, ModeWorkspace, "go.work", []string{"m"}, typedindex.Invalid},
		{"workspace escaping use", fakeSource{files: map[string][]byte{"go.work": []byte("go 1.25\n\nuse ../a\n")}}, ModeWorkspace, "go.work", []string{"m"}, typedindex.Invalid},
		{"workspace zero use", fakeSource{files: map[string][]byte{"go.work": []byte("go 1.25\n")}}, ModeWorkspace, "go.work", []string{"m"}, typedindex.Capacity},
		{"workspace replace", fakeSource{files: map[string][]byte{"go.work": []byte("go 1.25\n\nuse ./a\n\nreplace m => ./x\n"), "a/go.mod": goMod("example.com/a", "1.25")}}, ModeWorkspace, "go.work", []string{"example.com/a"}, typedindex.Unsupported},
		{"workspace cap+1", fakeSource{files: map[string][]byte{"go.work": workN(5)}}, ModeWorkspace, "go.work", []string{"m"}, typedindex.Capacity},
		{"workspace cap exactly 4", fakeSource{files: fourMods}, ModeWorkspace, "go.work", []string{"example.com/ma"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "nil source" {
				if _, err := Discover(ctx, nil, ModeSingle, "go.mod", []string{"m"}); err != typedindex.Invalid {
					t.Fatalf("nil source err = %v, want Invalid", err)
				}
				return
			}
			_, err := Discover(ctx, tc.src, tc.mode, tc.entry, tc.pkgs)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected err %v", err)
				}
				return
			}
			if err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDiscoverPerControlByteCap(t *testing.T) {
	ctx := context.Background()
	big := append([]byte("module example.com/big\n\ngo 1.25\n\n// "), make([]byte, MaxControlBytes)...)
	src := fakeSource{files: map[string][]byte{"go.mod": big}}
	if _, err := Discover(ctx, src, ModeSingle, "go.mod", []string{"example.com/big"}); err == nil {
		t.Fatal("over-cap control file accepted")
	}
}

func TestModuleSelectionCanonicalCodec(t *testing.T) {
	ctx := context.Background()
	src := fakeSource{files: map[string][]byte{
		"go.work":  []byte("go 1.25\n\nuse (\n\t./a\n\t./b\n)\n"),
		"a/go.mod": goMod("example.com/a", "1.25"),
		"b/go.mod": goMod("example.com/b", "1.25"),
	}}
	sel, err := Discover(ctx, src, ModeWorkspace, "go.work", []string{"example.com/b", "example.com/a"})
	if err != nil {
		t.Fatal(err)
	}
	if !stringsEqual(sel.Packages, []string{"example.com/a", "example.com/b"}) {
		t.Fatalf("packages not normalized ascending: %#v", sel.Packages)
	}
	raw, err := sel.Encode()
	if err != nil {
		t.Fatal(err)
	}
	d1 := sel.Digest()
	if !isDigest(d1) {
		t.Fatalf("bad digest %q", d1)
	}
	got, err := DecodeModuleSelection(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest() != d1 {
		t.Fatalf("round-trip digest %q != %q", got.Digest(), d1)
	}
	re, _ := got.Encode()
	if string(re) != string(raw) {
		t.Fatal("round-trip is not byte-stable")
	}

	// Non-canonical: re-encoded from a map (alphabetical keys) must refuse.
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	reordered, _ := json.Marshal(m)
	if _, err := DecodeModuleSelection(ctx, reordered); err != typedindex.Invalid {
		t.Fatalf("reordered-key bytes err = %v, want Invalid", err)
	}
	// Unknown field must refuse.
	if _, err := DecodeModuleSelection(ctx, []byte(strings.Replace(string(raw), `{"schema"`, `{"evil":1,"schema"`, 1))); err != typedindex.Invalid {
		t.Fatalf("unknown-field bytes err = %v, want Invalid", err)
	}
	// Tamper negative control: corrupt the schema value (same length) -> refuse.
	tampered := []byte(strings.Replace(string(raw), SelectionSchema, "phebs-typed-module-selection-v0", 1))
	if string(tampered) == string(raw) {
		t.Fatal("tamper control did not change bytes")
	}
	if _, err := DecodeModuleSelection(ctx, tampered); err != typedindex.Invalid {
		t.Fatalf("tampered schema err = %v, want Invalid", err)
	}
	// Tamper: unsort the roots -> validate refuses.
	bad := sel
	bad.Roots = []ModuleRoot{sel.Roots[1], sel.Roots[0]}
	badRaw, _ := bad.Encode()
	if _, err := DecodeModuleSelection(ctx, badRaw); err != typedindex.Invalid {
		t.Fatalf("unsorted roots err = %v, want Invalid", err)
	}
	// Empty and oversize inputs refuse.
	if _, err := DecodeModuleSelection(ctx, nil); err != typedindex.Invalid {
		t.Fatalf("empty err = %v, want Invalid", err)
	}
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestVersionAtMostPinned(t *testing.T) {
	for _, ok := range []string{"1.25", "1.25.0", "1.24", "1.24.3", "1.23", "1.25rc1", "1.9"} {
		if !versionAtMostPinned(ok) {
			t.Fatalf("versionAtMostPinned(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"1.26", "1.26.0", "1.26rc1", "2.0", "2.1.5", "", "abc", "1", "1.x"} {
		if versionAtMostPinned(bad) {
			t.Fatalf("versionAtMostPinned(%q) = true, want false", bad)
		}
	}
	// Overflow guard: an over-long component must refuse, not wrap into a value
	// that falsely compares <= the pinned Go (fail-closed, never a silent widen).
	for _, overflow := range []string{
		"1.99999999999999999999",
		"999999999999.0",
		"1.25.99999999999999999999",
		"1." + strings.Repeat("9", 30),
	} {
		if versionAtMostPinned(overflow) {
			t.Fatalf("versionAtMostPinned(%q) = true, want false (overflow guard)", overflow)
		}
	}
}

func TestIsDigest(t *testing.T) {
	lower := "sha256:" + strings.Repeat("ab0123", 10) + "abcd" // 60 + 4 = 64 lowercase hex
	if len(lower) != len("sha256:")+64 {
		t.Fatalf("test digest length wrong: %d", len(lower))
	}
	if !isDigest(lower) {
		t.Fatalf("isDigest(%q) = false, want true", lower)
	}
	upper := "sha256:" + strings.Repeat("AB0123", 10) + "ABCD"
	for _, bad := range []string{"", "sha256:", lower[:len(lower)-1], upper, "md5:" + strings.Repeat("a", 32), "sha256:" + strings.Repeat("g", 64), "sha256:" + strings.Repeat("a", 63)} {
		if isDigest(bad) {
			t.Fatalf("isDigest(%q) = true, want false", bad)
		}
	}
}

func TestLiteralSelector(t *testing.T) {
	for _, ok := range []string{"example.com/a/b", "./cmd/phebs", "./a", "github.com/x/y"} {
		if !literalSelector(ok) {
			t.Fatalf("literalSelector(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", ".", "./", "./...", "...", "all", "std", "m/*", "/abs", "a\\b", "a\x00b", strings.Repeat("a", MaxPathBytes+1)} {
		if literalSelector(bad) {
			t.Fatalf("literalSelector(%q) = true, want false", bad)
		}
	}
}
