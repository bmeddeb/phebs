// Command t111 is the T11.1 spike harness: revision-pinned Go/gRPC consumer
// evidence + service identity resolution, measured against the four gates in
// docs/BACKLOG.md. Spike-grade code kept for gate reruns (T1.3 precedent).
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	schemaVersion    = "t111-v4"
	extractorVersion = "spike-0.5.0"
	adapterConfig    = "corpus=recursive-ls-tree-cat-file-v2,exact-reviewed-gitlink-exclusions;go=modules,corpus-lock-build-tags,readonly-or-pinned-vendor,offline,workspace-off,toolchain-exact,cgo-off,local-replaces-in-snapshot,external-module-sums-verified-bound;proto=parser-only;identity=static-no-values-no-reach-v3"
)

var (
	toolchainIdentity   = "harness_runtime=" + runtime.Version()
	adapterConfigDigest = configuredAdapterDigest(toolchainIdentity)
)

func configuredAdapterDigest(identity string) string {
	return blobDigest([]byte(adapterConfig + ";tools=" + identity + ";target=" + runtime.GOOS + "/" + runtime.GOARCH))
}

func setToolchainIdentity(identity string) {
	toolchainIdentity = identity
	adapterConfigDigest = configuredAdapterDigest(identity)
}

// Confidence tiers are deterministic; numeric confidence is deliberately
// absent until calibrated against blind holdout labels (PLAN ADR 2026-07-12).
const (
	tierExact      = "exact"
	tierDerived    = "derived"
	tierHeuristic  = "heuristic"
	tierUnresolved = "unresolved"
)

// Code roles per T11.1 gate 2. Order of precedence in classifyRole.
const (
	roleVendor     = "vendor"
	roleMock       = "mock"
	roleGenerated  = "generated"
	roleTest       = "test"
	roleProduction = "production"
)

// Atom is the content-keyed evidence identity (PLAN ADR 2026-07-12):
// identical blobs produce identical atoms regardless of which repository or
// commit they appear in. Repository placement lives in Fact (the spike's
// flattened snapshot_evidence association), never in the atom.
type Atom struct {
	SchemaVersion       string `json:"schema_version"`
	BlobDigest          string `json:"blob_digest"` // sha256 of the file content
	StartByte           int    `json:"start_byte"`
	EndByte             int    `json:"end_byte"`
	RuleID              string `json:"rule_id"`
	ExtractorVersion    string `json:"extractor_version"`
	AdapterConfigDigest string `json:"adapter_config_digest"`
	FactFingerprint     string `json:"fact_fingerprint"`
}

// ID is deterministic: same input and extractor version, same ID (gate 1).
// Fields are length-delimited to make the encoding injective.
func (a Atom) ID() string {
	h := sha256.New()
	for _, s := range []string{
		a.SchemaVersion, a.BlobDigest,
		fmt.Sprintf("%d-%d", a.StartByte, a.EndByte),
		a.RuleID, a.ExtractorVersion, a.AdapterConfigDigest, a.FactFingerprint,
	} {
		_, _ = fmt.Fprintf(h, "%d:%s;", len(s), s)
	}
	return "ea_" + hex.EncodeToString(h.Sum(nil))
}

