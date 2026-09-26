package planner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Artifact paths come only from the pinned aspect's Bazel File providers.
type Artifact struct {
	Path      string `json:"path"`
	ShortPath string `json:"short_path"`
	Owner     string `json:"owner"`
	Source    bool   `json:"source"`
	Tree      bool   `json:"tree"`
}

type Import struct {
	Path   string `json:"path"`
	Owner  string `json:"owner"`
	Export string `json:"export"`
}

type ArchiveRef struct {
	Owner  string `json:"owner"`
	Export string `json:"export"`
}

type GoMode struct {
	GOOS   string   `json:"goos"`
	GOARCH string   `json:"goarch"`
	Cgo    bool     `json:"cgo"`
	Tags   []string `json:"tags"`
}

type Archive struct {
	Name         string     `json:"name"`
	Label        string     `json:"label"`
	Export       string     `json:"export"`
	ImportPath   string     `json:"import_path"`
	ImportMap    string     `json:"import_map"`
	GOOS         string     `json:"goos"`
	GOARCH       string     `json:"goarch"`
	Tags         []string   `json:"tags"`
	Cgo          bool       `json:"cgo"`
	TestFilter   string     `json:"test_filter"`
	Sources      []Artifact `json:"sources"`
	CgoDirectory *Artifact  `json:"cgo_directory,omitempty"`
	Imports      []Import   `json:"imports"`
	Stdlib       *SDKRef    `json:"stdlib,omitempty"`
}

type helperInput struct {
	Version  string      `json:"version"`
	Owner    string      `json:"owner"`
	Roots    []string    `json:"roots"`
	Embeds   []string    `json:"embeds"`
	Archives []Archive   `json:"archives"`
	SDK      *SDKInput   `json:"sdk,omitempty"`
	Forward  *ArchiveRef `json:"forward,omitempty"`
}

