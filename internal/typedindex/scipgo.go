package typedindex

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

const SCIPGoBlankAdapter = "phebs-scip-go-blank-metadata-v1"

// SCIPGoIndexerDigest is the retained T45.1b scip-go 0.2.7 linux/arm64 image.
// SCIPGoIndexerDigestAmd64 is linux/amd64 v1, rebuilt from the same tag, x/tools
// graph and go1.25.0 (spike/t457/native_tool_pins_amd64.json).
const SCIPGoIndexerDigest = "sha256:7d162fc544b6669fc8470c59480b754ea24339dfb6f27791e2f66346146ba765"
const SCIPGoIndexerDigestAmd64 = "sha256:31bf2f3bbbcb25efd4bba6964e08971a9c9c2fba745db4345c0d438ef28b93c4"

// SCIPGoIndexer is the pinned scip-go identity for an admitted architecture.
func SCIPGoIndexer(arch string) Tool {
	switch arch {
	case "arm64":
		return Tool{Version: "0.2.7", Digest: SCIPGoIndexerDigest}
	case "amd64":
		return Tool{Version: "0.2.7", Digest: SCIPGoIndexerDigestAmd64}
	}
	return Tool{}
}

type SCIPGoBlankMapping struct {
	Document       string `json:"document"`
	Original       string `json:"original"`
	Local          string `json:"local"`
	MetadataDigest string `json:"metadata_digest"`
}
type SCIPGoMemberReceipt struct {
	RawDigest    string               `json:"raw_digest"`
	OutputDigest string               `json:"output_digest"`
	Mappings     []SCIPGoBlankMapping `json:"mappings"`
}
type SCIPGoAdapterReceipt struct {
	Schema  string                `json:"schema"`
	Members []SCIPGoMemberReceipt `json:"members"`
}

