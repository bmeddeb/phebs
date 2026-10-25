//go:build linux

package t421

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Direct canonical/claim shapes only. A genuine private view, pressure volume,
// target exec and author transition remain opt-in native work.
func TestLinuxPressureAuthorCallerClaimAndSelection(t *testing.T) {
	tool, builds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
	claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
	tool.executorClaim = claim
	author := linuxPressureAuthorSelection{Schema: executionLinuxPressureAuthorSchema}
	owner := newLinuxPressureAuthorExecutorPrework(builds, tool, author)
	if owner.claim != claim || owner.pressure == nil || owner.pressure.author == nil || claim.outer != owner || claim.purpose != 2 || linuxExecutorPreworkPurpose(owner) != 2 {
		t.Fatal("author outer purpose not bound")
	}
	if !claim.consumeOuter(owner) || claim.consumeOuter(owner) {
		t.Fatal("author outer claim reusable")
	}
	for _, presented := range []*linuxExecutorPrework{
		newLinuxPressureAuthorExecutorPrework(builds, tool, author),
		newLinuxPressureExecutorPrework(builds, tool, linuxPressureBorrowSelection{}),
		newLinuxExecutorPrework(builds, tool, linuxExecutorPreworkSelection{}),
	} {
		if presented.claim.consumeOuter(presented) {
			t.Fatal("spent author claim accepted another mode")
		}
	}

	innerTool, innerBuilds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
	innerClaim := &linuxExecutorPreworkClaim{tool: innerTool, builds: innerBuilds}
	innerTool.executorClaim = innerClaim
	inner := &linuxExecutorInnerPrework{tool: innerTool, builds: innerBuilds}
	if !innerClaim.bindAuthorInner(inner) || innerClaim.purpose != 2 || innerClaim.consumeInner(inner) || !innerClaim.used.Load() || innerClaim.consumeAuthorInner(inner) {
		t.Fatal("author inner purpose accepted another mode")
	}
	freshTool, freshBuilds := &LinuxExecutionToolCustody{}, &LinuxExecutionGoBuildCustody{}
	freshClaim := &linuxExecutorPreworkClaim{tool: freshTool, builds: freshBuilds}
	freshTool.executorClaim = freshClaim
	fresh := &linuxExecutorInnerPrework{tool: freshTool, builds: freshBuilds}
	if !freshClaim.bindAuthorInner(fresh) || !freshClaim.consumeAuthorInner(fresh) {
		t.Fatal("fresh canonical author inner refused")
	}

	value := linuxPressureAuthorSelection{Schema: executionLinuxPressureAuthorSchema}
	encoded, err := encodeLinuxPressureAuthor(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLinuxPressureAuthor(encoded)
	if err != nil || decoded != value {
		t.Fatal("canonical author selection")
	}
	for _, raw := range []string{"", encoded + "=", strings.Repeat("A", maxExecutionSelectionCharacters+1)} {
		if _, err := decodeLinuxPressureAuthor(raw); err == nil {
			t.Fatal("unbounded/noncanonical author selection")
		}
	}
	if linuxPressureAuthorReceipt("a", false) != executionLinuxPressureAuthorSchema+":pressure-not-retained:a\n" ||
		linuxPressureAuthorReceipt("a", true) != executionLinuxPressureAuthorSchema+":actors-retired-pressure-retained:a\n" {
		t.Fatal("author terminal receipt")
	}
	if owner.pressure.receipt("a", false) != linuxPressureAuthorReceipt("a", false) ||
		owner.pressure.receipt("a", true) != linuxPressureAuthorReceipt("a", true) {
		t.Fatal("author prework success receipt")
	}
	ordinary := newLinuxPressureExecutorPrework(builds, tool, linuxPressureBorrowSelection{})
	if ordinary.pressure.receipt("a", false) != linuxPressureBorrowReceipt("a", false) ||
		ordinary.pressure.receipt("a", true) != linuxPressureBorrowReceipt("a", true) {
		t.Fatal("ordinary pressure prework receipt")
	}
}

// The author broker must apply the same explicit tool-identity, provenance,
// referenceInputs and image gates as the ordinary pressure broker.
func TestLinuxPressureAuthorToolParityGates(t *testing.T) {
	value := linuxExecutorPreworkSelection{SourceCommit: strings.Repeat("a", 40)}
	builds := &LinuxExecutionGoBuildCustody{}
	tool := &LinuxExecutionToolCustody{referenceInputs: builds}
	inner := &linuxExecutorInnerPrework{tool: tool, builds: builds}
	good := ExecutionToolIdentity{Role: "t422-execute", BuildVCSRevision: value.SourceCommit, Provenance: "go-build-info-vcs-v1"}
	if !linuxPressureToolIdentity(value, inner, good) {
		t.Fatal("exact author tool identity refused")
	}
	for name, identity := range map[string]ExecutionToolIdentity{
		"wrong-vcs":        {Role: "t422-execute", BuildVCSRevision: strings.Repeat("b", 40), Provenance: "go-build-info-vcs-v1"},
		"wrong-provenance": {Role: "t422-execute", BuildVCSRevision: value.SourceCommit, Provenance: "go-module-build-v1"},
		"empty":            {},
	} {
		if linuxPressureToolIdentity(value, inner, identity) {
			t.Fatal("wrong author tool identity admitted", name)
		}
	}
	unbound := &LinuxExecutionToolCustody{referenceInputs: &LinuxExecutionGoBuildCustody{}}
	if linuxPressureToolIdentity(value, &linuxExecutorInnerPrework{tool: unbound, builds: builds}, good) ||
		linuxPressureToolIdentity(value, &linuxExecutorInnerPrework{tool: tool}, good) ||
		linuxPressureToolIdentity(value, &linuxExecutorInnerPrework{builds: builds}, good) ||
		linuxPressureToolIdentity(value, nil, good) {
		t.Fatal("unbound author referenceInputs admitted")
	}
	if verifyLinuxPressureTool(context.Background(), value, nil, &linuxExecutorInnerPrework{builds: builds}) != ErrLinuxPressure ||
		verifyLinuxPressureTool(context.Background(), value, nil, nil) != ErrLinuxPressure {
		t.Fatal("nil author verifier admitted")
	}
}

// The author gate must refuse before root or volume work when its bound tool
// cannot produce a checked identity. This is the callable gate the broker uses;
// executing the broker branch itself still requires genuine native preparation.
func TestLinuxPressureAuthorGateRefusal(t *testing.T) {
	ctx := context.Background()
	value := linuxExecutorPreworkSelection{SourceCommit: strings.Repeat("a", 40)}
	image, err := os.CreateTemp(t.TempDir(), "sealed-image")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = image.Close() }()

	builds := &LinuxExecutionGoBuildCustody{}
	tool := &LinuxExecutionToolCustody{referenceInputs: builds}
	claim := &linuxExecutorPreworkClaim{tool: tool, builds: builds}
	tool.executorClaim = claim
	inner := &linuxExecutorInnerPrework{tool: tool, builds: builds}
	if err := prepareLinuxPressureAuthorGate(ctx, value, image, inner); err != ErrLinuxPressure {
		t.Fatal("author gate admitted an unchecked tool", err)
	}
	if !claim.used.Load() || claim.purpose != 2 || claim.inner != inner {
		t.Fatal("author gate did not consume its claim")
	}
	if err := prepareLinuxPressureAuthorGate(ctx, value, image, inner); err != ErrLinuxPressure {
		t.Fatal("spent author claim admitted")
	}

	pressureBuilds := &LinuxExecutionGoBuildCustody{}
	pressureTool := &LinuxExecutionToolCustody{referenceInputs: pressureBuilds}
	pressureClaim := &linuxExecutorPreworkClaim{tool: pressureTool, builds: pressureBuilds}
	pressureTool.executorClaim = pressureClaim
	pressureInner := &linuxExecutorInnerPrework{tool: pressureTool, builds: pressureBuilds}
	if !pressureClaim.bindPressureInner(pressureInner) || prepareLinuxPressureAuthorGate(ctx, value, image, pressureInner) != ErrLinuxPressure || pressureClaim.used.Load() {
		t.Fatal("author gate admitted a pressure-purpose claim")
	}

	if prepareLinuxPressureAuthorGate(ctx, value, image, nil) != ErrLinuxPressure ||
		prepareLinuxPressureAuthorGate(ctx, value, image, &linuxExecutorInnerPrework{builds: builds}) != ErrLinuxPressure ||
		prepareLinuxPressureAuthorGate(ctx, value, image, &linuxExecutorInnerPrework{tool: &LinuxExecutionToolCustody{referenceInputs: builds}, builds: builds}) != ErrLinuxPressure {
		t.Fatal("incomplete author gate admitted")
	}
}

