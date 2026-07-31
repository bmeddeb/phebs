package extract_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

var extractorImportAllowlist = map[string]bool{
	"bytes":         true,
	"context":       true,
	"crypto/sha1":   true,
	"crypto/sha256": true,
	"encoding/hex":  true,
	"encoding/json": true,
	"errors":        true,
	"fmt":           true,
	"path":          true,
	// reflect is allowed only for reflect.StructTag parsing — pure string
	// computation, no reflection over live values.
	"reflect":      true,
	"sort":         true,
	"strconv":      true,
	"strings":      true,
	"unicode":      true,
	"unicode/utf8": true,

	// In-process stdlib Go parsing for the T13.1 grpcgo and T13.2 generated
	// accessor extractors. Parsing is pure computation over the supplied blob:
	// no exec, no filesystem, no network, no dynamic loading.
	"go/ast":    true,
	"go/parser": true,
	"go/token":  true,
	"regexp":    true,

	"github.com/bufbuild/protocompile/ast":       true,
	"github.com/bufbuild/protocompile/parser":    true,
	"github.com/bufbuild/protocompile/reporter":  true,
	"github.com/scip-code/scip/bindings/go/scip": true,

	// In-process Thrift IDL parsing for the T19.2 thriftdecl extractor.
	// Parser-only packages: no codegen, no I/O, no process execution.
	"go.uber.org/thriftrw/ast": true,
	"go.uber.org/thriftrw/idl": true,

	"github.com/bmeddeb/phebs/internal/extract/scipsource": true,
	"github.com/bmeddeb/phebs/internal/extract/sdk":        true,
	"github.com/bmeddeb/phebs/internal/idlpreflight":       true,
	"github.com/bmeddeb/phebs/internal/resolverinput":      true,
}

var sdkImportAllowlist = map[string]bool{"context": true}
var scipSourceImportAllowlist = map[string]bool{"strings": true}

var idlPreflightImportAllowlist = map[string]bool{
	"context": true,
	"errors":  true,
	"fmt":     true,
}

var resolverInputImportAllowlist = map[string]bool{
	"encoding/json": true,
	"errors":        true,
	"io":            true,
	"path":          true,
	"strconv":       true,
	"strings":       true,
	"unicode/utf8":  true,
	// Pinned pure import-path validation; no filesystem/toolchain capability.
	"golang.org/x/mod/module": true,
}

type pureSourceTarget struct {
	dir     string
	allowed map[string]bool
}

