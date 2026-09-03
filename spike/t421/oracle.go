package t421

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"slices"

	"github.com/bmeddeb/phebs/internal/resolverinput"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

const combinedServices = 10_000

type oracleRole struct {
	Role   string `json:"role"`
	Origin string `json:"origin"`
}

type oracleClaim struct {
	ServiceKey  string       `json:"service_key"`
	Disposition string       `json:"disposition"`
	Roles       []oracleRole `json:"roles"`
}

type oraclePlacement struct {
	Path          string        `json:"path"`
	Unowned       bool          `json:"unowned"`
	UnownedOrigin string        `json:"unowned_origin,omitempty"`
	Claims        []oracleClaim `json:"claims"`
}

type oracleQueryPath struct {
	Path  string       `json:"path"`
	Roles []oracleRole `json:"roles"`
}

type oracleServiceQuery struct {
	ServiceKey string            `json:"service_key"`
	Paths      []oracleQueryPath `json:"paths"`
}

type oracleProductProjection struct {
	Schema        string `json:"schema"`
	Kind          string `json:"kind"`
	Family        string `json:"family"`
	LookupKey     string `json:"lookup_key"`
	ServiceKey    string `json:"service_key"`
	ConsumerKey   string `json:"consumer_key,omitempty"`
	Participation string `json:"participation"`
}

type identityBuilder struct {
	hash    hash.Hash
	bytes   uint64
	records uint64
}

func newIdentityBuilder(domain string) *identityBuilder {
	builder := &identityBuilder{hash: sha256.New()}
	builder.writeFrame([]byte(domain))
	return builder
}

func (builder *identityBuilder) add(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	builder.writeFrame(raw)
	builder.records++
	return nil
}

func (builder *identityBuilder) writeFrame(raw []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
	_, _ = builder.hash.Write(length[:])
	_, _ = builder.hash.Write(raw)
	builder.bytes += uint64(len(length) + len(raw))
}

func (builder *identityBuilder) finish() SetIdentity {
	return SetIdentity{
		Records: builder.records, FramedBytes: builder.bytes,
		SHA256: "sha256:" + hex.EncodeToString(builder.hash.Sum(nil)),
	}
}

// BuildIndependentOracle derives the expected logical and relationship sets
// from closed arithmetic only. It does not call the corpus author, Git, Phebs,
// or any retained execution result.
func BuildIndependentOracle() (Oracle, error) {
	catalog, err := independentCatalogIdentity(-1)
	if err != nil {
		return Oracle{}, err
	}
	memberships := newIdentityBuilder("t421-independent-memberships-v1")
	placements := newIdentityBuilder("t421-independent-placements-v1")
	unownedPrefixes := newIdentityBuilder("t421-independent-unowned-prefixes-v1")
	queries := newIdentityBuilder("t421-independent-service-queries-v1")
	for index := range combinedServices {
		for _, membership := range independentMemberships(index) {
			if err := memberships.add(membership); err != nil {
				return Oracle{}, err
			}
		}
		if err := queries.add(independentServiceQuery(index)); err != nil {
			return Oracle{}, err
		}
	}
	if err := walkIndependentPlacements(func(placement oraclePlacement) error {
		if placement.UnownedOrigin == servicecatalog.OriginOverride {
			if err := unownedPrefixes.add(servicecatalog.UnownedPlacement{
				Path: placement.Path, Origin: placement.UnownedOrigin,
			}); err != nil {
				return err
			}
		}
		return placements.add(placement)
	}); err != nil {
		return Oracle{}, err
	}
	relationships, err := independentRelationshipFamilies()
	if err != nil {
		return Oracle{}, err
	}
	product, rpcProduct, err := independentProductProjectionIdentity()
	if err != nil {
		return Oracle{}, err
	}
	return Oracle{
		Schema: OracleSchema, Independent: true, ConsumesPhebsResults: false,
		Catalog: catalog, Memberships: memberships.finish(),
		Placements: placements.finish(), UnownedPrefixes: unownedPrefixes.finish(),
		ServiceQueries: queries.finish(), QueryCases: frozenQueryCases(),
		Relationships: relationships,
		ProductRelationships: ProductRelationships{
			RPCProjections: 10_999, KafkaProducerProjections: 500,
			KafkaConsumerProjections: 9_500, TotalProjections: 20_999,
			ServiceReferences:      31_998,
			KafkaPairOraclePosture: "semantic_pairs_only_not_product_cooccurrence",
			Canonicalization:       "family_order=chain,layered_dag,bounded_fanout,hotspot;provider,slot,consumer;hotspot_producer_before_consumers;runtime_generation_ids_excluded",
			GlobalCallerPolicy:     "callerexecute-catalog-wide-direct-resolver-v1",
			CallerCandidateRecords: 21_603,
			CallerLeaves:           frozenCallerLeafProfiles(),
			ExpectedRPCProjections: rpcProduct,
			ExpectedProjections:    product,
		},
	}, nil
}