// The author broker branch requires genuine native preparation to execute, so
// no behavioral test reaches its gate call. This wiring oracle fails if the
// branch stops calling the author gate first, which would otherwise leave every
// behavioral test green while bypassing the author purpose and identity gates.
func TestLinuxPressureAuthorBrokerGateWiring(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "pressure_borrow_broker_linux.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "runLinuxPressureBrokerRecipe" {
			body = fn.Body
			break
		}
	}
	if body == nil {
		t.Fatal("author broker recipe missing")
	}
	checked := false
	ast.Inspect(body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.IfStmt)
		if !ok || stmt.Else == nil {
			return true
		}
		cond, ok := stmt.Cond.(*ast.BinaryExpr)
		if !ok || cond.Op != token.EQL {
			return true
		}
		left, leftOK := cond.X.(*ast.Ident)
		right, rightOK := cond.Y.(*ast.Ident)
		if !leftOK || !rightOK || left.Name != "author" || right.Name != "nil" {
			return true
		}
		block, ok := stmt.Else.(*ast.BlockStmt)
		if !ok || len(block.List) == 0 {
			t.Fatal("author broker branch missing")
		}
		assign, ok := block.List[0].(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			t.Fatal("author broker branch does not start with the author gate")
		}
		lhs, lhsOK := assign.Lhs[0].(*ast.Ident)
		call, callOK := assign.Rhs[0].(*ast.CallExpr)
		if !lhsOK || !callOK {
			t.Fatal("author broker branch does not call prepareLinuxPressureAuthorReference")
		}
		fn, fnOK := call.Fun.(*ast.Ident)
		if !fnOK || lhs.Name != "workErr" || fn.Name != "prepareLinuxPressureAuthorReference" {
			t.Fatal("author broker branch does not call prepareLinuxPressureAuthorReference")
		}
		checked = true
		return false
	})
	if !checked {
		t.Fatal("author broker branch not found")
	}
}

