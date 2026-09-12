package t421

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicequery"
	"github.com/bmeddeb/phebs/internal/t421catalogprojection"
	"github.com/bmeddeb/phebs/spike/t401"
)

// Construct once from the caller's actual protected catalog and admitted F,
// not the independent expected oracle. No file or store reads occur here.
type epochQueryProjectionContext struct {
	repository string
	final      epochFinalResponse
	services   []string
	placements map[string]oraclePlacement
}

func newEpochQueryProjectionContext(ctx context.Context, repository string, final epochFinalResponse, catalog servicecatalog.Catalog) (*epochQueryProjectionContext, error) {
	if ctx == nil || ctx.Err() != nil || repository == "" || final.Schema != "t421-final-authority-source-free-v1" || final.Projection.Schema != "t421-final-state-projection-source-free-v1" ||
		!final.Authority.Current || !gitobj.IsObjectID(final.Authority.PhysicalCommit) || !validDigest(final.Authority.CatalogRootSHA256) ||
		!validDigest(final.Authority.SourceGenerationSHA256) || !validDigest(final.Authority.SearchGenerationSHA256) || !validDigest(final.Projection.CatalogLogicalSHA256) ||
		!final.QueryAuthority.valid() {
		return nil, ErrExecutionEpochOne
	}
	actual, err := t421catalogprojection.Derive(ctx, catalog)
	p := final.Projection
	if err != nil || SetIdentity(actual.Catalog) != p.Catalog || SetIdentity(actual.Memberships) != p.MembershipSet ||
		SetIdentity(actual.Placements) != p.Placements || SetIdentity(actual.UnownedPrefixes) != p.UnownedPrefixes || SetIdentity(actual.ServiceQueries) != p.ServiceQueries {
		return nil, ErrExecutionEpochOne
	}
	queryAuthority := *final.QueryAuthority
	final.QueryAuthority = &queryAuthority
	result := &epochQueryProjectionContext{repository: repository, final: final, placements: make(map[string]oraclePlacement)}
	dispositions := make(map[string]string, len(catalog.Services))
	for _, service := range catalog.Services {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dispositions[service.Key] = service.Disposition
		if service.Disposition == servicecatalog.DispositionAccepted {
			result.services = append(result.services, service.Key)
		}
	}
	slices.Sort(result.services)
	// Fixed query paths only: two search placements plus RPC/Kafka source and
	// declaration paths. Build their real claims in one membership pass.
	paths := []string{"shared/group-0000/library.go", "tools/unowned-0000.go", "contracts/service-00000/api.proto", "services/service-00100/main.go"}
	for i := range 10 {
		paths = append(paths, fmt.Sprintf("services/service-%05d/main.go", i))
	}
	for _, path := range paths {
		result.placements[path] = oraclePlacement{Path: path, Claims: []oracleClaim{}}
	}
	for _, member := range catalog.Memberships {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		placement, wanted := result.placements[member.Path]
		if wanted {
			i := slices.IndexFunc(placement.Claims, func(claim oracleClaim) bool { return claim.ServiceKey == member.ServiceKey })
			if i < 0 {
				placement.Claims = append(placement.Claims, oracleClaim{ServiceKey: member.ServiceKey, Disposition: dispositions[member.ServiceKey], Roles: []oracleRole{}})
				i = len(placement.Claims) - 1
			}
			placement.Claims[i].Roles = append(placement.Claims[i].Roles, oracleRole{Role: member.Role, Origin: member.Origin})
			result.placements[member.Path] = placement
		}
	}
	for _, unowned := range catalog.Unowned {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if placement, wanted := result.placements[unowned.Path]; wanted {
			placement.Unowned, placement.UnownedOrigin = true, unowned.Origin
			result.placements[unowned.Path] = placement
		}
	}
	for path, placement := range result.placements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		slices.SortFunc(placement.Claims, func(a, b oracleClaim) int { return strings.Compare(a.ServiceKey, b.ServiceKey) })
		for i := range placement.Claims {
			sortEpochQueryRoles(placement.Claims[i].Roles)
		}
		result.placements[path] = placement
	}
	return result, nil
}