func frozenCallerLeafProfiles() []CallerLeafProfile {
	return []CallerLeafProfile{
		{Prefix: "000", CandidateRecords: 2_725, ResolvedPostings: 1_464, Abstentions: 1_440, Records: 2_904, CanonicalBytes: 2_859_819, EncodedBytes: 2_859_819},
		{Prefix: "001", CandidateRecords: 2_709, ResolvedPostings: 1_384, Abstentions: 1_443, Records: 2_827, CanonicalBytes: 2_721_461, EncodedBytes: 2_721_461},
		{Prefix: "010", CandidateRecords: 2_622, ResolvedPostings: 1_315, Abstentions: 1_424, Records: 2_739, CanonicalBytes: 2_597_027, EncodedBytes: 2_597_027},
		{Prefix: "011", CandidateRecords: 2_629, ResolvedPostings: 1_360, Abstentions: 1_408, Records: 2_768, CanonicalBytes: 2_671_782, EncodedBytes: 2_671_782},
		{Prefix: "100", CandidateRecords: 2_677, ResolvedPostings: 1_392, Abstentions: 1_422, Records: 2_814, CanonicalBytes: 2_730_870, EncodedBytes: 2_730_870},
		{Prefix: "101", CandidateRecords: 2_764, ResolvedPostings: 1_333, Abstentions: 1_518, Records: 2_851, CanonicalBytes: 2_649_147, EncodedBytes: 2_649_147},
		{Prefix: "110", CandidateRecords: 2_704, ResolvedPostings: 1_413, Abstentions: 1_423, Records: 2_836, CanonicalBytes: 2_767_091, EncodedBytes: 2_767_091},
		{Prefix: "111", CandidateRecords: 2_773, ResolvedPostings: 1_338, Abstentions: 1_525, Records: 2_863, CanonicalBytes: 2_658_846, EncodedBytes: 2_658_846},
	}
}

func independentCatalogIdentity(changedService int) (SetIdentity, error) {
	builder := newIdentityBuilder("t421-independent-catalog-v1")
	for index := range combinedServices {
		key := serviceKey(index)
		displayName := key
		if index == changedService {
			displayName += "-b"
		}
		if err := builder.add(servicecatalog.Service{
			Key: key, DisplayName: displayName, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		}); err != nil {
			return SetIdentity{}, err
		}
	}
	return builder.finish(), nil
}

