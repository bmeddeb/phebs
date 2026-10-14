//go:build t581oracle

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// In-memory corpus harness
// ---------------------------------------------------------------------------

const testCommit = "0123456789abcdef0123456789abcdef01234567"

// memCorpus is a synthetic in-memory corpus: path -> exact bytes. It never
// touches a real repository, so every GD rule is pinned against source this
// test file authored.
type memCorpus map[string]string

func (m memCorpus) open(_, _ string, paths []string) (blobReader, error) {
	return &memReader{files: m, paths: paths}, nil
}

type memReader struct {
	files memCorpus
	paths []string
	i     int
}

func (r *memReader) next(path string) ([]byte, error) {
	if r.i >= len(r.paths) {
		return nil, fmt.Errorf("%s: read past the end of the requested order", path)
	}
	want := r.paths[r.i]
	r.i++
	if want != path {
		return nil, fmt.Errorf("out-of-order read: got %q, want %q", path, want)
	}
	content, ok := r.files[path]
	if !ok {
		return nil, fmt.Errorf("%s: absent from the in-memory corpus", path)
	}
	return []byte(content), nil
}

func (r *memReader) close() error {
	if r.i != len(r.paths) {
		return fmt.Errorf("closed after %d of %d reads", r.i, len(r.paths))
	}
	return nil
}

// scanResult is every record one unit emitted, in emission order.
type scanResult struct {
	rows       []row
	aggregates []outOfDomainAggregate
	// kinds labels each record in emission order, so a test can pin that the
	// aggregate follows every site row of its unit rather than interleaving
	// with them.
	kinds []string
}

// scanWith collects one unit's records from any corpus and enforces the
// invariants that hold for every unit whatever the fixture: the site-row schema
// and unit name, exactly one out-of-domain aggregate, that aggregate last, and
// the aggregate's internal consistency.
func scanWith(t *testing.T, u unitRecord, src corpus) scanResult {
	t.Helper()
	var got scanResult
	if _, err := scanUnit(u, src, func(rec any) {
		switch v := rec.(type) {
		case row:
			got.rows = append(got.rows, v)
			got.kinds = append(got.kinds, "site")
		case outOfDomainAggregate:
			got.aggregates = append(got.aggregates, v)
			got.kinds = append(got.kinds, "aggregate")
		default:
			t.Fatalf("unknown record type %T", rec)
		}
	}); err != nil {
		t.Fatalf("scanUnit: %v", err)
	}
	for i, r := range got.rows {
		if r.Schema != siteSchema {
			t.Errorf("row %d schema = %q, want %q", i, r.Schema, siteSchema)
		}
		if r.Unit != u.Unit {
			t.Errorf("row %d unit = %q, want %q", i, r.Unit, u.Unit)
		}
		if !slices.Contains(siteStates, r.State) {
			t.Errorf("row %d state = %q, which is not in the site vocabulary %v", i, r.State, siteStates)
		}
	}
	// One aggregate per unit, whatever the unit contains: §6's disclosure is
	// per unit, so a unit that scanned successfully and published no aggregate
	// would report no out-of-domain count at all rather than a zero.
	if len(got.aggregates) != 1 {
		t.Fatalf("got %d out-of-domain aggregates, want exactly one: %+v", len(got.aggregates), got.aggregates)
	}
	if last := got.kinds[len(got.kinds)-1]; last != "aggregate" {
		t.Errorf("the unit's last record is %q, want the aggregate: %v", last, got.kinds)
	}
	a := got.aggregates[0]
	if a.Schema != outOfDomainSchema {
		t.Errorf("aggregate schema = %q, want %q", a.Schema, outOfDomainSchema)
	}
	if a.Unit != u.Unit {
		t.Errorf("aggregate unit = %q, want %q", a.Unit, u.Unit)
	}
	sum := 0
	for reason, n := range a.ByReason {
		if n < 0 {
			t.Errorf("by_reason[%q] = %d, want a count", reason, n)
		}
		sum += n
	}
	if sum != a.Total {
		t.Errorf("total = %d but by_reason sums to %d (%v)", a.Total, sum, a.ByReason)
	}
	// Every known cause is present even at zero, so a zero count can never be
	// confused with an absent key, and no unknown cause can appear.
	if len(a.ByReason) != len(outOfDomainReasons) {
		t.Errorf("by_reason has %d keys %v, want exactly %v", len(a.ByReason), a.ByReason, outOfDomainReasons)
	}
	for _, reason := range outOfDomainReasons {
		if _, ok := a.ByReason[reason]; !ok {
			t.Errorf("by_reason is missing %q", reason)
		}
	}
	if (a.FilesWithCandidates == 0) != (a.Total == 0) {
		t.Errorf("files_with_candidates = %d with total = %d: both are zero or neither is",
			a.FilesWithCandidates, a.Total)
	}
	if a.FilesWithCandidates > a.Total {
		t.Errorf("files_with_candidates = %d exceeds total = %d", a.FilesWithCandidates, a.Total)
	}
	return got
}

// runUnit scans one synthetic unit. stub_files is derived from the GD1 suffix
// unless declared is non-nil, which lets a test pin a disagreement with it.
func runUnit(t *testing.T, files map[string]string, declared []string) scanResult {
	t.Helper()
	return scanWith(t, unitFor(files, "example/repo", declared), memCorpus(files))
}