// The broker wiring oracle above pins the branch call, but not the gate the
// author wrapper hands to the shared constructor. Pin both wrapper arguments
// and each gate's purpose/verifier calls so a swapped or omitted gate cannot
// leave the suite green while the author purpose is consumed as ordinary
// pressure.
func TestLinuxPressureAuthorGateArgumentWiring(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "pressure_borrow_broker_linux.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := func(name string) *ast.BlockStmt {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
				return fn.Body
			}
		}
		t.Fatalf("%s missing", name)
		return nil
	}
	called := func(body *ast.BlockStmt) map[string]bool {
		names := map[string]bool{}
		ast.Inspect(body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				names[fn.Name] = true
			case *ast.SelectorExpr:
				names[fn.Sel.Name] = true
			}
			return true
		})
		return names
	}
	for _, wrapper := range []struct {
		entry string
		gate  string
	}{
		{"prepareLinuxPressureReference", "prepareLinuxPressureBorrowGate"},
		{"prepareLinuxPressureAuthorReference", "prepareLinuxPressureAuthorGate"},
	} {
		var returns []*ast.ReturnStmt
		ast.Inspect(body(wrapper.entry), func(node ast.Node) bool {
			if stmt, ok := node.(*ast.ReturnStmt); ok {
				returns = append(returns, stmt)
			}
			return true
		})
		if len(returns) != 1 || len(returns[0].Results) != 1 {
			t.Fatalf("%s does not have one shared-constructor return", wrapper.entry)
		}
		call, callOK := returns[0].Results[0].(*ast.CallExpr)
		if !callOK || len(call.Args) != 5 {
			t.Fatalf("%s does not call the shared constructor with a gate", wrapper.entry)
		}
		fn, fnOK := call.Fun.(*ast.Ident)
		if !fnOK || fn.Name != "prepareLinuxPressureReferenceFor" {
			t.Fatalf("%s does not call the shared constructor", wrapper.entry)
		}
		argument, argumentOK := call.Args[4].(*ast.Ident)
		if !argumentOK || argument.Name != wrapper.gate {
			t.Fatalf("%s does not pass %s", wrapper.entry, wrapper.gate)
		}
	}
	borrow := called(body("prepareLinuxPressureBorrowGate"))
	author := called(body("prepareLinuxPressureAuthorGate"))
	for _, name := range []string{"bindPressureInner", "consumePressureInner", "verifyLinuxPressureTool"} {
		if !borrow[name] {
			t.Fatalf("ordinary pressure gate omits %s", name)
		}
	}
	for _, name := range []string{"bindAuthorInner", "consumeAuthorInner", "verifyLinuxPressureTool"} {
		if !author[name] {
			t.Fatalf("author pressure gate omits %s", name)
		}
	}
	for _, name := range []string{"bindPressureInner", "consumePressureInner"} {
		if author[name] {
			t.Fatalf("author pressure gate consumes ordinary pressure via %s", name)
		}
	}
	for _, name := range []string{"bindAuthorInner", "consumeAuthorInner"} {
		if borrow[name] {
			t.Fatalf("ordinary pressure gate consumes the author purpose via %s", name)
		}
	}
}

