package provider

import (
	"bytes"
	"context"
	"path"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func decodeInputResult(ctx context.Context, i Invocation, selection, raw []byte) (DecodedResult, error) {
	if err := i.validate(ctx); err != nil {
		return DecodedResult{}, err
	}
	bound := int(typedsandbox.OutputBytes) - int(i.Allowance.WorkerBytesUsed)
	r, err := decode[inputResult](raw, bound)
	if err != nil {
		return DecodedResult{}, err
	}
	if r.Schema != inputResultSchema || r.RequestDigest != inputRequest(i) || r.Phase != i.Phase {
		return DecodedResult{}, typedindex.Stale
	}
	b, err := bindInputSelection(ctx, i, selection)
	if err != nil {
		return DecodedResult{}, err
	}
	if !bytes.Equal(r.Plan, b.plan.Bytes()) || len(r.Members) > typedindex.MaxSCIPMembers || len(r.Packages) > typedindex.MaxBundleUnits {
		return DecodedResult{}, typedindex.Stale
	}
	if b.selection.Module != nil {
		if err = verifyInputPackages(b, r.Packages); err != nil {
			return DecodedResult{}, err
		}
	} else if len(r.Packages) != 0 {
		return DecodedResult{}, typedindex.Invalid
	}
	out := DecodedResult{invocation: i, plan: b.plan, valid: true}
	if i.Phase == typedindex.Plan {
		if len(r.Members) != 0 {
			return DecodedResult{}, typedindex.Invalid
		}
		return out, nil
	}
	bundle, err := finalizeInput(ctx, i, b, r.Members)
	if err != nil {
		return DecodedResult{}, err
	}
	out.inputBundle = &bundle
	return out, nil
}

func finalizeInput(ctx context.Context, i Invocation, b inputBinding, members []typedindex.MemberInput) (typedindex.Bundle, error) {
	if i.Phase != typedindex.Execute || len(members) == 0 || len(members) > typedindex.MaxSCIPMembers {
		return typedindex.Bundle{}, typedindex.Invalid
	}
	var expected []string
	if b.selection.Module != nil {
		for n := range b.selection.Module.Roots {
			if len(inputPatterns(b, n)) != 0 {
				expected = append(expected, inputSlot(n))
			}
		}
	} else {
		for n := range b.selection.Import.Artifacts {
			expected = append(expected, inputSlot(n))
		}
	}
	if len(expected) != len(members) {
		return typedindex.Bundle{}, typedindex.Invalid
	}
	raws := make([][]byte, len(members))
	var total int
	for n, member := range members {
		if member.Name != expected[n] || len(member.SCIP) == 0 || len(member.SCIP) > typedindex.MaxSCIPMemberBytes || len(member.SCIP) > typedindex.MaxSCIPAggregateBytes-total {
			return typedindex.Bundle{}, typedindex.Invalid
		}
		total += len(member.SCIP)
		if b.selection.Import != nil {
			artifact := b.selection.Import.Artifacts[n]
			if int64(len(member.SCIP)) != artifact.Bytes || hash(member.SCIP) != artifact.Digest {
				return typedindex.Bundle{}, typedindex.Stale
			}
		}
		raws[n] = member.SCIP
	}
	var receipt typedindex.SCIPGoAdapterReceipt
	var err error
	if b.selection.Module != nil {
		// The module recipe verifies the actual pinned executable. Import's
		// declared producer is not that proof and cannot enable this adapter.
		raws, receipt, err = typedindex.AdaptSCIPGoBlanks(ctx, i.Profile, raws)
		if err != nil {
			return typedindex.Bundle{}, err
		}
	}
	normalized := make([]typedindex.MemberInput, len(members))
	indexes := make([]*scip.Index, len(members))
	for n, raw := range raws {
		// CanonicalSCIP enforces wire/message/record bounds before protobuf
		// allocation, even for raw operator-supplied artifacts.
		canonical, e := typedindex.CanonicalSCIP(ctx, raw)
		if e != nil {
			return typedindex.Bundle{}, e
		}
		var index scip.Index
		if proto.Unmarshal(canonical, &index) != nil {
			return typedindex.Bundle{}, typedindex.Invalid
		}
		m := index.Metadata
		if m.ToolInfo.Name != "scip-go" || m.ToolInfo.Version != "0.2.7" || m.TextDocumentEncoding != scip.TextEncoding_UTF8 {
			return typedindex.Bundle{}, typedindex.Unsupported
		}
		prefix := "."
		if b.selection.Module != nil {
			root := -1
			for j := range b.selection.Module.Roots {
				if inputSlot(j) == members[n].Name {
					root = j
				}
			}
			if root < 0 {
				return typedindex.Bundle{}, typedindex.Invalid
			}
			prefix = b.selection.Module.Roots[root].Path
			if m.ProjectRoot != "file://"+path.Join("/scratch/workspace", prefix) || !slices.Equal(m.ToolInfo.Arguments, inputSCIPArguments(b, root)) {
				return typedindex.Bundle{}, typedindex.Stale
			}
		} else {
			// Normalize only an independently sealed root. Empty mappings grant
			// no normalization; they retain the artifact's validated file URI.
			mapped := false
			for _, mapping := range b.selection.Import.Roots {
				if m.ProjectRoot == "file:///"+mapping.Input {
					prefix, mapped = mapping.Repo, true
				}
			}
			if len(b.selection.Import.Roots) != 0 && !mapped {
				return typedindex.Bundle{}, typedindex.Stale
			}
		}
		for _, document := range index.Documents {
			if document.Language != "go" || strings.HasSuffix(document.RelativePath, "_test.go") {
				return typedindex.Bundle{}, typedindex.Unsupported
			}
			// Declared producer identity is not execution proof. Independently
			// reject source definitions carrying a different producer or commit.
			for _, information := range document.Symbols {
				if !inputDefinitionMatches(information.Symbol, b.selection.Source.Commit) {
					return typedindex.Bundle{}, typedindex.Stale
				}
			}
			for _, occurrence := range document.Occurrences {
				if occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 && !inputDefinitionMatches(occurrence.Symbol, b.selection.Source.Commit) {
					return typedindex.Bundle{}, typedindex.Stale
				}
			}
			document.RelativePath = path.Join(prefix, document.RelativePath)
		}
		if b.selection.Module != nil || len(b.selection.Import.Roots) != 0 {
			// Raw bytes remain bound in the input/result. The bundle has a
			// single repository coordinate system across its mapped members.
			m.ProjectRoot = "file:///phebs"
			m.ToolInfo.Arguments = []string{}
		}
		indexes[n] = &index
	}
	if b.selection.Module != nil {
		if err := normalizeInputReferences(b, indexes); err != nil {
			return typedindex.Bundle{}, err
		}
	}
	for n, index := range indexes {
		normalized[n].Name = members[n].Name
		normalized[n].SCIP, err = proto.MarshalOptions{Deterministic: true}.Marshal(index)
		if err != nil {
			return typedindex.Bundle{}, typedindex.Invalid
		}
	}
	outcomes := make([]typedindex.UnitOutcome, 0, len(b.selection.Plan.Units))
	for _, unit := range b.selection.Plan.Units {
		outcomes = append(outcomes, typedindex.UnitOutcome{Unit: unit.ID, State: typedindex.UnitComplete})
	}
	audit := typedindex.InputReceipt{Schema: typedindex.InputReceiptSchema, Provider: i.Profile.Provider(), SelectionDigest: b.controlDigest, Members: make([]typedindex.ContentReference, len(members))}
	for n, member := range members {
		audit.Members[n] = typedindex.ContentReference{Name: member.Name, Digest: hash(member.SCIP), Bytes: len(member.SCIP)}
	}
	if b.selection.Module != nil {
		audit.ScopeDigest = b.selection.Module.Digest()
		return typedindex.BuildInputBundle(ctx, i.Execution, b.plan, outcomes, normalized, audit, &receipt)
	}
	audit.ScopeDigest = b.selection.Import.Digest()
	producer := i.Profile.Definition().Tools.Indexer
	audit.DeclaredProducer, audit.ProvenanceDigest = &producer, hash([]byte(b.selection.Import.Provenance))
	return typedindex.BuildInputBundle(ctx, i.Execution, b.plan, outcomes, normalized, audit, nil)
}

func inputDefinitionMatches(raw, commit string) bool {
	if scip.IsLocalSymbol(raw) {
		return true
	}
	symbol, err := scip.ParseSymbol(raw)
	return err == nil && symbol.Scheme == "scip-go" && symbol.Package != nil && symbol.Package.Manager == "gomod" && symbol.Package.Version == commit
}

// Pinned scip-go uses "." for sibling workspace module versions. Only map
// an admitted module's reference when the exact commit-bound definition exists.
// Imports never enter this adapter; their declared producer is not tool proof.
func normalizeInputReferences(b inputBinding, indexes []*scip.Index) error {
	definitions := map[string]bool{}
	for _, index := range indexes {
		for _, document := range index.Documents {
			for _, occurrence := range document.Occurrences {
				if occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 && !scip.IsLocalSymbol(occurrence.Symbol) {
					definitions[occurrence.Symbol] = true
					if len(definitions) > typedindex.MaxBundleSymbols {
						return typedindex.Capacity
					}
				}
			}
		}
	}
	rewrite := func(raw *string) error {
		if *raw == "" || scip.IsLocalSymbol(*raw) {
			return nil
		}
		symbol, err := scip.ParseSymbol(*raw)
		if err != nil {
			return typedindex.Invalid
		}
		if symbol.Scheme != "scip-go" || symbol.Package == nil || symbol.Package.Manager != "gomod" || symbol.Package.Version != "" {
			return nil
		}
		for _, root := range b.selection.Module.Roots {
			if symbol.Package.Name != root.Module {
				continue
			}
			symbol.Package.Version = b.selection.Source.Commit
			canonical := scip.VerboseSymbolFormatter.FormatSymbol(symbol)
			if !definitions[canonical] {
				return typedindex.Stale
			}
			*raw = canonical
			break
		}
		return nil
	}
	information := func(info *scip.SymbolInformation) error {
		if err := rewrite(&info.Symbol); err != nil {
			return err
		}
		if err := rewrite(&info.EnclosingSymbol); err != nil {
			return err
		}
		for _, relation := range info.Relationships {
			if err := rewrite(&relation.Symbol); err != nil {
				return err
			}
		}
		if info.SignatureDocumentation != nil {
			for _, occurrence := range info.SignatureDocumentation.Occurrences {
				if err := rewrite(&occurrence.Symbol); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, index := range indexes {
		for _, document := range index.Documents {
			for _, occurrence := range document.Occurrences {
				if err := rewrite(&occurrence.Symbol); err != nil {
					return err
				}
			}
			for _, info := range document.Symbols {
				if err := information(info); err != nil {
					return err
				}
			}
		}
		for _, info := range index.ExternalSymbols {
			if err := information(info); err != nil {
				return err
			}
		}
	}
	return nil
}
