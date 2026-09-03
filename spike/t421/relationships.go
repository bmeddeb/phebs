package t421

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"hash"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	relationshipServiceCount = 10_000
	relationshipLayerWidth   = 100
	relationshipLayerCount   = 2
	relationshipFanout       = 8
	relationshipFanoutSpan   = 50
	relationshipHotspotWidth = 20

	combinedModulePath = "example.invalid/t401-neutral"
)

type relationshipEdge struct {
	family   string
	protocol string
	identity string
	provider int
	consumer int
	slot     int
}

type relationshipEdgeRecord struct {
	Schema   string `json:"schema"`
	Family   string `json:"family"`
	Protocol string `json:"protocol"`
	Identity string `json:"identity"`
	Provider string `json:"provider"`
	Consumer string `json:"consumer"`
}

type relationshipAccumulator struct {
	name         string
	seed         string
	protocol     string
	acyclic      bool
	digest       hash.Hash
	records      uint64
	framedBytes  uint64
	inDegree     [relationshipServiceCount]uint16
	outDegree    [relationshipServiceCount]uint16
	havePrevious bool
	previous     relationshipEdge
}

type rpcCall struct {
	family   string
	provider int
	consumer int
	slot     int
	method   string
}

func combinedOverlayFile(path string, original []byte) (combinedPath string, content []byte, relationship bool, err error) {
	switch {
	case strings.HasPrefix(path, "contracts/service-") && strings.HasSuffix(path, "/api.proto"):
		service, parseErr := parseRelationshipServicePath(path, "contracts/service-", "/api.proto")
		if parseErr != nil {
			return "", nil, false, parseErr
		}
		return path, renderRelationshipProto(service), true, nil
	case strings.HasPrefix(path, "generated/service-") && strings.HasSuffix(path, "/client.pb.go"):
		service, parseErr := parseRelationshipServicePath(path, "generated/service-", "/client.pb.go")
		if parseErr != nil {
			return "", nil, false, parseErr
		}
		generatedPath := fmt.Sprintf("services/service-%05d/api_grpc.pb.go", service)
		generated, renderErr := renderRelationshipGRPC(service)
		if renderErr != nil {
			return "", nil, false, renderErr
		}
		return generatedPath, generated, true, nil
	case strings.HasPrefix(path, "services/service-") && strings.HasSuffix(path, "/main.go"):
		service, parseErr := parseRelationshipServicePath(path, "services/service-", "/main.go")
		if parseErr != nil {
			return "", nil, false, parseErr
		}
		mainSource, renderErr := renderRelationshipMain(service)
		if renderErr != nil {
			return "", nil, false, renderErr
		}
		return path, mainSource, true, nil
	case strings.HasSuffix(path, ".go"):
		fallback, renderErr := formatRelationshipGo(
			"neutral fallback "+path,
			[]byte("package neutral\n\nconst FixturePath = "+strconv.Quote(path)+"\n"),
		)
		if renderErr != nil {
			return "", nil, false, renderErr
		}
		return path, fallback, false, nil
	default:
		return path, original, false, nil
	}
}

