package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"path"
	"sort"
	"strings"
)

const MaxSDKPackages = 1024

// SDKRef is the exact GoStdLib artifact consumed by a configured GoArchive.
type SDKRef struct {
	Owner string `json:"owner"`
	List  string `json:"list"`
}

// SDKInput is emitted by the owned aspect, not accepted from a driver. Sources
// are individual GoSDK File paths; cache directories are declared tree artifacts.
type SDKInput struct {
	Mode    GoMode     `json:"mode"`
	Version string     `json:"version"`
	Root    string     `json:"root"`
	Prefix  string     `json:"prefix"`
	List    Artifact   `json:"list"`
	Caches  []Artifact `json:"caches"`
	Sources []string   `json:"sources"`
}

type SDKPackage struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	ImportPath      string            `json:"import_path"`
	GoFiles         []string          `json:"go_files"`
	CompiledGoFiles []string          `json:"compiled_go_files"`
	ExportFile      string            `json:"export_file"`
	Imports         map[string]string `json:"imports"`
}

type SDKDocument struct {
	Path      string `json:"path"`
	ExecPath  string `json:"exec_path"`
	Generated bool   `json:"generated"`
	SHA256    string `json:"sha256"`
	Bytes     int    `json:"bytes"`
}

// SDKProjection is a separate toolchain lane. These documents never count as
// repository coverage. Its producer is the pinned rules_go GoStdlibList action;
// shared producer code with the driver is explicit, not independent rederivation.
type SDKProjection struct {
	Mode       GoMode        `json:"mode"`
	Version    string        `json:"version"`
	Root       string        `json:"root"`
	Prefix     string        `json:"prefix"`
	List       Artifact      `json:"list"`
	ListSHA256 string        `json:"list_sha256"`
	Caches     []Artifact    `json:"caches"`
	Packages   []SDKPackage  `json:"packages"`
	Documents  []SDKDocument `json:"documents"`
}

type SDKPlan struct {
	ID    string     `json:"id"`
	Owner Configured `json:"owner"`
	SDKProjection
}

type sdkListPackage struct {
	ID, Name, PkgPath string
	Standard          bool
	Errors            []json.RawMessage
	GoFiles           []string
	CompiledGoFiles   []string
	OtherFiles        []string
	ExportFile        string
	Imports           map[string]string
}

func sdkPath(raw string) (string, error) {
	var name string
	if suffix, ok := strings.CutPrefix(raw, "__BAZEL_OUTPUT_BASE__/"); ok {
		name = suffix
	} else if suffix, ok := strings.CutPrefix(raw, "__BAZEL_EXECROOT__/"); ok {
		name = suffix
	} else {
		return "", errors.New("SDK file has no declared Bazel location")
	}
	if !relative(name) {
		return "", errors.New("invalid SDK file location")
	}
	return name, nil
}

func sdkLocation(s SDKProjection, name string) (string, bool, error) {
	if p, ok := strings.CutPrefix(name, s.Root+"/src/"); ok && relative(p) && strings.HasSuffix(p, ".go") {
		return "src/" + p, false, nil
	}
	for i, tree := range s.Caches {
		if p, ok := strings.CutPrefix(name, tree.Path+"/"); ok && relative(p) {
			return fmt.Sprintf("cache/%d/%s", i, p), true, nil
		}
	}
	return "", false, errors.New("SDK file outside declared source/cache artifacts")
}

