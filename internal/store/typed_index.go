package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// TypedIndexIntent is the bounded precious configuration/intent. Derived
// requests, plans, attempts, jobs and pointers are deliberately not backup data.
// Installing it is a trusted administrator operation, never request JSON.
type TypedIndexIntent struct {
	Repository      string `json:"repository"`
	ProfileJSON     string `json:"profile"`
	ProfileEpoch    int64  `json:"profile_epoch"`
	ProfileDigest   string `json:"profile_digest"`
	UniverseDigest  string `json:"universe_digest"`
	Desired         string `json:"desired"`
	Canceled        bool   `json:"canceled"`
	RestoreRequired bool   `json:"restore_required"`
}

const maxTypedIntentBytes = typedindex.MaxProfileBytes + typedindex.MaxRequestBytes
const maxTypedControlBytes = 4 << 10

type TypedIndexStage string

const (
	TypedPreflight   TypedIndexStage = "preflight"
	TypedPlanning    TypedIndexStage = "planning"
	TypedExecution   TypedIndexStage = "execution"
	TypedValidation  TypedIndexStage = "validation"
	TypedPublication TypedIndexStage = "publication"
	TypedComplete    TypedIndexStage = "complete"
)

var typedStages = [...]TypedIndexStage{TypedPreflight, TypedPlanning, TypedExecution, TypedValidation, TypedPublication}

type typedIndexRequest struct {
	Raw         string `json:"raw"`
	Root        string `json:"root"`
	SourceEpoch int64  `json:"source_epoch"`
}
type typedIndexPlan struct {
	Digest    string `json:"digest"`
	Successor string `json:"successor"`
}
type typedIndexAttempt struct {
	Root    string             `json:"root"`
	Request string             `json:"request"`
	Lease   string             `json:"lease"`
	Stage   TypedIndexStage    `json:"stage"`
	States  [5]string          `json:"states"`
	Reason  typedindex.Refusal `json:"reason"`
}

// TypedIndexWork contains immutable authority and durable stage state. Resume
// permits only another custody verification; it is never proof of quiescence.
type TypedIndexWork struct {
	Parent        typedindex.Admission
	Admission     typedindex.Admission
	RootDigest    string
	PlanDigest    string
	AttemptDigest string
	Stage         TypedIndexStage
	States        [5]string
	Resume        bool
}
type TypedIndexStatus struct {
	Desired         string                         `json:"request_digest"`
	Stage           TypedIndexStage                `json:"stage,omitempty"`
	States          [5]string                      `json:"stages"`
	Reason          typedindex.Refusal             `json:"reason,omitempty"`
	Canceled        bool                           `json:"canceled"`
	RestoreRequired bool                           `json:"restore_required"`
	Stale           bool                           `json:"stale"`
	Current         *typedindex.PublicationPointer `json:"current,omitempty"`
}

type typedBody struct {
	Body string `json:"body"`
}

func typedID(table, key string) models.RecordID { return models.NewRecordID(table, key) }
func typedDigest(v []byte) string               { h := sha256.Sum256(v); return "sha256:" + hex.EncodeToString(h[:]) }
func typedEncode(v any, limit int) (string, error) {
	b, err := json.Marshal(v)
	if err != nil || len(b) > limit {
		return "", typedindex.Capacity
	}
	return string(b), nil
}
func typedDecode(raw string, limit int, v any) error {
	if len(raw) == 0 || len(raw) > limit {
		return typedindex.Invalid
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return typedindex.Invalid
	}
	canonical, err := typedEncode(v, limit)
	if err != nil || canonical != raw {
		return typedindex.Invalid
	}
	return nil
}
func typedError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if strings.Contains(err.Error(), "typed-stale") {
		return typedindex.Stale
	}
	return typedindex.ExecutionFailed
}
func (s *Surreal) typedRead(ctx context.Context, table, key string) (string, error) {
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return "", err
	}
	results, err := storeQuery[[]typedBody](ctx, s.accounting, s.db, "SELECT body FROM $rid LIMIT 1", map[string]any{"rid": typedID(table, key)}, storeRead())
	if err != nil {
		return "", typedError(ctx, err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return "", nil
	}
	if len(rows) != 1 {
		return "", typedindex.Invalid
	}
	return rows[0].Body, nil
}
func (s *Surreal) typedWrite(ctx context.Context, statement string, vars map[string]any, rows uint64) error {
	if err := readaccounting.Charge(ctx, readaccounting.StoreWriteAttempt, 1); err != nil {
		return err
	}
	_, err := storeQuery[any](ctx, s.accounting, s.db, "BEGIN;\n"+statement+"\nCOMMIT;", vars, storeWrite(rows))
	return typedError(ctx, err)
}

