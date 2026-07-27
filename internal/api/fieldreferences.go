package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	fieldReferenceSchemaVersion = "field-reference-read-v1"
	fieldReferenceCursorVersion = "field-reference-cursor-v1"
	fieldReferenceDefaultPage   = 50
	fieldReferenceMaxPage       = 100
	fieldReferenceMaxFields     = 256
	fieldReferenceCursorLimit   = 16 << 10
	fieldReferenceCaveat        = "Static field-reference evidence only; an empty or exhausted page does not establish absence, compatibility, migration completeness, or decommissioning safety."
)

// FieldReferenceService is the shared, side-effect-free stable-field reader.
// The proof endpoint persists the value produced by this service; Workbench
// browsing calls List and therefore cannot mint a proof-bundle identity.
type FieldReferenceService struct {
	opts Options
}

func NewFieldReferenceService(opts Options) *FieldReferenceService {
	if opts.Store == nil || opts.Evidence == nil {
		return nil
	}
	return &FieldReferenceService{opts: opts}
}

type FieldReferenceQuery struct {
	Fields []compat.FieldIdentity `json:"fields"`
}

type FieldReferenceRow struct {
	Field     compat.FieldIdentity `json:"field"`
	Assertion store.Assertion      `json:"assertion"`
	Evidence  []BundleEvidence     `json:"evidence"`
}

type FieldReferencePage struct {
	SchemaVersion     string                      `json:"schema_version"`
	Query             FieldReferenceQuery         `json:"query"`
	Rows              []FieldReferenceRow         `json:"rows"`
	TotalRows         int                         `json:"total_rows"`
	Pagination        CallerMapPagination         `json:"pagination"`
	CoverageDigest    string                      `json:"coverage_digest"`
	Coverage          extract.CoverageCertificate `json:"coverage"`
	VisibilityContext VisibilityContext           `json:"visibility_context"`
	Caveat            string                      `json:"caveat"`
}

type fieldReferenceCursor struct {
	Schema                     string `json:"schema"`
	QueryDigest                string `json:"query_digest"`
	Principal                  string `json:"principal"`
	AuthorizationProvider      string `json:"authorization_provider"`
	PermissionSnapshot         string `json:"permission_snapshot"`
	VisibleRepositorySetDigest string `json:"visible_repository_set_digest"`
	CoverageDigest             string `json:"coverage_digest"`
	Offset                     int    `json:"offset"`
	Checksum                   string `json:"checksum"`
}

func (service *FieldReferenceService) List(
	ctx context.Context,
	query FieldReferenceQuery,
	pageSize int,
	cursor string,
) (*FieldReferencePage, error) {
	if service == nil {
		return nil, huma.Error503ServiceUnavailable(
			"field references unavailable",
		)
	}
	fields, err := canonicalFieldReferenceFields(query.Fields)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	query.Fields = fields
	if pageSize == 0 {
		pageSize = fieldReferenceDefaultPage
	}
	if pageSize < 1 || pageSize > fieldReferenceMaxPage {
		return nil, huma.Error400BadRequest(
			fmt.Sprintf(
				"page_size must be from 1 through %d",
				fieldReferenceMaxPage,
			),
		)
	}
	queryDigest := digestJSON(struct {
		Query    FieldReferenceQuery `json:"query"`
		PageSize int                 `json:"page_size"`
	}{Query: query, PageSize: pageSize})
	bundle, err := service.build(
		ctx,
		query,
		ProofQuery{
			Kind:    "read_proto_field_references",
			Domains: []string{"scip-proto-field"},
		},
	)
	if err != nil {
		return nil, err
	}
	offset, err := decodeFieldReferenceCursor(
		cursor,
		queryDigest,
		bundle.VisibilityContext,
		bundle.Coverage.Digest,
	)
	if err != nil {
		return nil, err
	}
	rows, err := fieldReferenceRows(fields, bundle)
	if err != nil {
		return nil, huma.Error500InternalServerError(
			"project field references",
			err,
		)
	}
	if offset > len(rows) {
		return nil, huma.Error409Conflict(
			"field reference cursor is no longer valid",
		)
	}
	end := min(offset+pageSize, len(rows))
	pagination := CallerMapPagination{Complete: end == len(rows)}
	if !pagination.Complete {
		pagination.NextCursor, err = encodeFieldReferenceCursor(
			queryDigest,
			bundle.VisibilityContext,
			bundle.Coverage.Digest,
			end,
		)
		if err != nil {
			return nil, huma.Error500InternalServerError(
				"encode field reference cursor",
				err,
			)
		}
	}
	return &FieldReferencePage{
		SchemaVersion:     fieldReferenceSchemaVersion,
		Query:             query,
		Rows:              slices.Clone(rows[offset:end]),
		TotalRows:         len(rows),
		Pagination:        pagination,
		CoverageDigest:    bundle.Coverage.Digest,
		Coverage:          bundle.Coverage,
		VisibilityContext: bundle.VisibilityContext,
		Caveat:            fieldReferenceCaveat,
	}, nil
}

