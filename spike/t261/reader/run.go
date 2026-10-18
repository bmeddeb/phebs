package reader

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type DeclarationSet struct {
	Manifest  string   `json:"manifest"`
	Relations []string `json:"relations"`
}
type Request struct {
	ID              string           `json:"id"`
	Path            string           `json:"path"`
	SHA256          string           `json:"sha256"`
	Dialect         string           `json:"dialect"`
	Engine          string           `json:"manifest_engine"`
	Class           string           `json:"source_class"`
	Lineage         string           `json:"lineage"`
	Repository      string           `json:"repository"`
	Commit          string           `json:"commit"`
	SourceRole      string           `json:"source_role"`
	DeclarationSets []DeclarationSet `json:"declaration_sets"`
	UseControlSets  bool             `json:"use_control_sets"`
	ProjectionFault string           `json:"projection_fault"`
}
type BlobResult struct {
	ID          string       `json:"id"`
	Path        string       `json:"path"`
	SHA256      string       `json:"sha256"`
	Repository  string       `json:"repository"`
	Commit      string       `json:"commit,omitempty"`
	Lineage     string       `json:"lineage"`
	Dialect     string       `json:"dialect"`
	Class       string       `json:"source_class"`
	Decision    string       `json:"decision"`
	Reason      string       `json:"reason,omitempty"`
	SchemaState string       `json:"schema_state,omitempty"`
	Statements  []StmtResult `json:"statements"`
}

// Frontend returns a category from actual imported AST parsing. It sees one
// admitted statement only. Nil means the explicit bounded subset frontend.
type Frontend func(sql string, category string) (string, error)
type Pipeline struct {
	Dialect                              string
	Frontend                             Frontend
	catalog                              map[string]map[string][]Fact
	schemaCount, schemaBytes, identities int
}

