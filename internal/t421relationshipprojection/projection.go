// Package t421relationshipprojection derives the frozen T42.1 relationship
// semantics from one completely validated relationship V3 snapshot.
package t421relationshipprojection

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"slices"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
)

const (
	serviceCount = 10_000

	productDomain           = "t421-independent-product-relationship-projections-v1"
	productCanonicalization = "family_order=chain,layered_dag,bounded_fanout,hotspot;" +
		"provider,slot,consumer;hotspot_producer_before_consumers;" +
		"runtime_generation_ids_excluded"
)

var ErrInvalid = errors.New("invalid T42.1 relationship projection")

// FamilySummary is the exact source-free semantic result for one frozen
// relationship family.
type FamilySummary struct {
	Name                     string `json:"name"`
	SemanticPairEdges        uint64 `json:"semantic_pair_edges"`
	MaxInDegree              uint64 `json:"max_in_degree"`
	MaxOutDegree             uint64 `json:"max_out_degree"`
	Acyclic                  bool   `json:"acyclic"`
	ObservedEdgesFramedBytes uint64 `json:"observed_edges_framed_bytes"`
	ObservedEdgesSHA256      string `json:"observed_edges_sha256"`
}

// ProductSummary is the exact source-free product projection result. Kafka
// pair rows are the semantic producer-consumer pairs, never a product-side
// cooccurrence expansion.
type ProductSummary struct {
	RPCProjections           uint64 `json:"rpc_projections"`
	KafkaProducerProjections uint64 `json:"kafka_producer_projections"`
	KafkaConsumerProjections uint64 `json:"kafka_consumer_projections"`
	KafkaPairRows            uint64 `json:"kafka_pair_rows"`
	TotalProjections         uint64 `json:"total_projections"`
	ServiceReferences        uint64 `json:"service_references"`
	Canonicalization         string `json:"canonicalization"`
	ProjectionRecords        uint64 `json:"projection_records"`
	ProjectionFramedBytes    uint64 `json:"projection_framed_bytes"`
	ProjectionSHA256         string `json:"projection_sha256"`
}

// Result contains the fixed-order family and product summaries.
type Result struct {
	Families []FamilySummary `json:"families"`
	Product  ProductSummary  `json:"product"`
}

type familySpec struct {
	name       string
	protocol   string
	count      uint64
	maxIn      uint64
	maxOut     uint64
	acyclic    bool
	framed     uint64
	sha256     string
	familyRank int
}

var familySpecs = [...]familySpec{
	{name: "chain", protocol: "grpc", count: 9_999, maxIn: 1, maxOut: 1, acyclic: true,
		framed: 2_019_798, sha256: "sha256:4005851e0c736f68e235391bf6091a52403834e5cf738d77202e82b0a2460a0d", familyRank: 0},
	{name: "layered_dag", protocol: "grpc", count: 200, maxIn: 2, maxOut: 2, acyclic: true,
		framed: 41_000, sha256: "sha256:e9b8ca8085dc642210ba3d8b9749c41ae8ccf2edada1566b21eba2760c4b556d", familyRank: 1},
	{name: "bounded_fanout", protocol: "grpc", count: 800, maxIn: 8, maxOut: 8, acyclic: false,
		framed: 168_800, sha256: "sha256:fd9b536c3a9af8f2f2e8328ba028c2d31577555c07520bd958f810f62f02c032", familyRank: 2},
	{name: "hotspot", protocol: "kafka", count: 9_500, maxIn: 1, maxOut: 19, acyclic: true,
		framed: 1_624_500, sha256: "sha256:e7fa77d603be93c31874718b1c6d1cbed9d68665b5bcc5526129086307baa3b7", familyRank: 3},
}

type edge struct {
	familyRank int
	family     string
	protocol   string
	identity   string
	provider   int
	consumer   int
	slot       int
}

type edgeRecord struct {
	Schema   string `json:"schema"`
	Family   string `json:"family"`
	Protocol string `json:"protocol"`
	Identity string `json:"identity"`
	Provider string `json:"provider"`
	Consumer string `json:"consumer"`
}

type productRecord struct {
	Schema        string `json:"schema"`
	Kind          string `json:"kind"`
	Family        string `json:"family"`
	LookupKey     string `json:"lookup_key"`
	ServiceKey    string `json:"service_key"`
	ConsumerKey   string `json:"consumer_key,omitempty"`
	Participation string `json:"participation"`
}

type productItem struct {
	record     productRecord
	familyRank int
	provider   int
	slot       int
	consumer   int
}

type derivation struct {
	edges          [len(familySpecs)][]edge
	product        []productItem
	seenProjection map[string]struct{}
	seenSemantic   map[string]struct{}
	kafkaSlots     [serviceCount / 20][20]bool
	rpcCount       uint64
	kafkaProducers uint64
	kafkaConsumers uint64
	references     uint64
}

