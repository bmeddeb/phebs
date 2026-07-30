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
			UnitDigest: "sha256:unit",
			Domain:     "proto-contract",
		},
		Disposition: DomainOutcomePublished,
		Generation: ExtractionGenerationIdentity{
			Extractor:               "v1",
			CandidateManifestDigest: "sha256:manifest",
		},
		RunID:         "extraction_run:1",
		ReceiptSchema: "phebs-extraction-operation-v1",
		Receipt:       `{"domain":"proto-contract"}`,
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
			"valid without receipt",
			func(o *ExtractionDomainOutcome) {
				o.Receipt = ""
				o.ReceiptSchema = ""
			},
			"",
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
			"repository, commit, and domain",
		},
		{
			"missing commit",
			func(o *ExtractionDomainOutcome) { o.Scope.Commit = "" },
			"repository, commit, and domain",
		},
		{
			"missing domain",
			func(o *ExtractionDomainOutcome) { o.Scope.Domain = "" },
			"repository, commit, and domain",
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
			"schema name",
		},
		{
			"receipt at the cap",
			func(o *ExtractionDomainOutcome) {
				o.Receipt = strings.Repeat("x", MaxExtractionOutcomeReceiptBytes)
			},
			"",
		},
		{
			"receipt one byte over the cap",
			func(o *ExtractionDomainOutcome) {
				o.Receipt = strings.Repeat("x", MaxExtractionOutcomeReceiptBytes+1)
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