type epochQueryProjection struct {
	context                            *epochQueryProjectionContext
	query                              QueryCase
	parameters                         map[string]string
	pages, records                     uint64
	paths                              map[string]struct{}
	pairs                              []semanticRelationshipPair
	projection                         any
	lastReference, binding, nextCursor string
	root                               *apiresponse.RelationshipRootReceipt
	err                                error
	refusal                            string // Private fixed clause name; never response content.
}

type epochQueryProjectionResult struct {
	Projection            []byte
	SHA256                string
	Pages, Records, Paths uint64
}

func (context *epochQueryProjectionContext) queryProjection(query QueryCase) (*epochQueryProjection, error) {
	if context == nil || !slices.ContainsFunc(correctedQueryCases(), func(value QueryCase) bool { return reflect.DeepEqual(value, query) }) {
		return nil, ErrExecutionEpochOne
	}
	parameters := make(map[string]string, len(query.Parameters))
	for _, parameter := range query.Parameters {
		value := strings.ReplaceAll(parameter.Value, "$authorized_repository", context.repository)
		if strings.HasPrefix(value, "$accepted_service_") {
			index, err := strconv.Atoi(strings.TrimPrefix(value, "$accepted_service_"))
			if err != nil || index < 0 || index >= len(context.services) {
				return nil, ErrExecutionEpochOne
			}
			value = context.services[index]
		}
		parameters[parameter.Name] = value
	}
	return &epochQueryProjection{context: context, query: query, parameters: parameters, paths: make(map[string]struct{})}, nil
}

// The transport has already authenticated the MCP envelope and error code.
func (projection *epochQueryProjection) addMCP(code string, structured []byte) (string, error) {
	if code != projection.query.ExpectedMCPCode || code != "ok" && len(structured) != 0 {
		_ = projection.relationshipRefusal("transport_code")
		return projection.refuse()
	}
	return projection.add(code == "ok", structured)
}

func (projection *epochQueryProjection) addHTTP(status int, body []byte) (string, error) {
	if status != int(projection.query.ExpectedStatus) {
		_ = projection.relationshipRefusal("transport_status")
		return projection.refuse()
	}
	if status != 200 {
		// Huma errors have optional descriptive fields; retain no repository
		// identity from an existence-hiding refusal in the semantic projection.
		var problem struct {
			Type     string            `json:"type"`
			Title    string            `json:"title"`
			Status   int               `json:"status"`
			Detail   string            `json:"detail"`
			Instance string            `json:"instance"`
			Errors   []json.RawMessage `json:"errors,omitempty"`
			Schema   string            `json:"$schema,omitempty"`
		}
		if decodeEpochQueryJSON(body, &problem) != nil || problem.Status != status || problem.Title != "Not Found" || problem.Detail != "search scope not found" || problem.Instance != "" || len(problem.Errors) != 0 {
			return projection.refuse()
		}
	}
	return projection.add(status == 200, body)
}

func (projection *epochQueryProjection) refuse() (string, error) {
	projection.err = ErrExecutionEpochOne
	return "", projection.err
}