// wantOutOfDomain states the single aggregate a unit must publish. Its zero
// value is the ordinary case: a unit with sites and no out-of-domain candidate
// still publishes total 0, which is what makes absence distinguishable from a
// missing record.
type wantOutOfDomain struct {
	total     int
	unindexed int
	zeroArgs  int
	files     int
}

func assertOutOfDomain(t *testing.T, got outOfDomainAggregate, want wantOutOfDomain) {
	t.Helper()
	if got.Total != want.total {
		t.Errorf("out-of-domain total = %d, want %d", got.Total, want.total)
	}
	wantReasons := map[string]int{
		reasonUnindexedSelector: want.unindexed,
		reasonZeroArguments:     want.zeroArgs,
	}
	if !reflect.DeepEqual(got.ByReason, wantReasons) {
		t.Errorf("by_reason = %v, want %v", got.ByReason, wantReasons)
	}
	if got.FilesWithCandidates != want.files {
		t.Errorf("files_with_candidates = %d, want %d", got.FilesWithCandidates, want.files)
	}
}

func unitFor(files map[string]string, unit string, declared []string) unitRecord {
	goFiles := make([]string, 0, len(files))
	for p := range files {
		goFiles = append(goFiles, p)
	}
	sort.Strings(goFiles)
	stubs := declared
	if stubs == nil {
		for _, p := range goFiles {
			if isStubPath(p) {
				stubs = append(stubs, p)
			}
		}
	}
	return unitRecord{
		Schema: unitSchema, Unit: unit, Repository: unit, Commit: testCommit,
		Dir: "example__repo", GoFiles: goFiles, StubFiles: stubs,
	}
}

// key renders a row the way the table below states expectations: the scored
// state plus the recorded (unscored) object and candidate count.
func key(r row) string {
	return fmt.Sprintf("%s %s %s %d", r.Path, r.State, r.Object, r.CandidateCount)
}

func keys(rows []row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, key(r))
	}
	return out
}

