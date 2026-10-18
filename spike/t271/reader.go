// Package t271 is a retained, declaration-only experiment. Production code
// must not import it. The frozen preregistration predates this implementation.
package t271

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const format = "phebs-document-schema-v1"

// Source is supplied by the caller; nothing in it is fetched or executed.
type Source struct {
	Repository   string `json:"repository"`
	ManifestPath string `json:"manifest_path"`
	Commit       string `json:"commit"`
}

type Refusal struct {
	Code  string `json:"code"`
	Limit string `json:"limit,omitempty"`
}

type Fact struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Owner   *Owner          `json:"owner"`
	Meaning json.RawMessage `json:"meaning"`
}

type Owner struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Field       string `json:"field,omitempty"`
	Index       string `json:"index,omitempty"`
	Association string `json:"association,omitempty"`
}

type Citation struct {
	FactID     string `json:"fact_id"`
	Source     Source `json:"source"`
	BlobSHA256 string `json:"blob_sha256"`
	Pointer    string `json:"pointer"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	SHA256     string `json:"sha256"`
}

type CatalogOwner struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Fields       []Fact `json:"fields"`
	Declarations []Fact `json:"declarations"`
}

type Catalog struct {
	SchemaID string         `json:"schema_id"`
	Tables   []CatalogOwner `json:"tables"`
	Views    []CatalogOwner `json:"views"`
}

type Result struct {
	State       string     `json:"state"`
	Checklist   string     `json:"checklist,omitempty"`
	Refusal     *Refusal   `json:"refusal,omitempty"`
	Source      Source     `json:"source"`
	Format      string     `json:"format,omitempty"`
	SourceClass string     `json:"source_class,omitempty"`
	BlobSHA256  string     `json:"blob_sha256,omitempty"`
	Facts       []Fact     `json:"facts"`
	Citations   []Citation `json:"citations"`
	Catalog     Catalog    `json:"catalog"`
}

type refusalError struct{ refusal Refusal }

func (e *refusalError) Error() string { return e.refusal.Code }

func reject(code string) *refusalError { return &refusalError{refusal: Refusal{Code: code}} }
func over(limit string) *refusalError {
	return &refusalError{refusal: Refusal{Code: "limit_exceeded", Limit: limit}}
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func emptyResult(source Source) Result {
	return Result{Source: source, Facts: []Fact{}, Citations: []Citation{},
		Catalog: Catalog{Tables: []CatalogOwner{}, Views: []CatalogOwner{}}}
}

// Read admits one supplied immutable blob atomically. Nil is a missing artifact;
// a supplied empty slice is malformed. There is no I/O, cache, worker or child.
func Read(source Source, raw []byte) Result {
	// Refusals/missing workflow states echo no unvalidated context strings.
	result := emptyResult(Source{})
	if raw == nil {
		result.State = "document-schema-artifact-missing"
		result.Checklist = "request-schema-export"
		return result
	}
	fail := func(err *refusalError) Result {
		result.State = "refused"
		result.Refusal = &err.refusal
		return result
	}
	if len(raw) > 65536 {
		return fail(over("manifest_bytes"))
	}
	if err := validateSource(source); err != nil {
		return fail(err)
	}
	// A short caller substring must not retain a much larger backing string.
	source = Source{Repository: strings.Clone(source.Repository), ManifestPath: strings.Clone(source.ManifestPath), Commit: strings.Clone(source.Commit)}
	if !utf8.Valid(raw) {
		return fail(reject("invalid_utf8"))
	}
	parser := tokenParser{raw: raw, decoder: json.NewDecoder(bytes.NewReader(raw))}
	parser.decoder.UseNumber()
	root, err := parser.read("", 0)
	if err != nil {
		return fail(err)
	}
	for _, char := range raw[root.end:] {
		switch char {
		case ' ', '\t', '\r', '\n':
		default:
			return fail(reject("trailing_bytes"))
		}
	}
	validated, err := validateManifest(root)
	if err != nil {
		return fail(err)
	}
	if len(validated.declarations) > 192 {
		return fail(over("emitted_citations"))
	}
	return publish(source, raw, validated)
}

func validateSource(source Source) *refusalError {
	if len(source.Repository) > 256 {
		return over("repository_utf8_bytes")
	}
	if len(source.ManifestPath) > 256 {
		return over("manifest_path_utf8_bytes")
	}
	if !validText(source.Repository) || !validText(source.ManifestPath) ||
		!utf8.ValidString(source.Repository) || !utf8.ValidString(source.ManifestPath) ||
		strings.Contains(source.ManifestPath, "\\") || path.IsAbs(source.ManifestPath) ||
		path.Clean(source.ManifestPath) != source.ManifestPath || source.ManifestPath == "." ||
		strings.HasPrefix(source.ManifestPath, "../") || source.ManifestPath == ".." ||
		len(source.Commit) != 40 {
		return reject("input_context_invalid")
	}
	for _, char := range source.Commit {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return reject("input_context_invalid")
		}
	}
	return nil
}

func validText(text string) bool {
	return strings.TrimSpace(text) != "" && !strings.ContainsFunc(text, unicode.IsControl)
}

type node struct {
	start, end int
	pointer    string
	object     map[string]*node
	keys       []string
	array      []*node
	kind       string
	scalar     any
}

type tokenParser struct {
	raw     []byte
	decoder *json.Decoder
}

func (p *tokenParser) start() int {
	offset := int(p.decoder.InputOffset())
	for offset < len(p.raw) && strings.ContainsRune(" \r\n\t,:", rune(p.raw[offset])) {
		offset++
	}
	return offset
}

func (p *tokenParser) read(pointer string, depth int) (*node, *refusalError) {
	start := p.start()
	token, err := p.decoder.Token()
	if err != nil {
		return nil, reject("invalid_json")
	}
	item := &node{start: start, end: int(p.decoder.InputOffset()), pointer: pointer}
	if delim, ok := token.(json.Delim); ok {
		if depth >= 32 {
			return nil, over("json_depth")
		}
		switch delim {
		case '{':
			item.kind, item.object = "object", make(map[string]*node)
			for p.decoder.More() {
				keyStart := p.start()
				keyToken, keyErr := p.decoder.Token()
				if keyErr != nil {
					return nil, reject("invalid_json")
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, reject("invalid_json")
				}
				if !pairedSurrogates(p.raw[keyStart:p.decoder.InputOffset()]) {
					return nil, reject("invalid_unicode_scalar")
				}
				if _, exists := item.object[key]; exists {
					return nil, reject("duplicate_key")
				}
				if forbiddenPayload(key) {
					return nil, reject("data_payload_forbidden")
				}
				child, childErr := p.read(pointer+"/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"), depth+1)
				if childErr != nil {
					return nil, childErr
				}
				item.object[key], item.keys = child, append(item.keys, key)
			}
		case '[':
			item.kind, item.array = "array", []*node{}
			for p.decoder.More() {
				child, childErr := p.read(pointer+"/"+strconv.Itoa(len(item.array)), depth+1)
				if childErr != nil {
					return nil, childErr
				}
				item.array = append(item.array, child)
			}
		default:
			return nil, reject("invalid_json")
		}
		if _, err := p.decoder.Token(); err != nil {
			return nil, reject("invalid_json")
		}
		item.end = int(p.decoder.InputOffset())
		return item, nil
	}
	item.scalar = token
	switch token.(type) {
	case string:
		item.kind = "string"
		if !pairedSurrogates(p.raw[start:item.end]) {
			return nil, reject("invalid_unicode_scalar")
		}
	case json.Number:
		item.kind = "number"
	case bool:
		item.kind = "boolean"
	case nil:
		item.kind = "null"
	default:
		return nil, reject("invalid_json")
	}
	return item, nil
}

// Go's JSON decoder replaces unpaired surrogate escapes; the neutral contract
// refuses them before that replacement could conflate exact source names.
func pairedSurrogates(raw []byte) bool {
	for index := 1; index+1 < len(raw); index++ {
		if raw[index] != '\\' {
			continue
		}
		index++
		if raw[index] != 'u' {
			continue
		}
		if index+4 >= len(raw) {
			return false
		}
		code, err := strconv.ParseUint(string(raw[index+1:index+5]), 16, 16)
		if err != nil {
			return false
		}
		index += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(raw[index+3:index+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		index += 6
	}
	return true
}

func forbiddenPayload(key string) bool {
	switch key {
	case "record", "records", "sample", "samples", "default", "defaults", "example", "examples", "value", "values", "data", "documents":
		return true
	default:
		return false
	}
}

// Keep the parser's external vocabulary closed, including an empty/EOF input.
var _ error = (*refusalError)(nil)
