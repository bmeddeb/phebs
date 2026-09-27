// Growth is prospective store authority only; no runtime caller is registered.
// After initialized source identity, acquisition/replay costs eight point reads
// plus one fenced transaction (one row for acquisition, zero for replay).
// Discovery costs one indexed read of at most two 4-KiB controls. Release
// inspection costs two bounded point reads; release is one one-row transaction
// repeating the exact attempt and chunk observations. No file, child, polling,
// process lock, cache, or time-based expiry is introduced. All metadata fits the
// existing attempt's 4-KiB body; startup adds only two schema definitions.
package store

import (
	"context"
	"encoding/json"
	"math"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

const typedGrowthKey = "typed-index"

// TypedIndexGrowthDomain is a trusted controller's bounded observation of one
// allocation domain. It is control metadata, not a reservation made by the OS.
// BaseInode names the already pinned base directory, never a private path.
type TypedIndexGrowthDomain struct {
	Device         uint64 `json:"device"`
	BaseInode      uint64 `json:"base_inode"`
	BlockBytes     uint64 `json:"block_bytes"`
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	TotalInodes    uint64 `json:"total_inodes"`
	FreeInodes     uint64 `json:"free_inodes"`
	FutureBytes    uint64 `json:"future_bytes"`
	FutureInodes   uint64 `json:"future_inodes"`
}
type TypedIndexGrowthSpec struct {
	Workspace TypedIndexGrowthDomain `json:"workspace"`
	Host      TypedIndexGrowthDomain `json:"host"`
}

// TypedIndexGrowth is immutable per-lease authority. Held promises survive
// cancellation, source/profile changes, deletion and lease expiry. A released
// attempt can never acquire again. Current publication reads remain permitted.
type TypedIndexGrowth struct {
	PlanningDigest string               `json:"planning_digest"`
	AttemptDigest  string               `json:"attempt_digest"`
	ChunkID        string               `json:"chunk_id"`
	ChunkIdentity  string               `json:"chunk_identity"`
	LeaseDigest    string               `json:"lease_digest"`
	State          string               `json:"state"`
	Spec           TypedIndexGrowthSpec `json:"spec"`
}

func validTypedGrowthSpec(s TypedIndexGrowthSpec) bool {
	for _, d := range []TypedIndexGrowthDomain{s.Workspace, s.Host} {
		if d.Device == 0 || d.BaseInode == 0 || d.BlockBytes == 0 || d.BlockBytes > 1<<20 || d.BlockBytes&(d.BlockBytes-1) != 0 || d.TotalBytes == 0 || d.TotalBytes > math.MaxInt64 || d.TotalBytes%d.BlockBytes != 0 || d.AvailableBytes > d.TotalBytes || d.TotalInodes == 0 || d.FreeInodes > d.TotalInodes || d.FutureBytes == 0 || d.FutureBytes > d.AvailableBytes || d.FutureInodes == 0 || d.FutureInodes > d.FreeInodes {
			return false
		}
	}
	a, b := s.Workspace, s.Host
	if a.Device == b.Device {
		if a.BlockBytes != b.BlockBytes || a.TotalBytes != b.TotalBytes || a.TotalInodes != b.TotalInodes {
			return false
		}
		// Both domains share one physical headroom observation, never two credits.
		freeBytes, freeInodes := min(a.AvailableBytes, b.AvailableBytes), min(a.FreeInodes, b.FreeInodes)
		if a.FutureBytes > freeBytes || b.FutureBytes > freeBytes-a.FutureBytes || a.FutureInodes > freeInodes || b.FutureInodes > freeInodes-a.FutureInodes {
			return false
		}
	}
	return true
}
func validTypedAttemptGrowth(a typedIndexAttempt) bool {
	g := a.Growth
	return g == nil && a.Custody == nil && a.Stage == TypedPreflight || g != nil && (validSHA256(g.AttemptDigest) && validTypedChunkID(g.ChunkID) && g.PlanningDigest == a.Root && g.ChunkIdentity == a.ChunkIdentity && g.LeaseDigest == a.Lease && (g.State == "held" || g.State == "released") && validTypedGrowthSpec(g.Spec))
}

// AcquireTypedIndexGrowth must precede any actual attempt growth. The controller
// first proves fresh per-device headroom for the complete new envelope. Exactly
// one holder exists installation-wide, enforced by a unique optional index. This
// method never infers physical capacity or native quiescence from supplied data.
func (s *Surreal) AcquireTypedIndexGrowth(ctx context.Context, chunk GenerationChunk, spec TypedIndexGrowthSpec) (TypedIndexGrowth, error) {
	return s.acquireTypedIndexGrowth(ctx, chunk, spec, nil)
}
func (s *Surreal) acquireTypedIndexGrowth(ctx context.Context, chunk GenerationChunk, spec TypedIndexGrowthSpec, disposition *TypedIndexDisposition) (TypedIndexGrowth, error) {
	if !validTypedGrowthSpec(spec) {
		return TypedIndexGrowth{}, typedindex.Invalid
	}
	x, err := s.typedExecution(ctx, chunk, true)
	if err != nil {
		return TypedIndexGrowth{}, err
	}
	guard := ""
	if disposition != nil {
		guard, err = disposition.fence(x)
		if err != nil {
			return TypedIndexGrowth{}, err
		}
	}
	if x.attempt.Growth != nil {
		if x.attempt.Growth.State != "held" || x.attempt.Growth.Spec != spec {
			return TypedIndexGrowth{}, typedindex.Stale
		}
		x.vars["attempt_before"] = x.attemptRaw
		err = s.typedFence(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL+guard+`IF (SELECT body FROM $attempt WHERE growth_key='typed-index' LIMIT 1)[0].body != $attempt_before { THROW 'typed-stale'; };`, x.vars)
		return *x.attempt.Growth, err
	}
	if x.attempt.Stage != TypedPreflight || x.attempt.Custody != nil {
		return TypedIndexGrowth{}, typedindex.Unprepared
	}
	g := TypedIndexGrowth{PlanningDigest: x.work.RootDigest, AttemptDigest: x.work.AttemptDigest, ChunkID: chunk.ID, ChunkIdentity: chunk.Identity, LeaseDigest: x.attempt.Lease, State: "held", Spec: spec}
	next := x.attempt
	next.Growth = &g
	body, err := typedEncode(next, maxTypedControlBytes)
	if err != nil {
		return TypedIndexGrowth{}, err
	}
	x.vars["body"], x.vars["attempt_before"] = body, x.attemptRaw
	err = s.typedWrite(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL+guard+`
 IF array::len((SELECT id FROM typed_index_attempt WHERE growth_key='typed-index' LIMIT 2)) != 0 { THROW 'typed-capacity'; };
 IF (SELECT body FROM $attempt LIMIT 1)[0].body != $attempt_before { THROW 'typed-stale'; };
 UPDATE $attempt SET body=$body,growth_key='typed-index' RETURN NONE;`, x.vars, 1)
	return g, err
}

// GetTypedIndexGrowth is one indexed read of at most two 4-KiB controls. Absence
// is meaningful only after the startup global control census has passed.
func (s *Surreal) GetTypedIndexGrowth(ctx context.Context) (TypedIndexGrowth, error) {
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return TypedIndexGrowth{}, err
	}
	r, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, "SELECT "+typedControlProjection+" FROM typed_index_attempt WHERE growth_key='typed-index' LIMIT 2", nil, storeRead())
	if err != nil {
		return TypedIndexGrowth{}, typedError(ctx, err)
	}
	rows := firstDomainRows(r)
	if len(rows) == 0 {
		return TypedIndexGrowth{}, ErrNotFound
	}
	if len(rows) != 1 || validateTypedControl(TypedIndexAttempts, rows[0]) != nil {
		return TypedIndexGrowth{}, typedindex.Invalid
	}
	var a typedIndexAttempt
	_ = typedDecode(rows[0].Body, maxTypedControlBytes, &a)
	if a.Growth == nil || a.Growth.State != "held" {
		return TypedIndexGrowth{}, typedindex.Invalid
	}
	return *a.Growth, nil
}