// A read-only exact-fence transaction charges a read, never a zero-row write.
func (s *Surreal) typedFence(ctx context.Context, statement string, vars map[string]any) error {
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return err
	}
	_, err := storeQuery[any](ctx, s.accounting, s.db, "BEGIN;\n"+statement+"\nCOMMIT;", vars, storeRead())
	return typedError(ctx, err)
}

// SQL source fence participates in every durable typed transition. The source
// epoch never aliases an unrelated evidence-publication revision.
const typedSourceFenceSQL = `
LET $source = (SELECT typed_incarnation, typed_source_epoch, indexed_commit_hash, deleting FROM $repo LIMIT 1)[0];
IF $source = NONE OR $source.deleting = true OR $source.typed_incarnation != $incarnation
 OR $source.typed_source_epoch != $source_epoch OR $source.indexed_commit_hash != $commit {
 THROW 'typed-stale';
};
`
const typedIntentFenceSQL = `
IF (SELECT body FROM $intent LIMIT 1)[0].body != $intent_before { THROW 'typed-stale'; };
`
const typedChunkFenceSQL = `
LET $owned = (SELECT id FROM $chunk WHERE status = 'running' AND lease_token = $lease
 AND claimed_by = $worker AND repository = $repository AND generation = $root
 AND stage = 'typed-index' AND resource_class = 'typed-index' AND schedule_digest = $schedule_digest LIMIT 1)[0].id;
IF $owned = NONE OR (SELECT schedule_digest FROM $schedule_current LIMIT 1)[0].schedule_digest != $schedule_digest {
 THROW 'typed-stale';
};
`

type typedAuthority struct {
	intent    TypedIndexIntent
	intentRaw string
	source    typedSourceRecord
	profile   typedindex.Profile
}

func (s *Surreal) typedAuthority(ctx context.Context, repository string) (typedAuthority, error) {
	if reponame.Validate(repository) != nil || len(repository) > 512 {
		return typedAuthority{}, typedindex.Invalid
	}
	if _, err := s.GetTypedSource(ctx, repository); err != nil {
		return typedAuthority{}, err
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return typedAuthority{}, err
	}
	results, err := storeQuery[[]typedSourceRecord](ctx, s.accounting, s.db, "SELECT "+typedSourceProjection+" FROM $rid LIMIT 1", map[string]any{"rid": repoID(repository)}, storeRead())
	if err != nil {
		return typedAuthority{}, typedError(ctx, err)
	}
	rows := firstDomainRows(results)
	if len(rows) != 1 || rows[0].Name != repository {
		return typedAuthority{}, typedindex.Stale
	}
	if _, err := typedSource(rows[0]); err != nil {
		return typedAuthority{}, err
	}
	raw, err := s.typedRead(ctx, "typed_index_intent", repository)
	if err != nil {
		return typedAuthority{}, err
	}
	a := typedAuthority{source: rows[0], intentRaw: raw}
	if raw == "" {
		return a, nil
	}
	if typedDecode(raw, maxTypedIntentBytes, &a.intent) != nil || a.intent.Repository != repository || a.intent.ProfileEpoch < 1 || (a.intent.Desired != "" && !validSHA256(a.intent.Desired)) || !validSHA256(a.intent.UniverseDigest) {
		return typedAuthority{}, typedindex.Invalid
	}
	a.profile, err = typedindex.DecodeProfile(ctx, []byte(a.intent.ProfileJSON))
	if err != nil || a.profile.Digest() != a.intent.ProfileDigest {
		return typedAuthority{}, typedindex.Invalid
	}
	return a, nil
}
func (a typedAuthority) variables() map[string]any {
	return map[string]any{"repo": repoID(a.source.Name), "repository": a.source.Name, "incarnation": a.source.Incarnation, "source_epoch": a.source.Epoch, "commit": a.source.Commit, "intent": typedID("typed_index_intent", a.source.Name), "intent_before": a.intentRaw}
}
func (a typedAuthority) admit(ctx context.Context, raw string, plan typedIndexPlan, parent string) (typedindex.Admission, error) {
	source, err := typedSource(a.source)
	if err != nil {
		return typedindex.Admission{}, err
	}
	if a.intentRaw == "" || a.intent.RestoreRequired {
		return typedindex.Admission{}, typedindex.Disabled
	}
	return typedindex.Admit(ctx, typedindex.Authority{Enabled: true, Administrator: true, Source: source, Profile: typedindex.Epoch{Number: uint64(a.intent.ProfileEpoch), Digest: a.intent.ProfileDigest}, UniverseDigest: a.intent.UniverseDigest, ParentRequestDigest: parent, PlanDigest: plan.Digest}, a.profile, []byte(raw))
}

