package api

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/compat"
)

const fieldReferenceMaxFields = 256

// FieldReferenceService builds side-effect-free stable-field proof values.
type FieldReferenceService struct {
	opts Options
}

func NewFieldReferenceService(opts Options) *FieldReferenceService {
	if opts.Store == nil || opts.Evidence == nil {
		return nil
	}
	return &FieldReferenceService{opts: opts}
}

func (service *FieldReferenceService) buildProtoProofBundle(
	ctx context.Context,
	field compat.FieldIdentity,
) (*ProofBundle, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable(
			"field references unavailable",
		)
	}
	fields, err := canonicalFieldReferenceFields(
		[]compat.FieldIdentity{field},
	)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	field = fields[0]
	number := field.Number
	return service.buildProto(
		ctx,
		fields,
		ProofQuery{
			Kind:        "find_proto_field_references",
			Lineage:     field.Lineage,
			Message:     field.Message,
			FieldNumber: &number,
			Domains:     []string{"scip-proto-field"},
		},
	)
}

func (service *FieldReferenceService) buildNeutralProofBundle(
	ctx context.Context,
	field compat.FieldIdentity,
	kind string,
) (*ProofBundle, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable(
			"field references unavailable",
		)
	}
	if err := validateNeutralFieldIdentity(
		field.Lineage,
		field.Message,
		field.Number,
	); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	packs := fieldReferencePacks(field.Number)
	domains := make([]string, 0, len(packs))
	filters := make([]assertionFilter, 0, len(packs))
	for _, pack := range packs {
		domains = append(domains, pack.fieldReferenceDomain)
		filters = append(filters, assertionFilter{
			Domain:    pack.fieldReferenceDomain,
			Predicate: pack.fieldReferencePredicate,
			Object:    field.Message + "#" + strconv.Itoa(field.Number),
			Lineage:   field.Lineage,
		})
	}
	sort.Strings(domains)
	number := field.Number
	return buildProofBundleValue(
		ctx,
		service.opts,
		ProofQuery{
			Kind: kind, Lineage: field.Lineage, Message: field.Message,
			FieldNumber: &number, Domains: domains,
		},
		filters,
		nil,
		false,
	)
}

func (service *FieldReferenceService) buildProto(
	ctx context.Context,
	fields []compat.FieldIdentity,
	proofQuery ProofQuery,
) (*ProofBundle, error) {
	filters := make([]assertionFilter, len(fields))
	for index, field := range fields {
		filters[index] = assertionFilter{
			Domain:    "scip-proto-field",
			Predicate: "REFERENCES_PROTO_FIELD",
			Object:    field.Message + "#" + strconv.Itoa(field.Number),
			Lineage:   field.Lineage,
		}
	}
	return buildProofBundleValue(
		ctx,
		service.opts,
		proofQuery,
		filters,
		nil,
		false,
	)
}

func canonicalFieldReferenceFields(
	fields []compat.FieldIdentity,
) ([]compat.FieldIdentity, error) {
	if len(fields) == 0 || len(fields) > fieldReferenceMaxFields {
		return nil, fmt.Errorf(
			"fields must contain 1 through %d stable identities",
			fieldReferenceMaxFields,
		)
	}
	fields = slices.Clone(fields)
	for _, field := range fields {
		if err := validateFieldIdentity(
			field.Lineage,
			field.Message,
			field.Number,
		); err != nil {
			return nil, err
		}
	}
	sort.Slice(fields, func(left, right int) bool {
		if fields[left].Lineage != fields[right].Lineage {
			return fields[left].Lineage < fields[right].Lineage
		}
		if fields[left].Message != fields[right].Message {
			return fields[left].Message < fields[right].Message
		}
		return fields[left].Number < fields[right].Number
	})
	for index := 1; index < len(fields); index++ {
		if fields[index] == fields[index-1] {
			return nil, fmt.Errorf(
				"fields contains duplicate stable identity %s:%s#%d",
				fields[index].Lineage,
				fields[index].Message,
				fields[index].Number,
			)
		}
	}
	return fields, nil
}