// assertRows compares rendered site rows against the expectation, in order.
func assertRows(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d site rows, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// desc is a stand-in for grpc.ServiceDesc so a fixture needs no import that
// resolves: GD3 keys on the identifier ServiceName, not on any package.
const descType = "type desc struct {\n\tServiceName string\n\tMethodName  string\n}\n\n"

// ---------------------------------------------------------------------------
// GD1-GD16 rule table
// ---------------------------------------------------------------------------

func TestOracleAppliesTheFrozenRules(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		// want lists the site rows in emission order. GD10's out-of-domain
		// candidates are not rows, so a case states them in ood instead; ood's
		// zero value means the unit publishes one aggregate with total 0.
		want []string
		ood  wantOutOfDomain
	}{
		{
			// GD3, GD4, GD8: one method entry bears the selector name.
			name: "GD8 resolved call",
			files: map[string]string{
				"api/svc_grpc.pb.go": "package api\n\n" + descType +
					"var d = desc{ServiceName: \"api.Svc\", MethodName: \"Say\"}\n\n" +
					"const full = \"/api.Svc/Say\"\n\n" +
					"func RegisterSvcServer(s any, impl any) {}\n",
				"client.go": "package client\n\nfunc call(x any) {\n\tx.Say(nil, nil)\n}\n",
			},
			want: []string{"client.go resolved_call /api.Svc/Say 0"},
		},
		{
			// GD4 anchoring: only the full-method literal indexes, so the call
			// is an unindexed-selector candidate that GD10 keeps out of the
			// rows and the aggregate counts instead.
			name: "GD4 anchoring keeps bare and decorated strings out",
			files: map[string]string{
				"api/svc_grpc.pb.go": "package api\n\n" + descType +
					"var d = desc{ServiceName: \"api.Svc\", MethodName: \"Say\"}\n\n" +
					"const bare = \"Say\"\nconst prefixed = \"prefix/api.Svc/Say\"\n" +
					"const suffixed = \"/api.Svc/Say/suffix\"\nconst two = \"/a/b/api.Svc/Say\"\n",
				"client.go": "package client\n\nfunc call(x any) {\n\tx.Say(nil, nil)\n}\n",
			},
			want: nil,
			ood:  wantOutOfDomain{total: 1, unindexed: 1, files: 1},
		},
		{
			// GD8: two entries bear the name, in two services.
			name: "GD8 ambiguous call across two services",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Fetch\"\n",
				"b/svc_grpc.pb.go": "package b\n\n" + descType +
					"var d = desc{ServiceName: \"b.Svc\"}\n\nconst full = \"/b.Svc/Fetch\"\n",
				"c.go": "package c\n\nfunc call(x any) {\n\tx.Fetch(nil, nil)\n}\n",
			},
			want: []string{"c.go ambiguous_call Fetch 2"},
		},
		{
			// GD6: the stub path is part of the dedup key, so one service full
			// name reached through two stubs is two competing entries.
			name: "GD6 same method through two stubs is ambiguous",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nconst full = \"/s.Svc/Do\"\n",
				"b/svc_grpc.pb.go": "package b\n\nconst full = \"/s.Svc/Do\"\n",
				"c.go":             "package c\n\nfunc call(x any) {\n\tx.Do(nil, nil)\n}\n",
			},
			want: []string{"c.go ambiguous_call Do 2"},
		},
		{
			// GD6: repeated occurrences of one literal in one stub deduplicate.
			name: "GD6 repeated literal in one stub stays resolved",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nconst one = \"/s.Svc/Do\"\nconst two = \"/s.Svc/Do\"\n" +
					"var three = \"/s.Svc/Do\"\n",
				"c.go": "package c\n\nfunc call(x any) {\n\tx.Do(nil, nil)\n}\n",
			},
			want: []string{"c.go resolved_call /s.Svc/Do 0"},
		},
		{
			// GD11, GD12: an Ident call and a SelectorExpr call both register.
			name: "GD12 resolved registration by identifier and by selector",
			files: map[string]string{
				"api/svc_grpc.pb.go": "package api\n\n" + descType +
					"var d = desc{ServiceName: \"api.Svc\"}\n\nfunc RegisterSvcServer(s any, impl any) {}\n",
				"server.go": "package server\n\nimport \"example.com/api\"\n\n" +
					"func register(s any, impl any) {\n\tRegisterSvcServer(s, impl)\n\tapi.RegisterSvcServer(s, impl)\n}\n",
			},
			want: []string{
				"server.go resolved_registration api.Svc 0",
				"server.go resolved_registration api.Svc 0",
			},
		},
		{
			// §1: ambiguous registration including one service full name
			// reached through two stubs.
			name: "GD12 ambiguous registration through two stubs",
			files: map[string]string{
				"x/svc_grpc.pb.go": "package x\n\n" + descType +
					"var d = desc{ServiceName: \"api.Svc\"}\n\nfunc RegisterSvcServer(s any, impl any) {}\n",
				"y/svc_grpc.pb.go": "package y\n\n" + descType +
					"var d = desc{ServiceName: \"api.Svc\"}\n\nfunc RegisterSvcServer(s any, impl any) {}\n",
				"s.go": "package s\n\nfunc register(a any, b any) {\n\tRegisterSvcServer(a, b)\n}\n",
			},
			want: []string{"s.go ambiguous_registration RegisterSvcServer 2"},
		},
		{
			// GD11 binds on the short name, so two services sharing one short
			// name in one stub compete.
			name: "GD11 two services sharing a short name are ambiguous",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var one = desc{ServiceName: \"a.Svc\"}\nvar two = desc{ServiceName: \"b.Svc\"}\n\n" +
					"func RegisterSvcServer(s any, impl any) {}\n",
				"c.go": "package c\n\nfunc register(a any, b any) {\n\tRegisterSvcServer(a, b)\n}\n",
			},
			want: []string{"c.go ambiguous_registration RegisterSvcServer 2"},
		},
		{
			// GD9: registration is tested first for a selector expression.
			name: "GD9 registration beats call for a colliding name",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Foo\"}\n\nconst full = \"/a.Foo/Do\"\n\n" +
					"func RegisterFooServer(s any, impl any) {}\n",
				"b/svc_grpc.pb.go": "package b\n\nconst odd = \"/b.Thing/RegisterFooServer\"\n" +
					"const plain = \"/b.Thing/Plain\"\n",
				"c.go": "package c\n\nfunc f(x any) {\n\tx.RegisterFooServer(nil, nil)\n" +
					"\tRegisterFooServer(nil, nil)\n\tx.Plain(nil, nil)\n}\n",
			},
			want: []string{
				"c.go resolved_registration a.Foo 0",
				"c.go resolved_registration a.Foo 0",
				"c.go resolved_call /b.Thing/Plain 0",
			},
		},
		{
			// GD5: a method literal with no matching ServiceName binding in
			// that stub is still indexed, through a synthetic entry.
			name: "GD5 synthetic entry indexes an unbound method",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nconst orphan = \"/orphan.Svc/Method\"\n",
				"c.go":             "package c\n\nfunc f(x any) {\n\tx.Method(nil, nil)\n}\n",
			},
			want: []string{"c.go resolved_call /orphan.Svc/Method 0"},
		},
		{
			// GD5 across stubs: a binding in another stub does not rescue this
			// one, but both attributions still compete for the same name.
			name: "GD5 synthetic and bound entries compete",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Do\"\n",
				"b/svc_grpc.pb.go": "package b\n\nconst full = \"/a.Svc/Do\"\n",
				"c.go":             "package c\n\nfunc f(x any) {\n\tx.Do(nil, nil)\n}\n",
			},
			want: []string{"c.go ambiguous_call Do 2"},
		},
		{
			// GD3: a ServiceName that is not a matching string literal binds
			// nothing, so the method falls back to GD5 and the registration
			// name never enters the index.
			name: "GD3 rejects a malformed or non-literal ServiceName",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var bad = desc{ServiceName: \"has space\"}\nvar notLit = desc{ServiceName: computed()}\n\n" +
					"const full = \"/ok.Svc/Do\"\n\nfunc RegisterSvcServer(s any, impl any) {}\n\n" +
					"func computed() string { return \"x\" }\n",
				"c.go": "package c\n\nfunc f(x any) {\n\tx.Do(nil, nil)\n\tRegisterSvcServer(nil, nil)\n}\n",
			},
			want: []string{"c.go resolved_call /ok.Svc/Do 0"},
		},
		{
			// GD10: an unindexed selector and a zero-argument call are out of
			// domain. Neither is a row; both are counted in the unit's one
			// aggregate, which is what §6 publishes.
			name: "GD10 out of domain",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Say\"\n",
				"c.go": "package c\n\nfunc f(x any) {\n\tx.Say()\n\tx.Unknown(nil, nil)\n\tx.Say(nil, nil)\n}\n",
			},
			want: []string{
				"c.go resolved_call /a.Svc/Say 0",
			},
			ood: wantOutOfDomain{total: 2, unindexed: 1, zeroArgs: 1, files: 1},
		},
		{
			// GD10's cause order: an unindexed selector is decided before the
			// argument count, so a zero-argument call to an unindexed name is
			// counted once, as unindexed_selector, and never twice.
			name: "GD10 unindexed selector wins over zero arguments",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Say\"\n",
				"c.go": "package c\n\nfunc f(x any) {\n\tx.Unknown()\n\tx.Say()\n}\n",
			},
			want: nil,
			ood:  wantOutOfDomain{total: 2, unindexed: 1, zeroArgs: 1, files: 1},
		},
		{
			// GD10's zero-argument rule is a client-call rule, so it does not
			// suppress a registration candidate.
			name: "GD10 zero arguments does not suppress a registration",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nfunc RegisterSvcServer(s any, impl any) {}\n",
				"c.go": "package c\n\nfunc f(x any) {\n\tRegisterSvcServer()\n\tx.RegisterSvcServer()\n}\n",
			},
			want: []string{
				"c.go resolved_registration a.Svc 0",
				"c.go resolved_registration a.Svc 0",
			},
		},
		{
			// GD7 plus GD12: a plain Ident call that is not a registration
			// emits nothing at all, and is not an out-of-domain candidate
			// either, because GD10's class belongs to client candidates. The
			// aggregate stays at zero.
			name: "GD7 plain identifier call that is not a registration emits nothing",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Say\"\n",
				"c.go": "package c\n\nfunc f() {\n\thelper(1)\n\tdoWork()\n\tSay(nil, nil)\n}\n\n" +
					"func helper(int) {}\nfunc doWork() {}\n",
			},
			want: nil,
		},
		{
			// GD7 admits only a SelectorExpr Fun, so a generic instantiation is
			// not a candidate: no row, and no out-of-domain count either, since
			// GD10 excludes candidates rather than admitting shapes.
			name: "GD7 generic instantiation is not a candidate",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nconst full = \"/a.Svc/Say\"\n",
				"c.go":             "package c\n\nfunc f(x any) {\n\tx.Say[int](nil, nil)\n}\n",
			},
			want: nil,
		},
		{
			// GD11: a Register…Server declaration with no same-file binding
			// enters nothing. The bare Ident call then emits nothing (GD7 plus
			// GD12), while the selector call falls through GD9 to the client
			// check and is an out-of-domain candidate like any other unindexed
			// selector name (GD10), so it lands in the aggregate and not in the
			// rows.
			name: "GD11 unbound registration declaration enters nothing",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nfunc RegisterSvcServer(s any, impl any) {}\n",
				"c.go":             "package c\n\nfunc f(x any) {\n\tRegisterSvcServer(nil, nil)\n\tx.RegisterSvcServer(nil, nil)\n}\n",
			},
			want: nil,
			ood:  wantOutOfDomain{total: 1, unindexed: 1, files: 1},
		},
		{
			// GD11: only a top-level FuncDecl with no receiver names a
			// registration.
			name: "GD11 methods and nested closures are not registration names",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\ntype impl struct{}\n\n" +
					"func (impl) RegisterSvcServer(s any, impl any) {}\n\n" +
					"func outer() func() {\n\treturn func() { RegisterSvcServer(nil, nil) }\n}\n",
				"c.go": "package c\n\nfunc f(x any) {\n\tRegisterSvcServer(nil, nil)\n}\n",
			},
			want: nil,
		},
		{
			// GD2: a stub feeds the index only and is never scanned for sites.
			name: "GD2 call sites inside a stub are not reported",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Say\"\n\n" +
					"func RegisterSvcServer(s any, impl any) {}\n\n" +
					"func insideStub(x any) {\n\tx.Say(nil, nil)\n\tRegisterSvcServer(nil, nil)\n}\n",
				"c.go": "package c\n\nfunc f(x any) {\n\tx.Say(nil, nil)\n}\n",
			},
			want: []string{"c.go resolved_call /a.Svc/Say 0"},
		},
		{
			// GD6: the index is repository-scoped, so two units never share it.
			// Pinned here as one unit whose stubs are both visible.
			name: "GD6 aggregates every stub in the unit",
			files: map[string]string{
				"a/one_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.One\"}\n\nconst full = \"/a.One/Alpha\"\n\n" +
					"func RegisterOneServer(s any, impl any) {}\n",
				"z/two_grpc.pb.go": "package z\n\n" + descType +
					"var d = desc{ServiceName: \"z.Two\"}\n\nconst full = \"/z.Two/Beta\"\n\n" +
					"func RegisterTwoServer(s any, impl any) {}\n",
				"m/c.go": "package c\n\nfunc f(x any) {\n\tx.Alpha(nil, nil)\n\tx.Beta(nil, nil)\n" +
					"\tRegisterOneServer(nil, nil)\n\tRegisterTwoServer(nil, nil)\n}\n",
			},
			want: []string{
				"m/c.go resolved_call /a.One/Alpha 0",
				"m/c.go resolved_call /z.Two/Beta 0",
				"m/c.go resolved_registration a.One 0",
				"m/c.go resolved_registration z.Two 0",
			},
		},
		{
			// GD3: a package-less full name is valid and its short name is the
			// whole string.
			name: "GD3 package-less service",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"Svc\"}\n\nconst full = \"/Svc/Do\"\n\n" +
					"func RegisterSvcServer(s any, impl any) {}\n",
				"c.go": "package c\n\nfunc f(x any) {\n\tx.Do(nil, nil)\n\tRegisterSvcServer(nil, nil)\n}\n",
			},
			want: []string{
				"c.go resolved_call /Svc/Do 0",
				"c.go resolved_registration Svc 0",
			},
		},
		{
			// GD14: a stub that fails to parse contributes no index entries, so
			// its services stay unresolvable repository-wide and its call sites
			// become unindexed-selector counts in the aggregate.
			name: "GD14 stub parse error suppresses its services repository-wide",
			files: map[string]string{
				"a/bad_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Do\"\n\n" +
					"func RegisterSvcServer(s any, impl any) {}\n\n@@@ not go @@@\n",
				"ok/good_grpc.pb.go": "package ok\n\nconst full = \"/ok.Svc/Fine\"\n",
				"c.go":               "package c\n\nfunc f(x any) {\n\tx.Do(nil, nil)\n\tRegisterSvcServer(nil, nil)\n\tx.Fine(nil, nil)\n}\n",
			},
			want: []string{
				"a/bad_grpc.pb.go gap_parse_error parse_error 0",
				"c.go resolved_call /ok.Svc/Fine 0",
			},
			ood: wantOutOfDomain{total: 1, unindexed: 1, files: 1},
		},
		{
			// GD14: a non-stub that fails to parse yields one gap and no sites.
			name: "GD14 non-stub parse error",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nconst full = \"/a.Svc/Do\"\n",
				"c.go":             "package c\n\nfunc f(x any) {\n\tx.Do(nil, nil)\n\n@@@ not go @@@\n",
			},
			want: []string{"c.go gap_parse_error parse_error 0"},
		},
		{
			// GD15: a zero-byte file yields no gap row and no sites, for a stub
			// and a non-stub alike. The unit still publishes its aggregate, at
			// total 0, so a unit with nothing to report is distinguishable from
			// a unit that was never scanned.
			name: "GD15 empty files emit nothing",
			files: map[string]string{
				"a/empty_grpc.pb.go": "",
				"empty.go":           "",
			},
			want: nil,
		},
		{
			// GD16: role is an attribute, not a filter. A test file's sites are
			// in scope, and the oracle classifies no role at all.
			name: "GD16 test-role files are scanned",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nconst full = \"/a.Svc/Do\"\n",
				"a/svc_test.go":    "package a\n\nimport \"testing\"\n\nfunc TestDo(t *testing.T) {\n\tx.Do(nil, nil)\n}\n",
				"mock/mock.go":     "package mock\n\nfunc Mock(x any) {\n\tx.Do(nil, nil)\n}\n",
			},
			want: []string{
				"a/svc_test.go resolved_call /a.Svc/Do 0",
				"mock/mock.go resolved_call /a.Svc/Do 0",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runUnit(t, tc.files, nil)
			assertRows(t, keys(res.rows), tc.want)
			assertOutOfDomain(t, res.aggregates[0], tc.ood)
		})
	}
}