func (p *Pipeline) Read(root string, r Request) (result BlobResult) {
	result = BlobResult{ID: r.ID, Path: r.Path, SHA256: r.SHA256, Repository: r.Repository, Commit: r.Commit, Lineage: r.Lineage, Dialect: r.Dialect, Class: r.Class, Decision: "admit", Statements: []StmtResult{}}
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Decision = "refuse"
			result.Reason = "panic"
			result.Statements = []StmtResult{}
		}
	}()
	refuse := func(reason string) BlobResult {
		result.Decision = "refuse"
		result.Reason = reason
		result.Statements = []StmtResult{}
		if reason == "schema-artifact-missing" {
			result.SchemaState = "request-schema-only-dump"
		}
		return result
	}
	if r.Dialect != p.Dialect {
		return refuse("other-dialect")
	}
	if r.Class == "generated-sql-go" {
		result.Decision = "gap"
		result.Reason = "generated-duplicate"
		return result
	}
	if r.Class == "manifest" {
		result.Decision = "gap"
		result.Reason = "metadata-only"
		return result
	}
	if r.Class == "missing" {
		return refuse("schema-artifact-missing")
	}
	if r.Path == "" || filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path || strings.HasPrefix(r.Path, "../") {
		return refuse("metadata-conflict")
	}
	path := filepath.Join(root, r.Path)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return refuse("schema-artifact-missing")
	}
	if info.Size() > MaxInput {
		return refuse("resource-limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return refuse("schema-artifact-missing")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxInput+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return refuse("input-read-failed")
	}
	if len(data) > MaxInput {
		return refuse("resource-limit")
	}
	if digest(data) != r.SHA256 {
		return refuse("source-digest-mismatch")
	}
	header := ""
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "--") {
			continue
		}
		next := ""
		if strings.HasPrefix(trim, "-- PostgreSQL database dump") {
			next = "postgresql"
		}
		if strings.HasPrefix(trim, "-- MySQL dump") || strings.HasPrefix(trim, "-- MariaDB dump") {
			next = "mysql"
		}
		if next != "" {
			if header != "" && header != next {
				return refuse("metadata-conflict")
			}
			header = next
		}
	}
	if r.Engine != "" && r.Engine != r.Dialect || header != "" && header != r.Dialect {
		return refuse("metadata-conflict")
	}
	if r.Engine == "" && header == "" {
		return refuse("dialect-unavailable")
	}
	stmts, err := lex(data, r.Dialect)
	if err != nil {
		return refuse(err.Error())
	}
	if r.Class == "captured-dump" {
		for _, s := range stmts {
			ts := stripSemi(s.Tokens)
			if len(ts) > 0 && (ts[0].is("COPY") || ts[0].is("INSERT") || ts[0].is("REPLACE") || ts[0].is("LOAD") && len(ts) > 1 && ts[1].is("DATA")) {
				return refuse("data-section")
			}
		}
	}
	for _, s := range stmts {
		for _, t := range s.Tokens {
			if t.is("DELIMITER") {
				result.Decision = "gap"
				result.Reason = "procedural-sql"
				result.Statements = []StmtResult{{Start: 0, End: len(data), Category: "procedural", Disposition: "gap", Gap: "procedural-sql", Frontend: "common-admission", Objects: []Fact{}, Sites: []Fact{}, Events: []Fact{}}}
				return result
			}
		}
	}
	if r.Class == "authored-query" && r.UseControlSets && len(r.DeclarationSets) > 1 {
		return refuse("ambiguous-schema-set")
	}
	factCount := 0
	for _, s := range stmts {
		ts := stripSemi(s.Tokens)
		if len(ts) == 0 {
			continue
		}
		cat := category(ts, r.Class)
		row := StmtResult{Start: s.Start, End: s.End, Category: cat, Disposition: "admit", Frontend: "subset", Objects: []Fact{}, Sites: []Fact{}, Events: []Fact{}}
		gap := func(reason string) {
			row.Disposition = "gap"
			row.Gap = reason
			row.Objects = []Fact{}
			row.Sites = []Fact{}
			row.Events = []Fact{}
		}
		wrapper := cat == "wrapper" || r.Class == "captured-dump" && (cat == "drop" || ts[0].is("SELECT") && index(ts, "set_config") >= 0)
		if wrapper {
			row.Disposition = "wrapper"
			row.Frontend = "common-admission"
			result.Statements = append(result.Statements, row)
			continue
		}
		if cat == "procedural" {
			gap("procedural-sql")
			row.Frontend = "common-admission"
			result.Statements = append(result.Statements, row)
			continue
		}
		if p.Frontend != nil {
			row.Frontend = "imported"
			sql := string(data[s.Start:s.End])
			if r.Dialect == "mysql" {
				sql = executableSQL(data, s)
			}
			accepted, e := p.Frontend(sql, cat)
			row.FrontendCategory = accepted
			if e != nil {
				gap("parser-gap")
				result.Statements = append(result.Statements, row)
				continue
			}
		}
		if r.ProjectionFault == "omit-source-position" {
			gap("parser-gap")
			result.Statements = append(result.Statements, row)
			continue
		}
		switch r.Class {
		case "authored-query":
			row.Sites, err = querySites(ts, data, r.Dialect)
			if err == nil {
				for i := range row.Sites {
					row.Sites[i].Binding = p.bind(r, row.Sites[i])
					if row.Sites[i].Binding == "name-only" {
						result.SchemaState = "schema-artifact-missing"
					}
					if row.Sites[i].Binding == "missing" {
						row.Gap = "unknown-declaration"
						row.Disposition = "gap"
						result.Decision = "gap"
						result.Reason = "unknown-declaration"
					}
				}
			}
		case "migration":
			direction := "unspecified"
			prefix := string(data[:s.Start])
			up := strings.LastIndex(prefix, "-- +goose Up")
			down := strings.LastIndex(prefix, "-- +goose Down")
			if up >= 0 && up > down {
				direction = "up"
			} else if down >= 0 {
				direction = "down"
			}
			row.Events, err = migration(ts, data, r.Dialect, direction)
		default:
			row.Objects, _, err = declarations(ts, data, r.Dialect)
			if err == nil && len(row.Objects) == 0 {
				row.Disposition = "wrapper"
			}
			for i := range row.Objects {
				row.Objects[i].SourceRole = r.SourceRole
			}
		}
		if err != nil {
			reason := err.Error()
			if reason != "dynamic-identifier" && reason != "resource-limit" {
				reason = "unsupported-statement"
			}
			if reason == "resource-limit" {
				return refuse(reason)
			}
			gap(reason)
		}
		factCount += len(row.Objects) + len(row.Sites) + len(row.Events)
		if factCount > MaxFacts {
			return refuse("resource-limit")
		}
		result.Statements = append(result.Statements, row)
	}
	if result.Decision == "admit" {
		for _, s := range result.Statements {
			if s.Disposition == "gap" {
				result.Decision = "gap"
				result.Reason = s.Gap
				break
			}
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return refuse("output-encoding")
	}
	if len(encoded) > MaxOutput {
		return refuse("resource-limit")
	}
	if r.Class == "authored-schema" || r.Class == "captured-dump" {
		var staged []Fact
		for _, s := range result.Statements {
			for _, f := range s.Objects {
				if f.Kind == "table" || f.Kind == "view" {
					staged = append(staged, f)
				}
			}
		}
		if p.schemaCount+1 > 64 || p.schemaBytes+len(data) > 8<<20 || p.identities+len(staged) > 16384 {
			return refuse("resource-limit")
		}
		// Admission is atomic for both returned facts and the subsequent binding
		// catalog. No prefix from a refused blob may survive into a later query.
		p.schemaCount++
		p.schemaBytes += len(data)
		p.identities += len(staged)
		if p.catalog == nil {
			p.catalog = map[string]map[string][]Fact{}
		}
		if p.catalog[r.Lineage] == nil {
			p.catalog[r.Lineage] = map[string][]Fact{}
		}
		for _, f := range staged {
			key := canonicalFact(f, r.Dialect)
			p.catalog[r.Lineage][key] = append(p.catalog[r.Lineage][key], f)
		}
	}
	return result
}
func canonicalFact(f Fact, dialect string) string {
	schema := f.Schema
	if dialect == "postgresql" && !f.SchemaQuoted {
		schema = asciiLower(schema)
	}
	return schema + "\x00" + f.CanonicalName
}
func (p *Pipeline) bind(r Request, f Fact) string {
	if r.UseControlSets {
		if len(r.DeclarationSets) == 0 {
			return "name-only"
		}
		matches := 0
		for _, name := range r.DeclarationSets[0].Relations {
			if name == f.CanonicalName {
				matches++
			}
		}
		if matches == 1 {
			return "unique"
		}
		if matches > 1 {
			return "ambiguous"
		}
		return "missing"
	}
	set := p.catalog[r.Lineage]
	if len(set) == 0 {
		return "name-only"
	}
	matches := len(set[canonicalFact(f, r.Dialect)])
	if matches == 1 {
		return "unique"
	}
	if matches > 1 {
		return "ambiguous"
	}
	return "missing"
}

