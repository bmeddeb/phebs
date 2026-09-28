// Package typedmodule implements the closed Go-module / go.work managed-input
// provider contract for T45.7. This leaf supplies ONLY the bounded, pure
// discovery closure: it parses one operator-selected entry control file (a single
// root go.mod, or one exact go.work plus every declared use module) from
// caller-supplied immutable control bytes, resolves each module root inside the
// admitted input inventory, and produces a closed ModuleSelection or refuses the
// whole requested scope.
//
// It reads no filesystem, launches no child/Bazel/driver/indexer/scip-go,
// downloads or builds no tool, and registers no provider. It does NOT decide the
// multi-module symbol/metadata contract: proving actual pinned scip-go output-root
// mapping and cross-module symbol identity requires the separately authorized
// native gate, so workspace handling here is scope closure only, never a
// correctness claim. Several boundaries deliberately refuse rather than guess
// (all replace/exclude/retract directives, toolchains newer than the pinned Go);
// they narrow admission and never widen authority.
package typedmodule

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// SelectionSchema versions the closed module discovery result.
const SelectionSchema = "phebs-typed-module-selection-v1"

// Bounds are the leaf-7.1 discovery ceilings. They are new discovery limits, not
// cap increases or validated corpus coverage; a larger valid repository refuses
// with Capacity rather than growing a bound.
const (
	MaxWorkspaceModules      = 4
	MaxControlBytes          = 64 << 10
	MaxAggregateControlBytes = 1 << 20
	MaxPathBytes             = 512
	MaxPackageSelectors      = 512

	GoModName  = "go.mod"
	GoWorkName = "go.work"

	// pinnedGo is the frozen native SDK line. A go/toolchain version above it is
	// unsupported; discovery never selects or downloads another SDK.
	pinnedGoMajor = 1
	pinnedGoMinor = 25
	pinnedGoPatch = 0
)

// Mode distinguishes the two closed discovery entry shapes.
type Mode string

const (
	ModeSingle    Mode = "single-module"
	ModeWorkspace Mode = "workspace"
)

func validMode(m Mode) bool { return m == ModeSingle || m == ModeWorkspace }

// ModuleRoot is one resolved module in the closed set.
type ModuleRoot struct {
	Path   string `json:"path"`   // repo-relative module root dir; "." is the repo root
	Module string `json:"module"` // module path declared by its go.mod
	GoMod  string `json:"go_mod"` // repo-relative go.mod path
	Digest string `json:"digest"` // sha256 of the exact go.mod bytes
}

// ModuleSelection is the admitted discovery result. Roots and Packages are
// strictly ascending and distinct; Controls maps every consumed control file's
// repo-relative path to its sha256 digest.
type ModuleSelection struct {
	Schema   string            `json:"schema"`
	Mode     Mode              `json:"mode"`
	Entry    string            `json:"entry"`
	Roots    []ModuleRoot      `json:"roots"`
	Packages []string          `json:"packages"`
	Controls map[string]string `json:"controls"`
}

// ControlSource supplies the immutable bytes of one repo-relative control file and
// proves it is a regular, non-executable member of the admitted input inventory.
// The run worker binds this to the sandbox inventory reader; tests bind synthetic
// fixtures. Read must refuse absent, non-regular, executable, escaping or
// over-bound paths; discovery additionally re-checks the returned length.
type ControlSource interface {
	Read(ctx context.Context, repoPath string, maxBytes int) ([]byte, error)
}

