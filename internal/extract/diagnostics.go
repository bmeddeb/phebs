package extract

import (
	"encoding/json"
	"errors"
	"log"
	"time"

	candidatepkg "github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/store"
)

func candidatePointerDiagnosticResult(err error) string {
	switch {
	case err == nil:
		return "current"
	case errors.Is(err, store.ErrNotFound):
		return "missing"
	case errors.Is(err, candidatepkg.ErrPublishing),
		errors.Is(err, ErrCandidateManifestStale):
		return "stale"
	default:
		return "error"
	}
}

const maxExtractionDiagnosticSize = 32 << 10

type extractionPreflightDiagnostic struct {
	Repository         string `json:"repository"`
	PointerResult      string `json:"pointer_result"`
	SettledDomains     int    `json:"settled_domains"`
	SettledComplete    bool   `json:"settled_complete"`
	StrictOpenRequired bool   `json:"strict_open_required"`
	ControlRevision    uint64 `json:"control_revision,omitempty"`
}

type extractionStrictOpenDiagnostic struct {
	Repository      string                       `json:"repository"`
	StrictOpenMS    int64                        `json:"strict_open_ms"`
	ControlRevision uint64                       `json:"control_revision"`
	Manifest        CandidateManifestDiagnostics `json:"manifest"`
}

type schedulerDomainDiagnostic struct {
	Domain            string `json:"domain"`
	State             string `json:"state"`
	Position          int    `json:"position,omitempty"`
	BudgetMS          int64  `json:"budget_ms,omitempty"`
	PreviousAttempt   string `json:"previous_attempt,omitempty"`
	TypedInputPresent *bool  `json:"typed_input_present,omitempty"`
}

type schedulerDiagnostic struct {
	Repository string                      `json:"repository"`
	Domains    []schedulerDomainDiagnostic `json:"domains"`
}

type schedulerDeferralDiagnostic struct {
	Repository string `json:"repository"`
	Trigger    string `json:"trigger"`
	Position   int    `json:"position"`
	Remaining  int    `json:"remaining_domains"`
}

type domainOutcomeDiagnostic struct {
	Repository  string                         `json:"repository"`
	Domain      string                         `json:"domain"`
	Disposition store.DomainOutcomeDisposition `json:"disposition"`
	Reason      string                         `json:"reason"`
	Generation  string                         `json:"generation"`
	Previous    string                         `json:"previous"`
}

func (worker *Worker) logDiagnostic(label string, value any) {
	if worker == nil || !worker.Diagnostics {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("encode %s diagnostic: %v", label, err)
		return
	}
	if len(data) > maxExtractionDiagnosticSize {
		log.Printf("encode %s diagnostic: report exceeds %d bytes", label, maxExtractionDiagnosticSize)
		return
	}
	log.Printf("%s: %s", label, data)
}

func (worker *Worker) logSchedule(
	repository string,
	domains []scheduledDomain,
	nonRunnable []schedulerDomainDiagnostic,
	deadline time.Time,
) {
	if worker == nil || !worker.Diagnostics {
		return
	}
	budget := durationMilliseconds(time.Until(deadline))
	report := schedulerDiagnostic{
		Repository: repository,
		Domains: make(
			[]schedulerDomainDiagnostic, 0, len(domains)+len(nonRunnable),
		),
	}
	for position, domain := range domains {
		state := domain.diagnosticState
		if state == "" {
			state = "never_attempted"
		}
		previous := ""
		if domain.retryable {
			if !domain.attemptedAt.IsZero() {
				previous = domain.attemptedAt.UTC().Format(time.RFC3339Nano)
			}
		}
		report.Domains = append(report.Domains, schedulerDomainDiagnostic{
			Domain: domain.extractor.domain, State: state,
			Position: position + 1, BudgetMS: budget,
			PreviousAttempt: previous, TypedInputPresent: diagnosticBool(
				domain.typedApplicable, domain.typedPresent,
			),
		})
	}
	report.Domains = append(report.Domains, nonRunnable...)
	worker.logDiagnostic("domain scheduler", report)
}

func diagnosticBool(applicable, value bool) *bool {
	if !applicable {
		return nil
	}
	result := value
	return &result
}

func (worker *Worker) logOutcome(
	scope store.ExtractionScope,
	generation store.ExtractionGenerationIdentity,
	disposition store.DomainOutcomeDisposition,
	reason string,
	previous store.DomainOutcomeDisposition,
) {
	if worker == nil || !worker.Diagnostics {
		return
	}
	previousValue := string(previous)
	if previousValue == "" {
		previousValue = "none_for_exact_generation"
	}
	worker.logDiagnostic("domain outcome", domainOutcomeDiagnostic{
		Repository: scope.Repository, Domain: scope.Domain,
		Disposition: disposition, Reason: reason,
		Generation: generation.Digest,
		Previous:   previousValue,
	})
}
