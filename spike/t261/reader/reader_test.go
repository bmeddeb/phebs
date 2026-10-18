package reader

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func input(t *testing.T, p *Pipeline, sql, class string) BlobResult {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "fixture.sql")
	if err := os.WriteFile(path, []byte(sql), 0600); err != nil {
		t.Fatal(err)
	}
	return p.Read(root, Request{ID: "test", Path: "fixture.sql", SHA256: digest([]byte(sql)), Dialect: p.Dialect, Engine: p.Dialect, Class: class, Lineage: "neutral"})
}
func TestAtomicAdmission(t *testing.T) {
	for _, dialect := range []string{"postgresql", "mysql"} {
		for _, tail := range []string{"\xff", "'unclosed", "/*unclosed", "("} {
			t.Run(dialect+tail, func(t *testing.T) {
				p := Pipeline{Dialect: dialect}
				r := input(t, &p, "CREATE TABLE admitted (id INT);"+tail, "authored-schema")
				if r.Decision != "refuse" || len(r.Statements) != 0 {
					t.Fatalf("partial invalid admission: %#v", r)
				}
			})
		}
	}
}
func TestDumpDataRefusesEarlierDeclarations(t *testing.T) {
	for _, dialect := range []string{"postgresql", "mysql"} {
		for _, stmt := range []string{"INSERT INTO admitted VALUES(1);", "COPY admitted FROM stdin;", "REPLACE INTO admitted VALUES(1);", "LOAD DATA INFILE 'x' INTO TABLE admitted;"} {
			t.Run(dialect+stmt, func(t *testing.T) {
				p := Pipeline{Dialect: dialect}
				r := input(t, &p, "CREATE TABLE admitted(id INT);"+stmt, "captured-dump")
				if r.Reason != "data-section" || len(r.Statements) != 0 {
					t.Fatalf("dump not atomically refused: %#v", r)
				}
			})
		}
	}
}
func TestVersionCommentsAndOffsets(t *testing.T) {
	sql := "/*!50001 CREATE ALGORITHM=UNDEFINED */\n/*!50001 VIEW `v` AS select `t`.`id` AS `id` from `t` where (`t`.`id`>=1) */;"
	ss, err := lex([]byte(sql), "mysql")
	if err != nil || len(ss) != 1 {
		t.Fatalf("lex: %v %d", err, len(ss))
	}
	transformed := executableSQL([]byte(sql), ss[0])
	if len(transformed) == 0 {
		t.Fatal("empty sql")
	}
	facts, _, err := declarations(stripSemi(ss[0].Tokens), []byte(sql), "mysql")
	if err != nil || len(facts) != 2 {
		t.Fatalf("declarations: %v %#v", err, facts)
	}
	for _, f := range facts {
		if digest([]byte(sql)[f.Start:f.End]) != f.SHA256 {
			t.Fatal("offset mismatch")
		}
	}
}
func TestReadWriteScopes(t *testing.T) {
	cases := []struct {
		sql           string
		names, access []string
	}{
		{"WITH picked AS (SELECT * FROM parent) SELECT * FROM picked JOIN child ON true;", []string{"parent", "child"}, []string{"read", "read"}},
		{"INSERT INTO child(id) SELECT id FROM parent;", []string{"child", "parent"}, []string{"write", "read"}},
		{"UPDATE child SET id=(SELECT id FROM parent);", []string{"child", "parent"}, []string{"read-write", "read"}},
		{"DELETE FROM child USING parent WHERE child.id=parent.id;", []string{"child", "parent"}, []string{"read-write", "read"}},
		{"SELECT * FROM (SELECT * FROM parent) p JOIN generate_series(1,2) g ON true;", []string{"parent"}, []string{"read"}},
		{"SELECT extract(day FROM now()),(SELECT id FROM parent) FROM child;", []string{"parent", "child"}, []string{"read", "read"}},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			ss, err := lex([]byte(c.sql), "postgresql")
			if err != nil {
				t.Fatal(err)
			}
			facts, err := querySites(stripSemi(ss[0].Tokens), []byte(c.sql), "postgresql")
			if err != nil {
				t.Fatal(err)
			}
			var names, access []string
			for _, f := range facts {
				names = append(names, f.Name)
				access = append(access, f.Access)
			}
			if !reflect.DeepEqual(names, c.names) || !reflect.DeepEqual(access, c.access) {
				t.Fatalf("got %v/%v want %v/%v", names, access, c.names, c.access)
			}
		})
	}
}
func TestMySQLDeleteAliases(t *testing.T) {
	sql := "DELETE c FROM child c JOIN parent p ON p.id=c.id;"
	ss, err := lex([]byte(sql), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	facts, err := querySites(stripSemi(ss[0].Tokens), []byte(sql), "mysql")
	if err != nil || len(facts) != 2 || facts[0].Access != "read-write" || facts[1].Access != "read" {
		t.Fatalf("alias resolution: %#v %v", facts, err)
	}
}
func TestFKOrderAndMultiplicity(t *testing.T) {
	sql := "CREATE TABLE c(x INT,y INT,CONSTRAINT a FOREIGN KEY(x,y) REFERENCES p(a,b),CONSTRAINT b FOREIGN KEY(y,x) REFERENCES p(a,b));"
	p := Pipeline{Dialect: "postgresql"}
	r := input(t, &p, sql, "authored-schema")
	if r.Decision != "admit" {
		t.Fatalf("%#v", r)
	}
	fs := r.Statements[0].Objects
	if len(fs) != 5 || fs[3].Name != "a" || fs[4].Name != "b" || !reflect.DeepEqual(fs[4].Columns, []string{"y", "x"}) {
		t.Fatalf("lost FK identity/order: %#v", fs)
	}
}
func TestProceduralDoesNotLeak(t *testing.T) {
	for _, tc := range []struct{ d, s string }{{"postgresql", "DO $x$ BEGIN EXECUTE 'CREATE TABLE secret(id int)'; END $x$;"}, {"mysql", "DELIMITER $$\nCREATE PROCEDURE p() BEGIN CREATE TABLE secret(id INT); END$$\nDELIMITER ;"}} {
		p := Pipeline{Dialect: tc.d}
		r := input(t, &p, tc.s, "authored-schema")
		if r.Decision != "gap" || r.Reason != "procedural-sql" {
			t.Fatalf("procedural: %#v", r)
		}
		for _, s := range r.Statements {
			if len(s.Objects)+len(s.Sites) > 0 {
				t.Fatal("procedural leak")
			}
		}
	}
}
func TestNoParserFallback(t *testing.T) {
	p := Pipeline{Dialect: "postgresql", Frontend: func(string, string) (string, error) { return "refused", failure("unsupported") }}
	r := input(t, &p, "CREATE TABLE t(id INT);", "authored-schema")
	if r.Decision != "gap" || r.Statements[0].Gap != "parser-gap" || len(r.Statements[0].Objects) > 0 {
		t.Fatal("imported gate bypassed")
	}
}

// Review controls are deliberately separate from the frozen60-case inventory.
func TestReviewAtomicCatalogOverflow(t *testing.T) {
	p := Pipeline{Dialect: "postgresql"}
	if r := input(t, &p, "CREATE TABLE prior(id INT);", "authored-schema"); r.Decision != "admit" {
		t.Fatalf("prior: %#v", r)
	}
	p.identities = 16383
	beforeCount, beforeBytes := p.schemaCount, p.schemaBytes
	r := input(t, &p, "CREATE TABLE would_leak(id INT); CREATE TABLE overflow(id INT);", "authored-schema")
	if r.Decision != "refuse" || r.Reason != "resource-limit" || len(r.Statements) != 0 {
		t.Fatalf("overflow: %#v", r)
	}
	if p.identities != 16383 || p.schemaCount != beforeCount || p.schemaBytes != beforeBytes {
		t.Fatal("refused blob mutated admission counters")
	}
	q := input(t, &p, "SELECT * FROM would_leak;", "authored-query")
	if len(q.Statements) != 1 || len(q.Statements[0].Sites) != 1 || q.Statements[0].Sites[0].Binding != "missing" {
		t.Fatalf("refused catalog prefix leaked: %#v", q)
	}
}

func TestReviewMixedHeaderOrders(t *testing.T) {
	for _, header := range []string{"-- MySQL dump\n-- PostgreSQL database dump\n", "-- PostgreSQL database dump\n-- MariaDB dump\n"} {
		p := Pipeline{Dialect: "postgresql"}
		r := input(t, &p, header+"CREATE TABLE t(id INT);", "captured-dump")
		if r.Reason != "metadata-conflict" || len(r.Statements) != 0 {
			t.Fatalf("conflicting header admitted: %#v", r)
		}
	}
}
