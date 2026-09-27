package planner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Repeated archive joins reuse complete ancestor sets. The source-heavy
// forward traversal previously exhausted the budget on this immutable graph.
func TestRepeatedConfiguredQueriesReuseCompleteAncestors(t *testing.T) {
	cq, aq, projections := syntheticPlan(t)
	common := "@@//common:lib"
	const denseTargets = 182
	const imports = 64
	for i := range denseTargets {
		deps := make([]string, i)
		for j := range i {
			deps[j] = fmt.Sprintf("@@//dense:node_%03d", j)
		}
		cq = append(cq, cqTarget(fmt.Sprintf("@@//dense:node_%03d", i), "filegroup", deps...)...)
	}
	oldRule := concat(pbString(1, common), pbString(2, "go_library"), pbBytes(15, pbString(1, "@@//common:lib.go")))
	configured := func(rule []byte, id uint64) []byte {
		return frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 1), pbBytes(2, rule))), pbInt(3, id))))
	}
	newRule := concat(oldRule, pbBytes(15, concat(pbString(1, "@@//dense:node_181"), pbString(2, testHash), pbInt(3, 1))))
	cq = bytes.Replace(cq, configured(oldRule, 1), configured(newRule, 1), 1)

	// The second configuration is in the requested universe, through a
	// different root, but is outside the first configuration's dependencies.
	secondHash := strings.Repeat("b", 64)
	cq = append(cq, frame(pbBytes(2, concat(pbInt(1, 2), pbString(4, secondHash))))...)
	cq = append(cq, configured(oldRule, 2)...)
	root := canonicalLabel(Roots()[0])
	rootRule := concat(pbString(1, root), pbString(2, "alias"), pbBytes(15, concat(pbString(1, common), pbString(2, secondHash), pbInt(3, 2))))
	cq = bytes.Replace(cq, cqTarget(root, "alias", common), configured(rootRule, 1), 1)
	aq = append(aq, pbBytes(5, concat(pbInt(1, 2), pbString(4, secondHash)))...)
	var first projection
	const projectionPath = "bazel-out/cfg/bin/common/lib.phebs-plan.json"
	if err := json.Unmarshal(projections[projectionPath], &first); err != nil {
		t.Fatal(err)
	}
	addTestProjection(t, &aq, projections, 2, 2, first)
	for i := range imports {
		first.Archives[0].Imports = append(first.Archives[0].Imports, Import{
			Path: fmt.Sprintf("example.test/alias/%03d", i), Owner: common,
			Export: "bazel-out/cfg/bin/common/lib.x",
		})
	}
	var err error
	projections[projectionPath], err = json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Assemble(cq, aq, projections); err != nil {
		t.Fatalf("repeated exact candidate lookups should reuse completed ancestry: %v", err)
	}
	// Make the second candidate reachable too. Complete cached negative sets
	// from a prior Assemble must never leak across the immutable graph boundary.
	ambiguousRule := concat(newRule, pbBytes(15, concat(pbString(1, common), pbString(2, secondHash), pbInt(3, 2))))
	cq = bytes.Replace(cq, configured(newRule, 1), configured(ambiguousRule, 1), 1)
	if _, err := Assemble(cq, aq, projections); err == nil || err.Error() != "missing or ambiguous declared archive owner" {
		t.Fatalf("memo reuse hid an ambiguous configured candidate: %v", err)
	}
}

