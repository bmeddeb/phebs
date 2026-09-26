package launcher

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"io"
	"maps"
	"slices"
	"sort"
)

// Reconcile accepts only exact roots, packages, source/compiled documents and
// direct imports. Exit zero, NotHandled, partial success and invented SDK
// packages never substitute for equality with the pre-execution authority.
func Reconcile(p Prepared, data []byte) error {
	if len(p.digest) != 64 || len(p.packages) == 0 || len(data) == 0 || len(data) > MaxResponseBytes {
		return errors.New("unprepared or empty/oversized driver response")
	}
	if err := uniqueJSON(data); err != nil {
		return err
	}
	var r response
	if err := planner.ValidateJSONFields(data, &r); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return err
	}
	// rules_go v0.63.0's constructor leaves all three optional size/version
	// fields unset. The launcher supplies the pinned SDK mode separately.
	if r.NotHandled || r.Compiler != "" || r.Arch != "" || r.GoVersion != 0 || len(r.Packages) != len(p.packages) {
		return errors.New("driver fallback/profile/package count mismatch")
	}
	if !sameSet(r.Roots, p.roots) {
		return errors.New("driver root set mismatch")
	}
	seen := map[string]bool{}
	for _, pkg := range r.Packages {
		want, ok := p.packages[pkg.ID]
		if !ok || seen[pkg.ID] || len(pkg.Errors) != 0 || len(pkg.OtherFiles) != 0 || pkg.Name != want.Name || pkg.PkgPath != want.PkgPath || pkg.ExportFile != want.ExportFile {
			return errors.New("driver package identity/error mismatch")
		}
		seen[pkg.ID] = true
		if !sameSet(pkg.GoFiles, want.GoFiles) || !sameSet(pkg.CompiledGoFiles, want.CompiledGoFiles) || !maps.Equal(pkg.Imports, want.Imports) {
			return errors.New("driver document or import set mismatch")
		}
	}
	return nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] || (i > 0 && (a[i] == a[i-1] || b[i] == b[i-1])) {
			return false
		}
	}
	return true
}

// Reject duplicate JSON members before encoding/json can overwrite them.
func uniqueJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return errors.New("driver JSON depth bound")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return errors.New("duplicate driver JSON member")
				}
				seen[key] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid driver JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing driver response data")
	}
	return nil
}