func (projection *epochQueryProjection) add(success bool, body []byte) (string, error) {
	maximum := uint64(1)
	if projection.query.PageSize > 0 {
		maximum = max(uint64(1), (projection.query.ExpectedRecords+projection.query.PageSize-1)/projection.query.PageSize)
	}
	if projection.err != nil || projection.pages >= maximum || projection.pages > 0 && projection.nextCursor == "" {
		_ = projection.relationshipRefusal("page_sequence")
		return projection.refuse()
	}
	var err error
	projection.nextCursor = ""
	if !success {
		projection.projection = semanticDeniedProjection{Schema: "t421-semantic-denied-projection-v1", Posture: "unknown_repository"}
	} else {
		switch projection.query.Surface {
		case "all_code_search", "service_search":
			err = projection.search(body)
		case "service_detail":
			err = projection.service(body)
		case "service_relationships":
			err = projection.relationship(body)
		default:
			err = ErrExecutionEpochOne
		}
	}
	if err != nil || projection.pages+1 < maximum && projection.nextCursor == "" || projection.pages+1 == maximum && projection.nextCursor != "" {
		if err == nil {
			_ = projection.relationshipRefusal("page_count_cursor")
		}
		return projection.refuse()
	}
	projection.pages++
	return projection.nextCursor, nil
}

func (projection *epochQueryProjection) finish() (epochQueryProjectionResult, error) {
	if projection.err != nil || projection.pages == 0 || projection.nextCursor != "" || projection.records != projection.query.ExpectedRecords || uint64(len(projection.paths)) != projection.query.ExpectedPaths {
		return epochQueryProjectionResult{}, ErrExecutionEpochOne
	}
	if value, ok := projection.projection.(semanticRelationshipProjection); ok {
		value.Pairs = slices.Clone(projection.pairs)
		// Frozen semantic order is provider/consumer, independent of native
		// transport reference-digest order and opaque cursor bytes.
		slices.SortFunc(value.Pairs, func(a, b semanticRelationshipPair) int {
			if n := strings.Compare(a.Provider, b.Provider); n != 0 {
				return n
			}
			return strings.Compare(a.Consumer, b.Consumer)
		})
		projection.projection = value
	}
	raw, err := json.Marshal(projection.projection)
	if err != nil {
		return epochQueryProjectionResult{}, err
	}
	return epochQueryProjectionResult{Projection: raw, SHA256: SHA256(raw), Pages: projection.pages, Records: projection.records, Paths: uint64(len(projection.paths))}, nil
}

func decodeEpochQueryJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrExecutionEpochOne
	}
	return nil
}

func sortEpochQueryRoles(roles []oracleRole) {
	slices.SortFunc(roles, func(a, b oracleRole) int {
		if n := strings.Compare(a.Role, b.Role); n != 0 {
			return n
		}
		return strings.Compare(a.Origin, b.Origin)
	})
}

