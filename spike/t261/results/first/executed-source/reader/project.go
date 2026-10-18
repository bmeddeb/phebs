package reader

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

type Fact struct {
	Kind           string   `json:"kind,omitempty"`
	Predicate      string   `json:"predicate,omitempty"`
	Name           string   `json:"name"`
	Quoted         bool     `json:"quoted"`
	Start          int      `json:"start"`
	End            int      `json:"end"`
	SHA256         string   `json:"sha256"`
	CanonicalName  string   `json:"canonical_name"`
	Schema         string   `json:"schema,omitempty"`
	SchemaQuoted   bool     `json:"schema_quoted,omitempty"`
	Relation       string   `json:"relation,omitempty"`
	Columns        []string `json:"columns,omitempty"`
	TargetRelation string   `json:"target_relation,omitempty"`
	TargetColumns  []string `json:"target_columns,omitempty"`
	PrefixLengths  []int    `json:"prefix_lengths,omitempty"`
	Access         string   `json:"access,omitempty"`
	Binding        string   `json:"binding,omitempty"`
	Direction      string   `json:"direction,omitempty"`
	SourceRole     string   `json:"source_role,omitempty"`
}
type StmtResult struct {
	Start            int    `json:"start"`
	End              int    `json:"end"`
	Category         string `json:"category"`
	Disposition      string `json:"disposition"`
	Gap              string `json:"gap,omitempty"`
	Frontend         string `json:"frontend"`
	FrontendCategory string `json:"frontend_category,omitempty"`
	Objects          []Fact `json:"objects"`
	Sites            []Fact `json:"sites"`
	Events           []Fact `json:"events"`
}

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func fact(t token, kind string, data []byte, dialect string) Fact {
	return Fact{Kind: kind, Name: t.Value, Quoted: t.Quoted, Start: t.Start, End: t.End, SHA256: digest(data[t.Start:t.End]), CanonicalName: normalized(t, dialect)}
}
func qualified(ts []token) (name token, schema token, n int, ok bool) {
	if len(ts) == 0 || !ts[0].identifier() {
		return
	}
	name = ts[0]
	n = 1
	ok = true
	if len(ts) > 1 && ts[1].is(".") {
		if len(ts) < 3 || !ts[2].identifier() {
			ok = false
			return
		}
		schema = name
		name = ts[2]
		n = 3
	}
	return
}
func relationFact(ts []token, kind string, data []byte, dialect string) (Fact, int, error) {
	n, s, count, ok := qualified(ts)
	if !ok {
		return Fact{}, 0, failure("unsupported-statement")
	}
	f := fact(n, kind, data, dialect)
	f.Schema = s.Value
	f.SchemaQuoted = s.Quoted
	return f, count, nil
}
func listNames(ts []token) ([]string, []int, error) {
	var names []string
	var prefixes []int
	for _, part := range split(ts) {
		if len(part) == 0 || !part[0].identifier() {
			return nil, nil, failure("unsupported-statement")
		}
		names = append(names, part[0].Value)
		p := 0
		i := 1
		if i < len(part) && part[i].is("(") {
			if len(part) < 4 || !part[i+1].KindIs("number") || !part[i+2].is(")") {
				return nil, nil, failure("unsupported-statement")
			}
			if _, err := fmt.Sscanf(part[i+1].Value, "%d", &p); err != nil {
				return nil, nil, failure("unsupported-statement")
			}
			i += 3
		}
		if i < len(part) && (part[i].is("ASC") || part[i].is("DESC")) {
			i++
		}
		if i < len(part) {
			return nil, nil, failure("unsupported-statement")
		}
		prefixes = append(prefixes, p)
	}
	return names, prefixes, nil
}
func (t token) KindIs(s string) bool { return t.Kind == s }