func authoredRelationshipFamilies() ([]RelationshipFamily, error) {
	families := make([]RelationshipFamily, 0, 4)

	chain := newRelationshipAccumulator("chain", "t421-chain-rpc-v1", "grpc", true)
	for provider := 0; provider+1 < relationshipServiceCount; provider++ {
		if err := chain.add(authoredRPCEdge("chain", provider, provider+1, 0)); err != nil {
			return nil, err
		}
	}
	chainFamily, err := chain.finish(9_999, 1, 1)
	if err != nil {
		return nil, err
	}
	families = append(families, chainFamily)

	layered := newRelationshipAccumulator("layered_dag", "t421-layered-dag-rpc-v1", "grpc", true)
	for layer := 0; layer < relationshipLayerCount-1; layer++ {
		for offset := 0; offset < relationshipLayerWidth; offset++ {
			provider := layer*relationshipLayerWidth + offset
			nextLayer := (layer + 1) * relationshipLayerWidth
			batch := []relationshipEdge{
				authoredRPCEdge("layered_dag", provider, nextLayer+offset, 0),
				authoredRPCEdge("layered_dag", provider, nextLayer+(offset+1)%relationshipLayerWidth, 1),
			}
			sortRelationshipEdges(batch)
			for _, edge := range batch {
				if err := layered.add(edge); err != nil {
					return nil, err
				}
			}
		}
	}
	layeredFamily, err := layered.finish(200, 2, 2)
	if err != nil {
		return nil, err
	}
	families = append(families, layeredFamily)

	fanout := newRelationshipAccumulator("bounded_fanout", "t421-bounded-fanout-rpc-v2", "grpc", false)
	for provider := 0; provider < relationshipServiceCount; provider++ {
		if !boundedFanoutProvider(provider) {
			continue
		}
		batch := make([]relationshipEdge, 0, relationshipFanout)
		for slot := 1; slot <= relationshipFanout; slot++ {
			batch = append(batch, authoredRPCEdge(
				"bounded_fanout", provider, (provider+slot+1)%relationshipServiceCount, slot,
			))
		}
		sortRelationshipEdges(batch)
		for _, edge := range batch {
			if err := fanout.add(edge); err != nil {
				return nil, err
			}
		}
	}
	fanoutFamily, err := fanout.finish(800, 8, 8)
	if err != nil {
		return nil, err
	}
	families = append(families, fanoutFamily)

	hotspot := newRelationshipAccumulator("hotspot", "t421-hotspot-kafka-v1", "kafka", true)
	for group := 0; group < relationshipServiceCount/relationshipHotspotWidth; group++ {
		provider := group * relationshipHotspotWidth
		for member := 1; member < relationshipHotspotWidth; member++ {
			if err := hotspot.add(authoredKafkaEdge(provider, provider+member, member)); err != nil {
				return nil, err
			}
		}
	}
	hotspotFamily, err := hotspot.finish(9_500, 1, 19)
	if err != nil {
		return nil, err
	}
	families = append(families, hotspotFamily)

	return families, nil
}

// independentRelationshipFamilies repeats the frozen graph arithmetic without
// calling the source author's edge constructors or traversals. The common
// accumulator owns only the versioned canonical record framing.
func independentRelationshipFamilies() ([]RelationshipFamily, error) {
	result := make([]RelationshipFamily, 0, 4)

	chain := newRelationshipAccumulator("chain", "t421-chain-rpc-v1", "grpc", true)
	for consumer := 1; consumer < relationshipServiceCount; consumer++ {
		provider := consumer - 1
		edge := independentRPCEdge("chain", provider, consumer, 0)
		if err := chain.add(edge); err != nil {
			return nil, err
		}
	}
	chainFamily, err := chain.finish(9_999, 1, 1)
	if err != nil {
		return nil, err
	}
	result = append(result, chainFamily)

	layered := newRelationshipAccumulator("layered_dag", "t421-layered-dag-rpc-v1", "grpc", true)
	for provider := 0; provider < (relationshipLayerCount-1)*relationshipLayerWidth; provider++ {
		layer := provider / relationshipLayerWidth
		offset := provider - layer*relationshipLayerWidth
		nextLayer := provider - offset + relationshipLayerWidth
		nextOffset := offset + 1
		if nextOffset == relationshipLayerWidth {
			nextOffset = 0
		}
		batch := []relationshipEdge{
			independentRPCEdge("layered_dag", provider, nextLayer+offset, 0),
			independentRPCEdge("layered_dag", provider, nextLayer+nextOffset, 1),
		}
		sortRelationshipEdges(batch)
		for _, edge := range batch {
			if err := layered.add(edge); err != nil {
				return nil, err
			}
		}
	}
	layeredFamily, err := layered.finish(200, 2, 2)
	if err != nil {
		return nil, err
	}
	result = append(result, layeredFamily)

	fanout := newRelationshipAccumulator("bounded_fanout", "t421-bounded-fanout-rpc-v2", "grpc", false)
	for provider := 0; provider < relationshipServiceCount; provider++ {
		if !boundedFanoutProvider(provider) {
			continue
		}
		batch := make([]relationshipEdge, relationshipFanout)
		for index := range batch {
			consumer := provider + index + 2
			if consumer >= relationshipServiceCount {
				consumer -= relationshipServiceCount
			}
			batch[index] = independentRPCEdge("bounded_fanout", provider, consumer, index+1)
		}
		sortRelationshipEdges(batch)
		for _, edge := range batch {
			if err := fanout.add(edge); err != nil {
				return nil, err
			}
		}
	}
	fanoutFamily, err := fanout.finish(800, 8, 8)
	if err != nil {
		return nil, err
	}
	result = append(result, fanoutFamily)

	hotspot := newRelationshipAccumulator("hotspot", "t421-hotspot-kafka-v1", "kafka", true)
	for provider := 0; provider < relationshipServiceCount; provider += relationshipHotspotWidth {
		for slot := 1; slot != relationshipHotspotWidth; slot++ {
			edge := relationshipEdge{
				family: "hotspot", protocol: "kafka",
				identity: fmt.Sprintf("t421.hotspot.%04d", provider/relationshipHotspotWidth),
				provider: provider, consumer: provider + slot, slot: slot,
			}
			if err := hotspot.add(edge); err != nil {
				return nil, err
			}
		}
	}
	hotspotFamily, err := hotspot.finish(9_500, 1, 19)
	if err != nil {
		return nil, err
	}
	result = append(result, hotspotFamily)

	return result, nil
}