// Discover resolves the closed module set for one selected entry control file.
// entryPath is the repo-relative go.mod (single mode) or go.work (workspace mode);
// packages are the explicit literal selectors the worker will pass to the indexer.
func Discover(ctx context.Context, src ControlSource, mode Mode, entryPath string, packages []string) (ModuleSelection, error) {
	if err := ctx.Err(); err != nil {
		return ModuleSelection{}, err
	}
	if src == nil || !validMode(mode) || !controlPath(entryPath) {
		return ModuleSelection{}, typedindex.Invalid
	}
	pkgs, err := normalizePackages(packages)
	if err != nil {
		return ModuleSelection{}, err
	}
	base := path.Base(entryPath)
	switch mode {
	case ModeSingle:
		if base != GoModName {
			return ModuleSelection{}, typedindex.Invalid
		}
	case ModeWorkspace:
		if base != GoWorkName {
			return ModuleSelection{}, typedindex.Invalid
		}
	}

	sel := ModuleSelection{Schema: SelectionSchema, Mode: mode, Entry: entryPath, Packages: pkgs, Controls: map[string]string{}}
	var aggregate int
	read := func(p string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b, err := src.Read(ctx, p, MaxControlBytes)
		if err != nil {
			return nil, err
		}
		if len(b) == 0 || len(b) > MaxControlBytes {
			return nil, typedindex.Invalid
		}
		aggregate += len(b)
		if aggregate > MaxAggregateControlBytes {
			return nil, typedindex.Capacity
		}
		sel.Controls[p] = digestBytes(b)
		return b, nil
	}

	switch mode {
	case ModeSingle:
		// A named single-module profile explicitly ignores any sibling go.work.
		root, err := moduleRoot(ctx, read, path.Dir(entryPath), entryPath)
		if err != nil {
			return ModuleSelection{}, err
		}
		sel.Roots = []ModuleRoot{root}
	case ModeWorkspace:
		workRaw, err := read(entryPath)
		if err != nil {
			return ModuleSelection{}, err
		}
		wf, err := modfile.ParseWork(entryPath, workRaw, nil)
		if err != nil || wf == nil {
			return ModuleSelection{}, typedindex.Invalid
		}
		if err := checkWorkVersion(wf.Go, wf.Toolchain); err != nil {
			return ModuleSelection{}, err
		}
		if len(wf.Replace) > 0 {
			return ModuleSelection{}, typedindex.Unsupported
		}
		if len(wf.Use) == 0 || len(wf.Use) > MaxWorkspaceModules {
			return ModuleSelection{}, typedindex.Capacity
		}
		workDir := path.Dir(entryPath)
		seenRoot := map[string]bool{}
		seenModule := map[string]bool{}
		for _, u := range wf.Use {
			if err := ctx.Err(); err != nil {
				return ModuleSelection{}, err
			}
			if u == nil {
				return ModuleSelection{}, typedindex.Invalid
			}
			dir, ok := resolveUse(workDir, u.Path)
			if !ok {
				return ModuleSelection{}, typedindex.Invalid
			}
			if seenRoot[dir] {
				return ModuleSelection{}, typedindex.Invalid
			}
			seenRoot[dir] = true
			goMod := joinGoMod(dir)
			root, err := moduleRoot(ctx, read, dir, goMod)
			if err != nil {
				return ModuleSelection{}, err
			}
			if seenModule[root.Module] {
				return ModuleSelection{}, typedindex.Invalid
			}
			seenModule[root.Module] = true
			sel.Roots = append(sel.Roots, root)
		}
		slices.SortFunc(sel.Roots, func(a, b ModuleRoot) int { return strings.Compare(a.Path, b.Path) })
	}
	return sel, nil
}

// moduleRoot reads and validates one module's go.mod at goMod within dir.
func moduleRoot(ctx context.Context, read func(string) ([]byte, error), dir, goMod string) (ModuleRoot, error) {
	if err := ctx.Err(); err != nil {
		return ModuleRoot{}, err
	}
	if !controlPath(goMod) {
		return ModuleRoot{}, typedindex.Invalid
	}
	raw, err := read(goMod)
	if err != nil {
		return ModuleRoot{}, err
	}
	f, err := modfile.Parse(goMod, raw, nil)
	if err != nil || f == nil || f.Module == nil || f.Module.Mod.Path == "" {
		return ModuleRoot{}, typedindex.Invalid
	}
	// Discovery refuses any directive that alters module-graph or replacement
	// authority; resolving those is a later, separately-proven leaf.
	if len(f.Replace) > 0 || len(f.Exclude) > 0 || len(f.Retract) > 0 {
		return ModuleRoot{}, typedindex.Unsupported
	}
	if err := checkGoVersion(f.Go, f.Toolchain); err != nil {
		return ModuleRoot{}, err
	}
	if !controlPath(dir) && dir != "." {
		return ModuleRoot{}, typedindex.Invalid
	}
	return ModuleRoot{Path: dir, Module: f.Module.Mod.Path, GoMod: goMod, Digest: digestBytes(raw)}, nil
}

