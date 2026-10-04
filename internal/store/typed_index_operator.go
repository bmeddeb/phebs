package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// TypedIndexOperator is a bounded read-only control snapshot. It contains no
// executable paths, worker output or source bytes. Profile remains internal;
// transports project only its named selection and closed resource policy.
type TypedIndexOperator struct {
	Source         typedindex.Source
	Profile        typedindex.Profile
	ProfileEpoch   uint64
	UniverseDigest string
	Revision       string
	Status         TypedIndexStatus
	Coordinator    JobStatus
	Schedule       *GenerationSchedule
	DesiredFresh   bool // Independently authenticated; current publication may be stale.
	// Desired is that authenticated request; zero unless DesiredFresh.
	Desired typedindex.Request
}

func operatorRevision(a typedAuthority) (string, error) {
	source, err := typedSource(a.source)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Source            typedindex.Source
		Epoch             int64
		Profile, Universe string
	}{source, a.intent.ProfileEpoch, a.intent.ProfileDigest, a.intent.UniverseDigest})
	if err != nil {
		return "", typedindex.Invalid
	}
	return typedDigest(raw), nil
}

// ReadTypedIndexOperator never initializes a source or scans job history. The
// creation-linked coordinator is a point lookup. Missing collected history is
// unavailable, never a successful job. Existing publication validation is shared.
func (s *Surreal) ReadTypedIndexOperator(ctx context.Context, repository string) (TypedIndexOperator, error) {
	a, err := s.readTypedAuthority(ctx, repository)
	if err != nil {
		return TypedIndexOperator{}, err
	}
	source, err := typedSource(a.source)
	if err != nil {
		return TypedIndexOperator{}, err
	}
	out := TypedIndexOperator{Source: source, Profile: a.profile, ProfileEpoch: uint64(a.intent.ProfileEpoch), UniverseDigest: a.intent.UniverseDigest}
	out.Revision, err = operatorRevision(a)
	if err != nil {
		return out, err
	}
	out.Status, err = s.typedIndexStatus(ctx, a)
	if err != nil {
		return out, err
	}
	if a.intent.Desired != "" {
		raw, e := s.typedReadControl(ctx, "typed_index_request", a.intent.Desired)
		if e != nil {
			return out, e
		}
		var request typedIndexRequest
		if typedDecode(raw, typedindex.MaxRequestBytes+1024, &request) != nil {
			return out, typedindex.Invalid
		}
		var desired typedindex.Request
		if typedDecode(request.Raw, typedindex.MaxRequestBytes, &desired) != nil || !typedStoredRequest(desired) {
			return out, typedindex.Invalid
		}
		if request.SourceEpoch != a.source.Epoch || desired.Source != source || desired.ProfileEpoch != uint64(a.intent.ProfileEpoch) || desired.ProfileDigest != a.intent.ProfileDigest || desired.UniverseDigest != a.intent.UniverseDigest {
			out.Status.Stale = true
			out.Status.Stage = ""
			out.Status.States = [5]string{}
			out.Status.Reason = ""
			out.Status.Check = nil
		} else {
			out.DesiredFresh = true
			out.Desired = desired
		}
		// The coordinator finishes before the single generation worker claims.
		// Read the desired root's exact schedule, never the current stage pointer.
		spec := typedIndexScheduleSpec(repository, request.Root)
		digest := generationScheduleDigest(spec)
		if e := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); e != nil {
			return out, e
		}
		result, e := storeQuery[[]generationScheduleRec](ctx, s.accounting, s.db,
			"SELECT * FROM $schedule LIMIT 1", map[string]any{"schedule": typedID("generation_schedule", strings.TrimPrefix(digest, "sha256:"))}, storeRead())
		if e != nil {
			return out, typedError(ctx, e)
		}
		rows := firstDomainRows(result)
		if len(rows) > 1 {
			return out, typedindex.Invalid
		}
		if len(rows) == 1 {
			schedule := rows[0].schedule()
			if ValidateGenerationSchedule(schedule) != nil || schedule.Digest != digest {
				return out, typedindex.Invalid
			}
			out.Schedule = &schedule
		}

		type link struct {
			Job  *models.RecordID `json:"latest_typed_job"`
			Root string           `json:"latest_typed_root"`
		}
		if e := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); e != nil {
			return out, e
		}
		linksResult, e := storeQuery[[]link](ctx, s.accounting, s.db, "SELECT latest_typed_job, latest_typed_root FROM $repo LIMIT 1", map[string]any{"repo": repoID(repository)}, storeRead())
		if e != nil {
			return out, typedError(ctx, e)
		}
		links := firstDomainRows(linksResult)
		if len(links) != 1 {
			return out, typedindex.Stale
		}
		if links[0].Job != nil && links[0].Root == request.Root {
			if links[0].Job.Table != string(JobTypedIndex) {
				return out, typedindex.Invalid
			}
			type job struct {
				Target string    `json:"target"`
				Status JobStatus `json:"status"`
			}
			if e := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); e != nil {
				return out, e
			}
			result, e := storeQuery[[]job](ctx, s.accounting, s.db, "SELECT target, status FROM $job LIMIT 1", map[string]any{"job": *links[0].Job}, storeRead())
			if e != nil {
				return out, typedError(ctx, e)
			}
			jobs := firstDomainRows(result)
			if len(jobs) > 1 {
				return out, typedindex.Invalid
			}
			if len(jobs) == 1 {
				if jobs[0].Target != repository || !validOperatorJob(jobs[0].Status) {
					return out, typedindex.Invalid
				}
				out.Coordinator = jobs[0].Status
			}
		}
	}
	confirmed, err := s.readTypedAuthority(ctx, repository)
	if err != nil {
		return out, err
	}
	if confirmed.intentRaw != a.intentRaw || confirmed.source != a.source {
		return out, typedindex.Stale
	}
	return out, nil
}
func validOperatorJob(status JobStatus) bool {
	switch status {
	case StatusPending, StatusClaimed, StatusRunning, StatusDone, StatusFailed, StatusCanceled:
		return true
	}
	return false
}

// EnqueueTypedIndexExpected binds the browser's preview revision. The existing
// enqueue transaction re-admits the exact request and fences source/profile
// atomically, including changes between this read and its transaction.
func (s *Surreal) EnqueueTypedIndexExpected(ctx context.Context, repository, expected string, raw []byte) (TypedIndexStatus, error) {
	a, err := s.readTypedAuthority(ctx, repository)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	revision, err := operatorRevision(a)
	if err != nil {
		return TypedIndexStatus{}, err
	}
	if revision != expected {
		return TypedIndexStatus{}, typedindex.Stale
	}
	return s.EnqueueTypedIndex(ctx, repository, raw)
}
