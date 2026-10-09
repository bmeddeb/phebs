package packrelease

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// maxSelectionEntries bounds how many directory entries one admitted selection
// may scan, keeping the startup read bounded regardless of the operator's
// directory contents. It is a defensive ceiling, not a capacity target.
const maxSelectionEntries = 1024

// Selection is the verified set of released PackRelease records admitted at one
// startup boundary. It is the only path by which a fixed in-tree pack enters
// the ordinary/released admission set: a record is admitted solely because it
// verified, governs its pack and carries the released lifecycle status, never
// because a provisional extraction switch was toggled. The zero value admits
// nothing.
type Selection struct {
	released  map[string]*PackRelease
	withdrawn []Withdrawal
}

// Withdrawal records a pack the selection directory named that is not admitted.
// It is operator-facing startup evidence: it carries a pack identifier from an
// authenticated record and a bounded cause, never a record's contents,
// signature, artifacts or any repository data.
type Withdrawal struct {
	// PackID is the canonical pack identifier the governing record carries.
	PackID string
	// Cause is CauseRevoked when operator configuration withdraws the governing
	// record, that record's own lifecycle status when the status admits no
	// ordinary load, or "expired" / "future_approval" when a released governing
	// record fails its clock check. All are bounded: a status has already
	// matched the closed lifecycle set before a record can govern.
	Cause string
}

// CauseRevoked is the withdrawal cause for a governing record whose release_id
// the operator listed as withdrawn.
const CauseRevoked = "revoked"

// Empty reports whether the selection admits no released pack. An empty
// selection adds no pack work.
func (s Selection) Empty() bool { return len(s.released) == 0 }

// Count returns the number of admitted released packs.
func (s Selection) Count() int { return len(s.released) }