// TestPureReaderImports is allowlist-based: an extractor cannot bypass a
// direct os/exec/net ban by importing an internal helper or a new third-party
// capability wrapper. All production Go files recursively below extractors/
// are checked, as is the capability-neutral SDK they receive.
func TestPureReaderImports(t *testing.T) {
	violations, err := scanPureSources(os.DirFS("."), ".")
	if err != nil {
		t.Fatalf("scan pure-reader sources: %v", err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// The shared IDL helper admitted above is itself recursively allowlisted, so
// moving the lexical preflight does not create a transitive filesystem,
// network, process, or asynchronous-work escape hatch.
func TestIDLPreflightImports(t *testing.T) {
	violations, err := scanPureTargets(
		os.DirFS(".."),
		[]pureSourceTarget{{
			dir:     "idlpreflight",
			allowed: idlPreflightImportAllowlist,
		}},
	)
	if err != nil {
		t.Fatalf("scan IDL preflight sources: %v", err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// Resolver inputs are shared with catalog materialization, but extractor use
// remains pure computation over supplied blob content. Recursively constrain
// the helper so it cannot become a capability bypass.
func TestResolverInputImports(t *testing.T) {
	violations, err := scanPureTargets(
		os.DirFS(".."),
		[]pureSourceTarget{{
			dir:     "resolverinput",
			allowed: resolverInputImportAllowlist,
		}},
	)
	if err != nil {
		t.Fatalf("scan resolver inputs: %v", err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// The SCIP source-path helper is shared by candidate planning and pure
// readers. Keep its transitive capability surface limited to string matching.
func TestSCIPSourcePolicyImports(t *testing.T) {
	violations, err := scanPureTargets(
		os.DirFS("."),
		[]pureSourceTarget{{
			dir:     "scipsource",
			allowed: scipSourceImportAllowlist,
		}},
	)
	if err != nil {
		t.Fatalf("scan SCIP source policy: %v", err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

// Regression: the former denylist accepted this transitive bypass because the
// extractor itself did not import os/exec. The allowlist rejects the internal
// wrapper import before the helper can expose its capable function.
func TestPureReaderRejectsInternalCapabilityBypass(t *testing.T) {
	fixture := fstest.MapFS{
		"extractors/bypass/bypass.go": &fstest.MapFile{Data: []byte(`package bypass
import (
    "context"
    "github.com/bmeddeb/phebs/internal/sync"
)
var _ = context.Background
var _ = sync.CatFile
`)},
		"sdk/sdk.go": &fstest.MapFile{Data: []byte("package sdk\nimport \"context\"\nvar _ context.Context\n")},
	}
	violations, err := scanPureSources(fixture, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0], "internal/sync") {
		t.Fatalf("violations = %v, want internal capability bypass", violations)
	}
}

func TestPureReaderRejectsLinkedProductionArtifact(t *testing.T) {
	fixture := fstest.MapFS{
		"extractors/native/native.go":   &fstest.MapFile{Data: []byte("package native\n")},
		"extractors/native/bypass.syso": &fstest.MapFile{Data: []byte("native object")},
		"sdk/sdk.go":                    &fstest.MapFile{Data: []byte("package sdk\nimport \"context\"\nvar _ context.Context\n")},
	}
	violations, err := scanPureSources(fixture, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0], "bypass.syso") {
		t.Fatalf("violations = %v, want linked artifact rejection", violations)
	}
}

func TestPureReaderRejectsWorkOutsidePanicBoundary(t *testing.T) {
	fixture := fstest.MapFS{
		"extractors/async/async.go": &fstest.MapFile{Data: []byte(`package async
import "context"
func bad(ctx context.Context) {
    go func() { panic("outside boundary") }()
    context.AfterFunc(ctx, func() { panic("outside boundary") })
}
`)},
		"sdk/sdk.go": &fstest.MapFile{Data: []byte("package sdk\nimport \"context\"\nvar _ context.Context\n")},
	}
	violations, err := scanPureSources(fixture, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 2 {
		t.Fatalf("violations = %v, want goroutine and AfterFunc rejection", violations)
	}
}

func scanPureSources(fsys fs.FS, root string) ([]string, error) {
	return scanPureTargets(fsys, []pureSourceTarget{
		{
			dir:     path.Join(root, "extractors"),
			allowed: extractorImportAllowlist,
		},
		{
			dir:     path.Join(root, "sdk"),
			allowed: sdkImportAllowlist,
		},
	})
}

func scanPureTargets(
	fsys fs.FS,
	targets []pureSourceTarget,
) ([]string, error) {
	var violations []string
	for _, target := range targets {
		if _, err := fs.Stat(fsys, target.dir); err != nil {
			return nil, fmt.Errorf("%s missing: %w", target.dir, err)
		}
		err := fs.WalkDir(fsys, target.dir, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				violations = append(violations,
					fmt.Sprintf("%s is a non-allowlisted production symlink", filePath))
				return nil
			}
			if !strings.HasSuffix(filePath, ".go") {
				violations = append(violations,
					fmt.Sprintf("%s is a non-allowlisted production artifact", filePath))
				return nil
			}
			if strings.HasSuffix(filePath, "_test.go") {
				return nil
			}
			source, err := fs.ReadFile(fsys, filePath)
			if err != nil {
				return fmt.Errorf("read %s: %w", filePath, err)
			}
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, filePath, source, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", filePath, err)
			}
			contextAliases := map[string]bool{}
			for _, spec := range parsed.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return fmt.Errorf("parse import in %s: %w", filePath, err)
				}
				if !target.allowed[importPath] {
					violations = append(violations,
						fmt.Sprintf("%s imports non-allowlisted capability %q", filePath, importPath))
				}
				if spec.Name != nil && spec.Name.Name == "." {
					violations = append(violations,
						fmt.Sprintf("%s uses a non-allowlisted dot import", filePath))
				}
				if importPath == "context" {
					alias := "context"
					if spec.Name != nil {
						alias = spec.Name.Name
					}
					contextAliases[alias] = true
				}
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.GoStmt:
					violations = append(violations,
						fmt.Sprintf("%s starts a goroutine outside the worker panic boundary", filePath))
				case *ast.SelectorExpr:
					identifier, ok := typed.X.(*ast.Ident)
					if ok && contextAliases[identifier.Name] && typed.Sel.Name == "AfterFunc" {
						violations = append(violations,
							fmt.Sprintf("%s schedules context.AfterFunc outside the worker panic boundary", filePath))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return violations, nil
}
