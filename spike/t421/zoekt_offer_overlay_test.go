package t421

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestZoektOfferOverlayRecipe(t *testing.T) {
	commit := strings.Repeat("a", 40)
	_, _, _, _, legacy, err := referenceToolRole("zoekt-git-index")
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"", PlanSchema, PlanV2Schema} {
		_, _, _, _, got, err := referenceToolRoleForSchema("zoekt-git-index", schema, commit)
		if err != nil || got != legacy {
			t.Fatal(schema, got, err)
		}
		root, args, check, err := referenceToolBuildArgs(t.Context(), "zoekt-git-index", schema, "/not-read", "/not-read", "/not-read", "image", "package")
		if err != nil || root != "/not-read" || check() != nil || !reflect.DeepEqual(args, []string{"build", "-trimpath", "-pgo=off", "-buildvcs=true", "-p=1", "-o", "image", "package"}) {
			t.Fatal(args, err)
		}
	}
	_, _, _, _, v3, err := referenceToolRoleForSchema("zoekt-git-index", PlanV3Schema, commit)
	if err != nil || v3 == legacy {
		t.Fatal(v3, err)
	}
	_, _, _, _, other, err := referenceToolRoleForSchema("zoekt-git-index", PlanV3Schema, strings.Repeat("b", 40))
	if err != nil || other == v3 {
		t.Fatal("overlay recipe did not bind source", err)
	}
	if _, _, _, _, _, err := referenceToolRoleForSchema("zoekt-git-index", PlanV3Schema, ""); err == nil {
		t.Fatal("missing source admitted")
	}
}

func TestZoektOfferOverlayFreezeProvenance(t *testing.T) {
	plan := accountingTestPlan(t)
	commits := executionFreezeTestCommits()
	tools := executionFreezeTestTools(plan, commits)
	if err := validateExecutionTools(tools, plan.ToolPolicy, commits.T422SourceCommit); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"legacy", "wrong source", "module", "invented vcs"} {
		t.Run(mode, func(t *testing.T) {
			candidate := append([]ExecutionToolIdentity(nil), tools...)
			for index := range candidate {
				tool := &candidate[index]
				if tool.Role != "zoekt-git-index" {
					continue
				}
				switch mode {
				case "legacy":
					tool.Provenance = "go-module-build-v1"
				case "wrong source":
					tool.BuildRecipeSHA256 = zoektOfferRecipe(plan.ToolPolicy, strings.Repeat("d", 40))
				case "module":
					tool.ModuleSum = "h1:wrong"
				case "invented vcs":
					tool.BuildVCSRevision = commits.T422SourceCommit
				}
			}
			if err := validateExecutionTools(candidate, plan.ToolPolicy, commits.T422SourceCommit); err == nil {
				t.Fatal("invalid overlay provenance admitted")
			}
		})
	}
	for _, schema := range []string{PlanSchema, PlanV2Schema} {
		legacy := Plan{Schema: schema, ToolPolicy: frozenToolPolicy()}
		if err := validateExecutionTools(executionFreezeTestTools(legacy, commits), legacy.ToolPolicy, commits.T422SourceCommit); err != nil {
			t.Fatal("retained provenance changed", schema, err)
		}
	}
}

// Reads only the already-required pinned module; no native index build or run.
func TestZoektOfferOverlayPinnedSource(t *testing.T) {
	command := exec.CommandContext(t.Context(), "go", "env", "GOMODCACHE")
	raw, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	cache := strings.TrimSpace(string(raw))
	policy := frozenToolPolicy()
	original := filepath.Join(cache, policy.ZoektModulePath+"@"+policy.ZoektModuleVersion, "gitindex", "index.go")
	source, err := readZoektOverlayFile(original, zoektOfferSourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	patched, err := transformZoektOffers(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(patched, []byte("// Copyright 2016 Google Inc.")) || bytes.Count(patched, []byte("if err := t422Offer();")) != 1 || !bytes.Contains(patched, []byte("doc, err := createDocument(key, repos, opts.BuildOptions)")) {
		t.Fatal("native insertion/license differs")
	}
	mutated := bytes.Clone(source)
	mutated[len(mutated)-1] = ' '
	if _, err := transformZoektOffers(mutated); err == nil {
		t.Fatal("changed source admitted")
	}
	if _, err := transformZoektOffers(patched); err == nil {
		t.Fatal("double overlay admitted")
	}
	path, check, err := prepareZoektOfferOverlay(filepath.Join(cache, policy.ZoektModulePath+"@"+policy.ZoektModuleVersion), t.TempDir())
	if err != nil || check() != nil {
		t.Fatal(path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if check() == nil {
		t.Fatal("mutated overlay map admitted")
	}
	t.Logf("replacement bytes=%d sha=%s", len(patched), SHA256(patched))
}

func TestZoektOfferOverlayFileRefusesUnboundedInput(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "input")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readZoektOverlayFile(path, 2); err == nil {
		t.Fatal("wrong size admitted")
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{link, directory, "relative"} {
		if _, err := readZoektOverlayFile(bad, 3); err == nil {
			t.Fatal("invalid shape admitted", bad)
		}
	}
}
