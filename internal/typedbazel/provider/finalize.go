package provider

import (
	"context"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// Finalize consumes only the private decoded protocol. The controller must
// separately prove actual sandbox success/cleanup and recheck durable authority
// before installation. Zero retained coverage is explicitly nonpublishable.
func (r DecodedResult) Finalize(ctx context.Context) (typedindex.Bundle, error) {
	if ctx == nil || !r.valid || r.result.Failure != nil || r.invocation.Phase != typedindex.Execute {
		return typedindex.Bundle{}, typedindex.Invalid
	}
	if err := ctx.Err(); err != nil {
		return typedindex.Bundle{}, err
	}
	// DecodeResult scanned every raw record before any omission and retained the
	// adapter output privately. No caller-visible byte accessor can change it.
	var index scip.Index
	if proto.Unmarshal(r.adapted, &index) != nil {
		return typedindex.Bundle{}, typedindex.Invalid
	}
	paths := make(map[string]DocumentOutcome, len(r.result.Documents))
	states := make(map[typedindex.PackageUnitID]typedindex.UnitState, len(r.result.Units))
	for _, u := range r.result.Units {
		states[u.Unit] = u.State
	}
	for _, d := range r.result.Documents {
		paths[d.RawPath] = d
	}
	retained := make([]*scip.Document, 0, len(index.Documents))
	for _, d := range index.Documents {
		if err := ctx.Err(); err != nil {
			return typedindex.Bundle{}, err
		}
		mapped, ok := paths[d.RelativePath]
		if !ok {
			return typedindex.Bundle{}, typedindex.Invalid
		}
		if mapped.State != "included" || states[mapped.Unit] != typedindex.UnitComplete {
			continue
		}
		d.RelativePath = mapped.Path
		retained = append(retained, d)
	}
	if len(retained) == 0 {
		return typedindex.Bundle{}, typedindex.Unsupported
	}
	index.Documents = retained
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&index)
	if err != nil {
		return typedindex.Bundle{}, typedindex.Invalid
	}
	// BuildBundle supplies canonicalization and all plan/route/source binding;
	// neither the raw result nor the audit receipt may supply routing facts.
	return typedindex.BuildBundleWithSCIPGoReceipt(ctx, r.invocation.Execution, r.plan, r.result.Units, []typedindex.MemberInput{{Name: "main", SCIP: raw}}, r.result.Generated, r.receipt)
}