func (projection *epochQueryProjection) search(body []byte) error {
	var value struct {
		Schema string `json:"$schema,omitempty"`
		search.Result
	}
	if decodeEpochQueryJSON(body, &value) != nil || value.Scope == nil || value.Stats.MatchCount < 0 || value.Stats.FileCount != len(value.Files) || value.Stats.DurationMS < 0 {
		return ErrExecutionEpochOne
	}
	scope := *value.Scope
	digest := scope.Digest
	scope.Digest = ""
	raw, _ := json.Marshal(scope)
	if scope.Schema != search.ScopeReceiptSchema || scope.Kind != projection.parameters["scope"] ||
		digest != SHA256(append([]byte("phebs-search-scope-v1\x00"), raw...)) ||
		scope.ExpressionDigest != SHA256([]byte("phebs-search-expression-v1\x00"+projection.parameters["query"])) ||
		scope.ResultFiles != len(value.Files) || scope.ResultMatches != value.Stats.MatchCount {
		return ErrExecutionEpochOne
	}
	f := projection.context.final.Authority
	if scope.Kind == search.ScopeService {
		a := scope.Authority
		if a == nil || servicequery.ValidateAuthority(*a) != nil || scope.Repository != projection.context.repository || scope.ServiceKey != projection.parameters["service_key"] ||
			scope.ServiceStatus != "current" || scope.MembershipPolicy != "accepted-roles-union-shared-included-unowned-excluded-v1" ||
			a.Repository != scope.Repository || a.ServiceKey != scope.ServiceKey || a.Status != "current" || a.RevisionCommit != f.PhysicalCommit ||
			a.CurrentCatalogGeneration != f.CatalogRootSHA256 || a.ActiveCatalogGeneration != f.CatalogRootSHA256 || a.ActiveSourceGeneration != projection.context.final.QueryAuthority.CatalogSourceGenerationSHA256 ||
			a.RepositorySourceGeneration != f.SourceGenerationSHA256 || a.RepositorySearchGeneration != f.SearchGenerationSHA256 {
			return ErrExecutionEpochOne
		}
	} else if scope.Authority != nil || scope.Repository != "" || scope.ServiceKey != "" || scope.ServiceStatus != "" || scope.MembershipPolicy != "visible-indexed-repositories-v1" {
		return ErrExecutionEpochOne
	}
	revisions := make([]search.ScopeRevision, 0, len(value.Files))
	type citation struct {
		Repository string `json:"repository"`
		Commit     string `json:"commit"`
		Path       string `json:"path"`
	}
	citations := make([]citation, 0, len(value.Files))
	var matches uint64
	var ordinals []uint64
	for _, file := range value.Files {
		if file.Repo != projection.context.repository || file.Ref != f.PhysicalCommit || file.Path == "" || len(file.Chunks) != 1 {
			return ErrExecutionEpochOne
		}
		chunk := file.Chunks[0]
		if chunk.StartLine < 1 || len(chunk.Ranges) != 1 {
			return ErrExecutionEpochOne
		}
		rangeValue := chunk.Ranges[0]
		if rangeValue.StartLine != chunk.StartLine || rangeValue.EndLine != chunk.StartLine || rangeValue.StartCol < 1 || rangeValue.EndCol <= rangeValue.StartCol || rangeValue.EndCol-1 > len(chunk.Content) {
			return ErrExecutionEpochOne
		}
		needle := "FixturePath"
		if scope.Kind == search.ScopeAllCode {
			needle = "T401Fixture"
		}
		if chunk.Content[rangeValue.StartCol-1:rangeValue.EndCol-1] != needle {
			return ErrExecutionEpochOne
		}
		matches++
		projection.paths[file.Path] = struct{}{}
		citations = append(citations, citation{Repository: file.Repo, Commit: file.Ref, Path: file.Path})
		revisions = append(revisions, search.ScopeRevision{Repository: file.Repo, Commit: file.Ref})
		if scope.Kind == search.ScopeAllCode {
			var bucket, cell, offset uint64
			if _, err := fmt.Sscanf(file.Path, "structural/cells/b%03d/c%05d/f%03d.go", &bucket, &cell, &offset); err != nil || file.Path != fmt.Sprintf("structural/cells/b%03d/c%05d/f%03d.go", bucket, cell, offset) || bucket != cell/100 {
				return ErrExecutionEpochOne
			}
			profile, err := frozenStructuralProfile()
			if err != nil || cell >= profile.Shape.Cells || offset >= profile.Shape.EligibleGoPathsPerCell {
				return ErrExecutionEpochOne
			}
			ordinal := cell*profile.Shape.EligibleGoPathsPerCell + offset
			path, content, err := t401.FrozenStructuralGoFixture(profile, ordinal)
			lines := bytes.Split(content, []byte{'\n'})
			if err != nil || path != file.Path || chunk.StartLine > len(lines) || strings.TrimSuffix(chunk.Content, "\n") != string(lines[chunk.StartLine-1]) {
				return ErrExecutionEpochOne
			}
			ordinals = append(ordinals, ordinal)
		}
	}
	if matches != uint64(value.Stats.MatchCount) || len(value.Files) > 1 {
		return ErrExecutionEpochOne
	}
	slices.SortFunc(citations, func(a, b citation) int { return strings.Compare(a.Path, b.Path) })
	raw, _ = json.Marshal(citations)
	if scope.ResultSetDigest != SHA256(append([]byte("phebs-search-result-citations-v1\x00"), raw...)) {
		return ErrExecutionEpochOne
	}
	revisions = slices.Compact(revisions)
	if !reflect.DeepEqual(scope.Revisions, revisions) {
		return ErrExecutionEpochOne
	}
	projection.records += matches
	if scope.Kind == search.ScopeAllCode {
		slices.Sort(ordinals)
		projection.projection = semanticSearchProjection{Schema: "t421-semantic-search-projection-v1", Scope: scope.Kind, QueryIdentity: "structural_marker", ReturnedOrdinals: ordinals, StructuralProfile: retainedStructuralProfile, MembershipPosture: "all_physical_paths"}
		return nil
	}
	path := "shared/group-0000/library.go"
	if projection.query.Name == "unowned_excluded_from_service_scope" {
		path = "tools/unowned-0000.go"
	}
	for returned := range projection.paths {
		if returned != path {
			return ErrExecutionEpochOne
		}
	}
	placement, ok := projection.context.placements[path]
	if !ok || placement.Unowned && len(placement.Claims) != 0 || !placement.Unowned && len(placement.Claims) == 0 {
		return ErrExecutionEpochOne
	}
	projection.projection = semanticPlacementProjection{Schema: "t421-semantic-placement-projection-v1", Placement: placement, Visible: matches != 0}
	return nil
}