func parseRelationshipServicePath(value, prefix, suffix string) (int, error) {
	indexText := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
	if len(indexText) != 5 {
		return 0, fmt.Errorf("T42.1 relationship path %q does not contain a five-digit service ordinal", value)
	}
	for _, character := range indexText {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("T42.1 relationship path %q has a non-decimal service ordinal", value)
		}
	}
	service, err := strconv.Atoi(indexText)
	if err != nil || service < 0 || service >= relationshipServiceCount {
		return 0, fmt.Errorf("T42.1 relationship path %q is outside the frozen service range", value)
	}
	if value != prefix+indexText+suffix {
		return 0, fmt.Errorf("T42.1 relationship path %q is not canonical", value)
	}
	return service, nil
}

func renderRelationshipProto(service int) []byte {
	var output bytes.Buffer
	fmt.Fprintf(&output, "syntax = \"proto3\";\n\n")
	fmt.Fprintf(&output, "package t421.service%05d;\n", service)
	fmt.Fprintf(
		&output,
		"option go_package = %q;\n\n",
		fmt.Sprintf("%s/services/service-%05d;service%05d", combinedModulePath, service, service),
	)
	output.WriteString("message Request {}\nmessage Response {}\n\n")
	fmt.Fprintf(&output, "service Service%05d {\n", service)
	for _, method := range outboundRPCMethods(service) {
		fmt.Fprintf(&output, "  rpc %s(Request) returns (Response);\n", method)
	}
	output.WriteString("}\n")
	return output.Bytes()
}

