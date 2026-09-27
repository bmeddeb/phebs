package launcher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
)

func TestNonGoExactSetV2(t *testing.T) {
	plan, roots := fixture()
	plan.Version = planner.PlanV2
	for _, row := range []struct{ id, name string }{{"asm", "lib/answer.s"}, {"c", "lib/source.c"}, {"header", "lib/header.h"}, {"object", "lib/object.syso"}} {
		plan.Documents = append(plan.Documents, planner.Document{ID: row.id, Kind: "source", Path: row.name, ExecPath: row.name, SHA256: hash([]byte("data")), Bytes: 4})
		plan.Units[0].OtherFiles = append(plan.Units[0].OtherFiles, row.id)
	}
	seal(&plan)
	p, err := Prepare(plan, roots)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.files) != 8 || len(p.packages["@@//lib:lib"].OtherFiles) != 4 {
		t.Fatal("non-Go files missing from verification inventory")
	}
	good, _ := json.Marshal(exactResponse(p))
	if err := Reconcile(p, good); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*flatPackage)
	}{
		{"missing", func(p *flatPackage) { p.OtherFiles = p.OtherFiles[1:] }},
		{"extra", func(p *flatPackage) { p.OtherFiles = append(p.OtherFiles, Workspace+"/outside.s") }},
		{"substituted", func(p *flatPackage) { p.OtherFiles[0] = Workspace + "/substitute.s" }},
		{"duplicate", func(p *flatPackage) { p.OtherFiles[0] = p.OtherFiles[1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r response
			_ = json.Unmarshal(good, &r)
			for i := range r.Packages {
				if r.Packages[i].ID == "@@//lib:lib" {
					tc.change(&r.Packages[i])
				}
			}
			b, _ := json.Marshal(r)
			if err := Reconcile(p, b); err == nil {
				t.Fatal("non-Go set mismatch accepted")
			}
		})
	}
	t.Run("order irrelevant", func(t *testing.T) {
		var r response
		_ = json.Unmarshal(good, &r)
		for i := range r.Packages {
			slices.Reverse(r.Packages[i].OtherFiles)
		}
		b, _ := json.Marshal(r)
		if err := Reconcile(p, b); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("legacy rejects authority", func(t *testing.T) {
		plan.Version = "phebs-t451a-plan-v1"
		seal(&plan)
		if _, err := Prepare(plan, roots); err == nil {
			t.Fatal("legacy accepted non-Go authority")
		}
	})
}

func TestNonGoBytesBeforeAndAfterDriver(t *testing.T) {
	name := filepath.Join(t.TempDir(), "assembly.s")
	original := []byte("assembly bytes")
	if err := os.WriteFile(name, original, 0600); err != nil {
		t.Fatal(err)
	}
	p := Prepared{files: map[string]fileIdentity{name: {digest: hash(original), bytes: len(original)}}}
	// Native preparation calls this exact census before executing the driver.
	if err := verifyFiles(context.Background(), p.files); err != nil {
		t.Fatal(err)
	}
	if _, err := sealNativeGoFiles(context.Background(), p, planner.Plan{}); err != nil {
		t.Fatal(err)
	}
	if err := verifyCompatibilityFiles(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func() error
	}{
		{"same-size mutation", func() error { return os.WriteFile(name, []byte("Assembly bytes"), 0600) }},
		{"missing", func() error { return os.Remove(name) }},
		{"substituted inode bytes", func() error {
			if err := os.Remove(name); err != nil {
				return err
			}
			return os.WriteFile(name, []byte("different data"), 0600)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(name, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := tc.mutate(); err != nil {
				t.Fatal(err)
			}
			if err := verifyCompatibilityFiles(context.Background(), p, nil); err == nil {
				t.Fatal("post-driver non-Go mutation accepted")
			}
			if _, err := sealNativeGoFiles(context.Background(), p, planner.Plan{}); err == nil {
				t.Fatal("pre-driver non-Go mutation accepted")
			}
		})
	}
}

func TestNativeV2LauncherIdentity(t *testing.T) {
	plan, roots := compatibilityFixture()
	declared, err := PrepareCompatibility(plan, roots, "load")
	if err != nil {
		t.Fatal(err)
	}
	matches := nativeRows(declared, plan)
	old, err := PrepareNativeCompatibility(plan, roots, "load", matches)
	if err != nil {
		t.Fatal(err)
	}
	plan.Version = planner.PlanV2
	next, err := PrepareNativeCompatibility(plan, roots, "load", matches)
	if err != nil {
		t.Fatal(err)
	}
	if old.Digest() == next.Digest() {
		t.Fatal("v2 launcher identity did not change")
	}
	var scope nativeScope
	if err = json.Unmarshal(next.scope, &scope); err != nil {
		t.Fatal(err)
	}
	if scope.Version != nativeScopeVersionV2 {
		t.Fatal("v2 uses legacy scope")
	}
	if _, err = inspectNativeScope(next.scope, hash(next.scope), "load"); err != nil {
		t.Fatal(err)
	}
}