type typedGrowthChunk struct {
	Status     string `json:"status"`
	Lease      string `json:"lease"`
	LeaseBytes uint64 `json:"lease_bytes"`
	Identity   string `json:"identity"`
	Root       string `json:"root"`
	Repository string `json:"repository"`
}

// Generic chunk schemas predate typed scalar bounds. Slices bound even malformed
// point responses; the byte length makes an overlong lease a refusal, not a
// truncated identity. Accepted observations retain every compared original byte.
const typedGrowthChunkProjection = `string::slice(status,0,33) AS status, string::slice(lease_token ?? '',0,129) AS lease, bytes::len(<bytes>(lease_token ?? '')) AS lease_bytes, string::slice(identity,0,72) AS identity, string::slice(generation,0,72) AS root, string::slice(repository,0,513) AS repository`

// TypedIndexGrowthRelease is an opaque, exact store snapshot, NOT a quiescence
// receipt. Only the trusted lifecycle/recovery controller may use it, after
// joining all writers and proving native cleanup and zero future growth. An
// ambiguous physical owner must retain its holder indefinitely.
type TypedIndexGrowthRelease struct {
	row    TypedIndexControl
	chunks []typedGrowthChunk
}

func (s *Surreal) InspectTypedIndexGrowthRelease(ctx context.Context, attempt string) (TypedIndexGrowthRelease, error) {
	var out TypedIndexGrowthRelease
	if !validSHA256(attempt) {
		return out, typedindex.Invalid
	}
	if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	r, err := storeQuery[[]TypedIndexControl](ctx, s.accounting, s.db, "SELECT "+typedControlProjection+" FROM $rid LIMIT 1", map[string]any{"rid": typedID("typed_index_attempt", attempt)}, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	rows := firstDomainRows(r)
	if len(rows) != 1 || validateTypedControl(TypedIndexAttempts, rows[0]) != nil {
		return out, typedindex.Invalid
	}
	out.row = rows[0]
	var a typedIndexAttempt
	_ = typedDecode(out.row.Body, maxTypedControlBytes, &a)
	if a.Growth == nil || a.Growth.State != "held" {
		return out, typedindex.Stale
	}
	if err = readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 1); err != nil {
		return out, err
	}
	cr, err := storeQuery[[]typedGrowthChunk](ctx, s.accounting, s.db, "SELECT "+typedGrowthChunkProjection+" FROM $chunk LIMIT 1", map[string]any{"chunk": typedID("generation_schedule_chunk", a.Growth.ChunkID)}, storeRead())
	if err != nil {
		return out, typedError(ctx, err)
	}
	out.chunks = firstDomainRows(cr)
	if len(out.chunks) > 1 {
		return out, typedindex.Invalid
	}
	if len(out.chunks) == 1 {
		c := out.chunks[0]
		if c.LeaseBytes > 128 || c.LeaseBytes != uint64(len(c.Lease)) || c.Identity != a.ChunkIdentity || c.Root != a.Root || c.Repository != out.row.Repository {
			return out, typedindex.Invalid
		}
		switch GenerationChunkStatus(c.Status) {
		case GenerationChunkPending, GenerationChunkRunning, GenerationChunkDone, GenerationChunkFailed, GenerationChunkCanceled:
		default:
			return out, typedindex.Invalid
		}
		if c.Status == string(GenerationChunkRunning) && GenerationLeaseTokenDigest(c.Lease) == a.Lease {
			return out, typedindex.Stale
		}
	}
	return out, nil
}
func (s *Surreal) ReleaseTypedIndexGrowth(ctx context.Context, selected TypedIndexGrowthRelease) error {
	if validateTypedControl(TypedIndexAttempts, selected.row) != nil {
		return typedindex.Invalid
	}
	var a typedIndexAttempt
	_ = typedDecode(selected.row.Body, maxTypedControlBytes, &a)
	if a.Growth == nil || a.Growth.State != "held" {
		return typedindex.Stale
	}
	g := *a.Growth
	g.State = "released"
	a.Growth = &g
	body, err := typedEncode(a, maxTypedControlBytes)
	if err != nil {
		return err
	}
	// JSON objects preserve precisely the bounded SQL projection for atomic CAS.
	raw, _ := json.Marshal(selected.chunks)
	var snapshot []map[string]any
	_ = json.Unmarshal(raw, &snapshot)
	if snapshot == nil {
		snapshot = []map[string]any{}
	}
	vars := map[string]any{"attempt": typedID("typed_index_attempt", selected.row.ID), "before": selected.row.Body, "repository": selected.row.Repository, "root": selected.row.Root, "key": selected.row.ID, "chunk": typedID("generation_schedule_chunk", g.ChunkID), "chunks": snapshot, "body": body}
	return s.typedWrite(ctx, `IF (SELECT body FROM $attempt WHERE repository=$repository AND request_root=$root AND control_key=$key AND growth_key='typed-index' LIMIT 1)[0].body != $before { THROW 'typed-stale'; };
 IF (SELECT `+typedGrowthChunkProjection+` FROM $chunk LIMIT 1) != $chunks { THROW 'typed-stale'; };
 UPDATE $attempt SET body=$body,growth_key=NONE RETURN NONE;`, vars, 1)
}

func validTypedChunkID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if c < 'a' || c > 'z' {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
func typedAttemptGrowthKey(a typedIndexAttempt) string {
	if a.Growth != nil && a.Growth.State == "held" {
		return typedGrowthKey
	}
	return ""
}