// PackIDs returns the admitted released pack identifiers in sorted order, for
// bounded diagnostics and deterministic recipe resolution.
func (s Selection) PackIDs() []string {
	ids := make([]string, 0, len(s.released))
	for id := range s.released {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Withdrawn returns the packs the directory named that are not admitted, in
// sorted pack order. A suspension or revocation therefore has an observable
// startup effect instead of only an absent pack, and the result is bounded by
// the governing-record count.
func (s Selection) Withdrawn() []Withdrawal {
	out := append([]Withdrawal(nil), s.withdrawn...)
	sort.Slice(out, func(i, j int) bool { return out[i].PackID < out[j].PackID })
	return out
}

// Released returns the admitted released record for packID and whether it is
// present. A structurally valid non-released record is never returned here.
func (s Selection) Released(packID string) (*PackRelease, bool) {
	release, ok := s.released[packID]
	return release, ok
}

// LoadSelection reads dir once, verifies every signed PackRelease record it
// holds against opts, and returns the admitted released selection. It is fail
// closed and is called only at the admitted startup boundary, never on a
// request, sync, or per-query path:
//
//   - An empty dir admits nothing and performs no read, so an absent production
//     selection adds no pack work.
//   - A configured directory that cannot be read, a *.json entry that is not a
//     regular file, or any record that fails authentication or structure
//     refuses the whole selection rather than being skipped. The clock is not
//     part of that check: a superseded record whose validation later expires
//     changes nothing.
//   - For each pack, the record with the highest release_version governs; two
//     records sharing that version refuse the selection. A governing record
//     that is not released (for example a later signed suspension), whose
//     release_id the operator revoked, or whose validation has expired or whose
//     approval lies in the future, withdraws the pack, so an older released
//     record never outlives its suspension or supersession. Withdrawal is the
//     whole effect: the pack is not admitted and no older record replaces it,
//     because a rollback is a newly signed record rather than a consequence of
//     revoking a later one.
//   - A governing released record is admitted only through the VerifyForLoad
//     bindings: the caller must supply the artifact resolver, the running
//     implementation identity and the present artifact root, and the record
//     must match them.
func LoadSelection(ctx context.Context, dir string, opts Options) (Selection, error) {
	selection := Selection{released: map[string]*PackRelease{}}
	if dir == "" {
		return selection, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return selection, fmt.Errorf("read release selection directory %q: %w", dir, err)
	}
	if len(entries) > maxSelectionEntries {
		return selection, fmt.Errorf(
			"release selection directory %q holds %d entries, exceeds the %d bound",
			dir, len(entries), maxSelectionEntries,
		)
	}

	// Authenticity and structure are judged for every record, and must stay
	// that way: an unsigned record claiming a higher release_version could
	// otherwise suppress the record that legitimately governs its pack. The
	// load bindings and the revocation list belong to the governing record
	// only, so a suspension written for an older implementation still
	// withdraws its pack, and revoking a superseded record withdraws nothing
	// instead of refusing the whole selection.
	authOpts := opts
	authOpts.Implementation, authOpts.ReferencedArtifactsRootDigest, authOpts.Resolver = nil, "", nil
	authOpts.Revoked = nil

	type candidate struct {
		name    string
		release *PackRelease
	}
	governing := map[string]candidate{}
	tied := map[string]string{}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		// ponytail: the ReadDir type is checked, not re-checked after open;
		// an operator racing a FIFO into place is outside this boundary.
		if !entry.Type().IsRegular() {
			return selection, fmt.Errorf("release record %q is not a regular file", entry.Name())
		}
		raw, err := readBounded(filepath.Join(dir, entry.Name()))
		if err != nil {
			return selection, fmt.Errorf("read release record %q: %w", entry.Name(), err)
		}
		release, err := verifyRecord(raw, authOpts)
		if err != nil {
			return selection, fmt.Errorf("release record %q: %w", entry.Name(), err)
		}
		current, seen := governing[release.PackID]
		switch order := compareVersion(release.ReleaseVersion, current.release); {
		case !seen || order > 0:
			governing[release.PackID] = candidate{entry.Name(), release}
			delete(tied, release.PackID)
		case order == 0:
			tied[release.PackID] = entry.Name()
		}
	}

	packIDs := make([]string, 0, len(governing))
	for packID := range governing {
		packIDs = append(packIDs, packID)
	}
	sort.Strings(packIDs)
	for _, packID := range packIDs {
		winner := governing[packID]
		if other, ok := tied[packID]; ok {
			return selection, fmt.Errorf("release records %q and %q both name pack %q at release_version %s",
				winner.name, other, packID, winner.release.ReleaseVersion)
		}
		// Revocation is judged before status, in the same precedence Verify
		// gives it, so an operator's configured withdrawal is the cause they
		// see even when the governing record separately carries a suspension.
		if _, revoked := opts.Revoked[winner.release.ReleaseID]; revoked {
			selection.withdrawn = append(selection.withdrawn, Withdrawal{
				PackID: packID, Cause: CauseRevoked,
			})
			continue
		}
		if winner.release.DerivedStatus != StatusReleased {
			selection.withdrawn = append(selection.withdrawn, Withdrawal{
				PackID: packID, Cause: winner.release.DerivedStatus,
			})
			continue
		}
		// Validation expiry is automatic suspension: an expired (or not yet
		// approved) governing record withdraws its pack like a signed
		// suspension. Only the governing record is judged against the clock,
		// so a superseded record expiring later changes nothing.
		if err := validateReleasedTime(winner.release, opts.now()); err != nil {
			reason, _ := ReasonOf(err)
			if reason != ReasonExpired && reason != ReasonFutureApproval {
				return selection, fmt.Errorf("release record %q: %w", winner.name, err)
			}
			selection.withdrawn = append(selection.withdrawn, Withdrawal{PackID: packID, Cause: string(reason)})
			continue
		}
		if err := requireLoadBindings(opts); err != nil {
			return selection, fmt.Errorf("release record %q: %w", winner.name, err)
		}
		if err := bindImplementation(winner.release, opts); err != nil {
			return selection, fmt.Errorf("release record %q: %w", winner.name, err)
		}
		if err := resolveReferences(ctx, winner.release, opts.Resolver); err != nil {
			return selection, fmt.Errorf("release record %q: %w", winner.name, err)
		}
		selection.released[packID] = winner.release
	}
	return selection, nil
}

// readBounded reads at most MaxReleaseBytes+1 bytes, so Verify refuses an
// oversized record without the whole file entering memory.
func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, MaxReleaseBytes+1))
}

// compareVersion orders a verified MAJOR.MINOR.PATCH against the current
// governing record's version; a nil current orders below everything.
func compareVersion(version string, current *PackRelease) int {
	if current == nil {
		return 1
	}
	left, right := strings.Split(version, "."), strings.Split(current.ReleaseVersion, ".")
	for i := range left {
		// Verify already matched both against the semver grammar.
		a, _ := strconv.Atoi(left[i])
		b, _ := strconv.Atoi(right[i])
		if a != b {
			if a > b {
				return 1
			}
			return -1
		}
	}
	return 0
}