func independentProductProjectionIdentity() (SetIdentity, SetIdentity, error) {
	builder := newIdentityBuilder("t421-independent-product-relationship-projections-v1")
	rpcBuilder := newIdentityBuilder("t421-independent-rpc-product-projections-v1")
	addRPC := func(edge relationshipEdge) error {
		for _, value := range []oracleProductProjection{
			{Schema: "t421-product-relationship-projection-v1", Kind: "rpc", Family: edge.family,
				LookupKey: edge.identity, ServiceKey: serviceKey(edge.provider),
				ConsumerKey: serviceKey(edge.consumer), Participation: "provider_consumer"},
		} {
			if err := builder.add(value); err != nil {
				return err
			}
			if err := rpcBuilder.add(value); err != nil {
				return err
			}
		}
		return nil
	}
	for provider := 0; provider+1 < relationshipServiceCount; provider++ {
		if err := addRPC(independentRPCEdge("chain", provider, provider+1, 0)); err != nil {
			return SetIdentity{}, SetIdentity{}, err
		}
	}
	for provider := 0; provider < (relationshipLayerCount-1)*relationshipLayerWidth; provider++ {
		layer, offset := provider/relationshipLayerWidth, provider%relationshipLayerWidth
		next := (layer + 1) * relationshipLayerWidth
		for slot, consumer := range []int{next + offset, next + (offset+1)%relationshipLayerWidth} {
			if err := addRPC(independentRPCEdge("layered_dag", provider, consumer, slot)); err != nil {
				return SetIdentity{}, SetIdentity{}, err
			}
		}
	}
	for provider := 0; provider < relationshipServiceCount; provider++ {
		if !boundedFanoutProvider(provider) {
			continue
		}
		for slot := 1; slot <= relationshipFanout; slot++ {
			if err := addRPC(independentRPCEdge(
				"bounded_fanout", provider, (provider+slot+1)%relationshipServiceCount, slot,
			)); err != nil {
				return SetIdentity{}, SetIdentity{}, err
			}
		}
	}
	for provider := 0; provider < relationshipServiceCount; provider += relationshipHotspotWidth {
		lookup := fmt.Sprintf("t421.hotspot.%04d", provider/relationshipHotspotWidth)
		if err := builder.add(oracleProductProjection{
			Schema: "t421-product-relationship-projection-v1", Kind: "kafka", Family: "hotspot",
			LookupKey: lookup, ServiceKey: serviceKey(provider), Participation: "producer",
		}); err != nil {
			return SetIdentity{}, SetIdentity{}, err
		}
		for consumer := provider + 1; consumer < provider+relationshipHotspotWidth; consumer++ {
			if err := builder.add(oracleProductProjection{
				Schema: "t421-product-relationship-projection-v1", Kind: "kafka", Family: "hotspot",
				LookupKey: lookup, ServiceKey: serviceKey(consumer), Participation: "consumer",
			}); err != nil {
				return SetIdentity{}, SetIdentity{}, err
			}
		}
	}
	result := builder.finish()
	if result.Records != 20_999 {
		return SetIdentity{}, SetIdentity{}, fmt.Errorf("T42.1 product relationship projections = %d, want 20999", result.Records)
	}
	rpc := rpcBuilder.finish()
	if rpc.Records != 10_999 {
		return SetIdentity{}, SetIdentity{}, fmt.Errorf("T42.1 RPC product projections = %d, want 10999", rpc.Records)
	}
	return result, rpc, nil
}

func serviceKey(index int) string {
	return fmt.Sprintf("svc.load-%05d", index)
}