// Fact is one spike assertion with its evidence occurrence flattened in.
// Subject stays at package/module granularity — global service identity is
// gate 3's separate measurement, never smuggled into gate 2 facts.
type Fact struct {
	Predicate string `json:"predicate"` // DECLARES_OPERATION | DECLARES_FIELD | IMPLEMENTS_SERVICE | CALLS_OPERATION
	Subject   string `json:"subject"`   // repo-relative package or proto path
	Object    string `json:"object"`    // fully-qualified proto service/method or field key
	CodeRole  string `json:"code_role"`
	Tier      string `json:"tier"`

	// Occurrence (snapshot_evidence, flattened for the spike).
	System    string `json:"system"` // corpus system name
	Commit    string `json:"commit"`
	Path      string `json:"path"` // repo-relative
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`

	RuleID string `json:"rule_id"`
	AtomID string `json:"atom_id"`
	Detail string `json:"detail,omitempty"`

	// Materialized atom provenance makes JSONL independently auditable: a
	// verifier can hash the pinned Git blob, reconstruct the atom, and compare
	// its ID without trusting this process's in-memory state.
	SchemaVersion        string `json:"schema_version"`
	BlobDigest           string `json:"blob_digest"`
	ExtractorVersion     string `json:"extractor_version"`
	AdapterConfigDigest  string `json:"adapter_config_digest"`
	FactFingerprint      string `json:"fact_fingerprint"`
	AssertionID          string `json:"assertion_id"`
	ToolchainIdentity    string `json:"toolchain_identity"`
	SemanticInputsDigest string `json:"semantic_inputs_digest,omitempty"`
}

func newFact(pred, subject, object, role, tier, system, commit, path string,
	startByte, endByte, startLine, endLine int, ruleID, blobDigest, detail string,
) Fact {
	fingerprint := factFingerprint(pred, subject, object, detail)
	atom := Atom{
		SchemaVersion:       schemaVersion,
		BlobDigest:          blobDigest,
		StartByte:           startByte,
		EndByte:             endByte,
		RuleID:              ruleID,
		ExtractorVersion:    extractorVersion,
		AdapterConfigDigest: adapterConfigDigest,
		FactFingerprint:     fingerprint,
	}
	fact := Fact{
		Predicate: pred, Subject: subject, Object: object,
		CodeRole: role, Tier: tier,
		System: system, Commit: commit, Path: path,
		StartByte: startByte, EndByte: endByte,
		StartLine: startLine, EndLine: endLine,
		RuleID: ruleID, AtomID: atom.ID(), Detail: detail,
		SchemaVersion: schemaVersion, BlobDigest: blobDigest,
		ExtractorVersion: extractorVersion, AdapterConfigDigest: adapterConfigDigest,
		FactFingerprint: fingerprint, ToolchainIdentity: toolchainIdentity,
	}
	fact.AssertionID = assertionID(fact)
	return fact
}

// factFingerprint identifies what a content span proves without repository
// placement. Primitive atoms deliberately omit repo-relative subjects and
// lineage detail so identical vendored blobs deduplicate across repositories.
// AssertionID below carries the complete occurrence-level assertion identity.
func factFingerprint(predicate, subject, object, detail string) string {
	values := []string{predicate, object}
	switch predicate {
	case "DECLARES_OPERATION", "DECLARES_FIELD", "IMPLEMENTS_SERVICE":
		// subject is a repo-relative occurrence; declaration detail is the
		// repository-derived lineage placeholder used only by this spike.
	case "CALLS_OPERATION", "REFERENCES_PROTO_FIELD":
		// Interface/type/access detail changes interpretation of the span and
		// is stable for identical source and module semantics.
		values = append(values, detail)
	case "SERVICE_REACHABLE_FROM_BINARY", "CALLER_REACHABLE_FROM_BINARY":
		// The content atom is the primitive supporting fact. Build target and
		// source-package placement live in AssertionID.
		values = append(values, detailValue(detail, "support"))
	default:
		// Identity facts derive their subject/detail from the same blob. They
		// are needed to distinguish multiple assertions sharing a coarse span.
		values = append(values, subject, detail)
	}
	h := sha256.New()
	for _, value := range values {
		_, _ = fmt.Fprintf(h, "%d:%s;", len(value), value)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func factFingerprintWithInputs(predicate, subject, object, detail, semanticInputs string) string {
	fingerprint := factFingerprint(predicate, subject, object, detail)
	if semanticInputs == "" {
		return fingerprint
	}
	return blobDigest([]byte(fingerprint + ";semantic_inputs=" + semanticInputs))
}

func detailValue(detail, key string) string {
	prefix := key + "="
	for _, field := range strings.Split(detail, ";") {
		if strings.HasPrefix(field, prefix) {
			return strings.TrimPrefix(field, prefix)
		}
	}
	return ""
}

func assertionID(f Fact) string {
	h := sha256.New()
	detail := f.Detail
	if f.Predicate == "SERVICE_REACHABLE_FROM_BINARY" || f.Predicate == "CALLER_REACHABLE_FROM_BINARY" {
		detail = ""
	}
	// Semantic subjects in this spike are repository-local packages, paths, or
	// deployables. Keep assertions independent of commit/evidence occurrence,
	// but scope them to the corpus repository so identical local names in two
	// repositories cannot alias.
	for _, value := range []string{f.System, f.Predicate, f.Subject, f.Object, detail} {
		_, _ = fmt.Fprintf(h, "%d:%s;", len(value), value)
	}
	return "as_" + hex.EncodeToString(h.Sum(nil))
}

func (f Fact) atom() Atom {
	return Atom{
		SchemaVersion:       f.SchemaVersion,
		BlobDigest:          f.BlobDigest,
		StartByte:           f.StartByte,
		EndByte:             f.EndByte,
		RuleID:              f.RuleID,
		ExtractorVersion:    f.ExtractorVersion,
		AdapterConfigDigest: f.AdapterConfigDigest,
		FactFingerprint:     f.FactFingerprint,
	}
}

func (f Fact) validateAtom() error {
	if f.SchemaVersion == "" || f.BlobDigest == "" || f.ExtractorVersion == "" ||
		f.AdapterConfigDigest == "" || f.FactFingerprint == "" || f.AtomID == "" || f.AssertionID == "" ||
		f.ToolchainIdentity == "" {
		return fmt.Errorf("incomplete materialized atom provenance")
	}
	if want := configuredAdapterDigest(f.ToolchainIdentity); f.AdapterConfigDigest != want {
		return fmt.Errorf("adapter config digest %s does not match recorded toolchain identity %s", f.AdapterConfigDigest, want)
	}
	if typeDerivedPredicate(f.Predicate) && f.SemanticInputsDigest == "" {
		return fmt.Errorf("type-derived fact lacks a semantic input digest")
	}
	if f.SemanticInputsDigest != "" {
		encoded := strings.TrimPrefix(f.SemanticInputsDigest, "sha256:")
		decoded, err := hex.DecodeString(encoded)
		if !strings.HasPrefix(f.SemanticInputsDigest, "sha256:") || err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("invalid semantic input digest %q", f.SemanticInputsDigest)
		}
	}
	wantFingerprint := factFingerprintWithInputs(f.Predicate, f.Subject, f.Object, f.Detail, f.SemanticInputsDigest)
	if f.FactFingerprint != wantFingerprint {
		return fmt.Errorf("fact fingerprint %s does not match semantic assertion %s", f.FactFingerprint, wantFingerprint)
	}
	if want := f.atom().ID(); f.AtomID != want {
		return fmt.Errorf("atom ID %s does not match materialized provenance %s", f.AtomID, want)
	}
	if want := assertionID(f); f.AssertionID != want {
		return fmt.Errorf("assertion ID %s does not match materialized assertion %s", f.AssertionID, want)
	}
	return nil
}

func typeDerivedPredicate(predicate string) bool {
	switch predicate {
	case "CALLS_OPERATION", "IMPLEMENTS_SERVICE", "REFERENCES_PROTO_FIELD",
		"SERVICE_REACHABLE_FROM_BINARY", "CALLER_REACHABLE_FROM_BINARY":
		return true
	default:
		return false
	}
}

func newWholeFileFact(pred, subject, object, role, tier, system, commit, root, path, ruleID, detail string) (Fact, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return Fact{}, fmt.Errorf("evidence path %q is not repository-relative", path)
	}
	content, err := os.ReadFile(filepath.Join(root, clean))
	if err != nil {
		return Fact{}, fmt.Errorf("read evidence %s: %w", path, err)
	}
	endLine := lineAtOffset(content, len(content))
	return newFact(pred, subject, object, role, tier, system, commit, filepath.ToSlash(clean),
		0, len(content), 1, endLine, ruleID, blobDigest(content), detail), nil
}

func lineAtOffset(content []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	return 1 + bytes.Count(content[:offset], []byte{'\n'})
}

func blobDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