// ---------------------------------------------------------------------------
// Spans (protocol §3, "Row identity used for scoring")
// ---------------------------------------------------------------------------

func TestCallSpanIsTheWholeCallExpr(t *testing.T) {
	const src = "package c\n\nfunc f(x any) {\n\tx.Say(\n\t\tnil,\n\t\tnil,\n\t)\n}\n"
	files := map[string]string{
		"a/svc_grpc.pb.go": "package a\n\nconst full = \"/a.Svc/Say\"\n",
		"c.go":             src,
	}
	rows := runUnit(t, files, nil).rows
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %v", len(rows), keys(rows))
	}
	r := rows[0]
	wantStart := strings.Index(src, "x.Say(")
	wantEnd := strings.Index(src, "\t)\n}") + len("\t)")
	if r.StartByte != wantStart || r.EndByte != wantEnd {
		t.Errorf("span = [%d,%d), want [%d,%d) - the whole CallExpr from its function expression to its closing parenthesis",
			r.StartByte, r.EndByte, wantStart, wantEnd)
	}
	if r.StartLine != 4 || r.EndLine != 7 {
		t.Errorf("lines = [%d,%d], want [4,7]", r.StartLine, r.EndLine)
	}
	if got := src[r.StartByte:r.EndByte]; got != "x.Say(\n\t\tnil,\n\t\tnil,\n\t)" {
		t.Errorf("span text = %q", got)
	}
}

