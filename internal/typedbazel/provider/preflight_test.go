package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

func preflightFixture(t *testing.T, names ...string) (typedindex.Source, typedindex.Profile, typedindex.Inventory) {
	t.Helper()
	i, s, _, _, _ := fixture(t)
	files := []typedindex.BundleFile{}
	for _, name := range names {
		files = append(files, typedindex.BundleFile{Path: "source/" + name, Bytes: 1, Digest: hash([]byte("x"))})
	}
	if len(files) == 0 {
		files = append(files, typedindex.BundleFile{Path: "other", Bytes: 1, Digest: hash([]byte("x"))})
	}
	inv := provisionInventory(t, files)
	d := i.Profile.Definition()
	d.BundleDigest = inv.Digest()
	p, e := typedindex.DecodeProfile(t.Context(), provisionJSON(t, d))
	if e != nil {
		t.Fatal(e)
	}
	return s.Source, p, inv
}
func TestPreflightPosture(t *testing.T) {
	for _, tc := range []struct {
		name            string
		files           []string
		posture, policy string
	}{
		{"module", []string{"MODULE.bazel", "MODULE.bazel.lock", "go.mod"}, "module", "supported"},
		{"mixed", []string{"MODULE.bazel", "MODULE.bazel.lock", "go.mod", "WORKSPACE", "WORKSPACE.bazel"}, "mixed", "supported"},
		{"workspace", []string{"WORKSPACE", "go.mod"}, "workspace_only", "unsupported"},
		{"no-lock", []string{"MODULE.bazel", "go.mod"}, "module", "unsupported"},
		{"missing", nil, "missing", "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, p, inv := preflightFixture(t, tc.files...)
			out, e := Preflight(t.Context(), source, p, inv, []string{"//app:a"})
			if e != nil {
				t.Fatal(e)
			}
			raw, e := json.Marshal(out)
			if e != nil || len(raw) > MaxPreflightBytes || out.Posture != tc.posture || out.SourceInventoryPolicy != tc.policy || out.DeclaredProfilePolicy != "supported" || out.RuntimeCompatibility != "not_observed" || out.SourceDigest != identity(source) || out.InventoryDigest != inv.Digest() {
				t.Fatal(out, e)
			}
			if len(out.Files) != 5 {
				t.Fatal(out)
			}
		})
	}
}
func TestPreflightRefusals(t *testing.T) {
	source, p, inv := preflightFixture(t, "MODULE.bazel", "MODULE.bazel.lock", "go.mod")
	for _, which := range []string{"cancel", "roots", "source", "profile", "inventory"} {
		t.Run(which, func(t *testing.T) {
			ctx := t.Context()
			s, profile, inventory := source, p, inv
			roots := []string{"//app:a"}
			switch which {
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "roots":
				roots = []string{"./..."}
			case "source":
				s.Generation = "invalid"
			case "profile":
				profile = typedindex.Profile{}
			case "inventory":
				inventory = typedindex.Inventory{}
			}
			if _, e := Preflight(ctx, s, profile, inventory, roots); e == nil {
				t.Fatal(which)
			}
		})
	}
	d := p.Definition()
	d.Tools.Go.Version = "different"
	changed, e := typedindex.DecodeProfile(t.Context(), provisionJSON(t, d))
	if e != nil {
		t.Fatal(e)
	}
	out, e := Preflight(t.Context(), source, changed, inv, []string{"//app:a"})
	if e != nil || out.DeclaredProfilePolicy != "unsupported" || out.RuntimeCompatibility != "not_observed" {
		t.Fatal(out, e)
	}
	files := inv.Files()
	files[0].Bytes = planner.MaxFileBytes + 1
	larger := provisionInventory(t, files)
	d = p.Definition()
	d.BundleDigest = larger.Digest()
	changed, e = typedindex.DecodeProfile(t.Context(), provisionJSON(t, d))
	if e != nil {
		t.Fatal(e)
	}
	out, e = Preflight(t.Context(), source, changed, larger, []string{"//app:a"})
	if e != nil || out.SourceInventoryPolicy != "unsupported" {
		t.Fatal(out, e)
	}
	if _, e = Preflight(t.Context(), source, p, larger, []string{"//app:a"}); !errors.Is(e, typedindex.Invalid) {
		t.Fatal("profile/inventory mismatch", e)
	}
}

func TestPreflightRootPermutation(t *testing.T) {
	source, p, inv := preflightFixture(t, "MODULE.bazel", "MODULE.bazel.lock", "go.mod")
	roots := []string{"//z:z", "//a:a"}
	first, e := Preflight(t.Context(), source, p, inv, roots)
	if e != nil {
		t.Fatal(e)
	}
	second, e := Preflight(t.Context(), source, p, inv, []string{"//a:a", "//z:z"})
	if e != nil || first != second || roots[0] != "//z:z" {
		t.Fatal("root canonicalization mutated input or changed report", first, second, e)
	}
}