// GetTypedIndexIntent returns trusted persisted configuration for constructing
// a request after restart. It does not grant caller authorization or custody.
func (s *Surreal) GetTypedIndexIntent(ctx context.Context, repository string) (TypedIndexIntent, error) {
	a, err := s.typedAuthority(ctx, repository)
	if err != nil {
		return TypedIndexIntent{}, err
	}
	if a.intentRaw == "" {
		return TypedIndexIntent{}, ErrNotFound
	}
	return a.intent, nil
}

// InstallTypedProfile CASes the operator epoch, including equal-value changes.
// After restore the trusted caller must revalidate tools/custody before calling.
func (s *Surreal) InstallTypedProfile(ctx context.Context, repository string, profile typedindex.Profile, universe string, expectedEpoch int64) (TypedIndexIntent, error) {
	if profile.Digest() == "" || !validSHA256(universe) || expectedEpoch < 0 || expectedEpoch == math.MaxInt64 {
		return TypedIndexIntent{}, typedindex.Invalid
	}
	a, err := s.typedAuthority(ctx, repository)
	if err != nil {
		return TypedIndexIntent{}, err
	}
	if a.intent.ProfileEpoch != expectedEpoch {
		return TypedIndexIntent{}, typedindex.Stale
	}
	profileRaw, err := typedEncode(profile.Definition(), typedindex.MaxProfileBytes)
	if err != nil {
		return TypedIndexIntent{}, err
	}
	next := TypedIndexIntent{Repository: repository, ProfileJSON: profileRaw, ProfileEpoch: expectedEpoch + 1, ProfileDigest: profile.Digest(), UniverseDigest: universe}
	body, err := typedEncode(next, maxTypedIntentBytes)
	if err != nil {
		return next, err
	}
	vars := a.variables()
	vars["body"] = body
	fence := typedIntentFenceSQL
	if a.intentRaw == "" {
		fence = "IF (SELECT id FROM $intent LIMIT 1)[0].id != NONE { THROW 'typed-stale'; };"
	}
	err = s.typedWrite(ctx, typedSourceFenceSQL+fence+"UPSERT $intent SET repository=$repository, body=$body RETURN NONE;", vars, 1)
	return next, err
}

// EnqueueTypedIndex only admits a planning request. An idempotent retry after
// sealing observes the same child and cannot switch desire back to its parent.
func (s *Surreal) EnqueueTypedIndex(ctx context.Context, repository string, raw []byte) (TypedIndexStatus, error) {
	if len(raw) > typedindex.MaxRequestBytes {
		return TypedIndexStatus{}, typedindex.Invalid
	}
	a, err := s.typedAuthority(ctx, repository)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	admission, err := a.admit(ctx, string(raw), typedIndexPlan{}, "")
	if err != nil {
		return TypedIndexStatus{}, err
	}
	if admission.Request().Action != typedindex.Plan {
		return TypedIndexStatus{}, typedindex.Invalid
	}
	canonical, err := typedEncode(admission.Request(), typedindex.MaxRequestBytes)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	raw = []byte(canonical)
	digest := admission.Digest()
	existing, err := s.typedRead(ctx, "typed_index_request", digest)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	if existing != "" {
		var stored typedIndexRequest
		if typedDecode(existing, typedindex.MaxRequestBytes+1024, &stored) != nil || stored.Raw != string(raw) || stored.Root != digest || stored.SourceEpoch != a.source.Epoch {
			return TypedIndexStatus{}, typedindex.Invalid
		}
		if a.intent.Canceled {
			return TypedIndexStatus{}, typedindex.Canceled
		}
		desired, err := s.typedRead(ctx, "typed_index_request", a.intent.Desired)
		if err != nil {
			return TypedIndexStatus{}, err
		}
		var child typedIndexRequest
		if typedDecode(desired, typedindex.MaxRequestBytes+1024, &child) != nil || child.Root != digest {
			return TypedIndexStatus{}, typedindex.Stale
		}
		return s.GetTypedIndexStatus(ctx, repository)
	}
	pending, err := s.queuePendingIDs(ctx, JobTypedIndex, repository, "")
	if err != nil {
		return TypedIndexStatus{}, typedError(ctx, err)
	}
	next := a.intent
	next.Desired = digest
	next.Canceled = false
	intentBody, err := typedEncode(next, maxTypedIntentBytes)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	requestBody, err := typedEncode(typedIndexRequest{Raw: string(raw), Root: digest, SourceEpoch: a.source.Epoch}, typedindex.MaxRequestBytes+1024)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	vars := a.variables()
	vars["request"] = typedID("typed_index_request", digest)
	vars["request_body"] = requestBody
	vars["body"] = intentBody
	vars["pending_ids"] = pending
	write := `CREATE ONLY $request SET repository=$repository, body=$request_body RETURN NONE;
UPDATE $intent SET body=$body RETURN NONE;`
	count := uint64(2)
	if len(pending) == 0 {
		nonce, e := newLeaseToken()
		if e != nil {
			return TypedIndexStatus{}, e
		}
		vars["new_job"] = typedID(string(JobTypedIndex), nonce)
		write += `CREATE ONLY $new_job CONTENT { target:$repository,status:'pending',attempts:0,created_at:time::now(),pending_key:$repository,force:false } RETURN NONE;`
		count++
	}
	err = s.typedWrite(ctx, typedSourceFenceSQL+typedIntentFenceSQL+`
IF (SELECT VALUE id FROM typed_index_job WHERE pending_key=$repository AND status='pending' ORDER BY created_at LIMIT 1) != $pending_ids { THROW 'typed-stale'; };
`+write, vars, count)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	return s.GetTypedIndexStatus(ctx, repository)
}