func constraint(ts []token, relation string, data []byte, dialect string) (Fact, error) {
	if len(ts) == 0 {
		return Fact{}, failure("unsupported-statement")
	}
	original := ts
	var name token
	if ts[0].is("CONSTRAINT") {
		if len(ts) < 3 || !ts[1].identifier() {
			return Fact{}, failure("unsupported-statement")
		}
		name = ts[1]
		ts = ts[2:]
	}
	kind := ""
	phrase := 1
	switch {
	case ts[0].is("PRIMARY") && len(ts) > 1 && ts[1].is("KEY"):
		kind = "primary_key"
		phrase = 2
	case ts[0].is("FOREIGN") && len(ts) > 1 && ts[1].is("KEY"):
		kind = "foreign_key"
		phrase = 2
	case ts[0].is("UNIQUE"):
		kind = "unique"
		if len(ts) > 1 && (ts[1].is("KEY") || ts[1].is("INDEX")) {
			phrase = 2
		}
	case ts[0].is("CHECK"):
		kind = "check"
	case ts[0].is("EXCLUDE"):
		kind = "exclusion"
	case ts[0].is("KEY") || ts[0].is("INDEX"):
		kind = "index"
	default:
		return Fact{}, failure("unsupported-statement")
	}
	if name.Kind == "" && phrase < len(ts) && ts[phrase].identifier() && kind != "exclusion" {
		name = ts[phrase]
		phrase++
	}
	if name.Kind == "" {
		name = token{Value: fmt.Sprintf("@%d", ts[0].Start), Kind: "word", Start: ts[0].Start, End: ts[phrase-1].End}
	}
	f := fact(name, kind, data, dialect)
	f.Relation = relation
	if kind == "check" || kind == "exclusion" {
		if index(ts, "(") < 0 {
			return Fact{}, failure("unsupported-statement")
		}
		return f, nil
	}
	open := index(ts[phrase:], "(")
	if open < 0 {
		return Fact{}, failure("unsupported-statement")
	}
	open += phrase
	end := matching(ts, open)
	if end < 0 {
		return Fact{}, failure("unsupported-statement")
	}
	var err error
	f.Columns, f.PrefixLengths, err = listNames(ts[open+1 : end])
	if err != nil {
		return Fact{}, err
	}
	hasPrefix := false
	for _, p := range f.PrefixLengths {
		hasPrefix = hasPrefix || p > 0
	}
	if !hasPrefix {
		f.PrefixLengths = nil
	}
	if kind == "foreign_key" {
		ref := after(ts[end+1:], "REFERENCES")
		target, _, count, ok := qualified(ref)
		if !ok || count >= len(ref) || !ref[count].is("(") {
			return Fact{}, failure("unsupported-statement")
		}
		close := matching(ref, count)
		f.TargetRelation = target.Value
		f.TargetColumns, _, err = listNames(ref[count+1 : close])
		if err != nil {
			return Fact{}, err
		}
	}
	_ = original
	return f, nil
}