func TestGapRowsSpanTheWholeFile(t *testing.T) {
	prefix := "package big\n\nvar x = 1\n"
	tooLarge := prefix + strings.Repeat("a", maxFileSize+1-len(prefix))
	parseError := "package c\n\nfunc f(x any) {\n\tx.Do(nil, nil)\n\n@@@ not go @@@\n"
	files := map[string]string{
		"a/svc_grpc.pb.go": "package a\n\nconst full = \"/a.Svc/Do\"\n",
		"big.go":           tooLarge,
		"broken.go":        parseError,
	}
	// A gap file yields no sites, so it can hold no out-of-domain candidate
	// either: GD13 and GD14 both stop before the call scan.
	res := runUnit(t, files, nil)
	rows := res.rows
	assertOutOfDomain(t, res.aggregates[0], wantOutOfDomain{})
	byPath := map[string]row{}
	for _, r := range rows {
		byPath[r.Path] = r
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want exactly two gaps: %v", len(rows), keys(rows))
	}
	large := byPath["big.go"]
	if large.State != stateGapTooLarge || large.Object != "source_too_large" {
		t.Errorf("big.go = %s/%s, want %s/source_too_large", large.State, large.Object, stateGapTooLarge)
	}
	if large.StartByte != 0 || large.EndByte != len(tooLarge) {
		t.Errorf("big.go span = [%d,%d), want [0,%d)", large.StartByte, large.EndByte, len(tooLarge))
	}
	if large.StartLine != 1 || large.EndLine != strings.Count(tooLarge, "\n")+1 {
		t.Errorf("big.go lines = [%d,%d], want [1,%d]", large.StartLine, large.EndLine, strings.Count(tooLarge, "\n")+1)
	}
	broken := byPath["broken.go"]
	if broken.State != stateGapParseError || broken.Object != "parse_error" {
		t.Errorf("broken.go = %s/%s, want %s/parse_error", broken.State, broken.Object, stateGapParseError)
	}
	if broken.StartByte != 0 || broken.EndByte != len(parseError) {
		t.Errorf("broken.go span = [%d,%d), want [0,%d)", broken.StartByte, broken.EndByte, len(parseError))
	}
}

