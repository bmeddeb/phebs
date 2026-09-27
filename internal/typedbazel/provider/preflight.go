package provider

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedbazel/planner"
	"github.com/bmeddeb/phebs/internal/typedindex"
)

const MaxPreflightBytes = 4096

type PostureFile struct {
	Name       string `json:"name"`
	Present    bool   `json:"present"`
	Bytes      int64  `json:"bytes"`
	Digest     string `json:"digest"`
	Executable bool   `json:"executable"`
}

// PreflightReport contains authenticated inventory facts and profile policy,
// never proof that a tool executed or that source declarations are compatible.
// SourceInventoryPolicy checks presence/size only, never module syntax.
// DeclaredProfilePolicy is the initial pinned invocation predicate, not the
// later complete physical tool/inventory proof.
// RuntimeCompatibility is deliberately fixed to not_observed by this operation.
type PreflightReport struct {
	Schema                string         `json:"schema"`
	SourceDigest          string         `json:"source_digest"`
	ProfileDigest         string         `json:"profile_digest"`
	InventoryDigest       string         `json:"inventory_digest"`
	RootsDigest           string         `json:"roots_digest"`
	RootCount             int            `json:"root_count"`
	Posture               string         `json:"posture"`
	SourceInventoryPolicy string         `json:"source_inventory_policy"`
	DeclaredProfilePolicy string         `json:"declared_profile_policy"`
	RuntimeCompatibility  string         `json:"runtime_compatibility"`
	Files                 [5]PostureFile `json:"files"`
}

// Preflight reads no filesystem and starts no child. The caller supplies the
// exact authenticated source inventory and installed profile; a decoder alone
// does not prove physical custody. Missing/unsupported posture is reported,
// whereas invalid identities/roots or a mismatched inventory refuse.
func Preflight(ctx context.Context, source typedindex.Source, profile typedindex.Profile, inv typedindex.Inventory, roots []string) (PreflightReport, error) {
	var out PreflightReport
	if ctx == nil || source.Validate() != nil || profile.Digest() == "" || inv.Digest() == "" || profile.Definition().BundleDigest != inv.Digest() {
		return out, typedindex.Invalid
	}
	if e := ctx.Err(); e != nil {
		return out, e
	}
	if _, e := planner.NativeCommands(roots); e != nil {
		return out, typedindex.Invalid
	}
	roots = slices.Clone(roots)
	slices.Sort(roots)
	out = PreflightReport{Schema: "phebs-bazel-preflight-v1", SourceDigest: identity(source), ProfileDigest: profile.Digest(), InventoryDigest: inv.Digest(), RootsDigest: identity(roots), RootCount: len(roots), Posture: "missing", SourceInventoryPolicy: "supported", DeclaredProfilePolicy: "supported", RuntimeCompatibility: "not_observed"}
	if !pinnedProfile(profile) {
		out.DeclaredProfilePolicy = "unsupported"
	}
	names := [5]string{"MODULE.bazel", "MODULE.bazel.lock", "WORKSPACE", "WORKSPACE.bazel", "go.mod"}
	for j, name := range names {
		out.Files[j].Name = name
	}
	var total int64
	for _, f := range inv.Files() {
		if e := ctx.Err(); e != nil {
			return PreflightReport{}, e
		}
		if strings.HasPrefix(f.Path, "source/") {
			if f.Bytes > planner.MaxFileBytes || f.Bytes > planner.MaxProtoBytes-total {
				out.SourceInventoryPolicy = "unsupported"
			} else {
				total += f.Bytes
			}
		}
		for j, name := range names {
			if f.Path == "source/"+name {
				out.Files[j] = PostureFile{name, true, f.Bytes, f.Digest, f.Executable}
			}
		}
	}
	module := out.Files[0].Present
	workspace := out.Files[2].Present || out.Files[3].Present
	switch {
	case module && workspace:
		out.Posture = "mixed"
	case module:
		out.Posture = "module"
	case workspace:
		out.Posture = "workspace_only"
	}
	if !module || !out.Files[1].Present || !out.Files[4].Present {
		out.SourceInventoryPolicy = "unsupported"
	}
	raw, e := json.Marshal(out)
	if e != nil || len(raw) > MaxPreflightBytes {
		return PreflightReport{}, typedindex.Capacity
	}
	return out, ctx.Err()
}
