package packrelease

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// maxSelectionEntries bounds how many directory entries one admitted selection
// may scan, keeping the startup read bounded regardless of the operator's
// directory contents. It is a defensive ceiling, not a capacity target.
const maxSelectionEntries = 1024

// Selection is the verified set of released PackRelease records admitted at one
// startup boundary. It is the only path by which a fixed in-tree pack enters
// the ordinary/released admission set: a record is admitted solely because it
// verified and carries the released lifecycle status, never because a
// provisional extraction switch was toggled. The zero value admits nothing.
type Selection struct {
	released map[string]*PackRelease
}

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
//   - A configured directory that cannot be read is refused rather than
//     silently treated as empty, so a misconfigured operator intent is loud.
//   - Any record that fails Verify refuses the whole selection; a verified
//     record whose derived_status is not released is structurally valid but
//     never admits an ordinary claim load, so it is skipped, not refused.
//   - Two admitted records naming the same pack identifier refuse the
//     selection: the release record, not a self-asserted field, is the sole
//     authority, and a duplicate is an unresolved release inconsistency.
//
// Reference resolution is governed by opts.Resolver exactly as in Verify: a nil
// resolver skips artifact-byte resolution but never skips digest-format or
// signature validation.
func LoadSelection(dir string, opts Options) (Selection, error) {
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
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return selection, fmt.Errorf("read release record %q: %w", name, err)
		}
		release, err := Verify(raw, opts)
		if err != nil {
			return selection, fmt.Errorf("release record %q: %w", name, err)
		}
		if release.DerivedStatus != StatusReleased {
			continue
		}
		if _, duplicate := selection.released[release.PackID]; duplicate {
			return selection, fmt.Errorf(
				"release record %q: duplicate released pack identifier %q", name, release.PackID,
			)
		}
		selection.released[release.PackID] = release
	}
	return selection, nil
}