func renderRelationshipGRPC(service int) ([]byte, error) {
	methods := outboundRPCMethods(service)
	serviceName := fmt.Sprintf("Service%05d", service)
	fullyQualifiedService := fmt.Sprintf("t421.service%05d.%s", service, serviceName)
	var source bytes.Buffer
	source.WriteString("// Code generated by protoc-gen-go-grpc. DO NOT EDIT.\n")
	fmt.Fprintf(&source, "// source: contracts/service-%05d/api.proto\n", service)
	fmt.Fprintf(&source, "package service%05d\n\n", service)
	for _, method := range methods {
		fmt.Fprintf(
			&source, "const %s_%s_FullMethodName = %q\n",
			serviceName, method, "/"+fullyQualifiedService+"/"+method,
		)
	}
	source.WriteByte('\n')
	fmt.Fprintf(&source, "type %sClient interface {\n", serviceName)
	for _, method := range methods {
		fmt.Fprintf(&source, "%s(ctx any, request any) (any, error)\n", method)
	}
	source.WriteString("}\n\n")
	clientType := fmt.Sprintf("service%05dClient", service)
	fmt.Fprintf(&source, "type %s struct{}\n\n", clientType)
	fmt.Fprintf(&source, "func New%sClient(any) %sClient { return %s{} }\n", serviceName, serviceName, clientType)
	for _, method := range methods {
		fmt.Fprintf(
			&source,
			"func (%s) %s(_ any, request any) (any, error) { return request, nil }\n",
			clientType, method,
		)
	}
	source.WriteByte('\n')
	fmt.Fprintf(
		&source,
		"var %s_ServiceDesc = struct{ ServiceName string }{ServiceName: %q}\n",
		serviceName, fullyQualifiedService,
	)
	fmt.Fprintf(&source, "type %sServer interface{}\n", serviceName)
	fmt.Fprintf(&source, "func Register%sServer(any, %sServer) {}\n", serviceName, serviceName)
	return formatRelationshipGo(fmt.Sprintf("generated service %05d", service), source.Bytes())
}

func renderRelationshipMain(service int) ([]byte, error) {
	calls := inboundRPCCalls(service)
	providers := make([]int, 0, len(calls))
	seen := [relationshipServiceCount]bool{}
	for _, call := range calls {
		if !seen[call.provider] {
			seen[call.provider] = true
			providers = append(providers, call.provider)
		}
	}
	sort.Ints(providers)

	var source bytes.Buffer
	fmt.Fprintf(&source, "package service%05d\n\n", service)
	source.WriteString("import (\n")
	for _, provider := range providers {
		fmt.Fprintf(
			&source,
			"provider%05d %q\n",
			provider, fmt.Sprintf("%s/services/service-%05d", combinedModulePath, provider),
		)
	}
	source.WriteString("\"github.com/IBM/sarama\"\n")
	source.WriteString(")\n\n")
	source.WriteString("func callRelationships(ctx any, request any")
	for _, provider := range providers {
		fmt.Fprintf(
			&source,
			", client%05d provider%05d.Service%05dClient",
			provider, provider, provider,
		)
	}
	source.WriteString(") {\n")
	for _, call := range calls {
		fmt.Fprintf(&source, "_, _ = client%05d.%s(ctx, request)\n", call.provider, call.method)
	}
	source.WriteString("}\n\n")
	topic := fmt.Sprintf("t421.hotspot.%04d", service/relationshipHotspotWidth)
	if service%relationshipHotspotWidth == 0 {
		source.WriteString("func publishHotspot(producer sarama.SyncProducer) {\n")
		fmt.Fprintf(
			&source,
			"_, _, _ = producer.SendMessage(&sarama.ProducerMessage{Topic: %q})\n",
			topic,
		)
		source.WriteString("}\n")
	} else {
		source.WriteString("func consumeHotspot(consumer sarama.Consumer) {\n")
		fmt.Fprintf(
			&source,
			"_, _ = consumer.ConsumePartition(%q, 0, sarama.OffsetNewest)\n",
			topic,
		)
		source.WriteString("}\n")
	}
	return formatRelationshipGo(fmt.Sprintf("service main %05d", service), source.Bytes())
}

func formatRelationshipGo(label string, source []byte) ([]byte, error) {
	formatted, err := format.Source(source)
	if err != nil {
		return nil, fmt.Errorf("format T42.1 %s: %w", label, err)
	}
	return formatted, nil
}

func outboundRPCMethods(provider int) []string {
	methods := make([]string, 0, 2)
	if provider < (relationshipLayerCount-1)*relationshipLayerWidth {
		methods = append(methods, relationshipMethodName("layered_dag", provider))
	}
	// Chain and bounded fanout deliberately share one declared operation.
	// Their disjoint +1 and +2..+9 service geometry keeps the semantic
	// families exact without exceeding the production declaration ceiling.
	methods = append(methods, relationshipMethodName("bounded_fanout", provider))
	return methods
}

