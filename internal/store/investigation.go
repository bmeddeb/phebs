package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// InvestigationStore is the T16.1 persistence boundary. Immutable semantic
// records have only create/read methods. Investigation and Watch are the two
// explicitly mutable projections; their update methods reject changes to
// immutable identity fields.
type InvestigationStore interface {
	CreateInvestigation(context.Context, Investigation) (*Investigation, error)
	GetInvestigation(context.Context, string) (*Investigation, error)
	UpdateInvestigation(context.Context, Investigation) (*Investigation, error)
	PutRevision(context.Context, Revision) (*Revision, error)
	GetRevision(context.Context, string) (*Revision, error)
	CreateRun(context.Context, RunRequest) (*Run, error)
	GetRun(context.Context, string) (*Run, error)
	ListRunEvents(context.Context, string) ([]RunEvent, error)
	AppendRunEvent(context.Context, string, RunEvent) (*RunEvent, error)
	PutRunArtifact(context.Context, RunArtifact) (*RunArtifact, error)
	GetRunArtifact(context.Context, string) (*RunArtifact, error)
	PutDecision(context.Context, Decision) (*Decision, error)
	GetDecision(context.Context, string) (*Decision, error)
	PutDisposition(context.Context, Disposition) (*Disposition, error)
	GetDisposition(context.Context, string) (*Disposition, error)
	PutBaselineDesignation(context.Context, BaselineDesignation) (*BaselineDesignation, error)
	GetBaselineDesignation(context.Context, string) (*BaselineDesignation, error)
	CreateWatch(context.Context, Watch) (*Watch, error)
	GetWatch(context.Context, string) (*Watch, error)
	UpdateWatch(context.Context, Watch) (*Watch, error)
	PutWatchRevision(context.Context, WatchRevision) (*WatchRevision, error)
	GetWatchRevision(context.Context, string) (*WatchRevision, error)
}

var _ InvestigationStore = (*Surreal)(nil)

const maxInvestigationFieldBytes = 64 << 10

type Investigation struct {
	ID                string    `json:"investigation_id"`
	Referent          string    `json:"referent"`
	ClaimFamily       string    `json:"claim_family"`
	Title             string    `json:"title"`
	Owner             string    `json:"owner"`
	Lifecycle         string    `json:"lifecycle"`
	CurrentRevisionID string    `json:"current_revision_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Revision struct {
	ID                 string    `json:"revision_id"`
	InvestigationID    string    `json:"investigation_id"`
	Seq                int       `json:"seq"`
	NormalizedQuestion string    `json:"normalized_question"`
	DecisionSought     string    `json:"decision_sought"`
	DeclaredUniverse   string    `json:"declared_universe"`
	SnapshotPolicy     string    `json:"snapshot_policy"`
	BuildConfiguration string    `json:"build_configuration"`
	PackSelection      string    `json:"pack_selection"`
	EnumerationMethod  string    `json:"enumeration_method"`
	Creator            string    `json:"creator"`
	CreatedAt          time.Time `json:"created_at"`
	ContentDigest      string    `json:"content_digest"`
}

type RunState string

const (
	RunQueued      RunState = "queued"
	RunEnumerating RunState = "enumerating"
	RunAnalyzing   RunState = "analyzing"
	RunPublishing  RunState = "publishing"
	RunPublished   RunState = "published"
	RunFailed      RunState = "failed"
	RunCanceled    RunState = "canceled"
)

type RunRequest struct {
	RevisionID              string   `json:"revision_id"`
	IdempotencyKey          string   `json:"idempotency_key"`
	ResolvedInputIdentities []string `json:"resolved_input_identities"`
	RequestedSnapshot       string   `json:"requested_snapshot"`
	InputManifestDigest     string   `json:"input_manifest_digest"`
	RequestedPackVersions   []string `json:"requested_pack_versions"`
	CreatedBy               string   `json:"created_by"`
}

type Run struct {
	ID                      string    `json:"run_id"`
	RevisionID              string    `json:"revision_id"`
	Seq                     int       `json:"seq"`
	IdempotencyKey          string    `json:"idempotency_key"`
	ResolvedInputIdentities []string  `json:"resolved_input_identities"`
	RequestedSnapshot       string    `json:"requested_snapshot"`
	InputManifestDigest     string    `json:"input_manifest_digest"`
	RequestedPackVersions   []string  `json:"requested_pack_versions"`
	CreatedBy               string    `json:"created_by"`
	CreatedAt               time.Time `json:"created_at"`
	State                   RunState  `json:"state"` // projection of RunEvents; never stored on the Run row
}

type RunEvent struct {
	ID         string    `json:"event_id"`
	RunID      string    `json:"run_id"`
	Sequence   int       `json:"sequence"`
	Attempt    int       `json:"attempt"`
	PriorState RunState  `json:"prior_state,omitempty"`
	NewState   RunState  `json:"new_state"`
	Actor      string    `json:"actor"`
	Reason     string    `json:"reason"`
	Timestamp  time.Time `json:"timestamp"`
	Digest     string    `json:"content_digest"`
}

type RunArtifact struct {
	ID                string    `json:"artifact_id"`
	Scope             string    `json:"scope"`
	RunID             string    `json:"run_id"`
	TerminalStatus    RunState  `json:"terminal_status"`
	SnapshotManifest  string    `json:"snapshot_manifest"`
	InputManifest     string    `json:"input_manifest"`
	CoverageLedger    string    `json:"coverage_ledger"`
	FactReferences    []string  `json:"fact_references,omitempty"`
	PinReferences     []string  `json:"pin_references,omitempty"`
	EligibilityResult string    `json:"eligibility_result"`
	DiagnosticData    string    `json:"diagnostic_data,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	ContentDigest     string    `json:"content_digest"`
}

type Decision struct {
	ID                string     `json:"decision_id"`
	InvestigationID   string     `json:"investigation_id"`
	ClaimScope        string     `json:"claim_scope"`
	Conclusion        string     `json:"conclusion"`
	RevisionID        string     `json:"revision_id"`
	RunArtifactID     string     `json:"run_artifact_id,omitempty"`
	BaselineID        string     `json:"baseline_id,omitempty"`
	EligibilityResult string     `json:"eligibility_result"`
	VisibleScope      string     `json:"visible_scope"`
	Authority         string     `json:"authority"`
	AuthoritySource   string     `json:"authority_source"`
	Rationale         string     `json:"rationale"`
	PolicyVersion     string     `json:"policy_version,omitempty"`
	EffectiveFrom     time.Time  `json:"effective_from"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	Supersedes        string     `json:"supersedes,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	ContentDigest     string     `json:"content_digest"`
}

type Disposition struct {
	ID                 string     `json:"disposition_id"`
	InvestigationID    string     `json:"investigation_id"`
	Category           string     `json:"category"`
	SubjectKind        string     `json:"subject_kind"`
	SubjectID          string     `json:"subject_id"`
	Actor              string     `json:"actor"`
	AuthorityBasis     string     `json:"authority_basis"`
	Rationale          string     `json:"rationale"`
	EffectiveFrom      time.Time  `json:"effective_from"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	PolicyReference    string     `json:"policy_reference,omitempty"`
	ExternalDecisionID string     `json:"external_decision_id,omitempty"`
	ReferencedFactID   string     `json:"referenced_fact_id,omitempty"`
	ReferencedCoverage string     `json:"referenced_coverage,omitempty"`
	Supersedes         string     `json:"supersedes,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	ContentDigest      string     `json:"content_digest"`
}

type BaselineDesignation struct {
	ID              string    `json:"designation_id"`
	InvestigationID string    `json:"investigation_id"`
	ClaimScope      string    `json:"claim_scope"`
	WorkflowScope   string    `json:"workflow_scope"`
	RevisionID      string    `json:"revision_id"`
	RunArtifactID   string    `json:"run_artifact_id"`
	Authority       string    `json:"authority"`
	Rationale       string    `json:"rationale"`
	Supersedes      string    `json:"supersedes,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	ContentDigest   string    `json:"content_digest"`
}

type Watch struct {
	ID                string     `json:"watch_id"`
	Owner             string     `json:"owner"`
	Enabled           bool       `json:"enabled"`
	CurrentRevisionID string     `json:"current_revision_id,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	EvaluationCursor  string     `json:"evaluation_cursor,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type WatchRevision struct {
	ID              string    `json:"watch_revision_id"`
	WatchID         string    `json:"watch_id"`
	Seq             int       `json:"seq"`
	InvestigationID string    `json:"investigation_id"`
	RevisionID      string    `json:"revision_id"`
	TypedQuery      string    `json:"typed_query"`
	TriggerPolicy   string    `json:"trigger_policy"`
	Coalescing      string    `json:"coalescing"`
	NoiseControls   string    `json:"noise_controls"`
	Creator         string    `json:"creator"`
	CreatedAt       time.Time `json:"created_at"`
	ContentDigest   string    `json:"content_digest"`
}

func investigationRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation", id)
}

func revisionRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_revision", id)
}

func runRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_run", id)
}

func runEventRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_run_event", id)
}

func runArtifactRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_run_artifact", id)
}

func decisionRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_decision", id)
}

func dispositionRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_disposition", id)
}

func baselineRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_baseline_designation", id)
}

func watchRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_watch", id)
}

func watchRevisionRecordID(id string) models.RecordID {
	return models.NewRecordID("investigation_watch_revision", id)
}

func firstDomainRows[T any](results *[]surrealdb.QueryResult[[]T]) []T {
	for _, result := range *results {
		if len(result.Result) > 0 {
			return result.Result
		}
	}
	return nil
}

func validateDomainString(name, value string, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(value) || len(value) > maxInvestigationFieldBytes {
		return fmt.Errorf("%s is not bounded valid UTF-8", name)
	}
	return nil
}

func canonicalStrings(name string, values []string) ([]string, error) {
	if len(values) > 10_000 {
		return nil, fmt.Errorf("%s list exceeds 10000 entries", name)
	}
	values = slices.Clone(values)
	for _, value := range values {
		if err := validateDomainString(name, value, true); err != nil {
			return nil, err
		}
	}
	slices.Sort(values)
	values = slices.Compact(values)
	return values, nil
}

func contentDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func newULID(now time.Time) (string, error) {
	var raw [16]byte
	millis := uint64(now.UTC().UnixMilli())
	for i := 5; i >= 0; i-- {
		raw[i] = byte(millis)
		millis >>= 8
	}
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("generate ULID: %w", err)
	}
	n := new(big.Int).SetBytes(raw[:])
	base := big.NewInt(32)
	var out [26]byte
	for i := len(out) - 1; i >= 0; i-- {
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(n, base, r)
		out[i] = crockford[r.Int64()]
		n = q
	}
	return string(out[:]), nil
}

func validULID(value string) bool {
	if len(value) != 26 || value[0] > '7' {
		return false
	}
	for i := range value {
		if !strings.ContainsRune(crockford, rune(value[i])) {
			return false
		}
	}
	return true
}

func validInvestigationLifecycle(value string) bool {
	return value == "draft" || value == "active" || value == "concluded" || value == "archived"
}

func validLifecycleTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case "draft":
		return to == "active" || to == "archived"
	case "active":
		return to == "concluded" || to == "archived"
	case "concluded":
		return to == "active" || to == "archived"
	default:
		return false
	}
}

func (s *Surreal) CreateInvestigation(ctx context.Context, in Investigation) (*Investigation, error) {
	for name, value := range map[string]string{
		"referent": in.Referent, "claim family": in.ClaimFamily,
		"title": in.Title, "owner": in.Owner,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return nil, fmt.Errorf("create investigation: %w", err)
		}
	}
	if in.Lifecycle == "" {
		in.Lifecycle = "draft"
	}
	if !validInvestigationLifecycle(in.Lifecycle) {
		return nil, errors.New("create investigation: invalid lifecycle")
	}
	if in.ID == "" {
		var err error
		in.ID, err = newULID(time.Now())
		if err != nil {
			return nil, err
		}
	} else if !validULID(in.ID) {
		return nil, errors.New("create investigation: invalid ULID")
	}
	if in.CurrentRevisionID != "" {
		return nil, errors.New("create investigation: current revision is set only after revision creation")
	}
	now := time.Now().UTC()
	in.CreatedAt, in.UpdatedAt = now, now
	results, err := surrealdb.Query[[]Investigation](ctx, s.db,
		`CREATE $rid SET investigation_id = $investigation_id, referent = $referent,
			claim_family = $claim_family, title = $title, owner = $owner,
			lifecycle = $lifecycle, created_at = $now, updated_at = $now RETURN AFTER`,
		map[string]any{
			"rid": investigationRecordID(in.ID), "investigation_id": in.ID,
			"referent": in.Referent, "claim_family": in.ClaimFamily, "title": in.Title,
			"owner": in.Owner, "lifecycle": in.Lifecycle, "now": now,
		})
	if err != nil {
		return nil, fmt.Errorf("create investigation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("create investigation returned no row")
	}
	return &rows[0], nil
}

