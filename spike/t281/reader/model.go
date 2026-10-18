// T28.1 is an isolated, unregistered experiment. It executes no source inputs.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxInput = 262144
const maxIdentities = 16384
const maxMetadata = 8 << 20

type inputFile struct {
	Population string `json:"population"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	Role       string `json:"role"`
	Digest     string `json:"sha256"`
	Bytes      int    `json:"bytes"`
	Missing    bool   `json:"missing,omitempty"`
}
type request struct {
	Format string      `json:"format"`
	Root   string      `json:"root"`
	Files  []inputFile `json:"files"`
}
type span struct {
	Start  int    `json:"start"`
	End    int    `json:"end"`
	SHA256 string `json:"sha256"`
}
type segment struct {
	Kind    string `json:"kind"`
	Literal []byte `json:"literal,omitempty"`
	Name    string `json:"name,omitempty"`
	Format  string `json:"format,omitempty"`
}
type value struct {
	Bytes    []byte    `json:"bytes,omitempty"`
	Segments []segment `json:"segments,omitempty"`
	State    string    `json:"state"`
	Reason   string    `json:"reason,omitempty"`
	Span     span      `json:"span"`
	Integer  bool      `json:"-"`
	TooLarge bool      `json:"-"`
}
type keyFact struct {
	Predicate    string   `json:"predicate"`
	Position     int      `json:"position"`
	Value        value    `json:"value"`
	Access       []string `json:"access"`
	ExpectedKind string   `json:"expected_kind,omitempty"`
	KindGap      string   `json:"kind_gap,omitempty"`
	HashTag      []byte   `json:"hash_tag,omitempty"`
	HashTagState string   `json:"hash_tag_state,omitempty"`
	ScriptPass   bool     `json:"script_pass,omitempty"`
}
type site struct {
	Method         string    `json:"method"`
	Span           span      `json:"span"`
	Recognized     bool      `json:"recognized"`
	Command        string    `json:"command,omitempty"`
	Gaps           []string  `json:"gaps"`
	Keys           []keyFact `json:"keys"`
	KeyCardinality string    `json:"key_cardinality,omitempty"`
}
type nativeField struct {
	Name    []byte            `json:"name"`
	Kind    string            `json:"kind"`
	Options map[string]string `json:"options,omitempty"`
	Span    span              `json:"span"`
}
type nativeFact struct {
	Kind       string            `json:"kind"`
	Name       []byte            `json:"name"`
	Source     span              `json:"source"`
	Prefixes   [][]byte          `json:"prefixes,omitempty"`
	Fields     []nativeField     `json:"fields,omitempty"`
	References [][]byte          `json:"references,omitempty"`
	Options    map[string]string `json:"options,omitempty"`
}
type aclTokenFact struct {
	Token []byte `json:"token"`
	Span  span   `json:"span"`
}
type aclPair struct {
	Principal aclTokenFact `json:"principal"`
	Pattern   aclTokenFact `json:"pattern"`
	Intent    string       `json:"intent"`
}
type blobResult struct {
	Population    string         `json:"population"`
	Repository    string         `json:"repository"`
	Commit        string         `json:"commit"`
	ModulePath    string         `json:"module_path,omitempty"`
	ClientVersion string         `json:"client_version,omitempty"`
	SourceScope   string         `json:"source_scope,omitempty"`
	Path          string         `json:"path"`
	Digest        string         `json:"sha256"`
	Role          string         `json:"role"`
	Status        string         `json:"status"`
	Boundary      string         `json:"boundary,omitempty"`
	Sites         []site         `json:"sites"`
	Native        []nativeFact   `json:"native"`
	Principals    []aclTokenFact `json:"principals"`
	ACLPairs      []aclPair      `json:"acl_pairs"`
}
type binding struct {
	Population string   `json:"population"`
	Path       string   `json:"path"`
	Source     span     `json:"source"`
	State      string   `json:"state"`
	Candidates []string `json:"candidates"`
}
type identity struct {
	Population, Path, Scope, ID string
	Source                      span
	Name                        []byte
	Prefixes                    [][]byte
	Key                         []byte
}
type module struct {
	Version string
	Valid   bool
}
type packageInfo struct{ Fields map[string][]fieldType }
type fieldType struct {
	Type  string
	Named string
}
type engine struct {
	root            string
	files           []inputFile
	fileMap         map[string]inputFile
	modules         map[string]module
	packages        map[string]*packageInfo
	commands        map[string]commandSpec
	metadataEntries int
	metadataBytes   int
	metadataFailed  bool
	catalog         []identity
	keys            []identity
	totalOutput     int
}

func sha(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func chosen(raw []byte, a, b int) span {
	if a < 0 || b <= a || b > len(raw) {
		return span{}
	}
	return span{a, b, sha(raw[a:b])}
}
func safePath(path string) bool {
	return path != "" && !strings.ContainsAny(path, "\\\x00") && !strings.HasPrefix(path, "/") && filepath.ToSlash(filepath.Clean(path)) == path && path != "." && path != ".." && !strings.HasPrefix(path, "../")
}
func fileKey(f inputFile) string { return f.Population + "\x00" + f.Path }
func (e *engine) read(f inputFile) ([]byte, string) {
	if f.Missing {
		return nil, "artifact-missing"
	}
	if f.Bytes < 0 || f.Bytes > maxInput {
		return nil, "resource-limit"
	}
	if len(f.Digest) != 64 {
		return nil, "artifact-missing"
	}
	if _, err := hex.DecodeString(f.Digest); err != nil {
		return nil, "artifact-missing"
	}
	path := filepath.Join(e.root, f.Digest)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(f.Bytes) {
		return nil, "artifact-missing"
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "artifact-missing"
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxInput+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || len(raw) != f.Bytes || sha(raw) != f.Digest {
		return nil, "artifact-missing"
	}
	return raw, ""
}
func (e *engine) account(entries, bytes int) bool {
	if entries < 0 || bytes < 0 || e.metadataEntries > maxIdentities-entries || e.metadataBytes > maxMetadata-bytes {
		e.metadataFailed = true
		return false
	}
	e.metadataEntries += entries
	e.metadataBytes += bytes
	return true
}
func (e *engine) moduleFor(f inputFile) (string, module) {
	dir := filepath.ToSlash(filepath.Dir(f.Path))
	for probes := 0; probes < 64; probes++ {
		path := "go.mod"
		if dir != "." {
			path = dir + "/go.mod"
		}
		if m, ok := e.modules[f.Population+"\x00"+path]; ok {
			return path, m
		}
		if dir == "." {
			break
		}
		dir = filepath.ToSlash(filepath.Dir(dir))
	}
	return "", module{}
}
func newResult(f inputFile) blobResult {
	return blobResult{Population: f.Population, Repository: f.Repository, Commit: f.Commit, Path: f.Path, Digest: f.Digest, Role: f.Role, Status: "processed", Sites: []site{}, Native: []nativeFact{}, Principals: []aclTokenFact{}, ACLPairs: []aclPair{}}
}
func refusal(f inputFile, reason string) blobResult {
	b := newResult(f)
	b.Status = "refused"
	b.Boundary = reason
	return b
}
func gaps(values ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
func emitJSON(out io.Writer, e *engine, record any, limit int) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return errors.New("output-encode")
	}
	return emitEncoded(out, e, raw, limit)
}
func emitEncoded(out io.Writer, e *engine, raw []byte, limit int) error {
	if len(raw) > limit || e.totalOutput > (32<<20)-len(raw)-1 {
		return errors.New("output-limit")
	}
	e.totalOutput += len(raw) + 1
	raw = append(raw, '\n')
	_, err := out.Write(raw)
	return err
}