func inboundRPCCalls(consumer int) []rpcCall {
	calls := make([]rpcCall, 0, 11)
	if consumer > 0 {
		provider := consumer - 1
		calls = append(calls, rpcCall{
			family: "chain", provider: provider, consumer: consumer,
			method: relationshipMethodName("chain", provider),
		})
	}
	if consumer >= relationshipLayerWidth && consumer < relationshipLayerCount*relationshipLayerWidth {
		offset := consumer % relationshipLayerWidth
		previousLayer := consumer - relationshipLayerWidth - offset
		providers := []int{
			previousLayer + offset,
			previousLayer + (offset+relationshipLayerWidth-1)%relationshipLayerWidth,
		}
		sort.Ints(providers)
		for slot, provider := range providers {
			calls = append(calls, rpcCall{
				family: "layered_dag", provider: provider, consumer: consumer, slot: slot,
				method: relationshipMethodName("layered_dag", provider),
			})
		}
	}
	providers := make([]int, 0, relationshipFanout)
	for offset := 2; offset <= relationshipFanout+1; offset++ {
		provider := consumer - offset
		if provider < 0 {
			provider += relationshipServiceCount
		}
		providers = append(providers, provider)
	}
	sort.Ints(providers)
	for slot, provider := range providers {
		if !boundedFanoutProvider(provider) {
			continue
		}
		calls = append(calls, rpcCall{
			family: "bounded_fanout", provider: provider, consumer: consumer, slot: slot,
			method: relationshipMethodName("bounded_fanout", provider),
		})
	}
	return calls
}

func boundedFanoutProvider(provider int) bool {
	return provider < relationshipFanoutSpan || provider >= relationshipServiceCount-relationshipFanoutSpan
}

func relationshipMethodName(family string, provider int) string {
	switch family {
	case "chain", "bounded_fanout":
		return fmt.Sprintf("BoundedFanoutP%05d", provider)
	case "layered_dag":
		return fmt.Sprintf("LayeredDagP%05d", provider)
	default:
		panic("unsupported frozen T42.1 RPC family")
	}
}

func authoredRPCEdge(family string, provider, consumer, slot int) relationshipEdge {
	method := relationshipMethodName(family, provider)
	return relationshipEdge{
		family: family, protocol: "grpc",
		identity: fmt.Sprintf("/t421.service%05d.Service%05d/%s", provider, provider, method),
		provider: provider, consumer: consumer, slot: slot,
	}
}

func independentRPCEdge(family string, provider, consumer, slot int) relationshipEdge {
	var method string
	switch family {
	case "chain", "bounded_fanout":
		method = fmt.Sprintf("BoundedFanoutP%05d", provider)
	case "layered_dag":
		method = fmt.Sprintf("LayeredDagP%05d", provider)
	default:
		panic("unsupported independent T42.1 RPC family")
	}
	return relationshipEdge{
		family: family, protocol: "grpc",
		identity: fmt.Sprintf("/t421.service%05d.Service%05d/%s", provider, provider, method),
		provider: provider, consumer: consumer, slot: slot,
	}
}

func authoredKafkaEdge(provider, consumer, slot int) relationshipEdge {
	return relationshipEdge{
		family: "hotspot", protocol: "kafka",
		identity: fmt.Sprintf("t421.hotspot.%04d", provider/relationshipHotspotWidth),
		provider: provider, consumer: consumer, slot: slot,
	}
}

func sortRelationshipEdges(edges []relationshipEdge) {
	sort.Slice(edges, func(left, right int) bool {
		if edges[left].provider != edges[right].provider {
			return edges[left].provider < edges[right].provider
		}
		if edges[left].consumer != edges[right].consumer {
			return edges[left].consumer < edges[right].consumer
		}
		return edges[left].slot < edges[right].slot
	})
}

func newRelationshipAccumulator(name, seed, protocol string, acyclic bool) *relationshipAccumulator {
	return &relationshipAccumulator{
		name: name, seed: seed, protocol: protocol, acyclic: acyclic, digest: sha256.New(),
	}
}