// TypedIndexSchedule is coordinator work only. The scheduler owns the actual
// execution lease and token; no process may run before BeginTypedIndex succeeds.
func (s *Surreal) TypedIndexSchedule(ctx context.Context, repository string) (GenerationScheduleSpec, error) {
	_, spec, err := s.typedSchedule(ctx, repository)
	return spec, err
}
func (s *Surreal) typedSchedule(ctx context.Context, repository string) (typedAuthority, GenerationScheduleSpec, error) {
	a, err := s.typedAuthority(ctx, repository)
	if err != nil {
		return typedAuthority{}, GenerationScheduleSpec{}, err
	}
	if a.intent.Canceled || a.intent.RestoreRequired || a.intent.Desired == "" {
		return typedAuthority{}, GenerationScheduleSpec{}, typedindex.Canceled
	}
	raw, err := s.typedRead(ctx, "typed_index_request", a.intent.Desired)
	if err != nil {
		return typedAuthority{}, GenerationScheduleSpec{}, err
	}
	var request typedIndexRequest
	if typedDecode(raw, typedindex.MaxRequestBytes+1024, &request) != nil {
		return typedAuthority{}, GenerationScheduleSpec{}, typedindex.Invalid
	}
	parentRaw, err := s.typedRead(ctx, "typed_index_request", request.Root)
	if err != nil {
		return typedAuthority{}, GenerationScheduleSpec{}, err
	}
	var parent typedIndexRequest
	if typedDecode(parentRaw, typedindex.MaxRequestBytes+1024, &parent) != nil || request.SourceEpoch != a.source.Epoch || parent.Root != request.Root {
		return typedAuthority{}, GenerationScheduleSpec{}, typedindex.Stale
	}
	admission, err := a.admit(ctx, parent.Raw, typedIndexPlan{}, "")
	if err != nil || admission.Digest() != request.Root {
		return typedAuthority{}, GenerationScheduleSpec{}, typedindex.Stale
	}
	return a, GenerationScheduleSpec{Repository: repository, Stage: "typed-index", Generation: request.Root, ResourceClass: GenerationResourceTypedIndex, TotalItems: 1, ChunkItems: 1, MaxAttempts: 3, RepositoryTokens: 1}, nil
}

type typedExecution struct {
	authority     typedAuthority
	request       typedIndexRequest
	parent        typedIndexRequest
	plan          typedIndexPlan
	work          TypedIndexWork
	attempt       typedIndexAttempt
	attemptRaw    string
	activeAttempt string
	vars          map[string]any
}