// Derive reduces one fully validated SemanticSnapshotV3 projection inventory
// to the exact frozen T42.1 semantic result. Runtime publication identities do
// not participate in the returned canonical hashes.
func Derive(ctx context.Context, projections []relationshippublication.Projection) (Result, error) {
	if ctx == nil {
		return Result{}, invalid("context")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(projections) != 20_999 {
		return Result{}, invalid("projection cardinality")
	}
	state := derivation{
		product:        make([]productItem, 0, len(projections)),
		seenProjection: make(map[string]struct{}, len(projections)),
		seenSemantic:   make(map[string]struct{}, len(projections)),
	}
	for index := range projections {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		if err := state.add(projections[index]); err != nil {
			return Result{}, err
		}
	}
	return state.finish(ctx)
}

func (state *derivation) add(value relationshippublication.Projection) error {
	if value.Schema != relationshippublication.ProjectionSchema ||
		!validDigest(value.PostingDigest) || !validDigest(value.Digest) {
		return invalid("projection envelope")
	}
	if _, duplicate := state.seenProjection[value.Digest]; duplicate {
		return invalid("duplicate projection")
	}
	state.seenProjection[value.Digest] = struct{}{}

	switch value.Kind {
	case "rpc":
		return state.addRPC(value)
	case "kafka":
		return state.addKafka(value)
	default:
		return invalid("projection kind")
	}
}

func (state *derivation) addRPC(value relationshippublication.Projection) error {
	if value.Class != "resolved" || value.Plane != "grpc" || value.Target == nil {
		return invalid("RPC projection shape")
	}
	consumer, err := exactServicePlacement(
		value.Source, "services/service-", "/main.go",
		[]relationshippublication.RoleClaim{{Role: "primary", Origin: "base"}},
	)
	if err != nil {
		return err
	}
	provider, err := exactServicePlacement(
		*value.Target, "contracts/service-", "/api.proto",
		[]relationshippublication.RoleClaim{
			{Role: "supporting", Origin: "base"},
			{Role: "typed", Origin: "base"},
		},
	)
	if err != nil {
		return err
	}

	family, rank, slot, err := rpcGeometry(value.LookupKey, provider, consumer)
	if err != nil {
		return err
	}
	semanticKey := fmt.Sprintf("rpc\x00%d\x00%d\x00%d\x00%d", rank, provider, consumer, slot)
	if _, duplicate := state.seenSemantic[semanticKey]; duplicate {
		return invalid("duplicate RPC semantic edge")
	}
	state.seenSemantic[semanticKey] = struct{}{}
	item := edge{
		familyRank: rank, family: family, protocol: "grpc", identity: value.LookupKey,
		provider: provider, consumer: consumer, slot: slot,
	}
	state.edges[rank] = append(state.edges[rank], item)
	state.product = append(state.product, productItem{
		record: productRecord{
			Schema: "t421-product-relationship-projection-v1", Kind: "rpc", Family: family,
			LookupKey: value.LookupKey, ServiceKey: serviceKey(provider),
			ConsumerKey: serviceKey(consumer), Participation: "provider_consumer",
		},
		familyRank: rank, provider: provider, slot: slot, consumer: consumer,
	})
	state.rpcCount++
	state.references += 2
	return nil
}

func (state *derivation) addKafka(value relationshippublication.Projection) error {
	if value.Class != "literal" || value.Target != nil ||
		(value.Plane != "producer" && value.Plane != "consumer") {
		return invalid("Kafka projection shape")
	}
	service, err := exactServicePlacement(
		value.Source, "services/service-", "/main.go",
		[]relationshippublication.RoleClaim{{Role: "primary", Origin: "base"}},
	)
	if err != nil {
		return err
	}
	group, err := parseHotspot(value.LookupKey)
	if err != nil {
		return err
	}
	base := group * 20
	slot := service - base
	if value.Plane == "producer" && slot != 0 ||
		value.Plane == "consumer" && (slot < 1 || slot >= 20) {
		return invalid("Kafka service geometry")
	}
	if state.kafkaSlots[group][slot] {
		return invalid("duplicate Kafka projection")
	}
	state.kafkaSlots[group][slot] = true
	semanticKey := fmt.Sprintf("kafka\x00%d\x00%d", group, slot)
	if _, duplicate := state.seenSemantic[semanticKey]; duplicate {
		return invalid("duplicate Kafka semantic projection")
	}
	state.seenSemantic[semanticKey] = struct{}{}
	state.product = append(state.product, productItem{
		record: productRecord{
			Schema: "t421-product-relationship-projection-v1", Kind: "kafka", Family: "hotspot",
			LookupKey: value.LookupKey, ServiceKey: serviceKey(service), Participation: value.Plane,
		},
		familyRank: 3, provider: base, slot: slot, consumer: service,
	})
	if slot == 0 {
		state.kafkaProducers++
	} else {
		state.kafkaConsumers++
		state.edges[3] = append(state.edges[3], edge{
			familyRank: 3, family: "hotspot", protocol: "kafka", identity: value.LookupKey,
			provider: base, consumer: service, slot: slot,
		})
	}
	state.references++
	return nil
}

func (state *derivation) finish(ctx context.Context) (Result, error) {
	for group := range state.kafkaSlots {
		for slot := range state.kafkaSlots[group] {
			if !state.kafkaSlots[group][slot] {
				return Result{}, invalid("incomplete Kafka group")
			}
		}
	}
	result := Result{Families: make([]FamilySummary, len(familySpecs))}
	for index, spec := range familySpecs {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		summary, err := summarizeFamily(ctx, spec, state.edges[index])
		if err != nil {
			return Result{}, err
		}
		result.Families[index] = summary
	}
	product, err := state.summarizeProduct(ctx)
	if err != nil {
		return Result{}, err
	}
	result.Product = product
	return result, nil
}

func summarizeFamily(ctx context.Context, spec familySpec, values []edge) (FamilySummary, error) {
	if uint64(len(values)) != spec.count {
		return FamilySummary{}, invalid("relationship family cardinality")
	}
	slices.SortFunc(values, func(left, right edge) int {
		if left.provider != right.provider {
			return left.provider - right.provider
		}
		if left.consumer != right.consumer {
			return left.consumer - right.consumer
		}
		return left.slot - right.slot
	})
	identity := newFramedIdentity("")
	inDegree := [serviceCount]uint16{}
	outDegree := [serviceCount]uint16{}
	for index, value := range values {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return FamilySummary{}, err
			}
		}
		if value.family != spec.name || value.protocol != spec.protocol ||
			value.familyRank != spec.familyRank ||
			spec.acyclic && value.provider >= value.consumer {
			return FamilySummary{}, invalid("relationship family edge")
		}
		if index > 0 && sameEdge(values[index-1], value) {
			return FamilySummary{}, invalid("duplicate relationship family edge")
		}
		if inDegree[value.consumer] == ^uint16(0) || outDegree[value.provider] == ^uint16(0) {
			return FamilySummary{}, invalid("relationship degree overflow")
		}
		inDegree[value.consumer]++
		outDegree[value.provider]++
		if err := identity.add(edgeRecord{
			Schema: "t421-relationship-edge-v1", Family: value.family,
			Protocol: value.protocol, Identity: value.identity,
			Provider: serviceKey(value.provider), Consumer: serviceKey(value.consumer),
		}); err != nil {
			return FamilySummary{}, err
		}
	}
	var maxIn, maxOut uint64
	for index := range serviceCount {
		maxIn = max(maxIn, uint64(inDegree[index]))
		maxOut = max(maxOut, uint64(outDegree[index]))
	}
	finished := identity.finish()
	if finished.records != spec.count || finished.bytes != spec.framed ||
		finished.sha256 != spec.sha256 || maxIn != spec.maxIn || maxOut != spec.maxOut {
		return FamilySummary{}, invalid("noncanonical relationship family inventory")
	}
	return FamilySummary{
		Name: spec.name, SemanticPairEdges: finished.records,
		MaxInDegree: maxIn, MaxOutDegree: maxOut, Acyclic: spec.acyclic,
		ObservedEdgesFramedBytes: finished.bytes, ObservedEdgesSHA256: finished.sha256,
	}, nil
}

