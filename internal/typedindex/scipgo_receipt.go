package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

const SCIPGoAttemptSchema = "phebs-scip-attempt-v2"

// BuildBundleWithSCIPGoReceipt adds bounded historical audit identities. The
// receipt grants no source, member, route or publication authority. Canonical
// members cannot replay the original producer bytes after omission/remapping;
// only the trusted finalizer, while holding those bytes, proves that derivation.
// Persistence authenticates this audit record through the existing root hash.
func BuildBundleWithSCIPGoReceipt(ctx context.Context, a Admission, p PackagePlan, outcomes []UnitOutcome, members []MemberInput, generated map[string][]byte, receipt SCIPGoAdapterReceipt) (Bundle, error) {
	return buildBundle(ctx, a, p, outcomes, members, generated, &receipt, nil)
}

func validateSCIPGoReceipt(ctx context.Context, p Profile, receipt *SCIPGoAdapterReceipt) error {
	if receipt == nil || receipt.Schema != SCIPGoBlankAdapter || len(receipt.Members) == 0 || len(receipt.Members) > MaxSCIPMembers || p.definition.Tools.Indexer != SCIPGoIndexer(p.definition.Config.GOARCH) {
		return Invalid
	}
	remaining := MaxAttemptBytes
	for _, member := range receipt.Members {
		if !digest(member.RawDigest) || !digest(member.OutputDigest) || member.Mappings == nil || len(member.Mappings) > MaxSCIPSymbols {
			return Invalid
		}
		var previous SCIPGoBlankMapping
		for j, m := range member.Mappings {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(m.Document) == 0 || len(m.Document) > MaxSCIPTextBytes || !utf8.ValidString(m.Document) || strings.ContainsAny(m.Document, "\x00\r\n\\") || len(m.Original) > MaxSCIPSymbolBytes || !digest(m.MetadataDigest) {
				return Invalid
			}
			blank, err := scipGoBlank(m.Original)
			if err != nil || !blank {
				return Invalid
			}
			key, _ := json.Marshal([]string{SCIPGoBlankAdapter, m.Document, m.MetadataDigest})
			if m.Local != "local phebs_blank_"+strings.TrimPrefix(hash(key), "sha256:") {
				return Invalid
			}
			if j > 0 && (previous.Document > m.Document || previous.Document == m.Document && (previous.Original > m.Original || previous.Original == m.Original && previous.Local > m.Local)) {
				return Invalid
			}
			previous = m
			raw, _ := json.Marshal(m)
			if len(raw)+1 > remaining {
				return Capacity
			}
			remaining -= len(raw) + 1
		}
	}
	return nil
}

// Canonical audit ordering is independent of member argument order. Exact
// duplicate mappings retain their multiplicity. No original receipt is mutated.
func canonicalSCIPGoReceipt(receipt SCIPGoAdapterReceipt) SCIPGoAdapterReceipt {
	receipt.Members = slices.Clone(receipt.Members)
	type keyed struct {
		member SCIPGoMemberReceipt
		key    string
	}
	rows := make([]keyed, len(receipt.Members))
	for j, m := range receipt.Members {
		raw, _ := json.Marshal(m)
		rows[j] = keyed{m, string(raw)}
	}
	slices.SortFunc(rows, func(a, b keyed) int { return strings.Compare(a.key, b.key) })
	for j, r := range rows {
		receipt.Members[j] = r.member
	}
	return receipt
}

// The shared attempt still has only 2MiB. In particular, receipt arrays are
// counted before decoding them into potentially much larger mapping structs.
func scipGoAttemptDimensions(ctx context.Context, raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxAttemptBytes {
		return Invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var visit func(string, int) error
	visit = func(at string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 16 {
			return Invalid
		}
		t, err := d.Token()
		if err != nil {
			return Invalid
		}
		v, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch v {
		case '{':
			for n := 0; d.More(); n++ {
				if n >= 64 {
					return Capacity
				}
				k, e := d.Token()
				key, ok := k.(string)
				if e != nil || !ok || len(key) > 4096 {
					return Invalid
				}
				if e = visit(at+"/"+strings.ToLower(key), depth+1); e != nil {
					return e
				}
			}
		case '[':
			limit := MaxBundleEdges
			switch at {
			case "/scip_go/members":
				limit = MaxSCIPMembers
			case "/input/members":
				limit = MaxSCIPMembers
			case "/scip_go/members/*/mappings":
				limit = MaxSCIPSymbols
			case "/units":
				limit = MaxBundleUnits
			case "/targets":
				limit = MaxBundleTargets
			case "/members":
				limit = MaxSCIPMembers
			case "/documents", "/generated":
				limit = MaxBundleDocuments
			}
			for n := 0; d.More(); n++ {
				if n >= limit {
					return Capacity
				}
				if e := visit(at+"/*", depth+1); e != nil {
					return e
				}
			}
		default:
			return Invalid
		}
		end, err := d.Token()
		if err != nil || v == '{' && end != json.Delim('}') || v == '[' && end != json.Delim(']') {
			return Invalid
		}
		return nil
	}
	if err := visit("", 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return Invalid
	}
	return nil
}