func declarations(ts []token, data []byte, dialect string) ([]Fact, string, error) {
	if len(ts) < 2 {
		return nil, "other", failure("unsupported-statement")
	}
	create := ts[0].is("CREATE")
	kindAt := 1
	if create {
		for kindAt < len(ts) && (ts[kindAt].is("OR") || ts[kindAt].is("REPLACE") || ts[kindAt].is("TEMP") || ts[kindAt].is("TEMPORARY") || ts[kindAt].is("UNLOGGED") || ts[kindAt].is("UNIQUE")) {
			kindAt++
		}
	}
	if kindAt >= len(ts) {
		return nil, "other", failure("unsupported-statement")
	}
	if create && ts[kindAt].is("TABLE") {
		i := kindAt + 1
		if i+2 < len(ts) && ts[i].is("IF") && ts[i+1].is("NOT") && ts[i+2].is("EXISTS") {
			i += 3
		}
		table, n, err := relationFact(ts[i:], "table", data, dialect)
		if err != nil {
			return nil, "create-table", err
		}
		i += n
		if i >= len(ts) || !ts[i].is("(") {
			return nil, "create-table", failure("unsupported-statement")
		}
		end := matching(ts, i)
		if end < 0 {
			return nil, "create-table", failure("unsupported-statement")
		}
		out := []Fact{table}
		for _, part := range split(ts[i+1 : end]) {
			if len(part) == 0 {
				continue
			}
			if part[0].is("LIKE") {
				if len(out) > 1 {
					return nil, "create-table", failure("unsupported-statement")
				}
				continue
			}
			if part[0].is("CONSTRAINT") || part[0].is("PRIMARY") || part[0].is("FOREIGN") || part[0].is("UNIQUE") || part[0].is("CHECK") || part[0].is("EXCLUDE") || part[0].is("KEY") || part[0].is("INDEX") {
				f, err := constraint(part, table.Name, data, dialect)
				if err != nil {
					return nil, "create-table", err
				}
				out = append(out, f)
				continue
			}
			if len(part) < 2 || !part[0].identifier() || !part[1].identifier() {
				return nil, "create-table", failure("unsupported-statement")
			}
			col := fact(part[0], "column", data, dialect)
			col.Relation = table.Name
			out = append(out, col)
			for j := 2; j < len(part); j++ {
				if part[j].is("PRIMARY") && j+1 < len(part) && part[j+1].is("KEY") || part[j].is("UNIQUE") {
					k := "unique"
					last := j
					if part[j].is("PRIMARY") {
						k = "primary_key"
						last++
					}
					t := token{Value: fmt.Sprintf("@%d", part[j].Start), Kind: "word", Start: part[j].Start, End: part[last].End}
					f := fact(t, k, data, dialect)
					f.Relation = table.Name
					f.Columns = []string{col.Name}
					out = append(out, f)
				}
			}
		}
		// Partition/expression/type clauses remain opaque source syntax, never
		// materialized partitions or evaluated/generated values.
		return out, "create-table", nil
	}
	if ts[0].is("ALTER") && ts[1].is("TABLE") {
		i := 2
		if i < len(ts) && ts[i].is("ONLY") {
			i++
		}
		table, n, err := relationFact(ts[i:], "table", data, dialect)
		if err != nil {
			return nil, "alter-table", err
		}
		i += n
		if i >= len(ts) || !ts[i].is("ADD") {
			return nil, "alter-table", failure("unsupported-statement")
		}
		i++
		f, err := constraint(ts[i:], table.Name, data, dialect)
		if err != nil {
			return nil, "alter-table", err
		}
		return []Fact{f}, "alter-table", nil
	}
	if create && ts[kindAt].is("INDEX") {
		i := kindAt + 1
		if i < len(ts) && ts[i].is("CONCURRENTLY") {
			i++
		}
		if i+2 < len(ts) && ts[i].is("IF") && ts[i+1].is("NOT") && ts[i+2].is("EXISTS") {
			i += 3
		}
		f, n, err := relationFact(ts[i:], "index", data, dialect)
		if err != nil {
			return nil, "create-index", err
		}
		rest := after(ts[i+n:], "ON")
		if len(rest) > 0 && rest[0].is("ONLY") {
			rest = rest[1:]
		}
		table, _, count, ok := qualified(rest)
		if !ok {
			return nil, "create-index", failure("unsupported-statement")
		}
		f.Relation = table.Value
		open := index(rest[count:], "(")
		if open < 0 {
			return nil, "create-index", failure("unsupported-statement")
		}
		open += count
		close := matching(rest, open)
		f.Columns, f.PrefixLengths, err = listNames(rest[open+1 : close])
		if err != nil {
			return nil, "create-index", err
		}
		f.PrefixLengths = nil
		return []Fact{f}, "create-index", nil
	}
	if create && (ts[kindAt].is("TYPE") || ts[kindAt].is("SEQUENCE")) {
		k := "sequence"
		if ts[kindAt].is("TYPE") {
			if index(ts, "ENUM") >= 0 {
				k = "enum"
			} else if index(ts, "RANGE") >= 0 {
				k = "range_type"
			} else {
				return nil, "create-type", failure("unsupported-statement")
			}
		}
		f, _, err := relationFact(ts[kindAt+1:], k, data, dialect)
		return []Fact{f}, "create-" + k, err
	}
	if create && index(ts, "VIEW") > 0 {
		i := index(ts, "VIEW") + 1
		f, n, err := relationFact(ts[i:], "view", data, dialect)
		if err != nil {
			return nil, "create-view", err
		}
		i += n
		out := []Fact{f}
		if i < len(ts) && ts[i].is("(") {
			close := matching(ts, i)
			for _, part := range split(ts[i+1 : close]) {
				if len(part) != 1 || !part[0].identifier() {
					return nil, "create-view", failure("unsupported-statement")
				}
				col := fact(part[0], "view_column", data, dialect)
				col.Relation = f.Name
				out = append(out, col)
			}
			return out, "create-view", nil
		}
		selectAt := index(ts[i:], "SELECT")
		if selectAt < 0 {
			return nil, "create-view", failure("unsupported-statement")
		}
		selectAt += i
		from := index(ts[selectAt+1:], "FROM")
		end := len(ts)
		if from >= 0 {
			end = selectAt + 1 + from
		}
		// A constant stand-in view in a captured dump is scaffolding, not
		// another current declaration. The final view has real FROM sources.
		if dialect == "mysql" && from < 0 {
			return nil, "temporary-view", nil
		}
		for _, part := range split(ts[selectAt+1 : end]) {
			a := index(part, "AS")
			var name token
			if a >= 0 && a+1 < len(part) && part[a+1].identifier() {
				name = part[a+1]
			} else if len(part) == 1 && part[0].identifier() {
				name = part[0]
			} else {
				return nil, "create-view", failure("unsupported-statement")
			}
			col := fact(name, "view_column", data, dialect)
			col.Relation = f.Name
			out = append(out, col)
		}
		return out, "create-view", nil
	}
	return nil, "other", failure("unsupported-statement")
}

