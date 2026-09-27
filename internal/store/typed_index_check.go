package store

import (
	"context"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

// typedCheckRelation requires coherent historical request/plan observations; it
// never invents current authority or rederives counts from a digest-only plan.
func typedCheckRelation(a typedIndexAttempt, parent typedindex.Request, plan string) bool {
	if a.Stage != TypedChecked {
		return a.Check == nil
	}
	c := a.Check
	return c != nil && c.Validate() == nil && parent.Schema == typedindex.ManagedRequestSchema && c.Purpose == parent.Purpose && c.ParentDigest == a.Root && c.RequestDigest == a.Request && c.PlanDigest == plan
}

// CompleteTypedIndexCheck commits one exact verified nonpublishing result. The
// returned summary is audit evidence, not a readable publication or native
// quiescence proof. Scheduler settlement and holder release remain separate.
func (s *Surreal) CompleteTypedIndexCheck(ctx context.Context, chunk GenerationChunk, bundle typedindex.Bundle) (typedindex.CheckedSummary, error) {
	x, err := s.typedExecution(ctx, chunk, true)
	if err != nil {
		return typedindex.CheckedSummary{}, err
	}
	summary, err := typedindex.CheckBundle(ctx, x.work.Admission, bundle)
	if err != nil {
		return typedindex.CheckedSummary{}, err
	}
	guard, err := typedCheckFence(x)
	if err != nil {
		return typedindex.CheckedSummary{}, err
	}
	if x.attempt.Stage == TypedChecked && x.attempt.Check != nil && *x.attempt.Check == summary {
		x.vars["attempt_before"] = x.attemptRaw
		err = s.typedFence(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL+`IF (SELECT body FROM $attempt WHERE request_root=$root LIMIT 1)[0].body != $attempt_before { THROW 'typed-stale'; };`+guard, x.vars)
		return summary, err
	}
	if x.attempt.Stage != TypedValidation || x.attempt.Custody == nil || x.attempt.Custody.Revision != 2 {
		return typedindex.CheckedSummary{}, typedindex.Stale
	}
	next := x.attempt
	next.Stage = TypedChecked
	next.States = [5]string{"complete", "complete", "complete", "complete", "not_requested"}
	next.Check = &summary
	if !validTypedAttempt(next) || !typedCheckRelation(next, x.work.Parent.Request(), x.work.PlanDigest) {
		return typedindex.CheckedSummary{}, typedindex.Invalid
	}
	if err = s.typedSaveAttempt(ctx, x, next, guard, 0); err != nil {
		return typedindex.CheckedSummary{}, err
	}
	return summary, nil
}

func typedCheckFence(x typedExecution) (string, error) {
	parent, err := typedEncode(x.parent, typedindex.MaxRequestBytes+1024)
	if err != nil {
		return "", err
	}
	request, err := typedEncode(x.request, typedindex.MaxRequestBytes+1024)
	if err != nil {
		return "", err
	}
	plan, err := typedEncode(x.plan, maxTypedControlBytes)
	if err != nil {
		return "", err
	}
	x.vars["check_parent_body"] = parent
	x.vars["check_request"] = typedID("typed_index_request", x.work.Admission.Digest())
	x.vars["check_request_body"] = request
	x.vars["check_plan"] = typedID("typed_index_plan", x.work.RootDigest)
	x.vars["check_plan_body"] = plan
	return `IF (SELECT body FROM $typed_root LIMIT 1)[0].body != $check_parent_body OR (SELECT body FROM $check_request LIMIT 1)[0].body != $check_request_body OR (SELECT body FROM $check_plan LIMIT 1)[0].body != $check_plan_body { THROW 'typed-stale'; };`, nil
}