func (s *Surreal) typedExecution(ctx context.Context, chunk GenerationChunk, requireAttempt bool) (typedExecution, error) {
	if validGenerationChunkLease(chunk) != nil || chunk.Stage != "typed-index" || chunk.ResourceClass != GenerationResourceTypedIndex || chunk.Offset != 0 || chunk.Length != 1 || !validSHA256(chunk.Generation) {
		return typedExecution{}, typedindex.Stale
	}
	a, err := s.typedAuthority(ctx, chunk.Repository)
	if err != nil {
		return typedExecution{}, err
	}
	if a.intent.Canceled {
		return typedExecution{}, typedindex.Canceled
	}
	if a.intent.Desired == "" || a.intent.RestoreRequired {
		return typedExecution{}, typedindex.Stale
	}
	x := typedExecution{authority: a, vars: a.variables()}
	raw, err := s.typedRead(ctx, "typed_index_request", a.intent.Desired)
	if err != nil {
		return x, err
	}
	if typedDecode(raw, typedindex.MaxRequestBytes+1024, &x.request) != nil || x.request.Root != chunk.Generation || x.request.SourceEpoch != a.source.Epoch {
		return x, typedindex.Stale
	}
	raw, err = s.typedRead(ctx, "typed_index_request", chunk.Generation)
	if err != nil {
		return x, err
	}
	if typedDecode(raw, typedindex.MaxRequestBytes+1024, &x.parent) != nil || x.parent.Root != chunk.Generation {
		return x, typedindex.Invalid
	}
	x.work.Parent, err = a.admit(ctx, x.parent.Raw, typedIndexPlan{}, "")
	if err != nil || x.work.Parent.Digest() != chunk.Generation {
		return x, typedindex.Stale
	}
	raw, err = s.typedRead(ctx, "typed_index_plan", chunk.Generation)
	if err != nil {
		return x, err
	}
	if raw != "" && typedDecode(raw, maxTypedControlBytes, &x.plan) != nil {
		return x, typedindex.Invalid
	}
	x.work.Admission = x.work.Parent
	if a.intent.Desired != chunk.Generation {
		x.work.Admission, err = a.admit(ctx, x.request.Raw, x.plan, chunk.Generation)
		if err != nil || x.plan.Successor != a.intent.Desired || x.work.Admission.Digest() != a.intent.Desired {
			return x, typedindex.Stale
		}
	}
	x.work.RootDigest = chunk.Generation
	x.work.PlanDigest = x.plan.Digest
	x.work.AttemptDigest = typedDigest([]byte(chunk.Identity + "\x00" + chunk.LeaseToken))
	x.vars["chunk"] = generationChunkRecordID(chunk)
	x.vars["lease"] = chunk.LeaseToken
	x.vars["worker"] = chunk.ClaimedBy
	x.vars["root"] = chunk.Generation
	x.vars["schedule_digest"] = chunk.ScheduleDigest
	x.vars["schedule_current"] = typedID("generation_schedule_current", strings.TrimPrefix(generationCurrentID(chunk.Repository, chunk.Stage), "sha256:"))
	x.vars["attempt"] = typedID("typed_index_attempt", x.work.AttemptDigest)
	statusRaw, err := s.typedRead(ctx, "typed_index_state", chunk.Repository)
	if err != nil {
		return x, err
	}
	if statusRaw != "" {
		if typedDecode(statusRaw, maxTypedControlBytes, &x.activeAttempt) != nil || !validSHA256(x.activeAttempt) {
			return x, typedindex.Invalid
		}
	}
	x.attemptRaw, err = s.typedRead(ctx, "typed_index_attempt", x.work.AttemptDigest)
	if err != nil {
		return x, err
	}
	if x.attemptRaw != "" {
		if typedDecode(x.attemptRaw, maxTypedControlBytes, &x.attempt) != nil || !validTypedAttempt(x.attempt) || x.attempt.Root != chunk.Generation || x.attempt.Request != a.intent.Desired || x.attempt.Lease != GenerationLeaseTokenDigest(chunk.LeaseToken) {
			return x, typedindex.Stale
		}
		x.work.Stage = x.attempt.Stage
		x.work.States = x.attempt.States
	}
	if requireAttempt && (x.attemptRaw == "" || x.activeAttempt != x.work.AttemptDigest || x.attempt.Reason != "") {
		return x, typedindex.Stale
	}
	return x, nil
}
func (s *Surreal) BeginTypedIndex(ctx context.Context, chunk GenerationChunk) (TypedIndexWork, error) {
	x, err := s.typedExecution(ctx, chunk, false)
	if err != nil {
		return TypedIndexWork{}, err
	}
	if x.attemptRaw != "" {
		if x.activeAttempt != x.work.AttemptDigest || x.attempt.Reason != "" {
			return TypedIndexWork{}, typedindex.Stale
		}
		if err := s.typedFence(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL, x.vars); err != nil {
			return TypedIndexWork{}, err
		}
		return x.work, nil
	}
	x.attempt = typedIndexAttempt{Root: chunk.Generation, Request: x.authority.intent.Desired, Lease: GenerationLeaseTokenDigest(chunk.LeaseToken), Stage: TypedPreflight, States: [5]string{"running", "pending", "pending", "pending", "pending"}}
	body, _ := typedEncode(x.attempt, maxTypedControlBytes)
	stateBody, _ := typedEncode(x.work.AttemptDigest, maxTypedControlBytes)
	x.vars["body"] = body
	x.vars["state_body"] = stateBody
	x.vars["state"] = typedID("typed_index_state", chunk.Repository)
	err = s.typedWrite(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL+`CREATE ONLY $attempt SET repository=$repository,body=$body RETURN NONE; UPSERT $state SET repository=$repository,body=$state_body RETURN NONE;`, x.vars, 2)
	x.work.Resume = chunk.Attempt > 0 || x.activeAttempt != ""
	x.work.Stage = x.attempt.Stage
	x.work.States = x.attempt.States
	return x.work, err
}
func typedStageIndex(stage TypedIndexStage) int {
	for n, s := range typedStages {
		if s == stage {
			return n
		}
	}
	return -1
}
func validTypedAttempt(a typedIndexAttempt) bool {
	if !validSHA256(a.Root) || !validSHA256(a.Request) || !validSHA256(a.Lease) {
		return false
	}
	index := typedStageIndex(a.Stage)
	if a.Stage == TypedComplete {
		if a.Reason != "" {
			return false
		}
		for _, state := range a.States {
			if state != "complete" {
				return false
			}
		}
		return true
	}
	if index < 0 || a.Reason != "" && !validTypedReason(a.Reason) {
		return false
	}
	for n, state := range a.States {
		want := "pending"
		if n < index {
			want = "complete"
		}
		if n == index {
			want = "running"
			if a.Reason != "" {
				want = "failed"
			}
		}
		if state != want {
			return false
		}
	}
	return true
}