func (state *derivation) summarizeProduct(ctx context.Context) (ProductSummary, error) {
	if state.rpcCount != 10_999 || state.kafkaProducers != 500 ||
		state.kafkaConsumers != 9_500 || state.references != 31_998 ||
		len(state.product) != 20_999 {
		return ProductSummary{}, invalid("product projection cardinality")
	}
	slices.SortFunc(state.product, func(left, right productItem) int {
		if left.familyRank != right.familyRank {
			return left.familyRank - right.familyRank
		}
		if left.provider != right.provider {
			return left.provider - right.provider
		}
		if left.slot != right.slot {
			return left.slot - right.slot
		}
		return left.consumer - right.consumer
	})
	identity := newFramedIdentity(productDomain)
	for index, item := range state.product {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return ProductSummary{}, err
			}
		}
		if err := identity.add(item.record); err != nil {
			return ProductSummary{}, err
		}
	}
	finished := identity.finish()
	if finished.records != 20_999 || finished.bytes != 4_673_604 ||
		finished.sha256 != "sha256:742f20fff1ca76f036b1114f5e2d556682b642e3257ab9c1ebba794dfe66653d" {
		return ProductSummary{}, invalid("noncanonical product projection inventory")
	}
	return ProductSummary{
		RPCProjections:           state.rpcCount,
		KafkaProducerProjections: state.kafkaProducers,
		KafkaConsumerProjections: state.kafkaConsumers,
		KafkaPairRows:            uint64(len(state.edges[3])), TotalProjections: uint64(len(state.product)),
		ServiceReferences: state.references, Canonicalization: productCanonicalization,
		ProjectionRecords: finished.records, ProjectionFramedBytes: finished.bytes,
		ProjectionSHA256: finished.sha256,
	}, nil
}