// Every generated producer below is distinct, so its reverse closure genuinely
// needs computing. Dense incoming edges exhaust the unchanged shared budget;
// repeated queries alone no longer provide an artificial exhaustion fixture.
func TestUniqueConfiguredTraversalsKeepExplicitWorkBound(t *testing.T) {
	for _, count := range []int{180, 190} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			cq, aq, projections := syntheticPlan(t)
			var p projection
			const projectionPath = "bazel-out/cfg/bin/common/lib.phebs-plan.json"
			if err := json.Unmarshal(projections[projectionPath], &p); err != nil {
				t.Fatal(err)
			}
			for i := range count {
				deps := make([]string, i)
				for j := range i {
					deps[j] = fmt.Sprintf("@@//dense:node_%03d", j)
				}
				owner := fmt.Sprintf("@@//dense:node_%03d", i)
				cq = append(cq, cqTarget(owner, "genrule", deps...)...)
				f := File{Artifact: Artifact{Path: fmt.Sprintf("bazel-out/cfg/bin/dense/file_%03d.go", i), ShortPath: fmt.Sprintf("dense/file_%03d.go", i), Owner: owner}, SHA256: testHash, Bytes: 12}
				p.Archives[0].GoFiles = append(p.Archives[0].GoFiles, f)
				p.Archives[0].CompiledGoFiles = append(p.Archives[0].CompiledGoFiles, f)
			}
			oldRule := concat(pbString(1, "@@//common:lib"), pbString(2, "go_library"), pbBytes(15, pbString(1, "@@//common:lib.go")))
			newRule := concat(oldRule, pbBytes(15, concat(pbString(1, fmt.Sprintf("@@//dense:node_%03d", count-1)), pbString(2, testHash), pbInt(3, 1))))
			configured := func(rule []byte) []byte {
				return frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 1), pbBytes(2, rule))), pbInt(3, 1))))
			}
			cq = bytes.Replace(cq, configured(oldRule), configured(newRule), 1)
			var err error
			projections[projectionPath], err = json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Assemble(cq, aq, projections)
			if count == 180 && err != nil {
				t.Fatalf("bounded completed traversals refused: %v", err)
			}
			if count == 190 && (err == nil || err.Error() != "configured join work limit") {
				t.Fatalf("unique traversal exhaustion did not return an explicit error: %v", err)
			}
		})
	}
}

func TestGeneratedSelfLabelCannotHideConfiguredAmbiguity(t *testing.T) {
	cq, aq, projections := syntheticPlan(t)
	common := "@@//common:lib"
	secondHash := strings.Repeat("b", 64)
	cq = append(cq, frame(pbBytes(2, concat(pbInt(1, 2), pbString(4, secondHash))))...)
	oldRule := concat(pbString(1, common), pbString(2, "go_library"), pbBytes(15, pbString(1, "@@//common:lib.go")))
	configured := func(rule []byte, id uint64) []byte {
		return frame(pbBytes(1, concat(pbBytes(1, concat(pbInt(1, 1), pbBytes(2, rule))), pbInt(3, id))))
	}
	cq = append(cq, configured(oldRule, 2)...)
	newRule := concat(oldRule, pbBytes(15, concat(pbString(1, common), pbString(2, secondHash), pbInt(3, 2))))
	cq = bytes.Replace(cq, configured(oldRule, 1), configured(newRule, 1), 1)
	aq = append(aq, pbBytes(5, concat(pbInt(1, 2), pbString(4, secondHash)))...)
	var p projection
	const projectionPath = "bazel-out/cfg/bin/common/lib.phebs-plan.json"
	if err := json.Unmarshal(projections[projectionPath], &p); err != nil {
		t.Fatal(err)
	}
	addTestProjection(t, &aq, projections, 2, 2, p)
	f := File{Artifact: Artifact{Path: "bazel-out/cfg/bin/common/own.go", ShortPath: "common/own.go", Owner: common}, SHA256: testHash, Bytes: 12}
	p.Archives[0].GoFiles = append(p.Archives[0].GoFiles, f)
	p.Archives[0].CompiledGoFiles = append(p.Archives[0].CompiledGoFiles, f)
	var err error
	projections[projectionPath], err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Assemble(cq, aq, projections); err == nil || err.Error() != "missing or ambiguous configured artifact owner" {
		t.Fatalf("same-label producer shortcut hid ambiguity: %v", err)
	}
}