func (projection *epochQueryProjection) service(body []byte) error {
	var value struct {
		Schema string `json:"$schema,omitempty"`
		apiresponse.ServiceDetail
	}
	if decodeEpochQueryJSON(body, &value) != nil {
		return ErrExecutionEpochOne
	}
	f, repository, service := projection.context.final.Authority, value.Repository, value.Service
	if value.SchemaVersion != "phebs-service-detail-v1" || repository.Repository != projection.context.repository || repository.SourceCommit != f.PhysicalCommit ||
		repository.CatalogGeneration != f.CatalogRootSHA256 || repository.CatalogDigest != projection.context.final.Projection.CatalogLogicalSHA256 ||
		service.Repository != repository.Repository || service.Key != projection.parameters["service_key"] || service.Disposition != "accepted" || service.Status != "current" || service.Removed ||
		service.ActiveCatalogGeneration != f.CatalogRootSHA256 || service.ActiveSourceGeneration != projection.context.final.QueryAuthority.CatalogSourceGenerationSHA256 || service.Incarnation == 0 || service.ControlRevision == 0 ||
		service.MembershipCount != len(value.Memberships) || len(value.Successors) != 0 {
		return ErrExecutionEpochOne
	}
	paths := make([]oracleQueryPath, 0, len(value.Memberships))
	last := ""
	for _, member := range value.Memberships {
		key := member.Path + "\x00" + member.Role + "\x00" + member.Origin
		if member.Path == "" || key <= last {
			return ErrExecutionEpochOne
		}
		last = key
		if len(paths) == 0 || paths[len(paths)-1].Path != member.Path {
			paths = append(paths, oracleQueryPath{Path: member.Path})
		}
		paths[len(paths)-1].Roles = append(paths[len(paths)-1].Roles, oracleRole{Role: member.Role, Origin: member.Origin})
		projection.paths[member.Path] = struct{}{}
	}
	if service.DistinctPathCount != len(paths) {
		return ErrExecutionEpochOne
	}
	projection.records += uint64(len(value.Memberships))
	projection.projection = semanticServiceProjection{Schema: "t421-semantic-service-projection-v1", Service: oracleServiceQuery{ServiceKey: service.Key, Paths: paths}}
	return nil
}

