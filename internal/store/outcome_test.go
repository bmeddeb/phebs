package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDomainOutcomeDispositionFrozenSet(t *testing.T) {
	tests := []struct {
		name        string
		disposition DomainOutcomeDisposition
		valid       bool
		settled     bool
	}{
		{"published", DomainOutcomePublished, true, true},
		{"unavailable prerequisite", DomainOutcomeUnavailablePrerequisite, true, true},
		{"terminal refusal", DomainOutcomeTerminalGenerationRefusal, true, true},
		{"retryable failure", DomainOutcomeRetryableFailure, true, false},
		{"empty", "", false, false},
		{"unknown", "wedged", false, false},
		// A near-miss must fail closed: the stored column is compared exactly,
		// so a disposition that only looks right would settle a generation the
		// worker never resolved.
		{"wrong case", "Published", false, false},
		{"padded", " published", false, false},
		{"t30.6a reason leaked in", "published_nonempty", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidDomainOutcomeDisposition(tt.disposition); got != tt.valid {
				t.Errorf("ValidDomainOutcomeDisposition(%q) = %v, want %v",
					tt.disposition, got, tt.valid)
			}
			if got := tt.disposition.Settled(); got != tt.settled {
				t.Errorf("%q.Settled() = %v, want %v",
					tt.disposition, got, tt.settled)
			}
			if !tt.valid && tt.disposition.Settled() {
				t.Errorf("%q settles a generation without being in the frozen set",
					tt.disposition)
			}
		})
	}
}

func validOutcome() ExtractionDomainOutcome {
	return ExtractionDomainOutcome{
		Scope: ExtractionScope{
			Repository: "example.invalid/mono",
			Commit:     strings.Repeat("a", 40),
			UnitDigest: "sha256:" + strings.Repeat("b", 64),
			Domain:     "proto-contract",
		},
		Disposition: DomainOutcomePublished,
		Generation: ExtractionGenerationIdentity{
			Extractor:                "v1",
			CandidateManifestDigest:  "sha256:" + strings.Repeat("c", 64),
			CandidatePolicyDigest:    "sha256:" + strings.Repeat("d", 64),
			CandidateControlRevision: 1,
			InventoryPolicy:          "candidate-manifest-v3-current",
			DependencyDigest:         "sha256:" + strings.Repeat("e", 64),
		},
		RunID:         "extraction_run:1",
		ReceiptSchema: ExtractionOutcomeReceiptSchema,
		Receipt:       `{"schema":"phebs-extraction-domain-outcome-v1"}`,
	}
}

func TestExtractionDomainOutcomeValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ExtractionDomainOutcome)
		wantErr string
	}{
		{"valid published", func(*ExtractionDomainOutcome) {}, ""},
		{
			"valid retryable without run id",
			func(o *ExtractionDomainOutcome) {
				o.Disposition = DomainOutcomeRetryableFailure
				o.RunID = ""
			},
			"",
		},
		{
			"valid whole-repository scope has no unit digest",
			func(o *ExtractionDomainOutcome) { o.Scope.UnitDigest = "" },
			"",
		},
		{
			"missing receipt",
			func(o *ExtractionDomainOutcome) {
				o.Receipt = ""
				o.ReceiptSchema = ""
			},
			"receipt schema",
		},
		{
			"unknown disposition",
			func(o *ExtractionDomainOutcome) { o.Disposition = "settled" },
			"frozen set",
		},
		{
			"empty disposition",
			func(o *ExtractionDomainOutcome) { o.Disposition = "" },
			"frozen set",
		},
		{
			"missing repository",
			func(o *ExtractionDomainOutcome) { o.Scope.Repository = "" },
			"repository",
		},
		{
			"missing commit",
			func(o *ExtractionDomainOutcome) { o.Scope.Commit = "" },
			"commit",
		},
		{
			"missing domain",
			func(o *ExtractionDomainOutcome) { o.Scope.Domain = "" },
			"domain",
		},
		{
			"missing extractor",
			func(o *ExtractionDomainOutcome) { o.Generation.Extractor = "" },
			"extractor version",
		},
		{
			"published without run id",
			func(o *ExtractionDomainOutcome) { o.RunID = "" },
			"run id",
		},
		{
			"receipt without schema",
			func(o *ExtractionDomainOutcome) { o.ReceiptSchema = "" },
			"receipt schema",
		},
		{
			"receipt at the cap",
			func(o *ExtractionDomainOutcome) {
				o.Receipt = `"` +
					strings.Repeat("x", MaxExtractionOutcomeReceiptBytes-2) +
					`"`
			},
			"",
		},
		{
			"receipt one byte over the cap",
			func(o *ExtractionDomainOutcome) {
				o.Receipt = `"` +
					strings.Repeat("x", MaxExtractionOutcomeReceiptBytes-1) +
					`"`
			},
			"limit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome := validOutcome()
			tt.mutate(&outcome)
			err := outcome.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("Validate() = nil, want error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestTerminalMarkerComposesWithClass(t *testing.T) {
	base := errors.New("candidate descriptor mismatch")

	if WithTerminal(nil) != nil {
		t.Error("WithTerminal(nil) must stay nil: there is nothing to refuse")
	}
	if IsTerminal(nil) || IsTerminal(base) {
		t.Error("an unmarked error must not read as terminal")
	}

	// Both orders must preserve both lookups, and both must survive %w
	// wrapping by an intermediate caller.
	tests := []struct {
		name string
		err  error
	}{
		{"terminal inside class", WithClass(ClassExtract, WithTerminal(base))},
		{"class inside terminal", WithTerminal(WithClass(ClassExtract, base))},
		{
			"wrapped by a caller",
			fmt.Errorf("extract mono: %w", WithClass(ClassExtract, WithTerminal(base))),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !IsTerminal(tt.err) {
				t.Error("IsTerminal = false, want true")
			}
			if got := Classify(tt.err); got != ClassExtract {
				t.Errorf("Classify = %q, want %q", got, ClassExtract)
			}
			if !errors.Is(tt.err, base) {
				t.Error("the underlying error must stay reachable via errors.Is")
			}
			if !strings.Contains(tt.err.Error(), base.Error()) {
				t.Errorf("message %q lost the underlying cause", tt.err.Error())
			}
		})
	}

	// A terminal marker must not change the backoff hint: the class still
	// decides how long to wait if some other caller does retry.
	terminal := WithClass(ClassExtract, WithTerminal(base))
	plain := WithClass(ClassExtract, base)
	if DefaultBackoff(terminal, 1) != DefaultBackoff(plain, 1) {
		t.Error("the terminal marker must not alter class-derived backoff")
	}
}

func TestExtractionGenerationIdentityInvalidatesEveryInput(t *testing.T) {
	base := validOutcome().Generation
	base.TypedInputKind = "scip"
	base.TypedInputObjectID = strings.Repeat("f", 40)
	base.TypedInputDigest = "sha256:" + strings.Repeat("1", 64)
	base.TypedInputPresent = true
	base.DependencyDigest = "sha256:" + strings.Repeat("2", 64)
	base.Digest = "caller-controlled"

	tests := []struct {
		name   string
		mutate func(*ExtractionGenerationIdentity)
	}{
		{"candidate manifest", func(g *ExtractionGenerationIdentity) {
			g.CandidateManifestDigest = "sha256:" + strings.Repeat("3", 64)
		}},
		{"candidate policy", func(g *ExtractionGenerationIdentity) {
			g.CandidatePolicyDigest = "sha256:" + strings.Repeat("4", 64)
		}},
		{"candidate control", func(g *ExtractionGenerationIdentity) {
			g.CandidateControlRevision++
		}},
		{"extractor", func(g *ExtractionGenerationIdentity) {
			g.Extractor = "v2"
		}},
		{"inventory policy", func(g *ExtractionGenerationIdentity) {
			g.InventoryPolicy = "candidate-manifest-v4-current"
		}},
		{"typed input kind", func(g *ExtractionGenerationIdentity) {
			g.TypedInputKind = ""
			g.TypedInputObjectID = ""
			g.TypedInputDigest = ""
			g.TypedInputPresent = false
		}},
		{"typed input object", func(g *ExtractionGenerationIdentity) {
			g.TypedInputObjectID = strings.Repeat("e", 40)
		}},
		{"typed input digest", func(g *ExtractionGenerationIdentity) {
			g.TypedInputDigest = "sha256:" + strings.Repeat("5", 64)
		}},
		{"typed input presence", func(g *ExtractionGenerationIdentity) {
			g.TypedInputObjectID = ""
			g.TypedInputDigest = ""
			g.TypedInputPresent = false
		}},
		{"dependency", func(g *ExtractionGenerationIdentity) {
			g.DependencyDigest = "sha256:" + strings.Repeat("6", 64)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			changed.Digest = base.Digest
			test.mutate(&changed)
			if SameExtractionGeneration(base, changed) {
				t.Fatal("changed generation remained current")
			}
		})
	}
	copyWithForgedDigest := base
	copyWithForgedDigest.Digest = "different-caller-value"
	if !SameExtractionGeneration(base, copyWithForgedDigest) {
		t.Fatal("caller-supplied cached digest affected canonical equality")
	}
}