func (s *Surreal) typedSaveAttempt(ctx context.Context, x typedExecution, next typedIndexAttempt, extra string, extraRows uint64) error {
	body, err := typedEncode(next, maxTypedControlBytes)
	if err != nil {
		return err
	}
	x.vars["body"] = body
	x.vars["attempt_before"] = x.attemptRaw
	return s.typedWrite(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL+`IF (SELECT body FROM $attempt LIMIT 1)[0].body != $attempt_before { THROW 'typed-stale'; }; UPDATE $attempt SET body=$body RETURN NONE;`+extra, x.vars, 1+extraRows)
}
func (s *Surreal) AdvanceTypedIndex(ctx context.Context, chunk GenerationChunk, expected TypedIndexStage) error {
	x, err := s.typedExecution(ctx, chunk, true)
	if err != nil {
		return err
	}
	index := typedStageIndex(expected)
	if x.attempt.Stage != expected || index < 0 || index == 1 || index >= 4 {
		return typedindex.Stale
	}
	if index >= 2 && x.work.Admission.Request().Action != typedindex.Execute {
		return typedindex.Invalid
	}
	next := x.attempt
	next.States[index] = "complete"
	next.States[index+1] = "running"
	next.Stage = typedStages[index+1]
	return s.typedSaveAttempt(ctx, x, next, "", 0)
}
func (s *Surreal) SealTypedIndexPlan(ctx context.Context, chunk GenerationChunk, plan typedindex.PackagePlan) (typedindex.Admission, error) {
	x, err := s.typedExecution(ctx, chunk, true)
	if err != nil {
		return typedindex.Admission{}, err
	}
	if x.attempt.Stage != TypedPlanning {
		return typedindex.Admission{}, typedindex.Stale
	}
	if _, err := typedindex.DecodePackagePlan(ctx, x.work.Parent, plan.Bytes(), plan.Digest()); err != nil {
		return typedindex.Admission{}, err
	}
	successor, err := typedindex.PlannedSuccessor(ctx, x.work.Parent, plan.Digest())
	if err != nil {
		return typedindex.Admission{}, err
	}
	successorRaw, _ := typedEncode(successor, typedindex.MaxRequestBytes)
	binding := typedIndexPlan{Digest: plan.Digest()}
	admission, err := x.authority.admit(ctx, successorRaw, binding, chunk.Generation)
	if err != nil {
		return typedindex.Admission{}, err
	}
	binding.Successor = admission.Digest()
	if x.plan.Digest != "" && x.plan != binding {
		return typedindex.Admission{}, typedindex.Stale
	}
	next := x.attempt
	next.Request = admission.Digest()
	next.Stage = TypedExecution
	next.States[1] = "complete"
	next.States[2] = "running"
	intent := x.authority.intent
	intent.Desired = admission.Digest()
	intentBody, _ := typedEncode(intent, maxTypedIntentBytes)
	x.vars["intent_body"] = intentBody
	extra := `UPDATE $intent SET body=$intent_body RETURN NONE;`
	rows := uint64(1)
	if x.plan.Digest == "" {
		x.vars["plan"] = typedID("typed_index_plan", chunk.Generation)
		x.vars["plan_body"], _ = typedEncode(binding, maxTypedControlBytes)
		x.vars["successor"] = typedID("typed_index_request", admission.Digest())
		x.vars["request_body"], _ = typedEncode(typedIndexRequest{Raw: successorRaw, Root: chunk.Generation, SourceEpoch: x.authority.source.Epoch}, typedindex.MaxRequestBytes+1024)
		extra += `CREATE ONLY $plan SET repository=$repository,body=$plan_body RETURN NONE; CREATE ONLY $successor SET repository=$repository,body=$request_body RETURN NONE;`
		rows += 2
	}
	err = s.typedSaveAttempt(ctx, x, next, extra, rows)
	return admission, err
}

