package store

import (
	"context"
	"encoding/hex"
	"strings"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func typedDigestControlKind(kind TypedIndexControlKind) bool {
	return kind == TypedIndexRequests || kind == TypedIndexAttempts || kind == TypedIndexPlans
}
func typedControlKind(kind TypedIndexControlKind) bool {
	return typedDigestControlKind(kind) || kind == TypedIndexIntents || kind == TypedIndexStates || kind == TypedIndexCurrents
}
func typedStoredDigest(s string) bool { return validSHA256(s) && strings.ToLower(s) == s }

func typedControlKey(kind TypedIndexControlKind, key string) bool {
	if typedDigestControlKind(kind) {
		return typedStoredDigest(key)
	}
	return typedControlKind(kind) && reponame.Validate(key) == nil && len(key) <= 512
}

// These structural checks mirror the immutable wire's scalar grammar. They
// deliberately establish neither a current profile nor execution admission.
func typedStoredToken(s string) bool {
	if len(s) == 0 || len(s) > 64 || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}
func typedStoredSource(s typedindex.Source) bool {
	if reponame.Validate(s.Repository) != nil || len(s.Repository) > 512 || !typedStoredToken(s.Incarnation) || !typedStoredDigest(s.Generation) || len(s.Commit) != 40 || strings.ToLower(s.Commit) != s.Commit {
		return false
	}
	_, err := hex.DecodeString(s.Commit)
	return err == nil
}
func typedStoredRequest(r typedindex.Request) bool {
	if r.ValidatePurpose() != nil || r.Provider != typedindex.ProviderID || !typedStoredSource(r.Source) || !typedStoredToken(r.ProfileName) || !typedStoredToken(r.IdempotencyKey) || r.ProfileEpoch == 0 {
		return false
	}
	for _, d := range []string{r.ProfileDigest, r.ConfigDigest, r.ToolsDigest, r.UniverseDigest, r.BundleDigest, r.PolicyDigest} {
		if !typedStoredDigest(d) {
			return false
		}
	}
	return r.Action == typedindex.Plan && r.ParentRequestDigest == "" && r.PlanDigest == "" || r.Action == typedindex.Execute && typedStoredDigest(r.ParentRequestDigest) && typedStoredDigest(r.PlanDigest)
}

func validateTypedCensusControl(ctx context.Context, kind TypedIndexControlKind, row TypedIndexControl) error {
	if typedDigestControlKind(kind) {
		if err := validateTypedControl(kind, row); err != nil {
			return err
		}
		if !typedStoredDigest(row.ID) || !typedStoredDigest(row.Root) {
			return typedindex.Invalid
		}
		if kind == TypedIndexAttempts {
			var a typedIndexAttempt
			if typedDecode(row.Body, maxTypedControlBytes, &a) != nil || !typedStoredDigest(a.ChunkIdentity) || !typedStoredDigest(a.Request) || !typedStoredDigest(a.Lease) {
				return typedindex.Invalid
			}
			if c := a.Custody; c != nil {
				for _, d := range []string{c.ManifestDigest, c.InputReceiptDigest, c.PublicationReceiptDigest, c.PublicationRequestDigest, c.PublicationPlanDigest, c.PublicationRootDigest} {
					if d != "" && !typedStoredDigest(d) {
						return typedindex.Invalid
					}
				}
			}

		}
		if kind == TypedIndexPlans {
			var p typedIndexPlan
			if typedDecode(row.Body, maxTypedControlBytes, &p) != nil || !typedStoredDigest(p.Digest) || !typedStoredDigest(p.Successor) {
				return typedindex.Invalid
			}
		}
		if kind == TypedIndexRequests {
			var stored typedIndexRequest
			var request typedindex.Request
			if typedDecode(row.Body, typedindex.MaxRequestBytes+1024, &stored) != nil || typedDecode(stored.Raw, typedindex.MaxRequestBytes, &request) != nil || !typedStoredRequest(request) {
				return typedindex.Invalid
			}
			source, err := typedSource(typedSourceRecord{Name: request.Source.Repository, Commit: request.Source.Commit, Incarnation: request.Source.Incarnation, Epoch: stored.SourceEpoch})
			if err != nil || source != request.Source {
				return typedindex.Invalid
			}
		}
		return nil
	}
	if !typedControlKey(kind, row.ID) || row.ID != row.Repository || row.StoredKey != "" || row.Root != "" || row.Parent || row.State != "" || row.GrowthKey != "" {
		return typedindex.Invalid
	}
	switch kind {
	case TypedIndexIntents:
		var intent TypedIndexIntent
		if typedDecode(row.Body, maxTypedIntentBytes, &intent) != nil || intent.Repository != row.Repository || intent.ProfileEpoch < 1 || !typedStoredDigest(intent.ProfileDigest) || !typedStoredDigest(intent.UniverseDigest) || intent.Desired != "" && !typedStoredDigest(intent.Desired) {
			return typedindex.Invalid
		}
		profile, err := typedindex.DecodeProfile(ctx, []byte(intent.ProfileJSON))
		if err != nil {
			return err
		}
		canonical, err := typedEncode(profile.Definition(), typedindex.MaxProfileBytes)
		if err != nil || canonical != intent.ProfileJSON || profile.Digest() != intent.ProfileDigest {
			return typedindex.Invalid
		}
	case TypedIndexStates:
		var attempt string
		if typedDecode(row.Body, maxTypedControlBytes, &attempt) != nil || !typedStoredDigest(attempt) {
			return typedindex.Invalid
		}
	case TypedIndexCurrents:
		current, err := decodeTypedIndexCurrent(row.Body)
		b := current.Pointer.Binding
		if err != nil || !typedStoredDigest(current.AttemptDigest) || !typedStoredDigest(current.Pointer.RootDigest) || !typedStoredDigest(b.RequestDigest) || b.Source.Repository != row.Repository || !typedStoredSource(b.Source) || !typedStoredDigest(b.ProfileDigest) || !typedStoredDigest(b.ToolsDigest) || !typedStoredDigest(b.PlanDigest) {
			return typedindex.Invalid
		}
	default:
		return typedindex.Invalid
	}
	return nil
}

// TypedIndexAttemptInspection is an immutable custody observation, not live
// admission, filesystem readiness, quiescence, or permission to delete. Parent
// is the original planning request even after its source/profile was replaced.
type TypedIndexAttemptInspection struct {
	Parent         typedindex.Request
	SourceEpoch    int64
	PlanningDigest string
	RequestDigest  string
	PlanDigest     string
	AttemptDigest  string
	ChunkIdentity  string
	LeaseDigest    string
	Stage          TypedIndexStage
	States         [5]string
	Reason         typedindex.Refusal
	Custody        *TypedIndexCustody
	Growth         *TypedIndexGrowth
}

type typedAttemptObservation struct {
	Attempt   []TypedIndexControl `json:"attempt"`
	Parent    []TypedIndexControl `json:"parent"`
	Request   []TypedIndexControl `json:"request"`
	Plan      []TypedIndexControl `json:"plan"`
	Successor []TypedIndexControl `json:"successor"`
}

// InspectTypedIndexAttempt uses three SDK reads: attempt and plan point
// selections, then one read-only transaction with five direct single-row
// lookups. The latter
// rechecks the complete selected attempt body/projections and observes all
// immutable relations together. At most three 9-KiB requests and four 4-KiB
// controls (including the initial selection) are retained. No current source,
// profile, repository listing, filesystem, child, or mutation is consulted.
func (s *Surreal) InspectTypedIndexAttempt(ctx context.Context, digest string) (TypedIndexAttemptInspection, error) {
	return s.inspectTypedIndexAttempt(ctx, digest, nil)
}

func (s *Surreal) inspectTypedIndexAttempt(ctx context.Context, digest string, beforeObservation func()) (TypedIndexAttemptInspection, error) {
	var out TypedIndexAttemptInspection
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !typedStoredDigest(digest) {
		return out, typedindex.Invalid
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	results, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, "SELECT "+typedControlProjection+" FROM $rid LIMIT 1;", map[string]any{"rid": typedID(string(TypedIndexAttempts), digest)}, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	rows := firstDomainRows(results)
	if len(rows) == 0 {
		return out, ErrNotFound
	}
	if len(rows) != 1 || rows[0].ID != digest || validateTypedCensusControl(ctx, TypedIndexAttempts, rows[0]) != nil {
		return out, typedindex.Invalid
	}
	selected := rows[0]
	var attempt typedIndexAttempt
	if typedDecode(selected.Body, maxTypedControlBytes, &attempt) != nil {
		return out, typedindex.Invalid
	}
	if attempt.Request == attempt.Root && attempt.Stage != TypedPreflight && attempt.Stage != TypedPlanning {
		return out, typedindex.Invalid
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	plans, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, "SELECT "+typedControlProjection+" FROM $rid LIMIT 1;", map[string]any{"rid": typedID(string(TypedIndexPlans), attempt.Root)}, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	initialPlan := firstDomainRows(plans)
	if len(initialPlan) > 1 {
		return out, typedindex.Invalid
	}
	successor := attempt.Root
	if len(initialPlan) == 1 {
		row := initialPlan[0]
		var plan typedIndexPlan
		if validateTypedCensusControl(ctx, TypedIndexPlans, row) != nil || row.Repository != selected.Repository || row.Root != attempt.Root || typedDecode(row.Body, maxTypedControlBytes, &plan) != nil {
			return out, typedindex.Invalid
		}
		successor = plan.Successor
	}
	if beforeObservation != nil {
		beforeObservation()
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	statement := `BEGIN TRANSACTION; RETURN [{
 attempt:(SELECT ` + typedControlProjection + ` FROM $attempt LIMIT 1),
 parent:(SELECT ` + typedControlProjection + ` FROM $parent LIMIT 1),
 request:(SELECT ` + typedControlProjection + ` FROM $request LIMIT 1),
 plan:(SELECT ` + typedControlProjection + ` FROM $plan LIMIT 1),
 successor:(SELECT ` + typedControlProjection + ` FROM $successor LIMIT 1)
 }]; COMMIT TRANSACTION;`
	observations, err := storeQuery[[]typedAttemptObservation](ctx, s.accounting, s.db, statement, map[string]any{
		"attempt": typedID(string(TypedIndexAttempts), digest), "parent": typedID(string(TypedIndexRequests), attempt.Root),
		"request": typedID(string(TypedIndexRequests), attempt.Request), "plan": typedID(string(TypedIndexPlans), attempt.Root),
		"successor": typedID(string(TypedIndexRequests), successor),
	}, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	observed := firstDomainRows(observations)
	if len(observed) != 1 || len(observed[0].Attempt) != 1 || observed[0].Attempt[0] != selected || len(observed[0].Plan) != len(initialPlan) || len(initialPlan) == 1 && observed[0].Plan[0] != initialPlan[0] {
		return out, typedindex.Stale
	}
	out, err = inspectTypedRequestRelations(ctx, selected.Repository, attempt.Root, attempt.Request, observed[0])
	if err != nil {
		return out, err
	}
	if attempt.Custody != nil && attempt.Custody.Revision == 3 && (attempt.Custody.PublicationRequestDigest != attempt.Request || attempt.Custody.PublicationPlanDigest != out.PlanDigest) {
		return TypedIndexAttemptInspection{}, typedindex.Invalid
	}
	out.AttemptDigest, out.ChunkIdentity, out.LeaseDigest = digest, attempt.ChunkIdentity, attempt.Lease
	out.Stage, out.States, out.Reason = attempt.Stage, attempt.States, attempt.Reason
	out.Custody, out.Growth = attempt.Custody, attempt.Growth
	return out, nil
}

// inspectTypedRequestRelations validates only historical immutable relationships,
// never current profile/source admission. The caller supplies one coherent read.
func inspectTypedRequestRelations(ctx context.Context, repository, root, requestDigest string, o typedAttemptObservation) (TypedIndexAttemptInspection, error) {
	var out TypedIndexAttemptInspection
	if len(o.Parent) != 1 || len(o.Request) != 1 || len(o.Plan) > 1 {
		return out, typedindex.Invalid
	}
	for _, row := range []TypedIndexControl{o.Parent[0], o.Request[0]} {
		if validateTypedCensusControl(ctx, TypedIndexRequests, row) != nil || row.Repository != repository || row.Root != root {
			return out, typedindex.Invalid
		}
	}
	if !o.Parent[0].Parent || o.Parent[0].ID != root || o.Request[0].ID != requestDigest {
		return out, typedindex.Invalid
	}
	var parent, request typedIndexRequest
	var wire typedindex.Request
	if typedDecode(o.Parent[0].Body, typedindex.MaxRequestBytes+1024, &parent) != nil || typedDecode(o.Request[0].Body, typedindex.MaxRequestBytes+1024, &request) != nil || parent.SourceEpoch != request.SourceEpoch || typedDecode(parent.Raw, typedindex.MaxRequestBytes, &out.Parent) != nil || typedDecode(request.Raw, typedindex.MaxRequestBytes, &wire) != nil {
		return TypedIndexAttemptInspection{}, typedindex.Invalid
	}
	if len(o.Plan) == 1 {
		row := o.Plan[0]
		if validateTypedCensusControl(ctx, TypedIndexPlans, row) != nil || row.Repository != repository || row.Root != root {
			return TypedIndexAttemptInspection{}, typedindex.Invalid
		}
		var plan typedIndexPlan
		if typedDecode(row.Body, maxTypedControlBytes, &plan) != nil {
			return TypedIndexAttemptInspection{}, typedindex.Invalid
		}
		successor := out.Parent
		successor.Action, successor.ParentRequestDigest, successor.PlanDigest = typedindex.Execute, root, plan.Digest
		successorRaw, err := typedEncode(successor, typedindex.MaxRequestBytes)
		if err != nil || typedDigest([]byte(successorRaw)) != plan.Successor || len(o.Successor) != 1 {
			return TypedIndexAttemptInspection{}, typedindex.Invalid
		}
		sr := o.Successor[0]
		var storedSuccessor typedIndexRequest
		if validateTypedCensusControl(ctx, TypedIndexRequests, sr) != nil || sr.Repository != repository || sr.ID != plan.Successor || sr.Root != root || typedDecode(sr.Body, typedindex.MaxRequestBytes+1024, &storedSuccessor) != nil || storedSuccessor.SourceEpoch != parent.SourceEpoch || storedSuccessor.Raw != successorRaw {
			return TypedIndexAttemptInspection{}, typedindex.Invalid
		}
	}
	if requestDigest != root {
		var plan typedIndexPlan
		if len(o.Plan) != 1 || typedDecode(o.Plan[0].Body, maxTypedControlBytes, &plan) != nil || plan.Successor != requestDigest || plan.Digest != wire.PlanDigest {
			return TypedIndexAttemptInspection{}, typedindex.Invalid
		}
		out.PlanDigest = plan.Digest
		wire.Action, wire.ParentRequestDigest, wire.PlanDigest = typedindex.Plan, "", ""
		if wire != out.Parent {
			return TypedIndexAttemptInspection{}, typedindex.Invalid
		}
	}
	out.SourceEpoch, out.PlanningDigest, out.RequestDigest = parent.SourceEpoch, root, requestDigest
	return out, nil
}