type File struct {
	Artifact
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

type projectedArchive struct {
	Name            string   `json:"name"`
	Label           string   `json:"label"`
	Export          string   `json:"export"`
	ImportPath      string   `json:"import_path"`
	ImportMap       string   `json:"import_map"`
	GoFiles         []File   `json:"go_files"`
	CompiledGoFiles []File   `json:"compiled_go_files"`
	Imports         []Import `json:"imports"`
	Stdlib          *SDKRef  `json:"stdlib,omitempty"`
	PackageName     string   `json:"package_name"`
	SourceImports   []string `json:"source_imports"`
	Mode            GoMode   `json:"mode"`
}

type projection struct {
	Version  string             `json:"version"`
	Owner    string             `json:"owner"`
	Roots    []string           `json:"roots"`
	Embeds   []string           `json:"embeds"`
	Archives []projectedArchive `json:"archives"`
	SDK      *SDKProjection     `json:"sdk,omitempty"`
	Forward  *ArchiveRef        `json:"forward,omitempty"`
}

func strictJSON(data []byte, dst any) error {
	if len(data) == 0 || len(data) > MaxProjectionBytes {
		return errors.New("projection byte limit")
	}
	// encoding/json normally accepts repeated object members. Reject them at
	// this authority boundary before its typed decoder sees the object.
	d := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return errors.New("JSON nesting limit")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return errors.New("duplicate JSON member")
				}
				seen[key] = true
				if e = visit(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e := visit(depth + 1); e != nil {
					return e
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	if err := ValidateJSONFields(data, dst); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	return nil
}

func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func boundedFile(name string, limit int) ([]byte, error) {
	if !relative(name) {
		return nil, errors.New("invalid declared artifact path")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open declared artifact: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat declared artifact: %w", err)
	}
	if !st.Mode().IsRegular() || st.Size() > int64(limit) {
		return nil, errors.New("declared artifact is not bounded regular data")
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("read declared artifact: %w", err)
	}
	if len(b) > limit {
		return nil, errors.New("declared artifact byte limit")
	}
	return b, nil
}

// RunHelper is the pinned runner's __plan_helper subcommand. Bazel invokes it
// with an owned input manifest and output path relative to its execution root.
// It never enumerates a repository: cgo membership is selected by exact names
// within the one explicitly declared cgo tree artifact.
func RunHelper(args []string) error {
	if len(args) != 2 || !relative(args[0]) || !relative(args[1]) || !strings.HasSuffix(args[1], ".phebs-plan.json") {
		return errors.New("invalid planner helper argv")
	}
	data, err := boundedFile(args[0], MaxProjectionBytes)
	if err != nil {
		return err
	}
	out, err := project(data, boundedFile)
	if err != nil {
		return err
	}
	if err = os.WriteFile(args[1], out, 0600); err != nil {
		return fmt.Errorf("write projection: %w", err)
	}
	return nil
}

func project(data []byte, read func(string, int) ([]byte, error)) ([]byte, error) {
	var in helperInput
	if err := strictJSON(data, &in); err != nil {
		return nil, err
	}
	if in.Version != "phebs-t451a-projection-input-v1" || !label(in.Owner) || len(in.Archives) > 64 || len(in.Embeds) > 256 || len(in.Roots) > 64 {
		return nil, errors.New("invalid planner input")
	}
	out := projection{Version: "phebs-t451a-projection-v1", Owner: canonicalLabel(in.Owner), Roots: in.Roots, Embeds: in.Embeds, Archives: []projectedArchive{}}
	if in.Forward != nil {
		if in.SDK != nil || len(in.Archives) != 0 || len(in.Roots) != 0 || len(in.Embeds) != 0 || !label(in.Forward.Owner) || canonicalLabel(in.Forward.Owner) == out.Owner || !relative(in.Forward.Export) || !strings.HasPrefix(in.Forward.Export, "bazel-out/") {
			return nil, errors.New("invalid forwarded archive projection")
		}
		out.Forward = &ArchiveRef{Owner: canonicalLabel(in.Forward.Owner), Export: in.Forward.Export}
		return marshalProjection(out)
	}
	if in.SDK != nil {
		if len(in.Archives) != 0 || len(in.Roots) != 0 || len(in.Embeds) != 0 {
			return nil, errors.New("mixed SDK/archive projection")
		}
		sdk, err := projectSDK(*in.SDK, read)
		if err != nil {
			return nil, err
		}
		out.SDK = &sdk
		return marshalProjection(out)
	}
	cache := map[string]File{}
	contents := map[string][]byte{}
	total := 0
	load := func(a Artifact) (File, []byte, error) {
		if !relative(a.Path) || a.Tree || !label(a.Owner) || !strings.HasSuffix(a.Path, ".go") {
			return File{}, nil, errors.New("invalid Go artifact")
		}
		a.Owner = canonicalLabel(a.Owner)
		if f, ok := cache[a.Path]; ok {
			if f.Artifact != a {
				return File{}, nil, errors.New("conflicting artifact identity")
			}
			return f, contents[a.Path], nil
		}
		if len(cache) >= MaxDocuments {
			return File{}, nil, errors.New("projection document limit")
		}
		b, err := read(a.Path, MaxFileBytes)
		if err != nil {
			return File{}, nil, err
		}
		total += len(b)
		if total > 64<<20 {
			return File{}, nil, errors.New("projection source byte limit")
		}
		f := File{a, digest(b), len(b)}
		cache[a.Path] = f
		contents[a.Path] = b
		return f, b, nil
	}
	for _, a := range in.Archives {
		if a.Name == "" || !label(a.Label) || !relative(a.Export) || a.ImportPath == "" || len(a.Sources) > MaxDocuments || len(a.Imports) > MaxUnits || len(a.Tags) > 64 {
			return nil, errors.New("invalid archive identity")
		}
		if a.GOOS != "linux" || a.GOARCH != "arm64" {
			return nil, errors.New("unsupported neutral archive mode")
		}
		if a.TestFilter != "" && a.TestFilter != "off" && a.TestFilter != "only" && a.TestFilter != "exclude" {
			return nil, errors.New("unknown test source filter")
		}
		p := projectedArchive{Name: a.Name, Label: canonicalLabel(a.Label), Export: a.Export, ImportPath: a.ImportPath, ImportMap: a.ImportMap, GoFiles: []File{}, CompiledGoFiles: []File{}, Imports: a.Imports, Stdlib: a.Stdlib, SourceImports: []string{}}
		p.Mode = GoMode{GOOS: a.GOOS, GOARCH: a.GOARCH, Cgo: a.Cgo, Tags: append([]string{}, a.Tags...)}
		sort.Strings(p.Mode.Tags)
		// Match the sealed Go 1.25.0 linux/arm64 SDK, independently of the
		// version and architecture used to compile this helper. The profile
		// does not expose GOEXPERIMENT or GOARM64 overrides.
		releaseTags := make([]string, 25)
		for i := range releaseTags {
			releaseTags[i] = fmt.Sprintf("go1.%d", i+1)
		}
		bctx := build.Context{GOOS: a.GOOS, GOARCH: a.GOARCH, CgoEnabled: a.Cgo, Compiler: "gc", BuildTags: a.Tags, ReleaseTags: releaseTags, ToolTags: []string{"arm64.v8.0", "goexperiment.regabiwrappers", "goexperiment.regabiargs", "goexperiment.aliastypeparams", "goexperiment.swissmap", "goexperiment.synchashtriemap", "goexperiment.dwarf5"}}
		bctx.OpenFile = func(name string) (io.ReadCloser, error) {
			b, ok := contents[filepath.ToSlash(name)]
			if !ok {
				return nil, errors.New("build constraint requested an undeclared source")
			}
			return io.NopCloser(bytes.NewReader(b)), nil
		}
		var cgoSources []Artifact
		seen := map[string]bool{}
		for _, src := range a.Sources {
			if !strings.HasSuffix(src.Path, ".go") {
				continue
			}
			if seen[src.Path] {
				return nil, errors.New("duplicate declared Go source")
			}
			seen[src.Path] = true
			f, b, err := load(src)
			if err != nil {
				return nil, err
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), src.Path, b, parser.ImportsOnly)
			if err != nil {
				return nil, fmt.Errorf("parse declared Go source: %w", err)
			}
			isTest := strings.HasSuffix(parsed.Name.Name, "_test")
			if (a.TestFilter == "only" && !isTest) || (a.TestFilter == "exclude" && isTest) {
				continue
			}
			match, err := bctx.MatchFile(filepath.Dir(src.Path), filepath.Base(src.Path))
			if err != nil {
				return nil, fmt.Errorf("source build constraint: %w", err)
			}
			// GoFiles records declared sources even when the active mode filters
			// them; CompiledGoFiles is the exact active input to the type checker.
			p.GoFiles = append(p.GoFiles, f)
			if !match {
				continue
			}
			isCgo := false
			for _, im := range parsed.Imports {
				v, e := strconv.Unquote(im.Path.Value)
				if e != nil {
					return nil, e
				}
				if v == "C" {
					isCgo = true
				}
			}
			if isCgo {
				if a.Cgo {
					cgoSources = append(cgoSources, src)
				}
				continue
			}
			p.CompiledGoFiles = append(p.CompiledGoFiles, f)
		}
		if len(cgoSources) > 0 {
			if a.CgoDirectory == nil || !a.CgoDirectory.Tree || !relative(a.CgoDirectory.Path) || !label(a.CgoDirectory.Owner) {
				return nil, errors.New("missing declared cgo tree")
			}
			names := []string{"_cgo_gotypes.go", "_cgo_imports.go"}
			for _, src := range cgoSources {
				names = append(names, strings.TrimSuffix(filepath.Base(src.Path), ".go")+".cgo1.go")
			}
			for _, name := range names {
				src := Artifact{Path: a.CgoDirectory.Path + "/" + name, ShortPath: a.CgoDirectory.ShortPath + "/" + name, Owner: a.CgoDirectory.Owner}
				f, _, err := load(src)
				if err != nil {
					return nil, fmt.Errorf("required cgo output: %w", err)
				}
				p.CompiledGoFiles = append(p.CompiledGoFiles, f)
			}
		}
		sort.Slice(p.GoFiles, func(i, j int) bool { return p.GoFiles[i].Path < p.GoFiles[j].Path })
		sort.Slice(p.CompiledGoFiles, func(i, j int) bool { return p.CompiledGoFiles[i].Path < p.CompiledGoFiles[j].Path })
		sort.Slice(p.Imports, func(i, j int) bool { return p.Imports[i].Path < p.Imports[j].Path })
		for _, f := range p.CompiledGoFiles {
			parsed, err := parser.ParseFile(token.NewFileSet(), f.Path, contents[f.Path], parser.ImportsOnly)
			if err != nil {
				return nil, fmt.Errorf("parse compiled source: %w", err)
			}
			if p.PackageName != "" && p.PackageName != parsed.Name.Name {
				return nil, errors.New("mixed compiled package names")
			}
			p.PackageName = parsed.Name.Name
			for _, im := range parsed.Imports {
				name, err := strconv.Unquote(im.Path.Value)
				if err != nil || name == "C" {
					return nil, errors.New("unresolved compiled source import")
				}
				p.SourceImports = append(p.SourceImports, name)
			}
		}
		p.SourceImports = unique(p.SourceImports)
		out.Archives = append(out.Archives, p)
	}
	sort.Strings(out.Roots)
	sort.Strings(out.Embeds)
	sort.Slice(out.Archives, func(i, j int) bool { return out.Archives[i].Export < out.Archives[j].Export })
	return marshalProjection(out)
}

func marshalProjection(out projection) ([]byte, error) {
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxProjectionBytes {
		return nil, errors.New("projection output byte limit")
	}
	return append(b, '\n'), nil
}