func (projection *epochQueryProjection) relationship(body []byte) error {
	var page struct {
		Schema string `json:"$schema,omitempty"`
		apiresponse.RelationshipPage
	}
	if decodeEpochQueryJSON(body, &page) != nil {
		return projection.relationshipRefusal("decode")
	}
	parameters, f := projection.parameters, projection.context.final.Authority
	q := apiresponse.RelationshipQuery{Repositories: []string{projection.context.repository}, ServiceKey: parameters["service_key"], View: parameters["view"], Kind: parameters["kind"], Plane: parameters["plane"], LookupKey: parameters["lookup_key"]}
	if page.SchemaVersion != "phebs-service-relationship-page-v1" || !reflect.DeepEqual(page.Query, q) || page.RowsState != "nonempty" ||
		len(page.Roots) != 1 || len(page.Rows) != 1 || page.Pagination.Order != "repository,reference_digest:asc" || page.Pagination.PageSize != 1 || page.Pagination.Returned != 1 ||
		page.Coverage.AuthorizedRepositories != 1 || page.Coverage.CompleteRoots != 1 || page.Coverage.EmptyRoots != 0 || page.Coverage.FailedRoots != 0 || page.Coverage.UnavailableRoots != 0 ||
		page.Coverage.Truncated || page.Coverage.ReturnedRows != 1 || page.Coverage.ScannedReferences != int(projection.query.ExpectedRecords) {
		return projection.relationshipRefusal("page_envelope")
	}
	root := page.Roots[0]
	a := root.Authority
	if root.Repository != projection.context.repository || root.State != "complete" || root.Reason != "" || root.RootSchema != relationshippublication.RootSchemaV3 || root.Generation != f.RelationshipGenerationSHA256 || root.RootDigest != f.RelationshipRootSHA256 ||
		root.ServiceKey != q.ServiceKey || root.ServiceIncarnation == 0 || !validDigest(root.ServiceGeneration) || !validDigest(root.AuthorityDigest) || root.Unavailable != nil ||
		!root.RepositoryComplete || !root.AllServicesComplete || root.FailedServiceCount != 0 || a == nil || a.Repository != root.Repository ||
		a.CatalogGenerationDigest != f.CatalogRootSHA256 || a.CatalogDigest != projection.context.final.Projection.CatalogLogicalSHA256 || a.CatalogSourceGeneration != projection.context.final.QueryAuthority.CatalogSourceGenerationSHA256 ||
		a.ResolverGenerationDigest != projection.context.final.QueryAuthority.ResolverNamespaceGenerationSHA256 ||
		a.ResolverRootDigest != projection.context.final.QueryAuthority.ResolverNamespaceRootSHA256 || a.Upstream == nil ||
		a.Upstream.Repository != root.Repository || a.Upstream.Observation.ObservationGenerationDigest != f.ObservationGenerationSHA256 ||
		a.Upstream.Observation.SourceGenerationDigest != f.SourceGenerationSHA256 {
		return projection.relationshipRefusal("root_authority")
	}
	if projection.root != nil && !reflect.DeepEqual(*projection.root, root) {
		return projection.relationshipRefusal("root_changed")
	}
	projection.root = &root
	row := page.Rows[0]
	participation := "source"
	if q.View == "callers" {
		participation = "target"
	}
	if row.Repository != root.Repository || row.ServiceKey != q.ServiceKey || row.ServiceIncarnation != root.ServiceIncarnation || row.ServiceGeneration != root.ServiceGeneration ||
		row.Kind != q.Kind || row.Plane != q.Plane || row.LookupKey != q.LookupKey || !reflect.DeepEqual(row.Participation, []string{participation}) ||
		!validDigest(row.ProjectionDigest) || !validDigest(row.PostingDigest) || row.Evidence.PostingDigest != row.PostingDigest || row.Evidence.Path != row.Source.Path ||
		row.Evidence.Kind != row.Kind || row.Evidence.Plane != row.Plane || row.Evidence.Class != row.Class || row.Evidence.Span.StartByte < 0 ||
		row.Evidence.Span.EndByte <= row.Evidence.Span.StartByte || row.Evidence.Span.StartLine < 1 || row.Evidence.Span.EndLine < row.Evidence.Span.StartLine ||
		!validDigest(row.Evidence.ContentDigest) || !gitobj.IsObjectID(row.Evidence.ObjectID) {
		return projection.relationshipRefusal("row_evidence")
	}
	// Reconstruct the actual publication projection/reference bytes; these
	// digests bind all returned claims rather than trusting semantic labels.
	native := relationshippublication.Projection{Schema: relationshippublication.ProjectionSchema, Kind: row.Kind, PostingDigest: row.PostingDigest, Class: row.Class, Plane: row.Plane, LookupKey: row.LookupKey}
	raw, _ := json.Marshal(row.Source)
	if json.Unmarshal(raw, &native.Source) != nil {
		return projection.relationshipRefusal("source_decode")
	}
	if row.Target != nil {
		raw, _ = json.Marshal(row.Target)
		if json.Unmarshal(raw, &native.Target) != nil {
			return projection.relationshipRefusal("target_decode")
		}
	}
	if !projection.context.placementMatches(row.Source) || row.Target != nil && !projection.context.placementMatches(*row.Target) {
		return projection.relationshipRefusal("placement")
	}
	raw, _ = json.Marshal(native)
	if SHA256(raw) != row.ProjectionDigest {
		return projection.relationshipRefusal("projection_digest")
	}
	reference := relationshippublication.ServiceReference{Schema: relationshippublication.ServiceReferenceSchema, ProjectionDigest: row.ProjectionDigest, PostingDigest: row.PostingDigest, Kind: row.Kind, Plane: row.Plane, LookupKey: row.LookupKey, Participation: row.Participation}
	raw, _ = json.Marshal(reference)
	referenceDigest := SHA256(raw)
	if referenceDigest <= projection.lastReference {
		return projection.relationshipRefusal("reference_order")
	}
	projection.lastReference = referenceDigest
	var citation struct {
		Schema     string `json:"schema"`
		Binding    string `json:"binding"`
		Repository string `json:"repository"`
		Source     int    `json:"source"`
		Projection string `json:"projection"`
	}
	if decodeEpochQueryToken(row.Citation, &citation) != nil || citation.Schema != "phebs-service-relationship-citation-v1" || citation.Binding == "" || citation.Repository != root.Repository || citation.Source != 0 || citation.Projection != row.ProjectionDigest {
		return projection.relationshipRefusal("citation")
	}
	if projection.binding != "" && projection.binding != citation.Binding {
		return projection.relationshipRefusal("binding_changed")
	}
	projection.binding = citation.Binding
	if page.Pagination.NextCursor != "" {
		var cursor struct {
			Schema      string `json:"schema"`
			Binding     string `json:"binding"`
			QueryDigest string `json:"query_digest"`
			Offset      int    `json:"offset"`
		}
		queryRaw, _ := json.Marshal(struct {
			Schema   string                        `json:"schema"`
			Query    apiresponse.RelationshipQuery `json:"query"`
			PageSize int                           `json:"page_size"`
		}{"phebs-service-relationship-page-v1", q, 1})
		if decodeEpochQueryToken(page.Pagination.NextCursor, &cursor) != nil || cursor.Schema != "phebs-service-relationship-cursor-v1" || cursor.Binding != projection.binding || cursor.Offset != int(projection.pages+1) || cursor.QueryDigest != SHA256(queryRaw) {
			return projection.relationshipRefusal("cursor")
		}
	}
	projection.nextCursor = page.Pagination.NextCursor
	consumer, err := epochQueryClaimService(row.Source)
	if err != nil {
		return projection.relationshipRefusal("source_claim")
	}
	projection.paths[row.Source.Path] = struct{}{}
	projection.paths[row.Evidence.Path] = struct{}{}
	if row.Kind == "kafka" {
		if row.Target != nil || row.Class != "literal" || row.Evidence.TopicSpelling != q.LookupKey || row.Evidence.SourceRole != "production" || consumer != row.ServiceKey || len(row.CounterpartServices) != 0 {
			return projection.relationshipRefusal("kafka_semantic")
		}
		projection.projection = semanticKafkaProjection{Schema: "t421-semantic-kafka-product-projection-v1", ServiceKey: row.ServiceKey, View: q.View, Kind: row.Kind, Plane: row.Plane, LookupKey: row.LookupKey, Participation: participation, ProductPairPosture: "independent_projection_not_product_cooccurrence"}
	} else {
		if row.Target == nil || row.Class != "resolved" || row.Evidence.SourceRole != "production" || row.Evidence.Operation != q.LookupKey || row.Evidence.DeclarationPath != row.Target.Path {
			return projection.relationshipRefusal("rpc_semantic")
		}
		provider, err := epochQueryClaimService(*row.Target)
		if err != nil {
			return projection.relationshipRefusal("target_claim")
		}
		counterpart, selected := provider, consumer
		if participation == "target" {
			counterpart, selected = consumer, provider
		}
		if selected != row.ServiceKey || !reflect.DeepEqual(row.CounterpartServices, []string{counterpart}) {
			return projection.relationshipRefusal("counterpart")
		}
		providerIndex, err := strconv.Atoi(strings.TrimPrefix(provider, "svc.load-"))
		consumerIndex, consumerErr := strconv.Atoi(strings.TrimPrefix(consumer, "svc.load-"))
		if err != nil || consumerErr != nil || provider != serviceKey(providerIndex) || consumer != serviceKey(consumerIndex) {
			return projection.relationshipRefusal("service_identity")
		}
		family := "bounded_fanout"
		if strings.Contains(row.LookupKey, "/LayeredDagP") {
			family = "layered_dag"
		} else if consumerIndex == (providerIndex+1)%relationshipServiceCount {
			family = "chain"
		}
		projection.pairs = append(projection.pairs, semanticRelationshipPair{Family: family, Provider: provider, Consumer: consumer})
		projection.paths[row.Target.Path] = struct{}{}
		projection.projection = semanticRelationshipProjection{Schema: "t421-semantic-rpc-product-projection-v1", ServiceKey: row.ServiceKey, View: q.View, Kind: row.Kind, Plane: row.Plane, LookupKey: row.LookupKey, Participation: participation}
	}
	projection.records++
	return nil
}

