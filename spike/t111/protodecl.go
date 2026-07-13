package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bufbuild/protocompile/ast"
	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
)

// extractProtoDecls walks a corpus system directory for .proto files and
// emits DECLARES_OPERATION and DECLARES_FIELD facts from the AST alone —
// parser-only, no import resolution, so the declared plane needs zero
// network and zero build context. Spans are exact byte offsets (tier exact).
//
// contract_lineage_id is a spike placeholder: the repo-relative directory of
// the descriptor set. Real lineage resolution is Epic 12.
func extractProtoDecls(system, commit, root string) ([]Fact, error) {
	var facts []Fact
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".proto") {
			return nil
		}
		fileFacts, ferr := protoFileFacts(system, commit, root, path)
		if ferr != nil {
			// Malformed protos are recorded, not fatal: coverage honesty.
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			failure, recordErr := newWholeFileFact("EXTRACTION_FAILURE", rel, ferr.Error(), "", tierUnresolved,
				system, commit, root, rel, "proto-parse-v2", "stage=parse")
			if recordErr != nil {
				return fmt.Errorf("record parse failure for %s: %w", rel, recordErr)
			}
			facts = append(facts, failure)
			return nil
		}
		facts = append(facts, fileFacts...)
		return nil
	})
	return facts, err
}

func protoFileFacts(system, commit, root, path string) ([]Fact, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, fmt.Errorf("rel: %w", err)
	}
	rel = filepath.ToSlash(rel)

	handler := reporter.NewHandler(nil)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	fileNode, parseErr := parser.Parse(path, f, handler)
	closeErr := f.Close()
	if parseErr != nil {
		return nil, fmt.Errorf("parse: %w", parseErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close: %w", closeErr)
	}

	digest := blobDigest(content)
	role := classifyRole(rel, content)
	lineage := "lineage:" + system + "/" + filepath.ToSlash(filepath.Dir(rel))

	var pkg string
	var facts []Fact

	span := func(n ast.Node) (sb, eb, sl, el int) {
		info := fileNode.NodeInfo(n)
		s, e := info.Start(), info.End()
		return s.Offset, e.Offset, s.Line, e.Line
	}

	var walkMsg func(prefix string, msg *ast.MessageNode)
	walkMsg = func(prefix string, msg *ast.MessageNode) {
		msgName := prefix + string(msg.Name.AsIdentifier())
		emitField := func(name string, tag uint64, n ast.Node) {
			sb, eb, sl, el := span(n)
			object := fmt.Sprintf("%s.%s#%d:%s", pkg, msgName, tag, name)
			facts = append(facts, newFact("DECLARES_FIELD", rel, object, role, tierExact,
				system, commit, rel, sb, eb, sl, el, "proto-field-v1", digest, lineage))
		}
		for _, el := range msg.Decls {
			switch fd := el.(type) {
			case *ast.FieldNode:
				emitField(string(fd.Name.AsIdentifier()), fd.Tag.Val, fd)
			case *ast.MapFieldNode:
				emitField(string(fd.Name.AsIdentifier()), fd.Tag.Val, fd)
			case *ast.OneofNode:
				for _, oel := range fd.Decls {
					if ofd, ok := oel.(*ast.FieldNode); ok {
						emitField(string(ofd.Name.AsIdentifier()), ofd.Tag.Val, ofd)
					}
				}
			case *ast.MessageNode:
				walkMsg(msgName+".", fd)
			}
		}
	}

	for _, decl := range fileNode.Decls {
		switch d := decl.(type) {
		case *ast.PackageNode:
			pkg = string(d.Name.AsIdentifier())
		case *ast.ServiceNode:
			svc := string(d.Name.AsIdentifier())
			for _, el := range d.Decls {
				rpc, ok := el.(*ast.RPCNode)
				if !ok {
					continue
				}
				sb, eb, sl, el2 := span(rpc)
				object := fmt.Sprintf("%s.%s/%s", pkg, svc, string(rpc.Name.AsIdentifier()))
				facts = append(facts, newFact("DECLARES_OPERATION", rel, object, role, tierExact,
					system, commit, rel, sb, eb, sl, el2, "proto-rpc-v1", digest, lineage))
			}
		case *ast.MessageNode:
			walkMsg("", d)
		}
	}
	return facts, nil
}