func independentMemberships(index int) []servicecatalog.Membership {
	key := serviceKey(index)
	return []servicecatalog.Membership{
		{ServiceKey: key, Path: fmt.Sprintf("contracts/service-%05d/api.proto", index), Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase},
		{ServiceKey: key, Path: fmt.Sprintf("contracts/service-%05d/api.proto", index), Role: servicecatalog.RoleTyped, Origin: servicecatalog.OriginBase},
		{ServiceKey: key, Path: fmt.Sprintf("generated/shared/group-%04d/types.pb.go", index/10), Role: servicecatalog.RoleGenerated, Origin: servicecatalog.OriginBase},
		{ServiceKey: key, Path: fmt.Sprintf("services/service-%05d/api_grpc.pb.go", index), Role: servicecatalog.RoleGenerated, Origin: servicecatalog.OriginBase},
		{ServiceKey: key, Path: fmt.Sprintf("services/service-%05d/main.go", index), Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
		{ServiceKey: key, Path: fmt.Sprintf("shared/group-%04d/library.go", index/20), Role: servicecatalog.RoleShared, Origin: servicecatalog.OriginBase},
	}
}

func independentServiceQuery(index int) oracleServiceQuery {
	memberships := independentMemberships(index)
	paths := make([]oracleQueryPath, 0, 5)
	for _, membership := range memberships {
		if len(paths) == 0 || paths[len(paths)-1].Path != membership.Path {
			paths = append(paths, oracleQueryPath{Path: membership.Path})
		}
		paths[len(paths)-1].Roles = append(paths[len(paths)-1].Roles, oracleRole{
			Role: membership.Role, Origin: membership.Origin,
		})
	}
	return oracleServiceQuery{ServiceKey: serviceKey(index), Paths: paths}
}

func walkIndependentPlacements(visit func(oraclePlacement) error) error {
	if err := visit(oraclePlacement{
		Path: ".phebs", Unowned: true, UnownedOrigin: servicecatalog.OriginOverride,
		Claims: []oracleClaim{},
	}); err != nil {
		return err
	}
	for index := range combinedServices {
		if err := visit(ownedPlacement(
			fmt.Sprintf("contracts/service-%05d/api.proto", index), []int{index},
			[]string{servicecatalog.RoleSupporting, servicecatalog.RoleTyped},
		)); err != nil {
			return err
		}
	}
	if err := visit(oraclePlacement{
		Path: resolverinput.GeneratedFromSnapshotPath, Unowned: true,
		UnownedOrigin: servicecatalog.OriginBase, Claims: []oracleClaim{},
	}); err != nil {
		return err
	}
	for group := range combinedServices / 10 {
		if err := visit(ownedPlacement(
			fmt.Sprintf("generated/shared/group-%04d/types.pb.go", group),
			integerRange(group*10, 10), []string{servicecatalog.RoleGenerated},
		)); err != nil {
			return err
		}
	}
	if err := visit(oraclePlacement{
		Path: "go.mod", Unowned: true, UnownedOrigin: servicecatalog.OriginOverride,
		Claims: []oracleClaim{},
	}); err != nil {
		return err
	}
	if err := visit(oraclePlacement{
		Path: typedIndexPath, Unowned: true, UnownedOrigin: servicecatalog.OriginBase,
		Claims: []oracleClaim{},
	}); err != nil {
		return err
	}
	for index := range combinedServices {
		if err := visit(ownedPlacement(
			fmt.Sprintf("services/service-%05d/api_grpc.pb.go", index), []int{index},
			[]string{servicecatalog.RoleGenerated},
		)); err != nil {
			return err
		}
		if err := visit(ownedPlacement(
			fmt.Sprintf("services/service-%05d/main.go", index), []int{index},
			[]string{servicecatalog.RolePrimary},
		)); err != nil {
			return err
		}
	}
	for group := range combinedServices / 20 {
		if err := visit(ownedPlacement(
			fmt.Sprintf("shared/group-%04d/library.go", group),
			integerRange(group*20, 20), []string{servicecatalog.RoleShared},
		)); err != nil {
			return err
		}
	}
	if err := visit(oraclePlacement{
		Path: "structural", Unowned: true, UnownedOrigin: servicecatalog.OriginOverride,
		Claims: []oracleClaim{},
	}); err != nil {
		return err
	}
	for index := range combinedServices / 100 {
		if err := visit(oraclePlacement{
			Path: fmt.Sprintf("tools/unowned-%04d.go", index), Unowned: true,
			UnownedOrigin: servicecatalog.OriginBase, Claims: []oracleClaim{},
		}); err != nil {
			return err
		}
	}
	return nil
}

func ownedPlacement(path string, serviceIndexes []int, roles []string) oraclePlacement {
	claims := make([]oracleClaim, 0, len(serviceIndexes))
	for _, index := range serviceIndexes {
		claim := oracleClaim{
			ServiceKey: serviceKey(index), Disposition: servicecatalog.DispositionAccepted,
			Roles: make([]oracleRole, 0, len(roles)),
		}
		for _, role := range roles {
			claim.Roles = append(claim.Roles, oracleRole{Role: role, Origin: servicecatalog.OriginBase})
		}
		claims = append(claims, claim)
	}
	return oraclePlacement{Path: path, Claims: claims}
}

func integerRange(start, count int) []int {
	values := make([]int, count)
	for index := range count {
		values[index] = start + index
	}
	return values
}

type queryCaseSpec struct {
	name             string
	surface          string
	http             RequestSpec
	mcpTool          string
	parameters       []QueryParameter
	pageSize         uint64
	cursorRule       string
	records          uint64
	paths            uint64
	authorityFence   string
	canonicalization string
	projection       any
	authorization    string
	expectedStatus   uint64
	expectedMCPCode  string
}

type semanticSearchProjection struct {
	Schema            string   `json:"schema"`
	Scope             string   `json:"scope"`
	QueryIdentity     string   `json:"query_identity"`
	ReturnedOrdinals  []uint64 `json:"returned_ordinals"`
	StructuralProfile string   `json:"structural_profile_sha256,omitempty"`
	MembershipPosture string   `json:"membership_posture"`
}

type semanticDeniedProjection struct {
	Schema  string `json:"schema"`
	Posture string `json:"posture"`
}

type semanticServiceProjection struct {
	Schema  string             `json:"schema"`
	Service oracleServiceQuery `json:"service"`
}

type semanticPlacementProjection struct {
	Schema    string          `json:"schema"`
	Placement oraclePlacement `json:"placement"`
	Visible   bool            `json:"visible"`
}

type semanticRelationshipPair struct {
	Family   string `json:"family"`
	Provider string `json:"provider"`
	Consumer string `json:"consumer"`
}

type semanticRelationshipProjection struct {
	Schema        string                     `json:"schema"`
	ServiceKey    string                     `json:"service_key"`
	View          string                     `json:"view"`
	Kind          string                     `json:"kind"`
	Plane         string                     `json:"plane"`
	LookupKey     string                     `json:"lookup_key"`
	Participation string                     `json:"participation"`
	Pairs         []semanticRelationshipPair `json:"pairs"`
}

type semanticKafkaProjection struct {
	Schema             string `json:"schema"`
	ServiceKey         string `json:"service_key"`
	View               string `json:"view"`
	Kind               string `json:"kind"`
	Plane              string `json:"plane"`
	LookupKey          string `json:"lookup_key"`
	Participation      string `json:"participation"`
	ProductPairPosture string `json:"product_pair_posture"`
}

func frozenQueryCases() []QueryCase {
	return []QueryCase{
		queryCase(queryCaseSpec{
			name: "all_code_structural_marker", surface: "all_code_search",
			http: RequestSpec{
				Method: "GET",
				Path:   "/api/search?q=T401Fixture&scope=all_code&max_matches=1&context_lines=0",
			},
			mcpTool: "search_code",
			parameters: []QueryParameter{
				{Name: "query", Value: "T401Fixture"},
				{Name: "scope", Value: "all_code"},
				{Name: "max_matches", Value: "1"},
				{Name: "context_lines", Value: "0"},
			},
			cursorRule: "not_paginated;cursor_must_be_absent", records: 1, paths: 1,
			authorityFence:   "authorize_before_read;confirm_physical_a-return_and_search_current_after_read",
			canonicalization: "repository,path,start_line,start_column,end_line,end_column:asc;runtime_oids_excluded",
			projection: semanticSearchProjection{
				Schema: "t421-semantic-search-projection-v1", Scope: "all_code",
				QueryIdentity:     "structural_marker",
				ReturnedOrdinals:  []uint64{0},
				StructuralProfile: "sha256:4227b0a75cc6a2cf1120e5d9e4c228fe23c0dbc2261313f513b6ae809364d430",
				MembershipPosture: "all_physical_paths",
			},
		}),
		queryCase(queryCaseSpec{
			name: "hidden_repository_denied", surface: "all_code_search",
			http:    RequestSpec{Method: "GET", Path: "/api/search?q=T401Fixture&scope=all_code&repository=$hidden_repository&max_matches=1&context_lines=0"},
			mcpTool: "search_code",
			parameters: []QueryParameter{
				{Name: "query", Value: "T401Fixture"}, {Name: "scope", Value: "all_code"},
				{Name: "repository", Value: "$hidden_repository"}, {Name: "max_matches", Value: "1"},
				{Name: "context_lines", Value: "0"},
			},
			authorization: "t421-authorized-hidden-v1", expectedStatus: 404,
			expectedMCPCode: "unknown_repository", cursorRule: "not_paginated;cursor_must_be_absent",
			records: 0, paths: 0,
			authorityFence:   "authorize_before_read;no_repository_or_generation_read;confirm_current_authority_unchanged",
			canonicalization: "existence-hiding-public-error-shape-v1;repository-identity-excluded",
			projection:       semanticDeniedProjection{Schema: "t421-semantic-denied-projection-v1", Posture: "unknown_repository"},
		}),
		queryCase(queryCaseSpec{
			name: "first_service", surface: "service_detail",
			http: RequestSpec{
				Method: "GET",
				Path:   "/api/service?repository=$authorized_repository&service_key=$accepted_service_00000",
			},
			mcpTool: "get_service",
			parameters: []QueryParameter{
				{Name: "repository", Value: "$authorized_repository"},
				{Name: "service_key", Value: "$accepted_service_00000"},
			},
			cursorRule: "not_paginated;cursor_must_be_absent", records: 6, paths: 5,
			authorityFence:   "authorize_before_read;confirm_a-return_catalog_and_service_state_after_read",
			canonicalization: "path,role,origin:asc;runtime_oids_excluded",
			projection: semanticServiceProjection{
				Schema: "t421-semantic-service-projection-v1", Service: independentServiceQuery(0),
			},
		}),
		queryCase(queryCaseSpec{
			name: "shared_placement_service_scope", surface: "service_search",
			http: RequestSpec{
				Method: "GET",
				Path:   "/api/search?q=FixturePath%20file%3A%5Eshared%2Fgroup-0000%2Flibrary%5C.go%24&scope=service&repository=$authorized_repository&service_key=$accepted_service_00000&max_matches=1&context_lines=0",
			},
			mcpTool: "search_code",
			parameters: []QueryParameter{
				{Name: "query", Value: `FixturePath file:^shared/group-0000/library\.go$`},
				{Name: "scope", Value: "service"},
				{Name: "repository", Value: "$authorized_repository"},
				{Name: "service_key", Value: "$accepted_service_00000"},
				{Name: "max_matches", Value: "1"},
				{Name: "context_lines", Value: "0"},
			},
			cursorRule: "not_paginated;cursor_must_be_absent", records: 1, paths: 1,
			authorityFence:   "authorize_before_read;confirm_a-return_search_catalog_and_service_state_after_read",
			canonicalization: "repository,path,start_line,start_column,end_line,end_column:asc;runtime_oids_excluded",
			projection: semanticPlacementProjection{
				Schema: "t421-semantic-placement-projection-v1",
				Placement: ownedPlacement(
					"shared/group-0000/library.go", integerRange(0, 20),
					[]string{servicecatalog.RoleShared},
				),
				Visible: true,
			},
		}),
		queryCase(queryCaseSpec{
			name: "unowned_excluded_from_service_scope", surface: "service_search",
			http: RequestSpec{
				Method: "GET",
				Path:   "/api/search?q=FixturePath%20file%3A%5Etools%2Funowned-0000%5C.go%24&scope=service&repository=$authorized_repository&service_key=$accepted_service_00000&max_matches=1&context_lines=0",
			},
			mcpTool: "search_code",
			parameters: []QueryParameter{
				{Name: "query", Value: `FixturePath file:^tools/unowned-0000\.go$`},
				{Name: "scope", Value: "service"},
				{Name: "repository", Value: "$authorized_repository"},
				{Name: "service_key", Value: "$accepted_service_00000"},
				{Name: "max_matches", Value: "1"},
				{Name: "context_lines", Value: "0"},
			},
			cursorRule: "not_paginated;cursor_must_be_absent", records: 0, paths: 0,
			authorityFence:   "authorize_before_read;confirm_a-return_search_catalog_and_service_state_after_read",
			canonicalization: "empty_semantic_result;runtime_oids_excluded",
			projection: semanticPlacementProjection{
				Schema: "t421-semantic-placement-projection-v1",
				Placement: oraclePlacement{
					Path: "tools/unowned-0000.go", Unowned: true,
					UnownedOrigin: servicecatalog.OriginBase, Claims: []oracleClaim{},
				},
				Visible: false,
			},
		}),
		queryCase(relationshipQueryCase(
			"chain_dependency", "$accepted_service_00001", "dependencies", "grpc",
			"/t421.service00000.Service00000/BoundedFanoutP00000", 1, 2,
			semanticRPCProjection(1, "dependencies", []relationshipEdge{
				independentRPCEdge("chain", 0, 1, 0),
			}),
		)),
		queryCase(relationshipQueryCase(
			"chain_callers", "$accepted_service_00000", "callers", "grpc",
			"/t421.service00000.Service00000/BoundedFanoutP00000", 9, 10,
			semanticChainCallerProjection(),
		)),
		queryCase(relationshipQueryCase(
			"layered_dag_dependency", "$accepted_service_00100", "dependencies", "grpc",
			"/t421.service00000.Service00000/LayeredDagP00000", 1, 2,
			semanticRPCProjection(100, "dependencies", []relationshipEdge{
				independentRPCEdge("layered_dag", 0, 100, 0),
			}),
		)),
		queryCase(relationshipQueryCase(
			"bounded_fanout_dependency", "$accepted_service_00002", "dependencies", "grpc",
			"/t421.service00000.Service00000/BoundedFanoutP00000", 1, 2,
			semanticRPCProjection(2, "dependencies", []relationshipEdge{
				independentRPCEdge("bounded_fanout", 0, 2, 1),
			}),
		)),
		queryCase(relationshipQueryCase(
			"kafka_producer_topic", "$accepted_service_00000", "topics", "producer",
			"t421.hotspot.0000", 1, 1,
			semanticKafkaProjection{
				Schema: "t421-semantic-kafka-product-projection-v1", ServiceKey: serviceKey(0),
				View: "topics", Kind: "kafka", Plane: "producer", LookupKey: "t421.hotspot.0000",
				Participation: "source", ProductPairPosture: "independent_projection_not_product_cooccurrence",
			},
		)),
		queryCase(relationshipQueryCase(
			"kafka_consumer_topic", "$accepted_service_00001", "topics", "consumer",
			"t421.hotspot.0000", 1, 1,
			semanticKafkaProjection{
				Schema: "t421-semantic-kafka-product-projection-v1", ServiceKey: serviceKey(1),
				View: "topics", Kind: "kafka", Plane: "consumer", LookupKey: "t421.hotspot.0000",
				Participation: "source", ProductPairPosture: "independent_projection_not_product_cooccurrence",
			},
		)),
	}
}

func relationshipQueryCase(
	name, service, view, plane, lookup string,
	records, paths uint64,
	projection any,
) queryCaseSpec {
	kind := relationshipKind(plane)
	return queryCaseSpec{
		name: name, surface: "service_relationships",
		http: RequestSpec{
			Method: "GET",
			Path: "/api/service-relationships?repository=$authorized_repository" +
				"&service_key=" + service + "&view=" + view + "&kind=" + kind +
				"&plane=" + plane + "&lookup_key=" + lookup + "&page_size=1",
		},
		mcpTool: "list_service_relationships",
		parameters: []QueryParameter{
			{Name: "repositories", Value: `["$authorized_repository"]`},
			{Name: "service_key", Value: service},
			{Name: "view", Value: view},
			{Name: "kind", Value: kind},
			{Name: "plane", Value: plane},
			{Name: "lookup_key", Value: lookup},
			{Name: "page_size", Value: "1"},
		},
		pageSize: 1,
		cursorRule: "first_request_omits_cursor;append_returned_opaque_cursor_last;" +
			"require_expected_status_each_page;stop_only_when_next_cursor_empty",
		records: records, paths: paths,
		authorityFence: "authorize_before_first_page;pin_a-return_relationship_root;" +
			"confirm_authorization_catalog_service_and_relationship_authority_after_last_page",
		canonicalization: "transport=repository,reference_digest:asc;" +
			"semantic=kind,plane,lookup_key,provider,consumer,participation:asc;runtime_oids_excluded",
		projection: projection,
	}
}

func relationshipKind(plane string) string {
	if plane == "producer" || plane == "consumer" {
		return "kafka"
	}
	return "rpc"
}

func semanticRPCProjection(
	serviceIndex int,
	view string,
	edges []relationshipEdge,
) semanticRelationshipProjection {
	pairs := make([]semanticRelationshipPair, 0, len(edges))
	for _, edge := range edges {
		pairs = append(pairs, semanticRelationshipPair{
			Family: edge.family, Provider: serviceKey(edge.provider), Consumer: serviceKey(edge.consumer),
		})
	}
	participation := "source"
	if view == "callers" {
		participation = "target"
	}
	lookup := ""
	if len(edges) != 0 {
		lookup = edges[0].identity
	}
	return semanticRelationshipProjection{
		Schema: "t421-semantic-rpc-product-projection-v1", ServiceKey: serviceKey(serviceIndex),
		View: view, Kind: "rpc", Plane: "grpc", LookupKey: lookup,
		Participation: participation, Pairs: pairs,
	}
}

func semanticChainCallerProjection() semanticRelationshipProjection {
	edges := make([]relationshipEdge, 0, relationshipFanout+1)
	edges = append(edges, independentRPCEdge("chain", 0, 1, 0))
	for slot := 1; slot <= relationshipFanout; slot++ {
		edges = append(edges, independentRPCEdge("bounded_fanout", 0, slot+1, slot))
	}
	return semanticRPCProjection(0, "callers", edges)
}

func queryCase(spec queryCaseSpec) QueryCase {
	projectionRaw, err := json.Marshal(spec.projection)
	if err != nil {
		panic(err)
	}
	authorization := spec.authorization
	if authorization == "" {
		authorization = "t421-authorized-visible-v1"
	}
	status := spec.expectedStatus
	if status == 0 {
		status = 200
	}
	mcpCode := spec.expectedMCPCode
	if mcpCode == "" {
		mcpCode = "ok"
	}
	return QueryCase{
		Name: spec.name, Surface: spec.surface, Revision: "a-return",
		Authorization: authorization, HTTP: spec.http,
		MCPTool: spec.mcpTool, Parameters: slices.Clone(spec.parameters),
		PageSize: spec.pageSize, CursorRule: spec.cursorRule,
		ExpectedStatus: status, ExpectedMCPCode: mcpCode,
		ExpectedRecords: spec.records, ExpectedPaths: spec.paths,
		AuthorityFence: spec.authorityFence, Canonicalization: spec.canonicalization,
		ProjectionSHA256: SHA256(projectionRaw),
	}
}

func deriveAuthoredOracle(catalog servicecatalog.Catalog) (Oracle, error) {
	services := slices.Clone(catalog.Services)
	slices.SortFunc(services, func(left, right servicecatalog.Service) int {
		return compareStrings(left.Key, right.Key)
	})
	memberships := slices.Clone(catalog.Memberships)
	slices.SortFunc(memberships, compareMemberships)
	unowned := slices.Clone(catalog.Unowned)
	slices.SortFunc(unowned, func(left, right servicecatalog.UnownedPlacement) int {
		return compareStrings(left.Path, right.Path)
	})

	catalogIdentity := newIdentityBuilder("t421-independent-catalog-v1")
	membershipIdentity := newIdentityBuilder("t421-independent-memberships-v1")
	placementIdentity := newIdentityBuilder("t421-independent-placements-v1")
	unownedPrefixIdentity := newIdentityBuilder("t421-independent-unowned-prefixes-v1")
	queryIdentity := newIdentityBuilder("t421-independent-service-queries-v1")
	for _, service := range services {
		if err := catalogIdentity.add(service); err != nil {
			return Oracle{}, err
		}
	}
	for _, membership := range memberships {
		if err := membershipIdentity.add(membership); err != nil {
			return Oracle{}, err
		}
	}

	dispositions := make(map[string]string, len(services))
	queries := make(map[string][]oracleQueryPath, len(services))
	placements := make(map[string]*oraclePlacement, len(memberships)+len(unowned))
	for _, service := range services {
		dispositions[service.Key] = service.Disposition
	}
	for _, membership := range memberships {
		placement := placements[membership.Path]
		if placement == nil {
			placement = &oraclePlacement{Path: membership.Path, Claims: []oracleClaim{}}
			placements[membership.Path] = placement
		}
		claimIndex := slices.IndexFunc(placement.Claims, func(claim oracleClaim) bool {
			return claim.ServiceKey == membership.ServiceKey
		})
		if claimIndex < 0 {
			placement.Claims = append(placement.Claims, oracleClaim{
				ServiceKey: membership.ServiceKey, Disposition: dispositions[membership.ServiceKey],
				Roles: []oracleRole{},
			})
			claimIndex = len(placement.Claims) - 1
		}
		placement.Claims[claimIndex].Roles = append(placement.Claims[claimIndex].Roles, oracleRole{
			Role: membership.Role, Origin: membership.Origin,
		})

		servicePaths := queries[membership.ServiceKey]
		if len(servicePaths) == 0 || servicePaths[len(servicePaths)-1].Path != membership.Path {
			servicePaths = append(servicePaths, oracleQueryPath{Path: membership.Path})
		}
		servicePaths[len(servicePaths)-1].Roles = append(servicePaths[len(servicePaths)-1].Roles, oracleRole{
			Role: membership.Role, Origin: membership.Origin,
		})
		queries[membership.ServiceKey] = servicePaths
	}
	for _, placement := range unowned {
		if placements[placement.Path] != nil {
			return Oracle{}, errors.New("T42.1 authored path is both owned and unowned")
		}
		placements[placement.Path] = &oraclePlacement{
			Path: placement.Path, Unowned: true, UnownedOrigin: placement.Origin,
			Claims: []oracleClaim{},
		}
		if placement.Origin == servicecatalog.OriginOverride {
			if err := unownedPrefixIdentity.add(placement); err != nil {
				return Oracle{}, err
			}
		}
	}
	paths := make([]string, 0, len(placements))
	for path := range placements {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := placementIdentity.add(*placements[path]); err != nil {
			return Oracle{}, err
		}
	}
	for _, service := range services {
		if err := queryIdentity.add(oracleServiceQuery{
			ServiceKey: service.Key, Paths: queries[service.Key],
		}); err != nil {
			return Oracle{}, err
		}
	}
	return Oracle{
		Schema: OracleSchema, Independent: false, ConsumesPhebsResults: false,
		Catalog: catalogIdentity.finish(), Memberships: membershipIdentity.finish(),
		Placements: placementIdentity.finish(), UnownedPrefixes: unownedPrefixIdentity.finish(),
		ServiceQueries: queryIdentity.finish(),
	}, nil
}

func compareMemberships(left, right servicecatalog.Membership) int {
	for _, pair := range [][2]string{
		{left.ServiceKey, right.ServiceKey}, {left.Path, right.Path},
		{left.Role, right.Role}, {left.Origin, right.Origin},
	} {
		if comparison := compareStrings(pair[0], pair[1]); comparison != 0 {
			return comparison
		}
	}
	return 0
}

func compareStrings(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