// GD13 reads "exceeds 4 MiB" strictly, so a file of exactly the bound is still
// parsed and scanned rather than gapped.
func TestExactlyFourMiBIsNotAGap(t *testing.T) {
	prefix := "package big\n//pad "
	content := prefix + strings.Repeat("a", maxFileSize-len(prefix)-1) + "\n"
	if len(content) != maxFileSize {
		t.Fatalf("fixture is %d bytes, want exactly %d", len(content), maxFileSize)
	}
	res := runUnit(t, map[string]string{"big.go": content}, nil)
	if len(res.rows) != 0 {
		t.Fatalf("a file of exactly 4 MiB produced rows: %v", keys(res.rows))
	}
	assertOutOfDomain(t, res.aggregates[0], wantOutOfDomain{})
}

// A stub over the bound contributes no index entries either, so its services
// stay unresolvable repository-wide just as GD14's parse failure does, and the
// calls against them land in the aggregate as unindexed selectors.
func TestOversizedStubContributesNoIndexEntries(t *testing.T) {
	prefix := "package a\n\nconst full = \"/a.Svc/Do\"\n\n"
	stub := prefix + strings.Repeat("a", maxFileSize+1-len(prefix))
	files := map[string]string{
		"a/svc_grpc.pb.go": stub,
		"c.go":             "package c\n\nfunc f(x any) {\n\tx.Do(nil, nil)\n}\n",
	}
	res := runUnit(t, files, nil)
	assertRows(t, keys(res.rows), []string{
		"a/svc_grpc.pb.go gap_source_too_large source_too_large 0",
	})
	assertOutOfDomain(t, res.aggregates[0], wantOutOfDomain{total: 1, unindexed: 1, files: 1})
}

// ---------------------------------------------------------------------------
// The per-unit out-of-domain aggregate (GD10 plus §6)
// ---------------------------------------------------------------------------

// TestOutOfDomainAggregateIsOneRecordPerUnit pins the amendment's whole shape:
// GD10's silence is published as exactly one counted record per unit, after
// that unit's site rows, no matter how many candidates it holds or how many
// files they are spread across. Enumerating them as rows would cost millions of
// unscored records over the locked population and would change no scored
// outcome, because a candidate row at a span the oracle reported nothing for is
// spurious either way.
func TestOutOfDomainAggregateIsOneRecordPerUnit(t *testing.T) {
	files := map[string]string{
		"a/svc_grpc.pb.go": "package a\n\n" + descType +
			"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Say\"\n\n" +
			"func RegisterSvcServer(s any, impl any) {}\n",
		// Two candidates in one file, plus one resolved call.
		"b/one.go": "package b\n\nfunc f(x any) {\n\tx.Say(nil, nil)\n\tx.Unknown(nil, nil)\n\tx.Say()\n}\n",
		// Two more candidates in a second file: an unindexed name, and a
		// zero-argument call to an unindexed name, which GD10's cause order
		// counts once as unindexed_selector.
		"c/two.go": "package c\n\nfunc g(y any) {\n\ty.Nope(nil)\n\ty.Also()\n}\n",
		// A third file with a registration and a resolved call and no
		// candidate at all, so it must not be counted as a file with candidates.
		"d/three.go": "package d\n\nfunc h(x any, s any) {\n\tRegisterSvcServer(s, x)\n\tx.Say(nil, nil)\n}\n",
	}
	res := runUnit(t, files, nil)

	// The scored rows are only the real sites, in path then source order.
	assertRows(t, keys(res.rows), []string{
		"b/one.go resolved_call /a.Svc/Say 0",
		"d/three.go resolved_registration a.Svc 0",
		"d/three.go resolved_call /a.Svc/Say 0",
	})

	if len(res.aggregates) != 1 {
		t.Fatalf("got %d aggregates for one unit, want exactly 1: %+v", len(res.aggregates), res.aggregates)
	}
	// Four candidates: b/one.go's unindexed selector and zero-argument call,
	// plus c/two.go's two unindexed selectors. Two files hold them.
	assertOutOfDomain(t, res.aggregates[0], wantOutOfDomain{
		total: 4, unindexed: 3, zeroArgs: 1, files: 2,
	})

	// The aggregate is the unit's last record, so a consumer reading the stream
	// in order sees every site row of a unit before its count.
	if got := res.kinds[len(res.kinds)-1]; got != "aggregate" {
		t.Errorf("last record = %q, want aggregate: %v", got, res.kinds)
	}
	for _, k := range res.kinds[:len(res.kinds)-1] {
		if k != "site" {
			t.Errorf("a %q record followed the aggregate: %v", k, res.kinds)
		}
	}
}