func projectSDK(in SDKInput, read func(string, int) ([]byte, error)) (SDKProjection, error) {
	out := SDKProjection{Mode: in.Mode, Version: in.Version, Root: in.Root, Prefix: in.Prefix, List: in.List, Caches: in.Caches, Packages: []SDKPackage{}, Documents: []SDKDocument{}}
	sort.Strings(out.Mode.Tags)
	if err := sdkMetadata(out); err != nil {
		return SDKProjection{}, err
	}
	if len(in.Sources) == 0 || len(in.Sources) > MaxDocuments {
		return SDKProjection{}, errors.New("SDK source declaration count")
	}
	sources := map[string]bool{}
	for _, name := range in.Sources {
		if !relative(name) || !strings.HasPrefix(name, in.Root+"/src/") || !strings.HasSuffix(name, ".go") || sources[name] {
			return SDKProjection{}, errors.New("invalid declared SDK source")
		}
		sources[name] = true
	}
	data, err := read(in.List.Path, MaxProjectionBytes)
	if err != nil {
		return SDKProjection{}, fmt.Errorf("read declared SDK list: %w", err)
	}
	out.ListSHA256 = digest(data)
	docs := map[string]SDKDocument{}
	packageNames := map[string]string{}
	total := len(data)
	addFile := func(raw string) (string, error) {
		name, err := sdkPath(raw)
		if err != nil {
			return "", err
		}
		if _, ok := docs[name]; ok {
			return name, nil
		}
		canonical, generated, err := sdkLocation(out, name)
		if err != nil {
			return "", fmt.Errorf("SDK list location %q outside declared root %q/cache: %w", name, out.Root, err)
		}
		if !generated && !sources[name] {
			return "", fmt.Errorf("SDK list names undeclared source %q", name)
		}
		b, err := read(name, MaxFileBytes)
		if err != nil {
			return "", fmt.Errorf("read declared SDK document: %w", err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, b, parser.ImportsOnly)
		if err != nil {
			return "", fmt.Errorf("parse declared SDK document: %w", err)
		}
		packageNames[name] = parsed.Name.Name
		total += len(b)
		if total > MaxProtoBytes || len(docs) >= MaxDocuments {
			return "", errors.New("SDK document byte/count bound")
		}
		docs[name] = SDKDocument{Path: canonical, ExecPath: name, Generated: generated, SHA256: digest(b), Bytes: len(b)}
		return name, nil
	}
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var raw json.RawMessage
		if err := d.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			return SDKProjection{}, fmt.Errorf("SDK list framing: %w", err)
		}
		if len(out.Packages) >= MaxSDKPackages {
			return SDKProjection{}, errors.New("SDK package bound")
		}
		var p sdkListPackage
		if err := strictJSON(raw, &p); err != nil {
			return SDKProjection{}, err
		}
		if !p.Standard || len(p.Errors) != 0 || len(p.OtherFiles) != 0 {
			return SDKProjection{}, errors.New("non-standard or failed SDK package")
		}
		pkg := SDKPackage{ID: p.ID, Name: p.Name, ImportPath: p.PkgPath, Imports: p.Imports, GoFiles: []string{}, CompiledGoFiles: []string{}}
		for index, files := range [][]string{p.GoFiles, p.CompiledGoFiles} {
			for _, raw := range files {
				name, err := addFile(raw)
				if err != nil {
					return SDKProjection{}, err
				}
				if packageNames[name] != p.Name {
					return SDKProjection{}, errors.New("SDK package/source name mismatch")
				}
				if index == 0 {
					pkg.GoFiles = append(pkg.GoFiles, name)
				} else {
					pkg.CompiledGoFiles = append(pkg.CompiledGoFiles, name)
				}
			}
		}
		// The pinned registry makes this same fallback before tag selection.
		if len(pkg.CompiledGoFiles) == 0 {
			pkg.CompiledGoFiles = append(pkg.CompiledGoFiles, pkg.GoFiles...)
		}
		if p.ExportFile != "" {
			pkg.ExportFile, err = sdkPath(p.ExportFile)
			if err != nil {
				return SDKProjection{}, err
			}
			if _, generated, err := sdkLocation(out, pkg.ExportFile); err != nil || !generated {
				return SDKProjection{}, errors.New("SDK export outside cache artifact")
			}
		}
		sort.Strings(pkg.GoFiles)
		sort.Strings(pkg.CompiledGoFiles)
		out.Packages = append(out.Packages, pkg)
	}
	for _, doc := range docs {
		out.Documents = append(out.Documents, doc)
	}
	sort.Slice(out.Documents, func(i, j int) bool { return out.Documents[i].ExecPath < out.Documents[j].ExecPath })
	sort.Slice(out.Packages, func(i, j int) bool { return out.Packages[i].ID < out.Packages[j].ID })
	if err := validateSDK(out); err != nil {
		return SDKProjection{}, err
	}
	return out, nil
}

func sdkMetadata(s SDKProjection) error {
	if s.Mode.GOOS != "linux" || s.Mode.GOARCH != "arm64" || len(s.Mode.Tags) > 64 {
		return errors.New("SDK mode outside pinned profile")
	}
	if s.Version != "1.25.0" || !relative(s.Root) || !strings.HasPrefix(s.Root, "external/") || !strings.HasSuffix(s.Prefix, "//stdlib:") || !strings.HasPrefix(s.Prefix, "@@") || !relative(s.List.Path) || !strings.HasPrefix(s.List.Path, "bazel-out/") || s.List.Source || s.List.Tree || !label(s.List.Owner) || len(s.Caches) != 1 {
		return errors.New("invalid pinned SDK metadata")
	}
	for _, tree := range s.Caches {
		if !tree.Tree || tree.Source || !relative(tree.Path) || !strings.HasPrefix(tree.Path, "bazel-out/") || canonicalLabel(tree.Owner) != canonicalLabel(s.List.Owner) {
			return errors.New("invalid SDK cache artifact")
		}
	}
	return nil
}

func validateSDK(s SDKProjection) error {
	if err := sdkMetadata(s); err != nil {
		return err
	}
	if !checksum(s.ListSHA256) || len(s.Packages) == 0 || len(s.Packages) > MaxSDKPackages || len(s.Documents) > MaxDocuments {
		return errors.New("invalid SDK projection bounds")
	}
	docs := map[string]bool{}
	for _, d := range s.Documents {
		canonical, generated, err := sdkLocation(s, d.ExecPath)
		if err != nil || d.Path != canonical || d.Generated != generated || !checksum(d.SHA256) || d.Bytes < 0 || d.Bytes > MaxFileBytes || docs[d.ExecPath] {
			return errors.New("invalid SDK document identity")
		}
		docs[d.ExecPath] = true
	}
	packages := map[string]bool{}
	used := map[string]bool{}
	for _, p := range s.Packages {
		if p.ID != s.Prefix+p.ImportPath || !relative(p.ImportPath) || strings.Contains(p.ImportPath, ":") || path.Base(p.Name) != p.Name || p.Name == "" || packages[p.ID] || len(p.Imports) > MaxSDKPackages {
			return errors.New("invalid SDK package identity")
		}
		packages[p.ID] = true
		for _, list := range [][]string{p.GoFiles, p.CompiledGoFiles} {
			seen := map[string]bool{}
			for _, f := range list {
				if !docs[f] || seen[f] {
					return errors.New("missing or duplicate SDK package document")
				}
				seen[f], used[f] = true, true
			}
		}
	}
	for _, p := range s.Packages {
		for name, id := range p.Imports {
			if name == "" || len(name) > 4096 || !packages[id] {
				return errors.New("missing SDK package import")
			}
		}
	}
	if len(used) != len(docs) {
		return errors.New("extra SDK document outside packages")
	}
	return nil
}