func rpcGeometry(lookup string, provider, consumer int) (string, int, int, error) {
	bounded := fmt.Sprintf(
		"/t421.service%05d.Service%05d/BoundedFanoutP%05d",
		provider, provider, provider,
	)
	if lookup == bounded {
		delta := (consumer - provider + serviceCount) % serviceCount
		if delta == 1 && provider < serviceCount-1 {
			return "chain", 0, 0, nil
		}
		if delta >= 2 && delta <= 9 && (provider < 50 || provider >= serviceCount-50) {
			return "bounded_fanout", 2, delta - 1, nil
		}
		return "", 0, 0, invalid("bounded RPC geometry")
	}
	layered := fmt.Sprintf(
		"/t421.service%05d.Service%05d/LayeredDagP%05d",
		provider, provider, provider,
	)
	if lookup != layered || provider < 0 || provider >= 100 {
		return "", 0, 0, invalid("RPC operation")
	}
	switch consumer {
	case 100 + provider:
		return "layered_dag", 1, 0, nil
	case 100 + (provider+1)%100:
		return "layered_dag", 1, 1, nil
	default:
		return "", 0, 0, invalid("layered RPC geometry")
	}
}

func exactServicePlacement(
	value relationshippublication.Placement,
	prefix, suffix string,
	roles []relationshippublication.RoleClaim,
) (int, error) {
	service, err := parseOrdinal(value.Path, prefix, suffix)
	if err != nil || value.Unowned || len(value.Claims) != 1 {
		return 0, invalid("service placement")
	}
	claim := value.Claims[0]
	if claim.ServiceKey != serviceKey(service) || claim.Disposition != "accepted" ||
		!slices.Equal(claim.Roles, roles) {
		return 0, invalid("accepted service placement")
	}
	return service, nil
}

func parseOrdinal(value, prefix, suffix string) (int, error) {
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return 0, invalid("service path")
	}
	ordinal := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
	if len(ordinal) != 5 || value != prefix+ordinal+suffix {
		return 0, invalid("service path ordinal")
	}
	for _, character := range ordinal {
		if character < '0' || character > '9' {
			return 0, invalid("service path ordinal")
		}
	}
	parsed, err := strconv.Atoi(ordinal)
	if err != nil || parsed < 0 || parsed >= serviceCount {
		return 0, invalid("service path range")
	}
	return parsed, nil
}

func parseHotspot(value string) (int, error) {
	const prefix = "t421.hotspot."
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+4 {
		return 0, invalid("Kafka topic")
	}
	ordinal := strings.TrimPrefix(value, prefix)
	for _, character := range ordinal {
		if character < '0' || character > '9' {
			return 0, invalid("Kafka topic")
		}
	}
	group, err := strconv.Atoi(ordinal)
	if err != nil || group < 0 || group >= serviceCount/20 ||
		value != fmt.Sprintf("t421.hotspot.%04d", group) {
		return 0, invalid("Kafka topic")
	}
	return group, nil
}

func serviceKey(value int) string { return fmt.Sprintf("svc.load-%05d", value) }

func sameEdge(left, right edge) bool {
	return left.familyRank == right.familyRank && left.provider == right.provider &&
		left.consumer == right.consumer && left.slot == right.slot
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func invalid(part string) error { return fmt.Errorf("%s: %w", part, ErrInvalid) }

type identityResult struct {
	records uint64
	bytes   uint64
	sha256  string
}

type framedIdentity struct {
	hash    hash.Hash
	records uint64
	bytes   uint64
}

func newFramedIdentity(domain string) *framedIdentity {
	result := &framedIdentity{hash: sha256.New()}
	if domain != "" {
		result.writeFrame([]byte(domain))
	}
	return result
}

func (identity *framedIdentity) add(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	identity.writeFrame(raw)
	identity.records++
	return nil
}

func (identity *framedIdentity) writeFrame(raw []byte) {
	var frame [8]byte
	binary.BigEndian.PutUint64(frame[:], uint64(len(raw)))
	_, _ = identity.hash.Write(frame[:])
	_, _ = identity.hash.Write(raw)
	identity.bytes += uint64(len(frame) + len(raw))
}

func (identity *framedIdentity) finish() identityResult {
	return identityResult{
		records: identity.records, bytes: identity.bytes,
		sha256: "sha256:" + hex.EncodeToString(identity.hash.Sum(nil)),
	}
}