func checkGoVersion(g *modfile.Go, tc *modfile.Toolchain) error {
	if g != nil && g.Version != "" && !versionAtMostPinned(g.Version) {
		return typedindex.Unsupported
	}
	if tc != nil && tc.Name != "" && !versionAtMostPinned(strings.TrimPrefix(tc.Name, "go")) {
		return typedindex.Unsupported
	}
	return nil
}

func checkWorkVersion(g *modfile.Go, tc *modfile.Toolchain) error { return checkGoVersion(g, tc) }

// versionAtMostPinned reports whether a leading major.minor[.patch] in v is <= the
// pinned Go line. An unparseable or newer version refuses (returns false).
func versionAtMostPinned(v string) bool {
	major, minor, patch, ok := parseVersion(v)
	if !ok {
		return false
	}
	if major != pinnedGoMajor {
		return major < pinnedGoMajor
	}
	if minor != pinnedGoMinor {
		return minor < pinnedGoMinor
	}
	return patch <= pinnedGoPatch
}

func parseVersion(v string) (major, minor, patch int, ok bool) {
	// num parses a bounded decimal component. It rejects empty strings and any
	// run longer than maxDigits, so a crafted over-long component can neither
	// overflow int nor wrap into a value that falsely compares <= the pinned Go.
	const maxDigits = 6
	num := func(s string) (int, bool) {
		if s == "" || len(s) > maxDigits {
			return 0, false
		}
		n := 0
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int(c-'0')
		}
		return n, true
	}
	// Take the leading dotted-numeric portion; tolerate a suffix like "rc1".
	fields := strings.Split(v, ".")
	if len(fields) < 2 || len(fields) > 3 {
		return 0, 0, 0, false
	}
	trim := func(s string) string {
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return s[:i]
	}
	var good bool
	major, good = num(fields[0])
	if !good {
		return 0, 0, 0, false
	}
	minorStr := trim(fields[1])
	if minor, good = num(minorStr); !good {
		return 0, 0, 0, false
	}
	patch = 0
	if len(fields) == 3 {
		patchStr := trim(fields[2])
		if patchStr != "" {
			if patch, good = num(patchStr); !good {
				return 0, 0, 0, false
			}
		}
	}
	return major, minor, patch, true
}

// resolveUse resolves a go.work use path (relative to the workfile directory) to a
// repo-relative module directory. It refuses absolute paths, any ".." escape, and
// anything that is not a valid bounded repo path.
func resolveUse(workDir, use string) (string, bool) {
	if use == "" || strings.HasPrefix(use, "/") {
		return "", false
	}
	for _, seg := range strings.Split(use, "/") {
		if seg == ".." {
			return "", false
		}
	}
	joined := path.Join(workDir, use)
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", false
	}
	if !repoDir(joined) {
		return "", false
	}
	return joined, true
}

func joinGoMod(dir string) string {
	if dir == "." {
		return GoModName
	}
	return dir + "/" + GoModName
}

func normalizePackages(packages []string) ([]string, error) {
	if len(packages) == 0 || len(packages) > MaxPackageSelectors {
		return nil, typedindex.Invalid
	}
	out := make([]string, 0, len(packages))
	seen := map[string]bool{}
	for _, p := range packages {
		if !literalSelector(p) || seen[p] {
			return nil, typedindex.Invalid
		}
		seen[p] = true
		out = append(out, p)
	}
	slices.Sort(out)
	return out, nil
}