func (s *Surreal) GetInvestigation(ctx context.Context, id string) (*Investigation, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]Investigation](ctx, s.db,
		"SELECT investigation_id, referent, claim_family, title, owner, lifecycle, current_revision_id, created_at, updated_at FROM $rid LIMIT 1",
		map[string]any{"rid": investigationRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get investigation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (s *Surreal) UpdateInvestigation(ctx context.Context, in Investigation) (*Investigation, error) {
	existing, err := s.GetInvestigation(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Referent != existing.Referent || in.ClaimFamily != existing.ClaimFamily ||
		(!in.CreatedAt.IsZero() && !in.CreatedAt.Equal(existing.CreatedAt)) {
		return nil, fmt.Errorf("update investigation: immutable identity field changed: %w", ErrConflict)
	}
	for name, value := range map[string]string{"title": in.Title, "owner": in.Owner} {
		if err := validateDomainString(name, value, true); err != nil {
			return nil, fmt.Errorf("update investigation: %w", err)
		}
	}
	if !validLifecycleTransition(existing.Lifecycle, in.Lifecycle) {
		return nil, fmt.Errorf("update investigation: invalid lifecycle transition: %w", ErrConflict)
	}
	if in.CurrentRevisionID != "" {
		revision, getErr := s.GetRevision(ctx, in.CurrentRevisionID)
		if getErr != nil || revision.InvestigationID != in.ID {
			return nil, fmt.Errorf("update investigation: current revision is unavailable: %w", ErrConflict)
		}
	}
	now := time.Now().UTC()
	results, err := surrealdb.Query[[]Investigation](ctx, s.db,
		`UPDATE $rid SET title = $title, owner = $owner, lifecycle = $lifecycle,
			current_revision_id = $current_revision_id, updated_at = $now
			WHERE referent = $referent AND claim_family = $claim_family RETURN AFTER`,
		map[string]any{
			"rid": investigationRecordID(in.ID), "title": in.Title, "owner": in.Owner,
			"lifecycle": in.Lifecycle, "current_revision_id": optionalValue(in.CurrentRevisionID),
			"now": now, "referent": existing.Referent, "claim_family": existing.ClaimFamily,
		})
	if err != nil {
		return nil, fmt.Errorf("update investigation: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, fmt.Errorf("update investigation: concurrent identity change: %w", ErrConflict)
	}
	return &rows[0], nil
}

func optionalValue(value string) string {
	return value
}

func revisionIdentity(investigationID string, seq int) string {
	return hashIdentity("ivr_", investigationID, fmt.Sprint(seq))
}

func revisionCore(revision Revision) any {
	return struct {
		ID, InvestigationID                string
		Seq                                int
		NormalizedQuestion, DecisionSought string
		DeclaredUniverse, SnapshotPolicy   string
		BuildConfiguration, PackSelection  string
		EnumerationMethod, Creator         string
	}{
		revision.ID, revision.InvestigationID, revision.Seq,
		revision.NormalizedQuestion, revision.DecisionSought,
		revision.DeclaredUniverse, revision.SnapshotPolicy,
		revision.BuildConfiguration, revision.PackSelection,
		revision.EnumerationMethod, revision.Creator,
	}
}

func validateRevision(revision *Revision) error {
	if !validULID(revision.InvestigationID) || revision.Seq < 1 {
		return errors.New("investigation ULID and positive sequence are required")
	}
	wantID := revisionIdentity(revision.InvestigationID, revision.Seq)
	if revision.ID != "" && revision.ID != wantID {
		return errors.New("revision id does not match investigation and sequence")
	}
	revision.ID = wantID
	for name, value := range map[string]string{
		"normalized question": revision.NormalizedQuestion,
		"decision sought":     revision.DecisionSought,
		"declared universe":   revision.DeclaredUniverse,
		"snapshot policy":     revision.SnapshotPolicy,
		"build configuration": revision.BuildConfiguration,
		"pack selection":      revision.PackSelection,
		"enumeration method":  revision.EnumerationMethod,
		"creator":             revision.Creator,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return err
		}
	}
	digest, err := contentDigest(revisionCore(*revision))
	if err != nil {
		return err
	}
	if revision.ContentDigest != "" && revision.ContentDigest != digest {
		return errors.New("revision content digest does not match immutable fields")
	}
	revision.ContentDigest = digest
	return nil
}

func (s *Surreal) PutRevision(ctx context.Context, revision Revision) (*Revision, error) {
	if err := validateRevision(&revision); err != nil {
		return nil, fmt.Errorf("put revision: %w", err)
	}
	if _, err := s.GetInvestigation(ctx, revision.InvestigationID); err != nil {
		return nil, fmt.Errorf("put revision: investigation: %w", err)
	}
	if existing, err := s.GetRevision(ctx, revision.ID); err == nil {
		if existing.ContentDigest != revision.ContentDigest {
			return nil, fmt.Errorf("put revision: immutable content changed: %w", ErrConflict)
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if revision.Seq > 1 {
		if _, err := s.GetRevision(ctx, revisionIdentity(revision.InvestigationID, revision.Seq-1)); err != nil {
			return nil, fmt.Errorf("put revision: previous sequence is unavailable: %w", ErrConflict)
		}
	}
	revision.CreatedAt = time.Now().UTC()
	results, err := surrealdb.Query[[]Revision](ctx, s.db,
		`CREATE $rid SET revision_id = $revision_id, investigation_id = $investigation_id,
			seq = $seq, normalized_question = $normalized_question, decision_sought = $decision_sought,
			declared_universe = $declared_universe, snapshot_policy = $snapshot_policy,
			build_configuration = $build_configuration, pack_selection = $pack_selection,
			enumeration_method = $enumeration_method, creator = $creator,
			created_at = $created_at, content_digest = $content_digest RETURN AFTER`,
		map[string]any{
			"rid": revisionRecordID(revision.ID), "revision_id": revision.ID,
			"investigation_id": revision.InvestigationID, "seq": revision.Seq,
			"normalized_question": revision.NormalizedQuestion, "decision_sought": revision.DecisionSought,
			"declared_universe": revision.DeclaredUniverse, "snapshot_policy": revision.SnapshotPolicy,
			"build_configuration": revision.BuildConfiguration, "pack_selection": revision.PackSelection,
			"enumeration_method": revision.EnumerationMethod, "creator": revision.Creator,
			"created_at": revision.CreatedAt, "content_digest": revision.ContentDigest,
		})
	if err != nil {
		// A concurrent exact create is idempotent; a different row at the same
		// pair is an immutable conflict.
		if existing, getErr := s.GetRevision(ctx, revision.ID); getErr == nil {
			if existing.ContentDigest == revision.ContentDigest {
				return existing, nil
			}
			return nil, fmt.Errorf("put revision: immutable content changed: %w", ErrConflict)
		}
		return nil, fmt.Errorf("put revision: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("put revision returned no row")
	}
	return &rows[0], nil
}

func (s *Surreal) GetRevision(ctx context.Context, id string) (*Revision, error) {
	if !strings.HasPrefix(id, "ivr_") {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]Revision](ctx, s.db,
		"SELECT revision_id, investigation_id, seq, normalized_question, decision_sought, declared_universe, snapshot_policy, build_configuration, pack_selection, enumeration_method, creator, created_at, content_digest FROM $rid LIMIT 1",
		map[string]any{"rid": revisionRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get revision: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	row := rows[0]
	storedDigest := row.ContentDigest
	if err := validateRevision(&row); err != nil || row.ContentDigest != storedDigest {
		return nil, errors.New("get revision: stored immutable content is inconsistent")
	}
	return &row, nil
}

type investigationRunRec struct {
	ID                      string    `json:"run_id"`
	RevisionID              string    `json:"revision_id"`
	Seq                     int       `json:"seq"`
	IdempotencyKey          string    `json:"idempotency_key"`
	ResolvedInputIdentities []string  `json:"resolved_input_identities"`
	RequestedSnapshot       string    `json:"requested_snapshot"`
	InputManifestDigest     string    `json:"input_manifest_digest"`
	RequestedPackVersions   []string  `json:"requested_pack_versions"`
	CreatedBy               string    `json:"created_by"`
	CreatedAt               time.Time `json:"created_at"`
	RequestDigest           string    `json:"request_digest"`
	EventRevision           int       `json:"event_revision"`
}

func (r investigationRunRec) run(state RunState) Run {
	return Run{
		ID: r.ID, RevisionID: r.RevisionID, Seq: r.Seq,
		IdempotencyKey:          r.IdempotencyKey,
		ResolvedInputIdentities: slices.Clone(r.ResolvedInputIdentities),
		RequestedSnapshot:       r.RequestedSnapshot, InputManifestDigest: r.InputManifestDigest,
		RequestedPackVersions: slices.Clone(r.RequestedPackVersions), CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, State: state,
	}
}

func normalizeRunRequest(request RunRequest) (RunRequest, string, error) {
	if !strings.HasPrefix(request.RevisionID, "ivr_") {
		return RunRequest{}, "", errors.New("revision id is required")
	}
	for name, value := range map[string]string{
		"idempotency key":       request.IdempotencyKey,
		"requested snapshot":    request.RequestedSnapshot,
		"input manifest digest": request.InputManifestDigest,
		"created by":            request.CreatedBy,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return RunRequest{}, "", err
		}
	}
	var err error
	request.ResolvedInputIdentities, err = canonicalStrings("resolved input identity", request.ResolvedInputIdentities)
	if err != nil {
		return RunRequest{}, "", err
	}
	request.RequestedPackVersions, err = canonicalStrings("requested pack version", request.RequestedPackVersions)
	if err != nil {
		return RunRequest{}, "", err
	}
	if len(request.ResolvedInputIdentities) == 0 || len(request.RequestedPackVersions) == 0 {
		return RunRequest{}, "", errors.New("resolved inputs and requested pack versions are required")
	}
	digest, err := contentDigest(request)
	return request, digest, err
}

func runIdentity(revisionID, idempotencyKey string) string {
	return hashIdentity("iru_", revisionID, idempotencyKey)
}

func (s *Surreal) getRunRec(ctx context.Context, id string) (*investigationRunRec, error) {
	if !strings.HasPrefix(id, "iru_") {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]investigationRunRec](ctx, s.db,
		"SELECT run_id, revision_id, seq, idempotency_key, resolved_input_identities, requested_snapshot, input_manifest_digest, requested_pack_versions, created_by, created_at, request_digest, event_revision FROM $rid LIMIT 1",
		map[string]any{"rid": runRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (s *Surreal) CreateRun(ctx context.Context, request RunRequest) (*Run, error) {
	request, requestDigest, err := normalizeRunRequest(request)
	if err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	if _, err := s.GetRevision(ctx, request.RevisionID); err != nil {
		return nil, fmt.Errorf("create run: revision: %w", err)
	}
	id := runIdentity(request.RevisionID, request.IdempotencyKey)
	if existing, err := s.getRunRec(ctx, id); err == nil {
		if existing.RequestDigest != requestDigest {
			return nil, fmt.Errorf("create run: idempotency key reused for a different request: %w", ErrConflict)
		}
		return s.GetRun(ctx, id)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	for attempt := 0; ; attempt++ {
		now := time.Now().UTC()
		eventID, idErr := newULID(now)
		if idErr != nil {
			return nil, idErr
		}
		initial := RunEvent{
			ID: eventID, RunID: id, Sequence: 1, Attempt: 1,
			NewState: RunQueued, Actor: request.CreatedBy,
			Reason: "run created", Timestamp: now,
		}
		initial.Digest, _ = contentDigest(runEventCore(initial))
		results, queryErr := surrealdb.Query[[]investigationRunRec](ctx, s.db,
			`BEGIN;
LET $existing = SELECT run_id, revision_id, seq, idempotency_key,
    resolved_input_identities, requested_snapshot, input_manifest_digest,
    requested_pack_versions, created_by, created_at, request_digest, event_revision
    FROM $rid LIMIT 1;
LET $last = SELECT seq FROM investigation_run WHERE revision_id = $revision_id ORDER BY seq DESC LIMIT 1;
LET $next_seq = IF array::len($last) = 0 THEN 1 ELSE $last[0].seq + 1 END;
LET $saved = IF array::len($existing) = 0 THEN
    (CREATE $rid SET run_id = $run_id, revision_id = $revision_id, seq = $next_seq,
        idempotency_key = $idempotency_key, resolved_input_identities = $resolved_input_identities,
        requested_snapshot = $requested_snapshot, input_manifest_digest = $input_manifest_digest,
        requested_pack_versions = $requested_pack_versions, created_by = $created_by,
        created_at = $created_at, request_digest = $request_digest, event_revision = 1 RETURN AFTER)
    ELSE $existing END;
IF array::len($existing) = 0 {
    CREATE $event_rid SET event_id = $event_id, run_id = $run_id, sequence = 1,
        attempt = 1, prior_state = '', new_state = 'queued', actor = $created_by,
        reason = 'run created', timestamp = $created_at, content_digest = $event_digest RETURN NONE
};
RETURN $saved;
COMMIT;`,
			map[string]any{
				"rid": runRecordID(id), "run_id": id, "revision_id": request.RevisionID,
				"idempotency_key":           request.IdempotencyKey,
				"resolved_input_identities": request.ResolvedInputIdentities,
				"requested_snapshot":        request.RequestedSnapshot,
				"input_manifest_digest":     request.InputManifestDigest,
				"requested_pack_versions":   request.RequestedPackVersions,
				"created_by":                request.CreatedBy, "created_at": now, "request_digest": requestDigest,
				"event_rid": runEventRecordID(eventID), "event_id": eventID, "event_digest": initial.Digest,
			})
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			if existing, getErr := s.getRunRec(ctx, id); getErr == nil {
				if existing.RequestDigest == requestDigest {
					return s.GetRun(ctx, id)
				}
				return nil, fmt.Errorf("create run: idempotency conflict: %w", ErrConflict)
			}
			return nil, fmt.Errorf("create run: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) != 1 {
			return nil, errors.New("create run returned no row")
		}
		if rows[0].RequestDigest != requestDigest {
			return nil, fmt.Errorf("create run: idempotency key reused for a different request: %w", ErrConflict)
		}
		return s.GetRun(ctx, id)
	}
}

func (s *Surreal) GetRun(ctx context.Context, id string) (*Run, error) {
	row, err := s.getRunRec(ctx, id)
	if err != nil {
		return nil, err
	}
	events, err := s.ListRunEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 || events[len(events)-1].Sequence != row.EventRevision {
		return nil, errors.New("get run: event stream is inconsistent")
	}
	run := row.run(events[len(events)-1].NewState)
	return &run, nil
}

func runEventCore(event RunEvent) any {
	return struct {
		ID, RunID            string
		Sequence, Attempt    int
		PriorState, NewState RunState
		Actor, Reason        string
		Timestamp            time.Time
	}{
		event.ID, event.RunID, event.Sequence, event.Attempt,
		event.PriorState, event.NewState, event.Actor, event.Reason, event.Timestamp.UTC(),
	}
}

func validRunState(state RunState) bool {
	return state == RunQueued || state == RunEnumerating || state == RunAnalyzing ||
		state == RunPublishing || state == RunPublished || state == RunFailed || state == RunCanceled
}

func terminalRunState(state RunState) bool {
	return state == RunPublished || state == RunFailed || state == RunCanceled
}

func validRunTransition(from, to RunState) bool {
	if to == RunFailed || to == RunCanceled {
		return !terminalRunState(from)
	}
	switch from {
	case RunQueued:
		return to == RunEnumerating
	case RunEnumerating:
		return to == RunAnalyzing
	case RunAnalyzing:
		return to == RunPublishing
	case RunPublishing:
		return to == RunPublished
	default:
		return false
	}
}

func (s *Surreal) ListRunEvents(ctx context.Context, runID string) ([]RunEvent, error) {
	if !strings.HasPrefix(runID, "iru_") {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]RunEvent](ctx, s.db,
		"SELECT event_id, run_id, sequence, attempt, prior_state, new_state, actor, reason, timestamp, content_digest FROM investigation_run_event WHERE run_id = $run_id ORDER BY sequence",
		map[string]any{"run_id": runID})
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	rows := firstDomainRows(results)
	for i := range rows {
		storedDigest := rows[i].Digest
		digest, digestErr := contentDigest(runEventCore(rows[i]))
		if digestErr != nil || storedDigest != digest || rows[i].Sequence != i+1 ||
			rows[i].RunID != runID || !validRunState(rows[i].NewState) {
			return nil, errors.New("list run events: stored event stream is inconsistent")
		}
		if i == 0 {
			if rows[i].PriorState != "" || rows[i].NewState != RunQueued {
				return nil, errors.New("list run events: invalid initial event")
			}
		} else if rows[i].PriorState != rows[i-1].NewState ||
			!validRunTransition(rows[i].PriorState, rows[i].NewState) {
			return nil, errors.New("list run events: invalid state transition")
		}
	}
	return rows, nil
}

func (s *Surreal) getRunEvent(ctx context.Context, id string) (*RunEvent, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]RunEvent](ctx, s.db,
		"SELECT event_id, run_id, sequence, attempt, prior_state, new_state, actor, reason, timestamp, content_digest FROM $rid LIMIT 1",
		map[string]any{"rid": runEventRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get run event: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	digest, digestErr := contentDigest(runEventCore(rows[0]))
	if digestErr != nil || digest != rows[0].Digest {
		return nil, errors.New("get run event: stored immutable content is inconsistent")
	}
	return &rows[0], nil
}

func (s *Surreal) AppendRunEvent(ctx context.Context, runID string, event RunEvent) (*RunEvent, error) {
	if event.ID != "" {
		if !validULID(event.ID) {
			return nil, errors.New("append run event: invalid ULID")
		}
		if existing, err := s.getRunEvent(ctx, event.ID); err == nil {
			digest, digestErr := contentDigest(runEventCore(event))
			if digestErr == nil && digest == existing.Digest {
				return existing, nil
			}
			return nil, fmt.Errorf("append run event: immutable content changed: %w", ErrConflict)
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if !validRunState(event.NewState) || event.NewState == RunQueued {
		return nil, errors.New("append run event: invalid new state")
	}
	for name, value := range map[string]string{"actor": event.Actor, "reason": event.Reason} {
		if err := validateDomainString(name, value, true); err != nil {
			return nil, fmt.Errorf("append run event: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		runRow, err := s.getRunRec(ctx, runID)
		if err != nil {
			return nil, err
		}
		events, err := s.ListRunEvents(ctx, runID)
		if err != nil {
			return nil, err
		}
		if len(events) == 0 || runRow.EventRevision != events[len(events)-1].Sequence {
			return nil, errors.New("append run event: event stream is inconsistent")
		}
		last := events[len(events)-1]
		if event.PriorState != "" && event.PriorState != last.NewState {
			return nil, fmt.Errorf("append run event: prior state mismatch: %w", ErrConflict)
		}
		if !validRunTransition(last.NewState, event.NewState) {
			return nil, fmt.Errorf("append run event: invalid transition %s -> %s: %w", last.NewState, event.NewState, ErrConflict)
		}
		if event.Attempt == 0 {
			event.Attempt = last.Attempt
		}
		if event.Attempt != last.Attempt {
			return nil, fmt.Errorf("append run event: attempt change requires the retry workflow: %w", ErrConflict)
		}
		event.RunID = runID
		event.Sequence = last.Sequence + 1
		event.PriorState = last.NewState
		event.Timestamp = time.Now().UTC()
		if event.ID == "" {
			event.ID, err = newULID(event.Timestamp)
			if err != nil {
				return nil, err
			}
		}
		event.Digest, err = contentDigest(runEventCore(event))
		if err != nil {
			return nil, err
		}
		results, queryErr := surrealdb.Query[[]RunEvent](ctx, s.db,
			`BEGIN;
LET $locked = UPDATE $run_rid SET event_revision = $next_sequence
    WHERE event_revision = $expected_sequence RETURN AFTER;
LET $created = IF array::len($locked) = 1 THEN
    (CREATE $event_rid SET event_id = $event_id, run_id = $run_id,
        sequence = $next_sequence, attempt = $attempt, prior_state = $prior_state,
        new_state = $new_state, actor = $actor, reason = $reason,
        timestamp = $timestamp, content_digest = $content_digest RETURN AFTER)
    ELSE [] END;
RETURN $created;
COMMIT;`,
			map[string]any{
				"run_rid": runRecordID(runID), "expected_sequence": last.Sequence,
				"next_sequence": event.Sequence, "event_rid": runEventRecordID(event.ID),
				"event_id": event.ID, "run_id": runID, "attempt": event.Attempt,
				"prior_state": string(event.PriorState), "new_state": string(event.NewState),
				"actor": event.Actor, "reason": event.Reason, "timestamp": event.Timestamp,
				"content_digest": event.Digest,
			})
		if queryErr != nil {
			if isRetryableEnqueue(queryErr) && ctx.Err() == nil && attempt+1 < maxQueueRetries {
				continue
			}
			return nil, fmt.Errorf("append run event: %w", queryErr)
		}
		rows := firstDomainRows(results)
		if len(rows) == 1 {
			return &rows[0], nil
		}
		if attempt+1 >= maxQueueRetries {
			return nil, fmt.Errorf("append run event: concurrent transition: %w", ErrConflict)
		}
	}
}

func runArtifactCore(artifact RunArtifact) any {
	return struct {
		Scope, RunID, TerminalStatus, SnapshotManifest string
		InputManifest, CoverageLedger                  string
		FactReferences, PinReferences                  []string
		EligibilityResult, DiagnosticData              string
	}{
		artifact.Scope, artifact.RunID, string(artifact.TerminalStatus), artifact.SnapshotManifest,
		artifact.InputManifest, artifact.CoverageLedger, artifact.FactReferences,
		artifact.PinReferences, artifact.EligibilityResult, artifact.DiagnosticData,
	}
}

// ComputeRunArtifactID returns the scoped content identity required by the
// domain contract. Receipt time and the ID itself are deliberately excluded.
func ComputeRunArtifactID(artifact RunArtifact) (string, error) {
	var err error
	artifact.FactReferences, err = canonicalStrings("fact reference", artifact.FactReferences)
	if err != nil {
		return "", err
	}
	artifact.PinReferences, err = canonicalStrings("pin reference", artifact.PinReferences)
	if err != nil {
		return "", err
	}
	digest, err := contentDigest(runArtifactCore(artifact))
	if err != nil {
		return "", err
	}
	return "ira_" + strings.TrimPrefix(digest, "sha256:"), nil
}

func normalizeRunArtifact(artifact RunArtifact) (RunArtifact, error) {
	for name, value := range map[string]string{
		"scope": artifact.Scope, "run id": artifact.RunID,
		"snapshot manifest": artifact.SnapshotManifest, "input manifest": artifact.InputManifest,
		"coverage ledger": artifact.CoverageLedger, "eligibility result": artifact.EligibilityResult,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return RunArtifact{}, err
		}
	}
	if !terminalRunState(artifact.TerminalStatus) {
		return RunArtifact{}, errors.New("terminal status is required")
	}
	var err error
	artifact.FactReferences, err = canonicalStrings("fact reference", artifact.FactReferences)
	if err != nil {
		return RunArtifact{}, err
	}
	artifact.PinReferences, err = canonicalStrings("pin reference", artifact.PinReferences)
	if err != nil {
		return RunArtifact{}, err
	}
	if artifact.TerminalStatus != RunPublished && len(artifact.FactReferences) != 0 {
		return RunArtifact{}, errors.New("failed or canceled artifacts cannot contain published fact references")
	}
	wantID, err := ComputeRunArtifactID(artifact)
	if err != nil {
		return RunArtifact{}, err
	}
	if artifact.ID != "" && artifact.ID != wantID {
		return RunArtifact{}, errors.New("artifact id does not match scoped content")
	}
	artifact.ID = wantID
	artifact.ContentDigest = "sha256:" + strings.TrimPrefix(wantID, "ira_")
	return artifact, nil
}

func (s *Surreal) PutRunArtifact(ctx context.Context, artifact RunArtifact) (*RunArtifact, error) {
	artifact, err := normalizeRunArtifact(artifact)
	if err != nil {
		return nil, fmt.Errorf("put run artifact: %w", err)
	}
	run, err := s.GetRun(ctx, artifact.RunID)
	if err != nil {
		return nil, fmt.Errorf("put run artifact: run: %w", err)
	}
	if run.State != artifact.TerminalStatus {
		return nil, fmt.Errorf("put run artifact: terminal status does not match event-derived run state: %w", ErrConflict)
	}
	if existing, err := s.GetRunArtifact(ctx, artifact.ID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	artifact.CreatedAt = time.Now().UTC()
	results, err := surrealdb.Query[[]RunArtifact](ctx, s.db,
		`CREATE $rid SET artifact_id = $artifact_id, scope = $scope, run_id = $run_id,
			terminal_status = $terminal_status, snapshot_manifest = $snapshot_manifest,
			input_manifest = $input_manifest, coverage_ledger = $coverage_ledger,
			fact_references = $fact_references, pin_references = $pin_references,
			eligibility_result = $eligibility_result, diagnostic_data = $diagnostic_data,
			created_at = $created_at, content_digest = $content_digest RETURN AFTER`,
		map[string]any{
			"rid": runArtifactRecordID(artifact.ID), "artifact_id": artifact.ID,
			"scope": artifact.Scope, "run_id": artifact.RunID,
			"terminal_status": string(artifact.TerminalStatus), "snapshot_manifest": artifact.SnapshotManifest,
			"input_manifest": artifact.InputManifest, "coverage_ledger": artifact.CoverageLedger,
			"fact_references": artifact.FactReferences, "pin_references": artifact.PinReferences,
			"eligibility_result": artifact.EligibilityResult, "diagnostic_data": artifact.DiagnosticData,
			"created_at": artifact.CreatedAt, "content_digest": artifact.ContentDigest,
		})
	if err != nil {
		if existing, getErr := s.GetRunArtifact(ctx, artifact.ID); getErr == nil {
			return existing, nil
		}
		return nil, fmt.Errorf("put run artifact: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("put run artifact returned no row")
	}
	return &rows[0], nil
}

func (s *Surreal) GetRunArtifact(ctx context.Context, id string) (*RunArtifact, error) {
	if !strings.HasPrefix(id, "ira_") {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]RunArtifact](ctx, s.db,
		"SELECT artifact_id, scope, run_id, terminal_status, snapshot_manifest, input_manifest, coverage_ledger, fact_references, pin_references, eligibility_result, diagnostic_data, created_at, content_digest FROM $rid LIMIT 1",
		map[string]any{"rid": runArtifactRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get run artifact: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	row, normalizeErr := normalizeRunArtifact(rows[0])
	if normalizeErr != nil || row.ID != id || row.ContentDigest != rows[0].ContentDigest {
		return nil, errors.New("get run artifact: stored immutable content is inconsistent")
	}
	row.CreatedAt = rows[0].CreatedAt
	return &row, nil
}

func ensureULID(id *string, now time.Time) error {
	if *id == "" {
		generated, err := newULID(now)
		if err != nil {
			return err
		}
		*id = generated
		return nil
	}
	if !validULID(*id) {
		return errors.New("invalid ULID")
	}
	return nil
}

func decisionCore(decision Decision) any {
	return struct {
		ID, InvestigationID, ClaimScope, Conclusion string
		RevisionID, RunArtifactID, BaselineID       string
		EligibilityResult, VisibleScope             string
		Authority, AuthoritySource, Rationale       string
		PolicyVersion, Supersedes                   string
		EffectiveFrom                               time.Time
		ExpiresAt                                   *time.Time
	}{
		decision.ID, decision.InvestigationID, decision.ClaimScope, decision.Conclusion,
		decision.RevisionID, decision.RunArtifactID, decision.BaselineID,
		decision.EligibilityResult, decision.VisibleScope, decision.Authority,
		decision.AuthoritySource, decision.Rationale, decision.PolicyVersion,
		decision.Supersedes, decision.EffectiveFrom.UTC(), utcTimePointer(decision.ExpiresAt),
	}
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func validateDecision(decision *Decision) error {
	if !validULID(decision.ID) || !validULID(decision.InvestigationID) ||
		!strings.HasPrefix(decision.RevisionID, "ivr_") || decision.EffectiveFrom.IsZero() {
		return errors.New("decision identity, investigation, revision, and effective time are required")
	}
	for name, value := range map[string]string{
		"claim scope": decision.ClaimScope, "conclusion": decision.Conclusion,
		"eligibility result": decision.EligibilityResult, "visible scope": decision.VisibleScope,
		"authority": decision.Authority, "authority source": decision.AuthoritySource,
		"rationale": decision.Rationale,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return err
		}
	}
	decision.EffectiveFrom = decision.EffectiveFrom.UTC()
	decision.ExpiresAt = utcTimePointer(decision.ExpiresAt)
	digest, err := contentDigest(decisionCore(*decision))
	if err != nil {
		return err
	}
	if decision.ContentDigest != "" && decision.ContentDigest != digest {
		return errors.New("decision content digest does not match immutable fields")
	}
	decision.ContentDigest = digest
	return nil
}

func (s *Surreal) PutDecision(ctx context.Context, decision Decision) (*Decision, error) {
	now := time.Now().UTC()
	if err := ensureULID(&decision.ID, now); err != nil {
		return nil, fmt.Errorf("put decision: %w", err)
	}
	if decision.EffectiveFrom.IsZero() {
		decision.EffectiveFrom = now
	}
	if err := validateDecision(&decision); err != nil {
		return nil, fmt.Errorf("put decision: %w", err)
	}
	if existing, err := s.GetDecision(ctx, decision.ID); err == nil {
		if existing.ContentDigest != decision.ContentDigest {
			return nil, fmt.Errorf("put decision: immutable content changed: %w", ErrConflict)
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	revision, err := s.GetRevision(ctx, decision.RevisionID)
	if err != nil || revision.InvestigationID != decision.InvestigationID {
		return nil, fmt.Errorf("put decision: revision is unavailable for investigation: %w", ErrConflict)
	}
	if decision.RunArtifactID != "" {
		if _, err := s.GetRunArtifact(ctx, decision.RunArtifactID); err != nil {
			return nil, fmt.Errorf("put decision: run artifact: %w", err)
		}
	}
	if decision.BaselineID != "" {
		baseline, err := s.GetBaselineDesignation(ctx, decision.BaselineID)
		if err != nil || baseline.InvestigationID != decision.InvestigationID {
			return nil, fmt.Errorf("put decision: baseline is unavailable: %w", ErrConflict)
		}
	}
	if err := s.validateDecisionSupersession(ctx, decision); err != nil {
		return nil, err
	}
	decision.CreatedAt = now
	results, err := surrealdb.Query[[]Decision](ctx, s.db,
		`CREATE $rid SET decision_id = $decision_id, investigation_id = $investigation_id,
			claim_scope = $claim_scope, conclusion = $conclusion, revision_id = $revision_id,
			run_artifact_id = $run_artifact_id, baseline_id = $baseline_id,
			eligibility_result = $eligibility_result, visible_scope = $visible_scope,
			authority = $authority, authority_source = $authority_source,
			rationale = $rationale, policy_version = $policy_version,
			effective_from = $effective_from, expires_at = $expires_at,
			supersedes = IF $supersedes = '' THEN NONE ELSE $supersedes END,
			created_at = $created_at,
			content_digest = $content_digest RETURN AFTER`,
		map[string]any{
			"rid": decisionRecordID(decision.ID), "decision_id": decision.ID,
			"investigation_id": decision.InvestigationID, "claim_scope": decision.ClaimScope,
			"conclusion": decision.Conclusion, "revision_id": decision.RevisionID,
			"run_artifact_id": optionalValue(decision.RunArtifactID), "baseline_id": optionalValue(decision.BaselineID),
			"eligibility_result": decision.EligibilityResult, "visible_scope": decision.VisibleScope,
			"authority": decision.Authority, "authority_source": decision.AuthoritySource,
			"rationale": decision.Rationale, "policy_version": optionalValue(decision.PolicyVersion),
			"effective_from": decision.EffectiveFrom, "expires_at": decision.ExpiresAt,
			"supersedes": optionalValue(decision.Supersedes), "created_at": decision.CreatedAt,
			"content_digest": decision.ContentDigest,
		})
	if err != nil {
		return nil, fmt.Errorf("put decision: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("put decision returned no row")
	}
	return &rows[0], nil
}

func (s *Surreal) validateDecisionSupersession(ctx context.Context, decision Decision) error {
	if decision.Supersedes == "" {
		return nil
	}
	if decision.Supersedes == decision.ID {
		return fmt.Errorf("put decision: cannot supersede itself: %w", ErrConflict)
	}
	old, err := s.GetDecision(ctx, decision.Supersedes)
	if err != nil || old.InvestigationID != decision.InvestigationID || old.ClaimScope != decision.ClaimScope {
		return fmt.Errorf("put decision: superseded decision is unavailable or out of scope: %w", ErrConflict)
	}
	return nil
}

func (s *Surreal) GetDecision(ctx context.Context, id string) (*Decision, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]Decision](ctx, s.db,
		"SELECT decision_id, investigation_id, claim_scope, conclusion, revision_id, run_artifact_id, baseline_id, eligibility_result, visible_scope, authority, authority_source, rationale, policy_version, effective_from, expires_at, supersedes, created_at, content_digest FROM $rid LIMIT 1",
		map[string]any{"rid": decisionRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get decision: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	storedDigest := rows[0].ContentDigest
	if err := validateDecision(&rows[0]); err != nil || rows[0].ContentDigest != storedDigest {
		return nil, errors.New("get decision: stored immutable content is inconsistent")
	}
	return &rows[0], nil
}

func dispositionCore(disposition Disposition) any {
	return struct {
		ID, InvestigationID, Category, SubjectKind, SubjectID string
		Actor, AuthorityBasis, Rationale                      string
		EffectiveFrom                                         time.Time
		ExpiresAt                                             *time.Time
		PolicyReference, ExternalDecisionID                   string
		ReferencedFactID, ReferencedCoverage, Supersedes      string
	}{
		disposition.ID, disposition.InvestigationID, disposition.Category,
		disposition.SubjectKind, disposition.SubjectID, disposition.Actor,
		disposition.AuthorityBasis, disposition.Rationale, disposition.EffectiveFrom.UTC(),
		utcTimePointer(disposition.ExpiresAt), disposition.PolicyReference,
		disposition.ExternalDecisionID, disposition.ReferencedFactID,
		disposition.ReferencedCoverage, disposition.Supersedes,
	}
}

func validateDisposition(disposition *Disposition) error {
	if !validULID(disposition.ID) || !validULID(disposition.InvestigationID) || disposition.EffectiveFrom.IsZero() {
		return errors.New("disposition identity, investigation, and effective time are required")
	}
	for name, value := range map[string]string{
		"category": disposition.Category, "subject kind": disposition.SubjectKind,
		"subject id": disposition.SubjectID, "actor": disposition.Actor,
		"authority basis": disposition.AuthorityBasis, "rationale": disposition.Rationale,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return err
		}
	}
	disposition.EffectiveFrom = disposition.EffectiveFrom.UTC()
	disposition.ExpiresAt = utcTimePointer(disposition.ExpiresAt)
	digest, err := contentDigest(dispositionCore(*disposition))
	if err != nil {
		return err
	}
	if disposition.ContentDigest != "" && disposition.ContentDigest != digest {
		return errors.New("disposition content digest does not match immutable fields")
	}
	disposition.ContentDigest = digest
	return nil
}

func (s *Surreal) PutDisposition(ctx context.Context, disposition Disposition) (*Disposition, error) {
	now := time.Now().UTC()
	if err := ensureULID(&disposition.ID, now); err != nil {
		return nil, fmt.Errorf("put disposition: %w", err)
	}
	if disposition.EffectiveFrom.IsZero() {
		disposition.EffectiveFrom = now
	}
	if err := validateDisposition(&disposition); err != nil {
		return nil, fmt.Errorf("put disposition: %w", err)
	}
	if existing, err := s.GetDisposition(ctx, disposition.ID); err == nil {
		if existing.ContentDigest != disposition.ContentDigest {
			return nil, fmt.Errorf("put disposition: immutable content changed: %w", ErrConflict)
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if _, err := s.GetInvestigation(ctx, disposition.InvestigationID); err != nil {
		return nil, fmt.Errorf("put disposition: investigation: %w", err)
	}
	if disposition.Supersedes != "" {
		old, err := s.GetDisposition(ctx, disposition.Supersedes)
		if err != nil || old.InvestigationID != disposition.InvestigationID ||
			old.SubjectKind != disposition.SubjectKind || old.SubjectID != disposition.SubjectID {
			return nil, fmt.Errorf("put disposition: superseded record is unavailable or out of scope: %w", ErrConflict)
		}
	}
	disposition.CreatedAt = now
	results, err := surrealdb.Query[[]Disposition](ctx, s.db,
		`CREATE $rid SET disposition_id = $disposition_id, investigation_id = $investigation_id,
			category = $category, subject_kind = $subject_kind, subject_id = $subject_id,
			actor = $actor, authority_basis = $authority_basis, rationale = $rationale,
			effective_from = $effective_from, expires_at = $expires_at,
			policy_reference = $policy_reference, external_decision_id = $external_decision_id,
			referenced_fact_id = $referenced_fact_id, referenced_coverage = $referenced_coverage,
			supersedes = IF $supersedes = '' THEN NONE ELSE $supersedes END,
			created_at = $created_at,
			content_digest = $content_digest RETURN AFTER`,
		map[string]any{
			"rid": dispositionRecordID(disposition.ID), "disposition_id": disposition.ID,
			"investigation_id": disposition.InvestigationID, "category": disposition.Category,
			"subject_kind": disposition.SubjectKind, "subject_id": disposition.SubjectID,
			"actor": disposition.Actor, "authority_basis": disposition.AuthorityBasis,
			"rationale": disposition.Rationale, "effective_from": disposition.EffectiveFrom,
			"expires_at": disposition.ExpiresAt, "policy_reference": optionalValue(disposition.PolicyReference),
			"external_decision_id": optionalValue(disposition.ExternalDecisionID),
			"referenced_fact_id":   optionalValue(disposition.ReferencedFactID),
			"referenced_coverage":  optionalValue(disposition.ReferencedCoverage),
			"supersedes":           optionalValue(disposition.Supersedes), "created_at": disposition.CreatedAt,
			"content_digest": disposition.ContentDigest,
		})
	if err != nil {
		return nil, fmt.Errorf("put disposition: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("put disposition returned no row")
	}
	return &rows[0], nil
}

func (s *Surreal) GetDisposition(ctx context.Context, id string) (*Disposition, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]Disposition](ctx, s.db,
		"SELECT disposition_id, investigation_id, category, subject_kind, subject_id, actor, authority_basis, rationale, effective_from, expires_at, policy_reference, external_decision_id, referenced_fact_id, referenced_coverage, supersedes, created_at, content_digest FROM $rid LIMIT 1",
		map[string]any{"rid": dispositionRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get disposition: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	storedDigest := rows[0].ContentDigest
	if err := validateDisposition(&rows[0]); err != nil || rows[0].ContentDigest != storedDigest {
		return nil, errors.New("get disposition: stored immutable content is inconsistent")
	}
	return &rows[0], nil
}

func baselineCore(baseline BaselineDesignation) any {
	return struct {
		ID, InvestigationID, ClaimScope, WorkflowScope string
		RevisionID, RunArtifactID, Authority           string
		Rationale, Supersedes                          string
	}{
		baseline.ID, baseline.InvestigationID, baseline.ClaimScope,
		baseline.WorkflowScope, baseline.RevisionID, baseline.RunArtifactID,
		baseline.Authority, baseline.Rationale, baseline.Supersedes,
	}
}

func validateBaseline(baseline *BaselineDesignation) error {
	if !validULID(baseline.ID) || !validULID(baseline.InvestigationID) ||
		!strings.HasPrefix(baseline.RevisionID, "ivr_") ||
		!strings.HasPrefix(baseline.RunArtifactID, "ira_") {
		return errors.New("baseline identity and references are required")
	}
	for name, value := range map[string]string{
		"claim scope": baseline.ClaimScope, "workflow scope": baseline.WorkflowScope,
		"authority": baseline.Authority, "rationale": baseline.Rationale,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return err
		}
	}
	digest, err := contentDigest(baselineCore(*baseline))
	if err != nil {
		return err
	}
	if baseline.ContentDigest != "" && baseline.ContentDigest != digest {
		return errors.New("baseline content digest does not match immutable fields")
	}
	baseline.ContentDigest = digest
	return nil
}

func (s *Surreal) PutBaselineDesignation(ctx context.Context, baseline BaselineDesignation) (*BaselineDesignation, error) {
	now := time.Now().UTC()
	if err := ensureULID(&baseline.ID, now); err != nil {
		return nil, fmt.Errorf("put baseline: %w", err)
	}
	if err := validateBaseline(&baseline); err != nil {
		return nil, fmt.Errorf("put baseline: %w", err)
	}
	if existing, err := s.GetBaselineDesignation(ctx, baseline.ID); err == nil {
		if existing.ContentDigest != baseline.ContentDigest {
			return nil, fmt.Errorf("put baseline: immutable content changed: %w", ErrConflict)
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	revision, err := s.GetRevision(ctx, baseline.RevisionID)
	if err != nil || revision.InvestigationID != baseline.InvestigationID {
		return nil, fmt.Errorf("put baseline: revision is unavailable: %w", ErrConflict)
	}
	artifact, err := s.GetRunArtifact(ctx, baseline.RunArtifactID)
	if err != nil || artifact.TerminalStatus != RunPublished {
		return nil, fmt.Errorf("put baseline: a published run artifact is required: %w", ErrConflict)
	}
	if baseline.Supersedes != "" {
		old, err := s.GetBaselineDesignation(ctx, baseline.Supersedes)
		if err != nil || old.InvestigationID != baseline.InvestigationID ||
			old.ClaimScope != baseline.ClaimScope || old.WorkflowScope != baseline.WorkflowScope {
			return nil, fmt.Errorf("put baseline: superseded record is unavailable or out of scope: %w", ErrConflict)
		}
	}
	baseline.CreatedAt = now
	results, err := surrealdb.Query[[]BaselineDesignation](ctx, s.db,
		`CREATE $rid SET designation_id = $designation_id, investigation_id = $investigation_id,
			claim_scope = $claim_scope, workflow_scope = $workflow_scope,
			revision_id = $revision_id, run_artifact_id = $run_artifact_id,
			authority = $authority, rationale = $rationale,
			supersedes = IF $supersedes = '' THEN NONE ELSE $supersedes END,
			created_at = $created_at, content_digest = $content_digest RETURN AFTER`,
		map[string]any{
			"rid": baselineRecordID(baseline.ID), "designation_id": baseline.ID,
			"investigation_id": baseline.InvestigationID, "claim_scope": baseline.ClaimScope,
			"workflow_scope": baseline.WorkflowScope, "revision_id": baseline.RevisionID,
			"run_artifact_id": baseline.RunArtifactID, "authority": baseline.Authority,
			"rationale": baseline.Rationale, "supersedes": optionalValue(baseline.Supersedes),
			"created_at": baseline.CreatedAt, "content_digest": baseline.ContentDigest,
		})
	if err != nil {
		return nil, fmt.Errorf("put baseline: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("put baseline returned no row")
	}
	return &rows[0], nil
}

func (s *Surreal) GetBaselineDesignation(ctx context.Context, id string) (*BaselineDesignation, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]BaselineDesignation](ctx, s.db,
		"SELECT designation_id, investigation_id, claim_scope, workflow_scope, revision_id, run_artifact_id, authority, rationale, supersedes, created_at, content_digest FROM $rid LIMIT 1",
		map[string]any{"rid": baselineRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get baseline: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	storedDigest := rows[0].ContentDigest
	if err := validateBaseline(&rows[0]); err != nil || rows[0].ContentDigest != storedDigest {
		return nil, errors.New("get baseline: stored immutable content is inconsistent")
	}
	return &rows[0], nil
}

func validateWatch(watch Watch) error {
	if !validULID(watch.ID) {
		return errors.New("invalid watch ULID")
	}
	if err := validateDomainString("owner", watch.Owner, true); err != nil {
		return err
	}
	if err := validateDomainString("evaluation cursor", watch.EvaluationCursor, false); err != nil {
		return err
	}
	return nil
}

func (s *Surreal) CreateWatch(ctx context.Context, watch Watch) (*Watch, error) {
	now := time.Now().UTC()
	if err := ensureULID(&watch.ID, now); err != nil {
		return nil, fmt.Errorf("create watch: %w", err)
	}
	if watch.CurrentRevisionID != "" {
		return nil, errors.New("create watch: current revision is set after watch creation")
	}
	if err := validateWatch(watch); err != nil {
		return nil, fmt.Errorf("create watch: %w", err)
	}
	watch.CreatedAt, watch.UpdatedAt = now, now
	results, err := surrealdb.Query[[]Watch](ctx, s.db,
		`CREATE $rid SET watch_id = $watch_id, owner = $owner, enabled = $enabled,
			expires_at = $expires_at, evaluation_cursor = $evaluation_cursor,
			created_at = $now, updated_at = $now RETURN AFTER`,
		map[string]any{
			"rid": watchRecordID(watch.ID), "watch_id": watch.ID, "owner": watch.Owner,
			"enabled": watch.Enabled, "expires_at": watch.ExpiresAt,
			"evaluation_cursor": optionalValue(watch.EvaluationCursor), "now": now,
		})
	if err != nil {
		return nil, fmt.Errorf("create watch: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("create watch returned no row")
	}
	return &rows[0], nil
}

func (s *Surreal) GetWatch(ctx context.Context, id string) (*Watch, error) {
	if !validULID(id) {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]Watch](ctx, s.db,
		"SELECT watch_id, owner, enabled, current_revision_id, expires_at, evaluation_cursor, created_at, updated_at FROM $rid LIMIT 1",
		map[string]any{"rid": watchRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get watch: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (s *Surreal) UpdateWatch(ctx context.Context, watch Watch) (*Watch, error) {
	existing, err := s.GetWatch(ctx, watch.ID)
	if err != nil {
		return nil, err
	}
	if !watch.CreatedAt.IsZero() && !watch.CreatedAt.Equal(existing.CreatedAt) {
		return nil, fmt.Errorf("update watch: immutable creation time changed: %w", ErrConflict)
	}
	if err := validateWatch(watch); err != nil {
		return nil, fmt.Errorf("update watch: %w", err)
	}
	if watch.CurrentRevisionID != "" {
		revision, getErr := s.GetWatchRevision(ctx, watch.CurrentRevisionID)
		if getErr != nil || revision.WatchID != watch.ID {
			return nil, fmt.Errorf("update watch: current revision is unavailable: %w", ErrConflict)
		}
	}
	now := time.Now().UTC()
	results, err := surrealdb.Query[[]Watch](ctx, s.db,
		`UPDATE $rid SET owner = $owner, enabled = $enabled,
			current_revision_id = $current_revision_id, expires_at = $expires_at,
			evaluation_cursor = $evaluation_cursor, updated_at = $now RETURN AFTER`,
		map[string]any{
			"rid": watchRecordID(watch.ID), "owner": watch.Owner, "enabled": watch.Enabled,
			"current_revision_id": optionalValue(watch.CurrentRevisionID), "expires_at": watch.ExpiresAt,
			"evaluation_cursor": optionalValue(watch.EvaluationCursor), "now": now,
		})
	if err != nil {
		return nil, fmt.Errorf("update watch: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func watchRevisionIdentity(watchID string, seq int) string {
	return hashIdentity("iwr_", watchID, fmt.Sprint(seq))
}

func watchRevisionCore(revision WatchRevision) any {
	return struct {
		ID, WatchID                 string
		Seq                         int
		InvestigationID, RevisionID string
		TypedQuery, TriggerPolicy   string
		Coalescing, NoiseControls   string
		Creator                     string
	}{
		revision.ID, revision.WatchID, revision.Seq, revision.InvestigationID,
		revision.RevisionID, revision.TypedQuery, revision.TriggerPolicy,
		revision.Coalescing, revision.NoiseControls, revision.Creator,
	}
}

func validateWatchRevision(revision *WatchRevision) error {
	if !validULID(revision.WatchID) || !validULID(revision.InvestigationID) ||
		!strings.HasPrefix(revision.RevisionID, "ivr_") || revision.Seq < 1 {
		return errors.New("watch, investigation, revision, and positive sequence are required")
	}
	wantID := watchRevisionIdentity(revision.WatchID, revision.Seq)
	if revision.ID != "" && revision.ID != wantID {
		return errors.New("watch revision id does not match watch and sequence")
	}
	revision.ID = wantID
	for name, value := range map[string]string{
		"typed query": revision.TypedQuery, "trigger policy": revision.TriggerPolicy,
		"coalescing": revision.Coalescing, "noise controls": revision.NoiseControls,
		"creator": revision.Creator,
	} {
		if err := validateDomainString(name, value, true); err != nil {
			return err
		}
	}
	digest, err := contentDigest(watchRevisionCore(*revision))
	if err != nil {
		return err
	}
	if revision.ContentDigest != "" && revision.ContentDigest != digest {
		return errors.New("watch revision content digest does not match immutable fields")
	}
	revision.ContentDigest = digest
	return nil
}

func (s *Surreal) PutWatchRevision(ctx context.Context, revision WatchRevision) (*WatchRevision, error) {
	if err := validateWatchRevision(&revision); err != nil {
		return nil, fmt.Errorf("put watch revision: %w", err)
	}
	if _, err := s.GetWatch(ctx, revision.WatchID); err != nil {
		return nil, fmt.Errorf("put watch revision: watch: %w", err)
	}
	domainRevision, err := s.GetRevision(ctx, revision.RevisionID)
	if err != nil || domainRevision.InvestigationID != revision.InvestigationID {
		return nil, fmt.Errorf("put watch revision: investigation revision is unavailable: %w", ErrConflict)
	}
	if existing, err := s.GetWatchRevision(ctx, revision.ID); err == nil {
		if existing.ContentDigest != revision.ContentDigest {
			return nil, fmt.Errorf("put watch revision: immutable content changed: %w", ErrConflict)
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if revision.Seq > 1 {
		if _, err := s.GetWatchRevision(ctx, watchRevisionIdentity(revision.WatchID, revision.Seq-1)); err != nil {
			return nil, fmt.Errorf("put watch revision: previous sequence is unavailable: %w", ErrConflict)
		}
	}
	revision.CreatedAt = time.Now().UTC()
	results, err := surrealdb.Query[[]WatchRevision](ctx, s.db,
		`CREATE $rid SET watch_revision_id = $watch_revision_id, watch_id = $watch_id,
			seq = $seq, investigation_id = $investigation_id, revision_id = $revision_id,
			typed_query = $typed_query, trigger_policy = $trigger_policy,
			coalescing = $coalescing, noise_controls = $noise_controls,
			creator = $creator, created_at = $created_at, content_digest = $content_digest RETURN AFTER`,
		map[string]any{
			"rid": watchRevisionRecordID(revision.ID), "watch_revision_id": revision.ID,
			"watch_id": revision.WatchID, "seq": revision.Seq,
			"investigation_id": revision.InvestigationID, "revision_id": revision.RevisionID,
			"typed_query": revision.TypedQuery, "trigger_policy": revision.TriggerPolicy,
			"coalescing": revision.Coalescing, "noise_controls": revision.NoiseControls,
			"creator": revision.Creator, "created_at": revision.CreatedAt,
			"content_digest": revision.ContentDigest,
		})
	if err != nil {
		return nil, fmt.Errorf("put watch revision: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 {
		return nil, errors.New("put watch revision returned no row")
	}
	return &rows[0], nil
}

func (s *Surreal) GetWatchRevision(ctx context.Context, id string) (*WatchRevision, error) {
	if !strings.HasPrefix(id, "iwr_") {
		return nil, ErrNotFound
	}
	results, err := surrealdb.Query[[]WatchRevision](ctx, s.db,
		"SELECT watch_revision_id, watch_id, seq, investigation_id, revision_id, typed_query, trigger_policy, coalescing, noise_controls, creator, created_at, content_digest FROM $rid LIMIT 1",
		map[string]any{"rid": watchRevisionRecordID(id)})
	if err != nil {
		return nil, fmt.Errorf("get watch revision: %w", err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	storedDigest := rows[0].ContentDigest
	if err := validateWatchRevision(&rows[0]); err != nil || rows[0].ContentDigest != storedDigest {
		return nil, errors.New("get watch revision: stored immutable content is inconsistent")
	}
	return &rows[0], nil
}