func (accumulator *relationshipAccumulator) add(edge relationshipEdge) error {
	if edge.family != accumulator.name || edge.protocol != accumulator.protocol {
		return errors.New("T42.1 relationship edge differs from its family")
	}
	if edge.provider < 0 || edge.provider >= relationshipServiceCount ||
		edge.consumer < 0 || edge.consumer >= relationshipServiceCount ||
		edge.provider == edge.consumer || edge.identity == "" || edge.slot < 0 {
		return errors.New("T42.1 relationship edge is outside the frozen graph")
	}
	if accumulator.acyclic && edge.provider >= edge.consumer {
		return errors.New("T42.1 acyclic relationship edge is not forward-only")
	}
	if accumulator.havePrevious {
		previous := accumulator.previous
		if edge.provider == previous.provider && edge.consumer == previous.consumer && edge.slot == previous.slot {
			return errors.New("T42.1 relationship edge is duplicated")
		}
		if edge.provider < previous.provider ||
			edge.provider == previous.provider && edge.consumer < previous.consumer ||
			edge.provider == previous.provider && edge.consumer == previous.consumer && edge.slot < previous.slot {
			return errors.New("T42.1 relationship edges are not in provider/consumer/slot order")
		}
	}
	record := relationshipEdgeRecord{
		Schema: "t421-relationship-edge-v1", Family: edge.family, Protocol: edge.protocol,
		Identity: edge.identity,
		Provider: fmt.Sprintf("svc.load-%05d", edge.provider),
		Consumer: fmt.Sprintf("svc.load-%05d", edge.consumer),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal T42.1 relationship edge: %w", err)
	}
	if uint64(len(raw)) > math.MaxUint64-8 || accumulator.framedBytes > math.MaxUint64-8-uint64(len(raw)) {
		return errors.New("T42.1 relationship edge bytes overflow")
	}
	var frame [8]byte
	binary.BigEndian.PutUint64(frame[:], uint64(len(raw)))
	if _, err := accumulator.digest.Write(frame[:]); err != nil {
		return fmt.Errorf("hash T42.1 relationship edge frame: %w", err)
	}
	if _, err := accumulator.digest.Write(raw); err != nil {
		return fmt.Errorf("hash T42.1 relationship edge: %w", err)
	}
	if accumulator.records == math.MaxUint64 ||
		accumulator.inDegree[edge.consumer] == math.MaxUint16 ||
		accumulator.outDegree[edge.provider] == math.MaxUint16 {
		return errors.New("T42.1 relationship edge count overflow")
	}
	accumulator.records++
	accumulator.framedBytes += 8 + uint64(len(raw))
	accumulator.inDegree[edge.consumer]++
	accumulator.outDegree[edge.provider]++
	accumulator.previous = edge
	accumulator.havePrevious = true
	return nil
}

func (accumulator *relationshipAccumulator) finish(wantEdges, wantMaxIn, wantMaxOut uint64) (RelationshipFamily, error) {
	var maxIn, maxOut uint64
	for index := range relationshipServiceCount {
		maxIn = max(maxIn, uint64(accumulator.inDegree[index]))
		maxOut = max(maxOut, uint64(accumulator.outDegree[index]))
	}
	if accumulator.records != wantEdges || maxIn != wantMaxIn || maxOut != wantMaxOut {
		return RelationshipFamily{}, fmt.Errorf(
			"T42.1 relationship family %q has edges/in/out %d/%d/%d, want %d/%d/%d",
			accumulator.name, accumulator.records, maxIn, maxOut, wantEdges, wantMaxIn, wantMaxOut,
		)
	}
	return RelationshipFamily{
		Name: accumulator.name, Seed: accumulator.seed,
		Protocols:         []string{accumulator.protocol},
		SemanticPairEdges: accumulator.records, MaxInDegree: maxIn, MaxOutDegree: maxOut,
		Acyclic: accumulator.acyclic,
		ExpectedEdges: SetIdentity{
			Records: accumulator.records, FramedBytes: accumulator.framedBytes,
			SHA256: "sha256:" + hex.EncodeToString(accumulator.digest.Sum(nil)),
		},
	}, nil
}