// A syntax category is not an emitted fact. It is only the independent imported
// frontend's statement-root admission witness, compared before publication.
func category(ts []token, plane string) string {
	if len(ts) == 0 {
		return "empty"
	}
	if ts[0].Kind == "meta" {
		return "wrapper"
	}
	if ts[0].is("DO") || ts[0].is("DELIMITER") || ts[0].is("CREATE") && (index(ts, "FUNCTION") >= 0 || index(ts, "PROCEDURE") >= 0 || index(ts, "TRIGGER") >= 0) {
		return "procedural"
	}
	if ts[0].is("SELECT") || ts[0].is("WITH") || ts[0].is("INSERT") || ts[0].is("UPDATE") || ts[0].is("DELETE") {
		if plane == "authored-query" {
			return "query"
		}
		return "other"
	}
	if ts[0].is("CREATE") {
		if index(ts, "TABLE") > 0 {
			return "create-table"
		}
		if index(ts, "INDEX") > 0 {
			return "create-index"
		}
		if index(ts, "VIEW") > 0 {
			return "create-view"
		}
		if len(ts) > 1 && ts[1].is("TYPE") {
			return "create-type"
		}
		if len(ts) > 1 && ts[1].is("SEQUENCE") {
			return "create-sequence"
		}
	}
	if ts[0].is("ALTER") && len(ts) > 1 && ts[1].is("TABLE") {
		return "alter-table"
	}
	if ts[0].is("DROP") {
		return "drop"
	}
	if ts[0].is("SET") {
		return "wrapper"
	}
	return "other"
}

func migration(ts []token, data []byte, dialect, direction string) ([]Fact, error) {
	if len(ts) < 3 {
		return nil, failure("unsupported-statement")
	}
	predicate := ""
	var f Fact
	var err error
	if ts[0].is("CREATE") || ts[0].is("DROP") {
		i := 1
		if ts[i].is("UNIQUE") {
			i++
		}
		if i >= len(ts) {
			return nil, failure("unsupported-statement")
		}
		kind := strings.ToUpper(ts[i].Value)
		if kind != "TABLE" && kind != "TYPE" && kind != "INDEX" {
			return nil, failure("unsupported-statement")
		}
		i++
		for i < len(ts) && (ts[i].is("IF") || ts[i].is("NOT") || ts[i].is("EXISTS") || ts[i].is("CONCURRENTLY")) {
			i++
		}
		f, _, err = relationFact(ts[i:], "", data, dialect)
		action := "CREATES"
		if ts[0].is("DROP") {
			action = "DROPS"
		}
		predicate = "MIGRATION_" + action + "_" + kind
	} else if ts[0].is("ALTER") && ts[1].is("TABLE") {
		i := 2
		if ts[i].is("ONLY") {
			i++
		}
		table, n, e := relationFact(ts[i:], "", data, dialect)
		if e != nil {
			return nil, e
		}
		i += n
		if i >= len(ts) {
			return nil, failure("unsupported-statement")
		}
		action := ts[i]
		i++
		if action.is("ADD") && i < len(ts) && (ts[i].is("CONSTRAINT") || ts[i].is("FOREIGN")) {
			f, err = constraint(ts[i:], table.Name, data, dialect)
			if err == nil && f.Kind != "foreign_key" {
				err = failure("unsupported-statement")
			}
			predicate = "MIGRATION_ADDS_FOREIGN_KEY"
			f.Kind = ""
		} else if action.is("ADD") || action.is("DROP") {
			if i < len(ts) && ts[i].is("COLUMN") {
				i++
			}
			for i < len(ts) && (ts[i].is("IF") || ts[i].is("NOT") || ts[i].is("EXISTS")) {
				i++
			}
			f, _, err = relationFact(ts[i:], "", data, dialect)
			f.Relation = table.Name
			f.Columns = []string{f.Name}
			predicate = "MIGRATION_ADDS_COLUMN"
			if action.is("DROP") {
				predicate = "MIGRATION_DROPS_COLUMN"
			}
		} else {
			return nil, failure("unsupported-statement")
		}
	} else {
		return nil, failure("unsupported-statement")
	}
	if err != nil {
		return nil, err
	}
	f.Predicate = predicate
	f.Direction = direction
	return []Fact{f}, nil
}

