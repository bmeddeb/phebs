package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func run(in io.Reader, out io.Writer) error {
	var req request
	rawManifest, err := io.ReadAll(io.LimitReader(in, (2<<20)+1))
	if err != nil || len(rawManifest) > 2<<20 {
		return errors.New("input-manifest-limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(rawManifest))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return errors.New("input-manifest")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("input-manifest-tail")
	}
	if req.Format != "t281-reader-input-v1" || len(req.Files) > 1024 || len(req.Files) == 0 {
		return errors.New("input-manifest-limit")
	}
	root, err := os.Lstat(req.Root)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return errors.New("input-root")
	}
	e := &engine{root: req.Root, files: req.Files, fileMap: map[string]inputFile{}, modules: map[string]module{}, packages: map[string]*packageInfo{}, commands: map[string]commandSpec{}}
	sort.Slice(e.files, func(i, j int) bool { return fileKey(e.files[i]) < fileKey(e.files[j]) })
	inputBytes, metadataFiles, metadataBytes := 0, 0, 0
	for _, f := range e.files {
		if !safePath(f.Path) || f.Population == "" || len(f.Population) > 4096 || inputBytes > (8<<20)-max(0, f.Bytes) {
			return errors.New("input-member-limit")
		}
		inputBytes += max(0, f.Bytes)
		if _, ok := e.fileMap[fileKey(f)]; ok {
			return errors.New("input-member-duplicate")
		}
		e.fileMap[fileKey(f)] = f
		if f.Role == "core-json" {
			metadataFiles++
			metadataBytes += f.Bytes
			if metadataBytes > 4<<20 {
				return errors.New("core-metadata-bytes")
			}
			raw, reason := e.read(f)
			if reason != "" || e.addCommand(raw) != nil {
				return errors.New("core-metadata")
			}
		}
		if strings.HasSuffix(f.Path, "go.mod") && f.Role == "module" {
			raw, reason := e.read(f)
			if reason != "" {
				return errors.New("module-input")
			}
			e.modules[fileKey(f)] = parseModule(raw)
			if !e.account(1, len(fileKey(f))+32) {
				return errors.New("module-metadata-limit")
			}
		}
	}
	if metadataFiles != 392 {
		return errors.New("core-metadata-count")
	}
	// Only compact declaration/import/type metadata survives this pass.
	for _, f := range e.files {
		if f.Role == "go-source" {
			if raw, reason := e.read(f); reason == "" {
				e.collect(f, raw)
			}
		}
	}
	processed := 0
	for _, f := range e.files {
		b := newResult(f)
		if f.Role == "rdb-data" || f.Role == "aof-data" {
			b = refusal(f, "data-bearing-input")
		} else if raw, reason := e.read(f); reason != "" {
			b = refusal(f, reason)
		} else if e.metadataFailed && f.Role == "go-source" {
			b = refusal(f, "resource-limit")
		} else {
			switch f.Role {
			case "go-source":
				b = e.goBlob(f, raw)
			case "acl":
				b = parseACL(f, raw)
			}
		}
		if len(b.Sites)+len(b.Native)+len(b.Principals)+len(b.ACLPairs) > 8192 {
			b = refusal(f, "resource-limit")
		}
		encoded, encodeErr := json.Marshal(struct {
			Type string     `json:"type"`
			Blob blobResult `json:"blob"`
		}{"blob", b})
		if encodeErr != nil {
			return errors.New("blob-output-encode")
		}
		if len(encoded) > 4<<20 {
			b = refusal(f, "resource-limit")
			encoded, _ = json.Marshal(struct {
				Type string     `json:"type"`
				Blob blobResult `json:"blob"`
			}{"blob", b})
		}
		stagedNatives, stagedKeys := []identity{}, []identity{}
		if b.Status == "processed" {
			for _, n := range b.Native {
				stagedNatives = append(stagedNatives, identity{Population: f.Population, Path: f.Path, Scope: b.SourceScope, Source: n.Source, Name: n.Name, Prefixes: n.Prefixes, ID: sha([]byte(b.SourceScope + "\x00index\x00" + string(n.Name)))})
			}
			for _, s := range b.Sites {
				for _, k := range s.Keys {
					if k.Value.State == "literal" && !k.ScriptPass {
						stagedKeys = append(stagedKeys, identity{Population: f.Population, Path: f.Path, Scope: b.SourceScope, Source: k.Value.Span, Key: k.Value.Bytes})
					}
				}
			}
			bytes, reservedNodes := 0, 0
			for _, n := range stagedNatives {
				bytes += len(n.Name) + 64
				for _, p := range n.Prefixes {
					bytes += len(p)
					reservedNodes += 2
				}
			}
			for _, k := range stagedKeys {
				bytes += len(k.Key)
			}
			if !e.account(len(stagedNatives)+len(stagedKeys)+reservedNodes, bytes) {
				b = refusal(f, "resource-limit")
				stagedNatives = nil
				stagedKeys = nil
				encoded, _ = json.Marshal(struct {
					Type string     `json:"type"`
					Blob blobResult `json:"blob"`
				}{"blob", b})
			}
		}
		// Output admission precedes catalog mutation; a refused blob leaks no prefix.
		if err := emitEncoded(out, e, encoded, 4<<20); err != nil {
			return err
		}
		e.catalog = append(e.catalog, stagedNatives...)
		e.keys = append(e.keys, stagedKeys...)
		processed++
	}
	indexes := map[string]*prefixNode{}
	for i, n := range e.catalog {
		if len(n.Prefixes) == 0 {
			continue
		}
		key := n.Population + "\x00" + n.Scope
		root := indexes[key]
		if root == nil {
			root = newPrefixNode(nil)
			indexes[key] = root
		}
		for _, prefix := range n.Prefixes {
			root.insert(prefix, i)
		}
	}
	for _, k := range e.keys {
		b := binding{Population: k.Population, Path: k.Path, Source: k.Source, State: "unbound", Candidates: []string{}}
		if root := indexes[k.Population+"\x00"+k.Scope]; root != nil {
			for index := range root.lookup(k.Key) {
				n := e.catalog[index]
				b.Candidates = append(b.Candidates, n.ID)
			}
		}
		sort.Strings(b.Candidates)
		if len(b.Candidates) == 1 {
			b.State = "bound-indexed-projection"
		}
		if len(b.Candidates) > 1 {
			b.State = "ambiguous-key-family"
		}
		if err := emitJSON(out, e, struct {
			Type    string  `json:"type"`
			Binding binding `json:"binding"`
		}{"binding", b}, 4<<20); err != nil {
			return err
		}
	}
	return emitJSON(out, e, struct {
		Type  string `json:"type"`
		Files int    `json:"files"`
	}{"end", processed}, 4096)
}
func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "t281 reader refused:", err.Error())
		os.Exit(1)
	}
}
