package store

import (
	"context"
	"math"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

// TypedIndexCustody is a trusted controller's small reference to an exact owner
// manifest and its immutable receipt controls. It is not proof that files exist
// or are ready: the controller must Verify/Open under the filesystem leases.
// Root and attempt identity are checked against the exact fenced attempt row.
type TypedIndexCustody struct {
	PlanningDigest           string `json:"planning_digest"`
	AttemptDigest            string `json:"attempt_digest"`
	ManifestDigest           string `json:"manifest_digest"`
	Revision                 uint64 `json:"revision"`
	DirectoryDevice          uint64 `json:"directory_device"`
	DirectoryInode           uint64 `json:"directory_inode"`
	InputReceiptDigest       string `json:"input_receipt_digest,omitempty"`
	PublicationReceiptDigest string `json:"publication_receipt_digest,omitempty"`
	PublicationRequestDigest string `json:"publication_request_digest,omitempty"`
	PublicationPlanDigest    string `json:"publication_plan_digest,omitempty"`
	PublicationRootDigest    string `json:"publication_root_digest,omitempty"`
}

func (c TypedIndexCustody) valid() bool {
	if !validSHA256(c.PlanningDigest) || !validSHA256(c.AttemptDigest) || !validSHA256(c.ManifestDigest) || c.DirectoryInode == 0 || c.Revision < 1 || c.Revision > 3 {
		return false
	}
	if c.Revision == 1 && c.InputReceiptDigest != "" || c.Revision >= 2 && !validSHA256(c.InputReceiptDigest) {
		return false
	}
	if c.Revision < 3 {
		return c.PublicationReceiptDigest == "" && c.PublicationRequestDigest == "" && c.PublicationPlanDigest == "" && c.PublicationRootDigest == ""
	}
	return validSHA256(c.PublicationReceiptDigest) && validSHA256(c.PublicationRequestDigest) && validSHA256(c.PublicationPlanDigest) && validSHA256(c.PublicationRootDigest)
}
func typedCustodyMatches(c *TypedIndexCustody, p typedindex.PublicationPointer) bool {
	return c != nil && c.valid() && c.Revision == 3 && c.PublicationRequestDigest == p.Binding.RequestDigest && c.PublicationPlanDigest == p.Binding.PlanDigest && c.PublicationRootDigest == p.RootDigest
}
func typedAttemptCustodyValid(a typedIndexAttempt) bool {
	if a.Custody != nil && (!a.Custody.valid() || a.Custody.PlanningDigest != a.Root) {
		return false
	}
	switch a.Stage {
	case TypedPreflight:
		return a.Custody == nil || a.Custody.Revision <= 2
	case TypedPlanning, TypedExecution:
		return a.Custody != nil && a.Custody.Revision == 2
	case TypedValidation:
		return a.Custody != nil && a.Custody.Revision >= 2
	case TypedPublication, TypedComplete:
		return a.Custody != nil && a.Custody.Revision == 3 && a.Custody.PublicationRequestDigest == a.Request
	}
	return false
}

// SaveTypedIndexCustody CASes the exact owner revision beside the existing
// attempt. One ordinary bounded execution-authority read sequence and one
// one-row transaction; no filesystem work, scan, child, or added process lock.
// Identical replay repeats the authority/body fence without a mutation.
func (s *Surreal) SaveTypedIndexCustody(ctx context.Context, chunk GenerationChunk, expectedManifest string, next TypedIndexCustody) error {
	if !next.valid() || expectedManifest != "" && !validSHA256(expectedManifest) {
		return typedindex.Invalid
	}
	x, err := s.typedExecution(ctx, chunk, true)
	if err != nil {
		return err
	}
	if next.PlanningDigest != x.work.RootDigest || next.AttemptDigest != x.work.AttemptDigest {
		return typedindex.Stale
	}
	old := x.attempt.Custody
	if old == nil {
		if expectedManifest != "" || next.Revision != 1 || x.attempt.Stage != TypedPreflight {
			return typedindex.Stale
		}
	} else {
		if expectedManifest != old.ManifestDigest {
			return typedindex.Stale
		}
		if next == *old {
			x.vars["attempt_before"] = x.attemptRaw
			return s.typedFence(ctx, typedSourceFenceSQL+typedIntentFenceSQL+typedChunkFenceSQL+`IF (SELECT body FROM $attempt WHERE request_root=$root LIMIT 1)[0].body != $attempt_before { THROW 'typed-stale'; };`, x.vars)
		}
		if next.Revision != old.Revision+1 || next.ManifestDigest == old.ManifestDigest || next.DirectoryDevice != old.DirectoryDevice || next.DirectoryInode != old.DirectoryInode || old.Revision >= 2 && next.InputReceiptDigest != old.InputReceiptDigest {
			return typedindex.Stale
		}
	}
	if next.Revision <= 2 && x.attempt.Stage != TypedPreflight {
		return typedindex.Stale
	}
	if next.Revision == 3 && (x.attempt.Stage != TypedValidation || x.work.Admission.Request().Action != typedindex.Execute || next.PublicationRequestDigest != x.work.Admission.Digest() || next.PublicationPlanDigest != x.work.PlanDigest) {
		return typedindex.Stale
	}
	attempt := x.attempt
	attempt.Custody = &next
	return s.typedSaveAttempt(ctx, x, attempt, "", 0)
}

// The private current envelope adds a direct completed-attempt reference while
// leaving the public PublicationPointer unchanged. Old pointer-only controls
// explicitly refuse; no legacy owner identity is invented.
type typedIndexCurrent struct {
	Pointer       typedindex.PublicationPointer `json:"pointer"`
	AttemptDigest string                        `json:"attempt_digest"`
}

func decodeTypedIndexCurrent(raw string) (typedIndexCurrent, error) {
	var c typedIndexCurrent
	if typedDecode(raw, maxTypedControlBytes, &c) != nil || !validSHA256(c.AttemptDigest) || c.Pointer.Epoch < 1 || c.Pointer.Epoch > math.MaxInt64 || !validSHA256(c.Pointer.Binding.RequestDigest) || !validSHA256(c.Pointer.RootDigest) {
		return c, typedindex.Invalid
	}
	return c, nil
}

// TypedIndexCurrentCustody supplies exact identities for the controller's
// independent filesystem reopen. It grants no lease or readiness by itself.
type TypedIndexCurrentCustody struct {
	Parent         typedindex.Admission
	Admission      typedindex.Admission
	ChunkIdentity  string
	LeaseDigest    string
	Pointer        typedindex.PublicationPointer
	PlanningDigest string
	AttemptDigest  string
	Custody        TypedIndexCustody
}

// ResolveTypedIndexCurrentCustody adds one exact <=4KiB attempt read and one
// body predicate in the existing final authority fence, never an attempt scan.
// Reconstructing the planning request inverses PlannedSuccessor and verifies its
// exact digest under the same authority (one additional bounded request encode/admit).
func (s *Surreal) ResolveTypedIndexCurrentCustody(ctx context.Context, repository string) (TypedIndexCurrentCustody, error) {
	a, err := s.typedAuthority(ctx, repository)
	if err != nil {
		return TypedIndexCurrentCustody{}, err
	}
	return s.resolveTypedIndexCurrentCustody(ctx, a)
}