// Remove only executable-comment delimiters/version digits. All projected spans
// still refer to original bytes; imported parsing sees the same SQL tokens.
func executableSQL(data []byte, s statement) string {
	ts := s.Tokens
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			gap := data[ts[i-1].End:t.Start]
			if strings.Contains(string(gap), "/*") || strings.Contains(string(gap), "*/") {
				for range gap {
					b.WriteByte(' ')
				}
			} else {
				b.Write(gap)
			}
		}
		b.Write(data[t.Start:t.End])
	}
	return b.String()
}

func Main(dialect string, frontend Frontend) error {
	if len(os.Args) != 3 {
		return errors.New("usage: reader ROOT REQUESTS.json")
	}
	file, err := os.Open(os.Args[2])
	if err != nil {
		return fmt.Errorf("open requests: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only metadata; decode errors are checked below.
	var requests []Request
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&requests); err != nil {
		return fmt.Errorf("decode requests: %w", err)
	}
	if len(requests) > 1024 {
		return errors.New("request count exceeds bound")
	}
	p := Pipeline{Dialect: dialect, Frontend: frontend}
	results := make([]BlobResult, 0, len(requests))
	// Requests are in stable schema-first order. The binding catalog retains
	// identities only, with no raw SQL or AST. All emitted metadata results are
	// retained separately until canonical encoding; the observed process-RSS
	// stop is not a hard bound on that complete result heap.
	for _, r := range requests {
		results = append(results, p.Read(os.Args[1], r))
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(results); err != nil {
		return fmt.Errorf("encode outputs: %w", err)
	}
	return nil
}