// AdaptSCIPGoBlanks preserves every record and caller byte while localizing only
// unreferenced global blank metadata from the exact pinned producer. It scans
// ALL members together: invoking it independently per member is insufficient.
// The returned receipt must share the existing attempt's 2MiB budget, not gain a
// separate allowance. It does not admit document paths, source, ranges or a
// publication; plan-derived path mapping and CanonicalSCIP remain mandatory.
// Raw producer bytes and the adapted artifact have distinct recorded identities.
// No filesystem, locks, children or persistent cache are used.
func AdaptSCIPGoBlanks(ctx context.Context, profile Profile, members [][]byte) ([][]byte, SCIPGoAdapterReceipt, error) {
	var empty SCIPGoAdapterReceipt
	if ctx == nil {
		return nil, empty, Invalid
	}
	if err := ctx.Err(); err != nil {
		return nil, empty, err
	}
	if profile.digest == "" || profile.definition.Tools.Indexer != SCIPGoIndexer(profile.definition.Config.GOARCH) {
		return nil, empty, Unsupported
	}
	if len(members) == 0 || len(members) > MaxSCIPMembers {
		return nil, empty, Capacity
	}
	indexes := make([]*scip.Index, len(members))
	total := 0
	for i, raw := range members {
		if len(raw) == 0 {
			return nil, empty, Invalid
		}
		if len(raw) > MaxSCIPMemberBytes || len(raw) > MaxSCIPAggregateBytes-total {
			return nil, empty, Capacity
		}
		total += len(raw)
		counts := scipCounts{}
		if err := counts.wire(ctx, raw, (&scip.Index{}).ProtoReflect().Descriptor(), 0); err != nil {
			return nil, empty, err
		}
		index := new(scip.Index)
		if proto.Unmarshal(raw, index) != nil {
			return nil, empty, Invalid
		}
		if index.Metadata == nil || index.Metadata.ToolInfo == nil || index.Metadata.ToolInfo.Name != "scip-go" || index.Metadata.ToolInfo.Version != "0.2.7" {
			return nil, empty, Unsupported
		}
		indexes[i] = index
	}
	type localKey struct{ document, symbol string }
	existing := make(map[localKey]bool)
	blanks := make(map[string]bool)
	// First discover every blank key, including records in later members. Existing
	// locals include references without definitions so adaptation cannot capture one.
	for _, index := range indexes {
		for _, doc := range index.Documents {
			for _, symbol := range doc.Symbols {
				blank, err := scipGoBlank(symbol.Symbol)
				if err != nil {
					return nil, empty, err
				}
				if blank {
					blanks[symbol.Symbol] = true
				}
				if scip.IsLocalSymbol(symbol.Symbol) {
					existing[localKey{doc.RelativePath, symbol.Symbol}] = true
				}
			}
		}
		for _, symbol := range index.ExternalSymbols {
			blank, err := scipGoBlank(symbol.Symbol)
			if err != nil {
				return nil, empty, err
			}
			if blank {
				return nil, empty, Unsupported
			}
		}
	}
	check := func(document, symbol string) error {
		if symbol != "" {
			blank, err := scipGoBlank(symbol)
			if err != nil {
				return err
			}
			if blank {
				return Unsupported
			}
		}
		if scip.IsLocalSymbol(symbol) {
			existing[localKey{document, symbol}] = true
		}
		return nil
	}
	for _, index := range indexes {
		if err := ctx.Err(); err != nil {
			return nil, empty, err
		}
		for _, doc := range index.Documents {
			for _, occurrence := range doc.Occurrences {
				if err := check(doc.RelativePath, occurrence.Symbol); err != nil {
					return nil, empty, err
				}
			}
			for _, symbol := range doc.Symbols {
				if err := scipGoReferences(doc.RelativePath, symbol, check); err != nil {
					return nil, empty, err
				}
			}
		}
		for _, symbol := range index.ExternalSymbols {
			if err := scipGoReferences("", symbol, check); err != nil {
				return nil, empty, err
			}
		}
	}
	receipt := SCIPGoAdapterReceipt{Schema: SCIPGoBlankAdapter, Members: make([]SCIPGoMemberReceipt, len(members))}
	output := make([][]byte, len(members))
	// Leave space for the fixed receipt envelope before allocating mapping rows.
	mappingBytes, totalOutput := 0, 0
	generated := make(map[localKey]string)
	for i, index := range indexes {
		member := SCIPGoMemberReceipt{RawDigest: hash(members[i]), Mappings: []SCIPGoBlankMapping{}}
		for _, doc := range index.Documents {
			for _, symbol := range doc.Symbols {
				if !blanks[symbol.Symbol] {
					continue
				}
				if err := ctx.Err(); err != nil {
					return nil, empty, err
				}
				// Canonicalize only a clone for identity. Ordered fields and every original
				// metadata field in the returned artifact remain untouched.
				canonical := proto.Clone(symbol).(*scip.SymbolInformation)
				if err := canonicalSymbols([]*scip.SymbolInformation{canonical}, doc.RelativePath, make(map[string][]byte)); err != nil {
					return nil, empty, err
				}
				raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(canonical)
				if err != nil {
					return nil, empty, Invalid
				}
				metadata := hash(raw)
				identity, _ := json.Marshal([]string{SCIPGoBlankAdapter, doc.RelativePath, metadata})
				local := "local phebs_blank_" + strings.TrimPrefix(hash(identity), "sha256:")
				key := localKey{doc.RelativePath, local}
				if existing[key] || generated[key] != "" && generated[key] != metadata {
					return nil, empty, Invalid
				}
				generated[key] = metadata
				mapping := SCIPGoBlankMapping{Document: doc.RelativePath, Original: symbol.Symbol, Local: local, MetadataDigest: metadata}
				encoded, _ := json.Marshal(mapping)
				if len(encoded)+1 > MaxAttemptBytes-2048-mappingBytes {
					return nil, empty, Capacity
				}
				mappingBytes += len(encoded) + 1
				member.Mappings = append(member.Mappings, mapping)
				symbol.Symbol = local
			}
		}
		slices.SortFunc(member.Mappings, func(a, b SCIPGoBlankMapping) int {
			if c := strings.Compare(a.Document, b.Document); c != 0 {
				return c
			}
			if c := strings.Compare(a.Original, b.Original); c != 0 {
				return c
			}
			return strings.Compare(a.Local, b.Local)
		})
		raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(index)
		if err != nil {
			return nil, empty, Invalid
		}
		if len(raw) > MaxSCIPMemberBytes || len(raw) > MaxSCIPAggregateBytes-totalOutput {
			return nil, empty, Capacity
		}
		totalOutput += len(raw)
		member.OutputDigest = hash(raw)
		receipt.Members[i], output[i] = member, raw
	}
	if err := ctx.Err(); err != nil {
		return nil, empty, err
	}
	encoded, err := json.Marshal(receipt)
	if err != nil || len(encoded) > MaxAttemptBytes {
		return nil, empty, Capacity
	}
	return output, receipt, nil
}

func scipGoBlank(value string) (bool, error) {
	symbol, err := scip.ParseSymbol(value)
	if err != nil {
		return false, Invalid
	}
	if len(symbol.Descriptors) == 0 {
		return false, nil
	}
	terminal := symbol.Descriptors[len(symbol.Descriptors)-1]
	if terminal.Name != "_" || terminal.Suffix != scip.Descriptor_Term {
		return false, nil
	}
	if symbol.Scheme != "scip-go" || symbol.Package == nil || symbol.Package.Manager != "gomod" {
		return false, Unsupported
	}
	return true, nil
}

func scipGoReferences(document string, symbol *scip.SymbolInformation, check func(string, string) error) error {
	if err := check(document, symbol.EnclosingSymbol); err != nil {
		return err
	}
	for _, relationship := range symbol.Relationships {
		if err := check(document, relationship.Symbol); err != nil {
			return err
		}
	}
	if symbol.SignatureDocumentation != nil {
		for _, occurrence := range symbol.SignatureDocumentation.Occurrences {
			if err := check(document, occurrence.Symbol); err != nil {
				return err
			}
		}
	}
	return nil
}