// TestAggregateIsEmittedWithZeroOutOfDomainCandidates pins that the record is
// unconditional: a unit that scanned successfully and found nothing out of
// domain still publishes total 0, so an absent record means "this unit was
// never scanned" and never "this unit had no candidates".
func TestAggregateIsEmittedWithZeroOutOfDomainCandidates(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "sites but no candidate",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\n" + descType +
					"var d = desc{ServiceName: \"a.Svc\"}\n\nconst full = \"/a.Svc/Say\"\n\n" +
					"func RegisterSvcServer(s any, impl any) {}\n",
				"c.go": "package c\n\nfunc f(x any, s any) {\n\tx.Say(nil, nil)\n\tRegisterSvcServer(s, x)\n}\n",
			},
			want: []string{
				"c.go resolved_call /a.Svc/Say 0",
				"c.go resolved_registration a.Svc 0",
			},
		},
		{
			name: "only stubs, so no file is scanned for sites at all",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nconst full = \"/a.Svc/Say\"\n",
			},
			want: nil,
		},
		{
			name: "non-candidate call shapes only",
			files: map[string]string{
				"a/svc_grpc.pb.go": "package a\n\nconst full = \"/a.Svc/Say\"\n",
				"c.go":             "package c\n\nfunc f(x any) {\n\thelper(1)\n\tx.Say[int](nil, nil)\n}\n\nfunc helper(int) {}\n",
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runUnit(t, tc.files, nil)
			assertRows(t, keys(res.rows), tc.want)
			if len(res.aggregates) != 1 {
				t.Fatalf("got %d aggregates, want exactly 1: %+v", len(res.aggregates), res.aggregates)
			}
			assertOutOfDomain(t, res.aggregates[0], wantOutOfDomain{})
			// A zero total still renders every cause key, so the record's shape
			// does not depend on what the unit happened to contain.
			encoded, err := json.Marshal(res.aggregates[0])
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, reason := range outOfDomainReasons {
				if !strings.Contains(string(encoded), `"`+reason+`":0`) {
					t.Errorf("record omits %q at zero: %s", reason, encoded)
				}
			}
		})
	}
}

