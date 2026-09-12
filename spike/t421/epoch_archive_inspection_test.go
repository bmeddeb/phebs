package t421

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestEpochArchiveSemanticBinding(t *testing.T) {
	input := epochArchiveInput{BackupRoot: "t422-backup-aB123", Device: 7, Inode: 8, FSID: [2]int32{9, 10},
		BackupCommandSHA256: testDigest("actual command"), RestoreCommandSHA256: testDigest("actual command")}
	epoch := ExecutionEpochConfig{Epoch: 5, Repository: "fixture/repo", ConfigSHA256: testDigest("config")}
	legacy, err := epochSemanticInput(testDigest("plan"), epoch, nil)
	if err != nil || bytes.Contains(legacy, []byte(`"archive"`)) {
		t.Fatal("omitted archive changed legacy input", err)
	}
	omitted, err := epochSemanticInput(testDigest("plan"), epoch, nil, nil)
	if err != nil || !bytes.Equal(legacy, omitted) {
		t.Fatal("nil optional archive changed bytes", err)
	}
	for _, mode := range []string{"valid", "wrong_epoch", "empty", "prefix", "absolute", "traversal", "separator", "unicode", "long", "inode", "volume", "digest", "unequal", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			value, target := input, epoch
			switch mode {
			case "wrong_epoch":
				target.Epoch = 1
			case "empty":
				value.BackupRoot = "t422-backup-"
			case "prefix":
				value.BackupRoot = "backup-123"
			case "absolute":
				value.BackupRoot = "/" + value.BackupRoot
			case "traversal":
				value.BackupRoot += ".."
			case "separator":
				value.BackupRoot += "/archive"
			case "unicode":
				value.BackupRoot += "é"
			case "long":
				value.BackupRoot += strings.Repeat("a", 129)
			case "inode":
				value.Inode = 0
			case "volume":
				value.FSID = [2]int32{}
			case "digest":
				value.BackupCommandSHA256, value.RestoreCommandSHA256 = "bad", "bad"
			case "unequal":
				value.RestoreCommandSHA256 = testDigest("other command")
			}
			inputs := []*epochArchiveInput{&value}
			if mode == "duplicate" {
				inputs = append(inputs, &value)
			}
			raw, err := epochSemanticInput(testDigest("plan"), target, nil, inputs...)
			if (err == nil) != (mode == "valid") {
				t.Fatal("archive selection", err)
			}
			if err == nil && !bytes.Contains(raw, []byte(`"archive":{"backup_root":"t422-backup-aB123","device":7,"inode":8,"fsid":[9,10],"backup_command_sha256":`)) {
				t.Fatal("canonical native field order changed")
			}
		})
	}
}

// This models the retained native wire/ledger handoff. It does not supply an
// actual pressure-volume pass or exercise protected author construction.
func TestEpochArchivePriorPressureBinding(t *testing.T) {
	for _, mode := range []string{"valid", "nil", "error", "phase", "step", "no_final", "no_baseline", "no_rows", "unaccepted", "epoch", "missing_final", "changed_compact", "changed_projection", "changed_root", "changed_digest"} {
		t.Run(mode, func(t *testing.T) {
			reader, wire := epochTestFinal(t)
			authority, _, err := reader.decodeFinal(epochTestJSON(t, wire, true))
			if err != nil {
				t.Fatal(err)
			}
			reader.staleAuthority = withPhase(authority, "stale_lease")
			reader.projection.Phase = "pressure_75"
			reader.pressure.step, reader.finalUsed = 9, true
			digest := sha256.Sum256(epochTestJSON(t, wire, true))
			reader.pressureBaseline = &digest
			reader.evidence.rows = []ExecutionPhaseInspection{{ServerEpoch: 4, Phase: "pressure_75", SelectorAccepted: true,
				Final: cloneInspectionFinal(ExecutionInspectionFinal{Authority: authority.AuthorityState, Projection: reader.projection})}}
			switch mode {
			case "nil":
				reader = nil
			case "error":
				reader.err = errEpochInspection
			case "phase":
				reader.projection.Phase = "pressure_90"
			case "step":
				reader.pressure.step--
			case "no_final":
				reader.finalUsed = false
			case "no_baseline":
				reader.pressureBaseline = nil
			case "no_rows":
				reader.evidence.rows = nil
			case "unaccepted":
				reader.evidence.rows[0].SelectorAccepted = false
			case "epoch":
				reader.evidence.rows[0].ServerEpoch = 5
			case "missing_final":
				reader.evidence.rows[0].Final = nil
			case "changed_compact":
				reader.evidence.rows[0].Final.Authority.Current = false
			case "changed_projection":
				reader.evidence.rows[0].Final.Projection.SemanticSHA256 = testDigest("changed")
			case "changed_root":
				reader.staleAuthority.ExtractionRoots[0].RootSHA256 = testDigest("changed")
			case "changed_digest":
				reader.pressureBaseline[0] ^= 1
			}
			prior, err := archivePriorFromPressure(reader)
			if (err == nil) != (mode == "valid") {
				t.Fatal("pressure binding", err)
			}
			if err == nil {
				if prior.Phase != "pressure_75" || prior.ExtractionRoots[0].RootSHA256 != authority.ExtractionRoots[0].RootSHA256 {
					t.Fatal("actual prior changed")
				}
				prior.ExtractionRoots[0].PartitionResults[0].ResultDigestSHA256 = testDigest("detached")
				if reader.staleAuthority.ExtractionRoots[0].PartitionResults[0].ResultDigestSHA256 == testDigest("detached") {
					t.Fatal("successor retained mutable predecessor partition slice")
				}
			}
		})
	}
}

// These are comparator counterexamples, not a fabricated native authority
// receipt. The full reader separately validates projection and detailed roots.
func TestEpochArchiveAuthorityComparator(t *testing.T) {
	prior := AuthorityPhaseResult{Phase: "pressure_75", Outcome: "passed", AuthorityState: AuthorityState{
		Current: true, PhysicalCommit: "source", RelationshipGenerationSHA256: "generation", RelationshipRootSHA256: "root",
		RelationshipProvenanceSHA256: "provenance", ResolverCatalogGenerationSHA256: "resolver", CallerGenerationSHA256: "caller"}}
	for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema} {
		for _, mode := range []string{"same", "rebuilt", "generation_only", "root_only", "provenance_only", "resolver", "caller", "physical", "not_current"} {
			t.Run(schema+"/"+mode, func(t *testing.T) {
				current := withPhase(prior, "archive_restore")
				switch mode {
				case "rebuilt":
					current.RelationshipGenerationSHA256, current.RelationshipRootSHA256, current.RelationshipProvenanceSHA256 = "new-generation", "new-root", "new-provenance"
				case "generation_only":
					current.RelationshipGenerationSHA256 = "new-generation"
				case "root_only":
					current.RelationshipRootSHA256 = "new-root"
				case "provenance_only":
					current.RelationshipProvenanceSHA256 = "new-provenance"
				case "resolver":
					current.ResolverCatalogGenerationSHA256 = "new-resolver"
				case "caller":
					current.CallerGenerationSHA256 = "new-caller"
				case "physical":
					current.PhysicalCommit = "other-source"
				case "not_current":
					current.Current = false
				}
				want := mode == "same" || mode == "rebuilt" || schema == PlanSchema && mode != "physical" && mode != "not_current"
				if err := validateArchiveAuthorityContinuity(current, prior, Plan{Schema: schema}); (err == nil) != want {
					t.Fatal("archive authority policy changed", err)
				}
			})
		}
	}
}