func (s *Surreal) PublishTypedIndex(ctx context.Context, chunk GenerationChunk, expected typedindex.PublicationPointer, bundle typedindex.Bundle) (typedindex.PublicationPointer, error) {
	x, err := s.typedExecution(ctx, chunk, true)
	if err != nil {
		return expected, err
	}
	if x.attempt.Stage != TypedPublication || expected.Epoch >= math.MaxInt64 {
		return expected, typedindex.Stale
	}
	currentRaw, err := s.typedRead(ctx, "typed_index_current", chunk.Repository)
	if err != nil {
		return expected, err
	}
	var current typedindex.PublicationPointer
	if currentRaw != "" && typedDecode(currentRaw, maxTypedControlBytes, &current) != nil {
		return expected, typedindex.Invalid
	}
	nextPointer, err := typedindex.NextPublication(ctx, current, expected, x.work.Admission, bundle)
	if err != nil {
		return current, err
	}
	x.vars["current"] = typedID("typed_index_current", chunk.Repository)
	x.vars["current_before"] = currentRaw
	x.vars["current_body"], _ = typedEncode(nextPointer, maxTypedControlBytes)
	guard := `IF (SELECT body FROM $current LIMIT 1)[0].body != $current_before { THROW 'typed-stale'; };`
	if currentRaw == "" {
		guard = `IF (SELECT id FROM $current LIMIT 1)[0].id != NONE { THROW 'typed-stale'; };`
	}
	next := x.attempt
	next.Stage = TypedComplete
	next.States[4] = "complete"
	err = s.typedSaveAttempt(ctx, x, next, guard+`UPSERT $current SET repository=$repository,body=$current_body RETURN NONE;`, 1)
	return nextPointer, err
}
func validTypedReason(reason typedindex.Refusal) bool {
	switch reason {
	case typedindex.Invalid, typedindex.Unsupported, typedindex.Stale, typedindex.Capacity, typedindex.Unprepared, typedindex.WallLimit, typedindex.ExecutionFailed, typedindex.Containment, typedindex.Canceled:
		return true
	}
	return false
}
func (s *Surreal) FailTypedIndex(ctx context.Context, chunk GenerationChunk, reason typedindex.Refusal) error {
	if !validTypedReason(reason) {
		return typedindex.Invalid
	}
	x, err := s.typedExecution(ctx, chunk, true)
	if err != nil {
		return err
	}
	index := typedStageIndex(x.attempt.Stage)
	if index < 0 {
		return typedindex.Stale
	}
	next := x.attempt
	next.Reason = reason
	next.States[index] = "failed"
	return s.typedSaveAttempt(ctx, x, next, "", 0)
}
func (s *Surreal) CancelTypedIndex(ctx context.Context, repository, expected string) error {
	a, err := s.typedAuthority(ctx, repository)
	if err != nil {
		return err
	}
	if a.intent.Desired != expected || expected == "" {
		return typedindex.Stale
	}
	if a.intent.Canceled {
		return nil
	}
	next := a.intent
	next.Canceled = true
	body, _ := typedEncode(next, maxTypedIntentBytes)
	vars := a.variables()
	vars["body"] = body
	return s.typedWrite(ctx, typedSourceFenceSQL+typedIntentFenceSQL+`UPDATE $intent SET body=$body RETURN NONE;`, vars, 1)
}