func (service *FieldReferenceService) buildProofBundle(
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
	return service.build(
		ctx,
		FieldReferenceQuery{Fields: fields},
		ProofQuery{
			Kind:        "find_proto_field_references",
			Lineage:     field.Lineage,
			Message:     field.Message,
			FieldNumber: field.Number,
			Domains:     []string{"scip-proto-field"},
		},
	)
}

func (service *FieldReferenceService) build(
	ctx context.Context,
	query FieldReferenceQuery,
	proofQuery ProofQuery,
) (*ProofBundle, error) {
	filters := make([]assertionFilter, len(query.Fields))
	for index, field := range query.Fields {
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

func fieldReferenceRows(
	fields []compat.FieldIdentity,
	bundle *ProofBundle,
) ([]FieldReferenceRow, error) {
	fieldByKey := make(map[string]compat.FieldIdentity, len(fields))
	for _, field := range fields {
		fieldByKey[fieldReferenceKey(
			field.Lineage,
			field.Message+"#"+strconv.Itoa(field.Number),
		)] = field
	}
	evidenceByKey := make(map[string]BundleEvidence, len(bundle.Evidence))
	for _, evidence := range bundle.Evidence {
		evidenceByKey[strings.Join([]string{
			evidence.Repository,
			evidence.RunID,
			evidence.Atom.ID,
		}, "\x00")] = evidence
	}
	rows := make([]FieldReferenceRow, 0, len(bundle.Assertions))
	for _, assertion := range bundle.Assertions {
		field, ok := fieldByKey[fieldReferenceKey(
			assertion.Lineage,
			assertion.Object,
		)]
		if !ok || assertion.Predicate != "REFERENCES_PROTO_FIELD" {
			return nil, fmt.Errorf(
				"field-reference assertion is outside the requested identities",
			)
		}
		atomIDs := append(
			append(
				[]string(nil),
				assertion.Supporting...,
			),
			assertion.Contradicting...,
		)
		sort.Strings(atomIDs)
		evidence := make([]BundleEvidence, 0, len(atomIDs))
		for _, atomID := range atomIDs {
			value, ok := evidenceByKey[strings.Join([]string{
				assertion.Repo,
				assertion.RunID,
				atomID,
			}, "\x00")]
			if !ok {
				return nil, fmt.Errorf(
					"field-reference evidence is incomplete",
				)
			}
			evidence = append(evidence, value)
		}
		rows = append(rows, FieldReferenceRow{
			Field:     field,
			Assertion: assertion,
			Evidence:  evidence,
		})
	}
	return rows, nil
}

func fieldReferenceKey(lineage, object string) string {
	return lineage + "\x00" + object
}

func decodeFieldReferenceCursor(
	encoded, queryDigest string,
	binding VisibilityContext,
	coverageDigest string,
) (int, error) {
	if encoded == "" {
		return 0, nil
	}
	if len(encoded) > fieldReferenceCursorLimit {
		return 0, huma.Error400BadRequest(
			"field reference cursor is invalid",
		)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) > fieldReferenceCursorLimit ||
		!json.Valid(raw) {
		return 0, huma.Error400BadRequest(
			"field reference cursor is invalid",
		)
	}
	var cursor fieldReferenceCursor
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return 0, huma.Error400BadRequest(
			"field reference cursor is invalid",
		)
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if cursor.Schema != fieldReferenceCursorVersion || checksum == "" ||
		checksum != digestJSON(cursor) || cursor.Offset < 1 {
		return 0, huma.Error400BadRequest(
			"field reference cursor is invalid",
		)
	}
	if cursor.QueryDigest != queryDigest {
		return 0, huma.Error400BadRequest(
			"field reference cursor does not match the query",
		)
	}
	if cursor.Principal != binding.Principal ||
		cursor.AuthorizationProvider != binding.AuthorizationProvider ||
		cursor.PermissionSnapshot != binding.PermissionSnapshot ||
		cursor.VisibleRepositorySetDigest !=
			binding.VisibleRepositorySetDigest ||
		cursor.CoverageDigest != coverageDigest {
		return 0, huma.Error409Conflict(
			"field reference cursor is no longer valid",
		)
	}
	return cursor.Offset, nil
}

func encodeFieldReferenceCursor(
	queryDigest string,
	binding VisibilityContext,
	coverageDigest string,
	offset int,
) (string, error) {
	cursor := fieldReferenceCursor{
		Schema:                     fieldReferenceCursorVersion,
		QueryDigest:                queryDigest,
		Principal:                  binding.Principal,
		AuthorizationProvider:      binding.AuthorizationProvider,
		PermissionSnapshot:         binding.PermissionSnapshot,
		VisibleRepositorySetDigest: binding.VisibleRepositorySetDigest,
		CoverageDigest:             coverageDigest,
		Offset:                     offset,
	}
	cursor.Checksum = digestJSON(cursor)
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