// literalSelector accepts only an explicit literal package selector: a valid
// import path, or a repo-relative "./dir" path. It refuses wildcards, recursive
// "./..." patterns, the all/std defaults, absolutes and empty/oversized values, so
// no owned invocation can silently expand to a recursive default.
func literalSelector(s string) bool {
	if s == "" || len(s) > MaxPathBytes || strings.ContainsAny(s, "*\\\x00") {
		return false
	}
	if strings.Contains(s, "...") {
		return false
	}
	switch s {
	case ".", "./", "all", "std", "./...", "..":
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if strings.HasPrefix(s, "/") {
		return false
	}
	if strings.HasPrefix(s, "./") {
		rest := s[2:]
		return rest != "" && fs.ValidPath(rest)
	}
	return module.CheckImportPath(s) == nil
}

// controlPath validates a repo-relative control-file path with the same grammar as
// the inventory bundle paths: a valid, bounded, non-escaping path with no
// backslash/colon/control bytes and no over-long component. "." alone is rejected.
func controlPath(s string) bool {
	if s == "." || s == "" || len(s) > MaxPathBytes || !fs.ValidPath(s) || strings.ContainsAny(s, "\\:") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, c := range strings.Split(s, "/") {
		if c == "" || c == ".." || len(c) > 255 {
			return false
		}
	}
	return true
}

// repoDir validates a repo-relative directory path ("." allowed).
func repoDir(s string) bool {
	if s == "." {
		return true
	}
	return controlPath(s)
}

func digestBytes(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// Encode returns the canonical JSON bytes of the selection.
func (s ModuleSelection) Encode() ([]byte, error) { return json.Marshal(s) }

// Digest is the sha256 identity of the canonical encoding, or "" if unencodable.
func (s ModuleSelection) Digest() string {
	b, err := s.Encode()
	if err != nil {
		return ""
	}
	return digestBytes(b)
}

// DecodeModuleSelection strictly decodes canonical bytes and re-validates the
// closed invariants, so a stored or transported selection cannot drift from one
// Discover produced.
func DecodeModuleSelection(ctx context.Context, raw []byte) (ModuleSelection, error) {
	if err := ctx.Err(); err != nil {
		return ModuleSelection{}, err
	}
	if len(raw) == 0 || len(raw) > MaxAggregateControlBytes {
		return ModuleSelection{}, typedindex.Invalid
	}
	var s ModuleSelection
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&s) != nil {
		return ModuleSelection{}, typedindex.Invalid
	}
	want, err := json.Marshal(s)
	if err != nil {
		return ModuleSelection{}, typedindex.Invalid
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || !bytes.Equal(compact.Bytes(), want) {
		return ModuleSelection{}, typedindex.Invalid
	}
	if err := s.validate(); err != nil {
		return ModuleSelection{}, err
	}
	return s, nil
}

func (s ModuleSelection) validate() error {
	if s.Schema != SelectionSchema || !validMode(s.Mode) || !controlPath(s.Entry) {
		return typedindex.Invalid
	}
	base := path.Base(s.Entry)
	if s.Mode == ModeSingle && base != GoModName || s.Mode == ModeWorkspace && base != GoWorkName {
		return typedindex.Invalid
	}
	if len(s.Roots) == 0 || len(s.Roots) > MaxWorkspaceModules {
		return typedindex.Capacity
	}
	seenRoot, seenModule := map[string]bool{}, map[string]bool{}
	prev := ""
	expected := map[string]bool{}
	if s.Mode == ModeWorkspace {
		expected[s.Entry] = true
	}
	for _, r := range s.Roots {
		if !repoDir(r.Path) || r.Path <= prev || r.Module == "" || !controlPath(r.GoMod) || !isDigest(r.Digest) {
			return typedindex.Invalid
		}
		if joinGoMod(r.Path) != r.GoMod || seenRoot[r.Path] || seenModule[r.Module] {
			return typedindex.Invalid
		}
		seenRoot[r.Path] = true
		seenModule[r.Module] = true
		expected[r.GoMod] = true
		prev = r.Path
	}
	if s.Mode == ModeSingle && s.Roots[0].GoMod != s.Entry {
		return typedindex.Invalid
	}
	pkgs, err := normalizePackages(s.Packages)
	if err != nil || !slices.Equal(pkgs, s.Packages) {
		return typedindex.Invalid
	}
	if len(s.Controls) != len(expected) {
		return typedindex.Invalid
	}
	for p, d := range s.Controls {
		if !expected[p] || !isDigest(d) {
			return typedindex.Invalid
		}
	}
	return nil
}

func isDigest(s string) bool {
	if !strings.HasPrefix(s, "sha256:") {
		return false
	}
	hexPart := strings.TrimPrefix(s, "sha256:")
	if len(hexPart) != 64 {
		return false
	}
	// Lowercase-only, matching the frozen typedindex digest grammar; digestBytes
	// always emits lowercase, so an uppercase variant is a non-canonical refusal.
	for _, c := range hexPart {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
