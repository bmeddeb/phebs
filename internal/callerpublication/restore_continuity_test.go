package callerpublication

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/downstreamauthority/authorityvalidate"
)

func restoreContinuityManifest(t *testing.T, runID string) Manifest {
	t.Helper()
	fixture := newPublicationFixture(t, filepath.Join(t.TempDir(), "leaves"), "example.invalid/continuity", '1')
	digest := "sha256:" + strings.Repeat("a", 64)
	type domainIdentity struct {
		Domain  string `json:"domain"`
		Version string `json:"version"`
	}
	// This fixture uses the canonical current upstream wire, not a publication
	// writer change. The production validator checks both hashes below.
	upstream := struct {
		Schema           string                              `json:"schema"`
		Repository       string                              `json:"repository"`
		Observation      json.RawMessage                     `json:"observation"`
		Required         []domainIdentity                    `json:"required"`
		Domains          []authorityvalidate.DomainAuthority `json:"domains"`
		ProvenanceDigest string                              `json:"provenance_digest,omitempty"`
		Digest           string                              `json:"digest"`
	}{Schema: authorityvalidate.Schema, Repository: fixture.generation.Repository,
		Required:    []domainIdentity{{Domain: "grpc-caller", Version: "1.0.0"}},
		Observation: json.RawMessage(`{"version":"observation-v2","repository":"example.invalid/continuity","source_generation_digest":"` + digest + `","source_root_digest":"` + digest + `","observation_generation_digest":"` + digest + `","observation_root_digest":"` + digest + `","partition_policy_digest":"` + digest + `","observation_policy_digest":"` + digest + `","inventory_policy_digest":"` + digest + `","record_count":1,"observed_count":1}`),
		Domains: []authorityvalidate.DomainAuthority{{Domain: "grpc-caller", Version: "1.0.0", RunID: runID, Disposition: "empty",
			PlanDigest: digest, RootDigest: digest, CandidateManifestDigest: digest, CandidatePartitionRootDigest: digest,
			CandidatePolicyDigest: digest, SourceGenerationDigest: digest, ObservationGenerationDigest: digest,
			ExtractionPolicyDigest: digest, DomainIndexDigest: digest, DomainScheduleDigest: digest}},
	}
	encode := func() []byte {
		raw, err := json.Marshal(upstream)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	hash := func(domain string, raw []byte) string {
		sum := sha256.Sum256(append([]byte(domain), raw...))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	upstream.ProvenanceDigest = hash(authorityvalidate.Schema+"-provenance\x00", encode())
	provenance := upstream.ProvenanceDigest
	upstream.ProvenanceDigest, upstream.Domains[0].RunID = "", ""
	semantic := hash(authorityvalidate.Schema+"\x00", encode())
	upstream.ProvenanceDigest, upstream.Domains[0].RunID, upstream.Digest = provenance, runID, semantic
	generation := fixture.generation
	generation.Upstream, generation.UpstreamDigest = encode(), semantic
	generation, err := callerleaf.NewGenerationIdentity(generation)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := callerleaf.NewPairIdentity(generation, fixture.pair.Domain, fixture.pair.ExtractorVersion, fixture.pair.Leaf)
	if err != nil {
		t.Fatal(err)
	}
	receipt := fixture.receipt
	_, receipt.MetadataDigest, err = callerleaf.MetadataFor(generation, pair)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Name = callerleafid.ArtifactName(pair.Digest, receipt.ContentDigest)
	manifest, err := BuildManifest(generation, []PairReceipt{{Pair: pair, Receipt: receipt}})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestRestoreContinuitySHA256AllowsOnlyRunProvenance(t *testing.T) {
	before, after := restoreContinuityManifest(t, "original"), restoreContinuityManifest(t, "restored")
	if before.Generation.Digest != after.Generation.Digest || before.Digest == after.Digest {
		t.Fatal("fixture did not preserve semantic generation and replace exact root")
	}
	publication := &Publication{manifest: before}
	original, _ := json.Marshal(publication.manifest)
	want, err := publication.RestoreContinuitySHA256()
	got, gotErr := (&Publication{manifest: after}).RestoreContinuitySHA256()
	retained, _ := json.Marshal(publication.manifest)
	if err != nil || gotErr != nil || !validDigest(want) || got != want || !bytes.Equal(original, retained) {
		t.Fatalf("continuity = %q/%q, %v/%v; retained unchanged=%v", want, got, err, gotErr, bytes.Equal(original, retained))
	}
	for _, test := range []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"pair set", func(v *Manifest) { v.PairSetDigest = "changed" }},
		{"aggregate", func(v *Manifest) { v.Aggregate.CanonicalBytes++ }},
		{"generation", func(v *Manifest) { v.Generation.CallerPolicy.MaxOpenFiles++ }},
		{"pair", func(v *Manifest) { v.Pairs[0].Pair.Leaf.DeclaredBytes++ }},
		{"content", func(v *Manifest) { v.Pairs[0].Receipt.ContentDigest = "changed" }},
		{"receipt", func(v *Manifest) { v.Pairs[0].Receipt.SourceBlobBytes++ }},
		{"metadata", func(v *Manifest) { v.Pairs[0].Receipt.MetadataDigest = "changed" }},
		{"coverage", func(v *Manifest) { v.Pairs[0].Receipt.CoverageRecordCount++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneManifest(before)
			test.mutate(&changed)
			// Deliberate post-admission corruption pins which fields this
			// projection commits; ordinary cold admission validates their values.
			if digest, err := (&Publication{manifest: changed}).RestoreContinuitySHA256(); err == nil && digest == want {
				t.Fatal("non-provenance field was discarded")
			}
		})
	}
}

func TestRestoreContinuitySHA256RefusesUnavailableAuthority(t *testing.T) {
	manifest := restoreContinuityManifest(t, "original")
	for _, name := range []string{"nil", "schema", "generation", "root", "upstream"} {
		t.Run(name, func(t *testing.T) {
			publication := &Publication{manifest: cloneManifest(manifest)}
			switch name {
			case "nil":
				publication = nil
			case "schema":
				publication.manifest.Schema = "future"
			case "generation":
				publication.manifest.Generation.Schema = callerleaf.GenerationSchema
			case "root":
				publication.manifest.Digest = ""
			case "upstream":
				publication.manifest.Generation.Upstream = []byte(`{}`)
			}
			if _, err := publication.RestoreContinuitySHA256(); err == nil {
				t.Fatal("unavailable authority accepted")
			}
		})
	}
}