// The author target constructor requires a genuine root-created,
// target-UID-owned private view to execute, so no unprivileged test reaches its
// gate chain. Pin the exact ordered condition: the CAS, construction with the
// private author view, the author purpose on owner.inner, then the shared
// verifier, with the whole condition returning fail(). A branch that keeps both
// consuming claims but drops, short-circuits or reorders the verifier, or swaps
// any pinned argument, would otherwise leave every behavioral test green.
// Resolution is name-based; a same-name local shadow is outside this oracle.
func TestLinuxPressureAuthorTargetGateWiring(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "pressure_author_workspace_linux.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var constructor *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "newLinuxPressureAuthorWorkspace" {
			constructor = fn
		}
	}
	if constructor == nil {
		t.Fatal("author target constructor missing")
	}
	var orChain func(ast.Expr) []ast.Expr
	orChain = func(expr ast.Expr) (predicates []ast.Expr) {
		if binary, ok := expr.(*ast.BinaryExpr); ok && binary.Op == token.LOR {
			return append(orChain(binary.X), orChain(binary.Y)...)
		}
		return []ast.Expr{expr}
	}
	var name func(ast.Expr) string
	name = func(expr ast.Expr) string {
		switch value := expr.(type) {
		case *ast.Ident:
			return value.Name
		case *ast.SelectorExpr:
			if prefix := name(value.X); prefix != "" {
				return prefix + "." + value.Sel.Name
			}
		case *ast.CallExpr:
			if callee := name(value.Fun); callee != "" {
				return callee + "()"
			}
		}
		return ""
	}
	predicate := func(expr ast.Expr) (string, bool, *ast.CallExpr) {
		switch value := expr.(type) {
		case *ast.UnaryExpr:
			if value.Op != token.NOT {
				return "", false, nil
			}
			call, ok := value.X.(*ast.CallExpr)
			if !ok {
				return "", false, nil
			}
			return name(call.Fun), true, call
		case *ast.BinaryExpr:
			if value.Op != token.NEQ {
				return "", false, nil
			}
			if nilValue, ok := value.Y.(*ast.Ident); !ok || nilValue.Name != "nil" {
				return "", false, nil
			}
			call, ok := value.X.(*ast.CallExpr)
			if !ok {
				return "", false, nil
			}
			return name(call.Fun), false, call
		}
		return "", false, nil
	}
	var gate *ast.IfStmt
	ast.Inspect(constructor.Body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.IfStmt)
		if !ok || gate != nil {
			return true
		}
		predicates := orChain(stmt.Cond)
		if callee, negated, _ := predicate(predicates[0]); callee == "owner.inner.attempted.CompareAndSwap" && negated {
			gate = stmt
		}
		return true
	})
	if gate == nil {
		t.Fatal("author target gate missing")
	}
	predicates := orChain(gate.Cond)
	want := []struct {
		callee  string
		negated bool
	}{
		{"owner.inner.attempted.CompareAndSwap", true},
		{"owner.inner.prepareReferenceLayout", false},
		{"owner.inner.tool.executorClaim.bindAuthorInner", true},
		{"owner.inner.tool.executorClaim.consumeAuthorInner", true},
		{"verifyLinuxPressureTool", false},
	}
	if len(predicates) != len(want) {
		t.Fatalf("author target gate has %d predicates, want %d", len(predicates), len(want))
	}
	for index, expected := range want {
		callee, negated, _ := predicate(predicates[index])
		if callee != expected.callee || negated != expected.negated {
			t.Fatalf("author target gate predicate %d calls %q negated=%t, want %q negated=%t", index, callee, negated, expected.callee, expected.negated)
		}
	}
	arguments := [][]string{
		{"false", "true"},
		{"ctx", "target", "image", "view"},
		{"owner.inner"},
		{"owner.inner"},
		{"ctx", "target", "image", "owner.inner"},
	}
	for index, expected := range arguments {
		_, _, call := predicate(predicates[index])
		if len(call.Args) != len(expected) {
			t.Fatalf("author target gate predicate %d has %d arguments, want %d", index, len(call.Args), len(expected))
		}
		for position, argument := range call.Args {
			if got := name(argument); got != expected[position] {
				t.Fatalf("author target gate predicate %d argument %d is %q, want %q", index, position, got, expected[position])
			}
		}
	}
	if len(gate.Body.List) != 1 {
		t.Fatalf("author target gate has %d statements, want one fail return", len(gate.Body.List))
	}
	returned, ok := gate.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		t.Fatal("author target gate does not return fail()")
	}
	failCall, ok := returned.Results[0].(*ast.CallExpr)
	if !ok || len(failCall.Args) != 0 || name(failCall.Fun) != "fail" {
		t.Fatal("author target gate does not return fail()")
	}
	ast.Inspect(constructor.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee := name(call.Fun)
		if strings.HasSuffix(callee, ".bindPressureInner") || strings.HasSuffix(callee, ".consumePressureInner") {
			t.Fatalf("author target workspace consumes ordinary pressure via %s", callee)
		}
		return true
	})
}