// querySites is a deliberately bounded lexical relation grammar. It walks
// explicit query scopes and subqueries, never arbitrary procedural strings.
func querySites(ts []token, data []byte, dialect string) ([]Fact, error) {
	var out []Fact
	var walk func([]token, map[string]bool, int) error
	walk = func(q []token, inherited map[string]bool, depth int) error {
		if depth > MaxDepth {
			return failure("resource-limit")
		}
		if len(q) == 0 {
			return nil
		}
		ctes := map[string]bool{}
		for k, v := range inherited {
			ctes[k] = v
		}
		main := 0
		if q[0].is("WITH") {
			i := 1
			if i < len(q) && q[i].is("RECURSIVE") {
				i++
			}
			for {
				if i >= len(q) || !q[i].identifier() {
					return failure("unsupported-statement")
				}
				ctes[normalized(q[i], dialect)] = true
				i++
				if i < len(q) && q[i].is("(") {
					i = matching(q, i) + 1
				}
				if i >= len(q) || !q[i].is("AS") {
					return failure("unsupported-statement")
				}
				i++
				if i < len(q) && q[i].is("NOT") {
					i++
				}
				if i < len(q) && q[i].is("MATERIALIZED") {
					i++
				}
				if i >= len(q) || !q[i].is("(") {
					return failure("unsupported-statement")
				}
				end := matching(q, i)
				if err := walk(q[i+1:end], ctes, depth+1); err != nil {
					return err
				}
				i = end + 1
				if i < len(q) && q[i].is(",") {
					i++
					continue
				}
				main = i
				break
			}
		}
		if main >= len(q) {
			return failure("unsupported-statement")
		}
		op := q[main]
		if !op.is("SELECT") && !op.is("INSERT") && !op.is("UPDATE") && !op.is("DELETE") {
			return failure("unsupported-statement")
		}
		if index(q, "$") >= 0 && index(q, "{") >= 0 {
			return failure("dynamic-identifier")
		}
		aliases := map[string]int{}
		var deleteTargets []string
		if op.is("DELETE") && dialect == "mysql" && main+1 < len(q) && !q[main+1].is("FROM") {
			for i := main + 1; i < len(q) && !q[i].is("FROM"); i++ {
				if q[i].identifier() {
					deleteTargets = append(deleteTargets, normalized(q[i], dialect))
				}
			}
		}
		addRelation := func(i int, access string) (int, error) {
			for i < len(q) && (q[i].is("ONLY") || q[i].is("LATERAL")) {
				i++
			}
			if i >= len(q) {
				return i, failure("unsupported-statement")
			}
			if q[i].is("(") {
				return i - 1, nil
			}
			f, n, err := relationFact(q[i:], "", data, dialect)
			if err != nil {
				return i, err
			}
			end := i + n
			if end < len(q) && q[end].is("(") && access == "read" {
				return end - 1, nil
			} // table-valued function, not a relation
			name, _, _, _ := qualified(q[i:])
			alias := normalized(name, dialect)
			if end < len(q) && q[end].is("AS") {
				end++
				if end < len(q) && q[end].identifier() {
					alias = normalized(q[end], dialect)
					end++
				}
			} else if end < len(q) && q[end].identifier() && !keyword(q[end]) {
				alias = normalized(q[end], dialect)
				end++
			}
			if f.Schema == "" && ctes[normalized(name, dialect)] {
				return end - 1, nil
			}
			f.Access = access
			out = append(out, f)
			aliases[alias] = len(out) - 1
			aliases[normalized(name, dialect)] = len(out) - 1
			return end - 1, nil
		}
		fromList := false
		targetDone := false
		for i := main; i < len(q); i++ {
			if q[i].is("(") {
				end := matching(q, i)
				inner := q[i+1 : end]
				if len(inner) > 0 && (inner[0].is("SELECT") || inner[0].is("WITH") || inner[0].is("INSERT") || inner[0].is("UPDATE") || inner[0].is("DELETE")) {
					if err := walk(inner, ctes, depth+1); err != nil {
						return err
					}
				} else {
					if err := nestedQueries(inner, ctes, depth+1, walk); err != nil {
						return err
					}
				}
				i = end
				continue
			}
			if i == main && op.is("UPDATE") {
				next, err := addRelation(i+1, "read-write")
				if err != nil {
					return err
				}
				i = next
				targetDone = true
				continue
			}
			if op.is("INSERT") && q[i].is("INTO") && !targetDone {
				access := "write"
				if index(q, "CONFLICT") >= 0 && index(q, "UPDATE") >= 0 || index(q, "DUPLICATE") >= 0 && index(q, "UPDATE") >= 0 {
					access = "read-write"
				}
				next, err := addRelation(i+1, access)
				if err != nil {
					return err
				}
				i = next
				targetDone = true
				continue
			}
			intro := q[i].is("FROM") || q[i].is("JOIN") || op.is("DELETE") && q[i].is("USING")
			if intro || fromList && q[i].is(",") {
				access := "read"
				if op.is("DELETE") && !targetDone && len(deleteTargets) == 0 {
					access = "read-write"
					targetDone = true
				}
				next, err := addRelation(i+1, access)
				if err != nil {
					return err
				}
				i = next
				fromList = true
				continue
			}
			if q[i].is("WHERE") || q[i].is("GROUP") || q[i].is("ORDER") || q[i].is("HAVING") || q[i].is("LIMIT") || q[i].is("RETURNING") || q[i].is("SET") || q[i].is("ON") {
				fromList = false
			}
		}
		for _, target := range deleteTargets {
			at, ok := aliases[target]
			if !ok {
				return failure("dynamic-identifier")
			}
			out[at].Access = "read-write"
		}
		return nil
	}
	if err := walk(ts, nil, 0); err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, f := range out {
		if seen[f.Start] {
			return nil, errors.New("duplicate relation occurrence")
		}
		seen[f.Start] = true
	}
	return out, nil
}
func nestedQueries(q []token, ctes map[string]bool, depth int, walk func([]token, map[string]bool, int) error) error {
	for i := 0; i < len(q); i++ {
		if !q[i].is("(") {
			continue
		}
		end := matching(q, i)
		inner := q[i+1 : end]
		if len(inner) > 0 && (inner[0].is("SELECT") || inner[0].is("WITH")) {
			if err := walk(inner, ctes, depth); err != nil {
				return err
			}
		} else if err := nestedQueries(inner, ctes, depth+1, walk); err != nil {
			return err
		}
		i = end
	}
	return nil
}
func keyword(t token) bool {
	if t.Quoted {
		return false
	}
	switch strings.ToUpper(t.Value) {
	case "WHERE", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "CROSS", "FULL", "ON", "USING", "SET", "GROUP", "ORDER", "BY", "LIMIT", "OFFSET", "HAVING", "RETURNING", "UNION", "FOR", "WINDOW", "VALUES", "SELECT", "CONFLICT", "DO", "AS", "USE", "FORCE", "IGNORE", "INDEX", "STRAIGHT_JOIN":
		return true
	}
	return false
}