// TestOutOfDomainAggregateJSONShape pins the exact record the round publishes:
// field names, field order and the cause-key set. json.Encoder sorts map keys,
// so by_reason renders in the same order as outOfDomainReasons.
func TestOutOfDomainAggregateJSONShape(t *testing.T) {
	tally := newOutOfDomainTally()
	tally.add("b/one.go", reasonUnindexedSelector)
	tally.add("b/one.go", reasonZeroArguments)
	tally.add("c/two.go", reasonUnindexedSelector)
	encoded, err := json.Marshal(tally.aggregate("example/repo"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"schema":"t581-out-of-domain-v1","unit":"example/repo","total":3,` +
		`"by_reason":{"unindexed_selector":2,"zero_arguments":1},"files_with_candidates":2}`
	if string(encoded) != want {
		t.Errorf("aggregate JSON =\n%s\nwant\n%s", encoded, want)
	}
}

// TestAggregateIsNotEmittedForAUnitThatFailedToScan pins that the count is
// withheld when the unit could not be read completely: a partial unit's
// aggregate would understate GD10's blind spot and look like evidence.
func TestAggregateIsNotEmittedForAUnitThatFailedToScan(t *testing.T) {
	files := map[string]string{"a/svc_grpc.pb.go": "package a\n\nconst full = \"/a.Svc/Do\"\n"}
	u := unitFor(files, "example/repo", nil)
	u.GoFiles = append(u.GoFiles, "absent.go")
	sort.Strings(u.GoFiles)
	var kinds []string
	_, err := scanUnit(u, memCorpus(files), func(any) { kinds = append(kinds, "record") })
	if err == nil {
		t.Fatal("a path absent from the corpus was accepted")
	}
	if len(kinds) != 0 {
		t.Errorf("a failed unit emitted %d records, want none", len(kinds))
	}
}

// ---------------------------------------------------------------------------
// Determinism
// ---------------------------------------------------------------------------

// TestRowsAreEmittedInPathThenSourceOrder pins the output order the
// determinism guarantee rests on: sorted repository-relative path, then parser
// traversal order within one file, with the unit's aggregate after all of them.
func TestRowsAreEmittedInPathThenSourceOrder(t *testing.T) {
	stub := "package a\n\n" + descType +
		"var one = desc{ServiceName: \"a.Svc\"}\nvar two = desc{ServiceName: \"b.Svc\"}\n\n" +
		"const m1 = \"/a.Svc/Do\"\nconst m2 = \"/b.Svc/Do\"\nconst m3 = \"/a.Svc/Only\"\n\n" +
		"func RegisterSvcServer(s any, impl any) {}\n"
	cgo := "package c\n\nfunc f(x any) {\n\tx.Only(nil, nil)\n\tx.Do(nil, nil)\n\tRegisterSvcServer(nil, nil)\n}\n"
	ago := "package a\n\nfunc g(x any) {\n\tx.Only(nil, nil)\n}\n"
	files := map[string]string{
		"a/svc_grpc.pb.go": stub,
		"c.go":             cgo,
		"a/g.go":           ago,
		"b.go":             "package b\n",
	}
	want := []string{
		"a/g.go resolved_call /a.Svc/Only 0",
		"c.go resolved_call /a.Svc/Only 0",
		"c.go ambiguous_call Do 2",
		"c.go ambiguous_registration RegisterSvcServer 2",
	}
	res := runUnit(t, files, nil)
	assertRows(t, keys(res.rows), want)
	assertOutOfDomain(t, res.aggregates[0], wantOutOfDomain{})
	wantKinds := make([]string, 0, len(want)+1)
	for range want {
		wantKinds = append(wantKinds, "site")
	}
	wantKinds = append(wantKinds, "aggregate")
	if !reflect.DeepEqual(res.kinds, wantKinds) {
		t.Errorf("emission order = %v, want %v", res.kinds, wantKinds)
	}
}

// TestGitCorpusIsDeterministic exercises the real `git cat-file --batch` path
// against a throwaway synthetic clone and pins that two runs produce
// byte-identical output that matches the in-memory corpus for the same bytes.
func TestGitCorpusIsDeterministic(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("the oracle reads every blob through git: %v", err)
	}
	stub := "package api\n\n" + descType +
		"var d = desc{ServiceName: \"api.Svc\"}\n\nconst full = \"/api.Svc/Say\"\n\n" +
		"func RegisterSvcServer(s any, impl any) {}\n"
	client := "package client\n\nfunc call(x any) {\n\tx.Say(nil, nil)\n\tx.Say()\n}\n"
	server := "package server\n\nfunc register(s any, i any) {\n\tRegisterSvcServer(s, i)\n}\n"
	empty := ""
	files := map[string]string{
		"api/svc_grpc.pb.go": stub,
		"client.go":          client,
		"server.go":          server,
		"empty.go":           empty,
	}

	root := t.TempDir()
	dir := "example__repo"
	repo := filepath.Join(root, dir)
	for path, content := range files {
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, stderr.String())
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "--template=")
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	git(append([]string{"add", "--"}, paths...)...)
	// Pin the identity and disable hooks and signing so a developer's global
	// git config cannot make this fixture non-deterministic.
	hooks := filepath.Join(root, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	git("-c", "user.name=t581", "-c", "user.email=t581@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath="+hooks,
		"commit", "-q", "-m", "fixture")
	commit := git("rev-parse", "HEAD")

	u := unitFor(files, "example/repo", nil)
	u.Commit = commit

	render := func(src corpus) string {
		t.Helper()
		var buf strings.Builder
		enc := json.NewEncoder(&buf)
		if _, err := scanUnit(u, src, func(rec any) {
			if err := enc.Encode(rec); err != nil {
				t.Fatalf("encode: %v", err)
			}
		}); err != nil {
			t.Fatalf("scanUnit: %v", err)
		}
		return buf.String()
	}
	gitSrc := gitCorpus{root: root}
	first, second := render(gitSrc), render(gitSrc)
	if first != second {
		t.Fatalf("two runs over one corpus differ\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if first == "" {
		t.Fatal("the git corpus produced no records")
	}

	// The git plumbing must not change the result: the same bytes through the
	// in-memory corpus give a byte-identical stream, aggregate included.
	if inMemory := render(memCorpus(files)); inMemory != first {
		t.Errorf("git and in-memory streams differ\ngit:\n%s\nin-memory:\n%s", first, inMemory)
	}

	// The serialized stream is what a consumer sees, so pin the ordering there
	// too: every site row of the unit, then exactly one aggregate line last.
	lines := strings.Split(strings.TrimSuffix(first, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the stream has %d lines, want two site rows and one aggregate:\n%s", len(lines), first)
	}
	if wantPrefix := `{"schema":"` + outOfDomainSchema + `"`; !strings.HasPrefix(lines[len(lines)-1], wantPrefix) {
		t.Errorf("the stream's last line is not the aggregate %s…: %s", wantPrefix, lines[len(lines)-1])
	}

	fromGit := scanWith(t, u, gitCorpus{root: root})
	assertRows(t, keys(fromGit.rows), []string{
		"client.go resolved_call /api.Svc/Say 0",
		"server.go resolved_registration api.Svc 0",
	})
	// client.go's `x.Say()` is GD10's zero-argument cause, so it is counted in
	// the aggregate rather than cited as a row: one candidate in one file.
	assertOutOfDomain(t, fromGit.aggregates[0], wantOutOfDomain{total: 1, zeroArgs: 1, files: 1})
}

// TestGitCorpusRejectsAMissingBlob pins that a path the pinned commit does not
// hold is a hard error rather than a silently skipped file: a unit's candidate
// set must be read completely, and a unit that could not be read completely
// publishes no aggregate either.
func TestGitCorpusRejectsAMissingBlob(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("the oracle reads every blob through git: %v", err)
	}
	root := t.TempDir()
	repo := filepath.Join(root, "example__repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "present.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "--template=")
	run("add", "--", "present.go")
	hooks := filepath.Join(root, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	run("-c", "user.name=t581", "-c", "user.email=t581@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath="+hooks,
		"commit", "-q", "-m", "fixture")
	commit := run("rev-parse", "HEAD")

	u := unitRecord{
		Schema: unitSchema, Unit: "example/repo", Repository: "example/repo", Commit: commit,
		Dir: "example__repo", GoFiles: []string{"absent.go", "present.go"},
	}
	var records int
	_, err := scanUnit(u, gitCorpus{root: root}, func(any) { records++ })
	if err == nil {
		t.Fatal("a path absent at the pinned commit was accepted")
	}
	if !strings.Contains(err.Error(), "absent.go") {
		t.Errorf("error does not name the missing path: %v", err)
	}
	if records != 0 {
		t.Errorf("a unit that could not be read completely emitted %d records, want none", records)
	}
}