// ResolveTypedIndexCurrent intentionally ignores pending/rejected replacement
// desire: a still-valid previous publication remains readable until replacement.
func (s *Surreal) ResolveTypedIndexCurrent(ctx context.Context, repository string) (typedindex.PublicationPointer, error) {
	a, err := s.typedAuthority(ctx, repository)
	if err != nil {
		return typedindex.PublicationPointer{}, err
	}
	return s.resolveTypedIndexCurrent(ctx, a)
}
func (s *Surreal) resolveTypedIndexCurrent(ctx context.Context, a typedAuthority) (typedindex.PublicationPointer, error) {
	repository := a.source.Name
	if a.intent.RestoreRequired || a.intentRaw == "" {
		return typedindex.PublicationPointer{}, ErrNotFound
	}
	raw, err := s.typedRead(ctx, "typed_index_current", repository)
	if err != nil {
		return typedindex.PublicationPointer{}, err
	}
	if raw == "" {
		return typedindex.PublicationPointer{}, ErrNotFound
	}
	var pointer typedindex.PublicationPointer
	if typedDecode(raw, maxTypedControlBytes, &pointer) != nil || pointer.Epoch < 1 || pointer.Epoch > math.MaxInt64 {
		return pointer, typedindex.Invalid
	}
	requestRaw, err := s.typedRead(ctx, "typed_index_request", pointer.Binding.RequestDigest)
	if err != nil {
		return pointer, err
	}
	var request typedIndexRequest
	if typedDecode(requestRaw, typedindex.MaxRequestBytes+1024, &request) != nil {
		return pointer, typedindex.Invalid
	}
	var wire typedindex.Request
	if json.Unmarshal([]byte(request.Raw), &wire) != nil || wire.Action != typedindex.Execute {
		return pointer, typedindex.Invalid
	}
	if request.SourceEpoch != a.source.Epoch {
		return typedindex.PublicationPointer{}, typedindex.Stale
	}
	admission, err := a.admit(ctx, request.Raw, typedIndexPlan{Digest: wire.PlanDigest}, wire.ParentRequestDigest)
	if err != nil {
		return typedindex.PublicationPointer{}, err
	}
	if admission.Digest() != pointer.Binding.RequestDigest || pointer.Binding.Source != admission.Request().Source || pointer.Binding.ProfileDigest != a.profile.Digest() || pointer.Binding.ToolsDigest != admission.Request().ToolsDigest || pointer.Binding.PlanDigest != wire.PlanDigest || !validSHA256(pointer.RootDigest) {
		return typedindex.PublicationPointer{}, typedindex.Invalid
	}
	vars := a.variables()
	vars["current"] = typedID("typed_index_current", repository)
	vars["pointer_before"] = raw
	if err := s.typedFence(ctx, typedSourceFenceSQL+typedIntentFenceSQL+`IF (SELECT body FROM $current LIMIT 1)[0].body != $pointer_before { THROW 'typed-stale'; };`, vars); err != nil {
		return typedindex.PublicationPointer{}, err
	}
	return pointer, nil
}
func (s *Surreal) GetTypedIndexStatus(ctx context.Context, repository string) (TypedIndexStatus, error) {
	a, err := s.typedAuthority(ctx, repository)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	out := TypedIndexStatus{Desired: a.intent.Desired, Canceled: a.intent.Canceled, RestoreRequired: a.intent.RestoreRequired}
	stateRaw, err := s.typedRead(ctx, "typed_index_state", repository)
	if err != nil {
		return out, err
	}
	var activeAttempt string
	if stateRaw != "" && (typedDecode(stateRaw, maxTypedControlBytes, &activeAttempt) != nil || !validSHA256(activeAttempt)) {
		return out, typedindex.Invalid
	}
	if activeAttempt != "" {
		raw, err := s.typedRead(ctx, "typed_index_attempt", activeAttempt)
		if err != nil {
			return out, err
		}
		var attempt typedIndexAttempt
		if typedDecode(raw, maxTypedControlBytes, &attempt) != nil || !validTypedAttempt(attempt) {
			return out, typedindex.Invalid
		}
		if attempt.Request == a.intent.Desired {
			out.Stage = attempt.Stage
			out.States = attempt.States
			out.Reason = attempt.Reason
		}
	}
	current, err := s.resolveTypedIndexCurrent(ctx, a)
	if err == nil {
		out.Current = &current
	} else if errors.Is(err, typedindex.Stale) {
		out.Stale = true
	} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, typedindex.Stale) && !errors.Is(err, typedindex.Disabled) {
		return out, err
	}
	return out, nil
}

// ClearTypedIndexForRestore is offline-only. No imported current, request,
// attempt, plan or lease survives; bounded intent requires explicit trusted
// source/tool revalidation (InstallTypedProfile) before any new request.
func (s *Surreal) ClearTypedIndexForRestore(ctx context.Context) error {
	for _, table := range []string{"typed_index_current", "typed_index_state", "typed_index_attempt", "typed_index_plan", "typed_index_request", string(JobTypedIndex)} {
		if err := s.clearRestoreTable(ctx, table, "", "", map[string]any{}); err != nil {
			return err
		}
	}
	after := ""
	for {
		statement := `SELECT VALUE id FROM typed_index_intent WHERE repository > $after ORDER BY repository LIMIT $limit;`
		results, err := storeQuery[[]models.RecordID](ctx, s.accounting, s.db, statement, map[string]any{"after": after, "limit": restoreClearRows}, storeRead())
		if err != nil {
			return typedError(ctx, err)
		}
		ids := firstDomainRows(results)
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			repository, ok := id.ID.(string)
			if !ok {
				return typedindex.Invalid
			}
			raw, err := s.typedRead(ctx, "typed_index_intent", repository)
			if err != nil {
				return err
			}
			var intent TypedIndexIntent
			if typedDecode(raw, maxTypedIntentBytes, &intent) != nil || intent.Repository != repository {
				return typedindex.Invalid
			}
			intent.Desired = ""
			intent.Canceled = false
			intent.RestoreRequired = true
			body, _ := typedEncode(intent, maxTypedIntentBytes)
			if err := s.typedWrite(ctx, `IF (SELECT body FROM $intent LIMIT 1)[0].body != $before { THROW 'typed-stale'; }; UPDATE $intent SET body=$body RETURN NONE;`, map[string]any{"intent": id, "before": raw, "body": body}, 1); err != nil {
				return err
			}
			after = repository
		}
	}
}