func (projection *epochQueryProjection) relationshipRefusal(clause string) error {
	if projection.refusal == "" {
		projection.refusal = clause
	}
	return ErrExecutionEpochOne
}

func (context *epochQueryProjectionContext) placementMatches(value apiresponse.RelationshipPlacement) bool {
	want, ok := context.placements[value.Path]
	if !ok || value.Unowned != want.Unowned || len(value.Claims) != len(want.Claims) {
		return false
	}
	for i, claim := range value.Claims {
		w := want.Claims[i]
		if claim.ServiceKey != w.ServiceKey || claim.Disposition != w.Disposition || len(claim.Roles) != len(w.Roles) {
			return false
		}
		for j, role := range claim.Roles {
			if role.Role != w.Roles[j].Role || role.Origin != w.Roles[j].Origin {
				return false
			}
		}
	}
	return true
}

func epochQueryClaimService(value apiresponse.RelationshipPlacement) (string, error) {
	if value.Path == "" || value.Unowned || len(value.Claims) != 1 || value.Claims[0].Disposition != "accepted" || len(value.Claims[0].Roles) == 0 {
		return "", ErrExecutionEpochOne
	}
	return value.Claims[0].ServiceKey, nil
}

// Tokens are authenticated by the native response producer. The parent can
// check canonical framing and provenance links, not the server-private HMAC.
func decodeEpochQueryToken(value string, target any) error {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok || len(value) > 16<<10 {
		return ErrExecutionEpochOne
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	mac, macErr := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || macErr != nil || base64.RawURLEncoding.EncodeToString(raw) != payload || base64.RawURLEncoding.EncodeToString(mac) != signature || len(mac) != 32 {
		return ErrExecutionEpochOne
	}
	return decodeEpochQueryJSON(raw, target)
}
