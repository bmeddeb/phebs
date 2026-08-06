// Package t364 retains Epic 36's deterministic source-free mechanics receipt.
// It exercises all four adapters over one neutral shared parse and binds the
// production publication, progress, recovery, authorization, and parity gates
// by executable test name without retaining source text or paths in results.
package t364

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

const Schema = "t364-observation-plane-receipt-v1"

const neutralSource = `package demo

import (
	"context"
	grpcapi "example.com/repo/grpc"
	thriftapi "example.com/repo/thrift"
	"github.com/IBM/sarama"
)

const auditTopic = "audit-v2"

func invoke(ctx context.Context, grpcClient grpcapi.GreeterClient, thriftClient thriftapi.AgentClient, producer sarama.SyncProducer, consumer sarama.Consumer) {
	grpcClient.SayHello()
	thriftClient.EmitBatch()
	producer.SendMessage(&sarama.ProducerMessage{Topic: "orders-v1"})
	producer.SendMessage(&sarama.ProducerMessage{Topic: auditTopic})
	consumer.ConsumePartition("clicks", 0, sarama.OffsetNewest)
}
`

type Receipt struct {
	Schema                  string                `json:"schema"`
	Outcome                 string                `json:"outcome"`
	ObservationPolicyDigest string                `json:"observation_policy_digest"`
	ContentDigest           string                `json:"content_digest"`
	SharedParses            int                   `json:"shared_parses"`
	Packs                   []Pack                `json:"packs"`
	OperationReceipt        OperationReceiptShape `json:"operation_receipt"`
	Cases                   []Case                `json:"cases"`
	Claims                  Claims                `json:"claims"`
}

type Pack struct {
	Name        string `json:"name"`
	Evidence    int    `json:"evidence"`
	Resolved    int    `json:"resolved"`
	Dynamic     int    `json:"dynamic"`
	Ambiguous   int    `json:"ambiguous"`
	Unsupported int    `json:"unsupported"`
}

type OperationReceiptShape struct {
	Schema             string `json:"schema"`
	InputBlobs         int    `json:"input_blobs"`
	SourceBlobReads    int    `json:"source_blob_reads"`
	ParsedObservations int    `json:"parsed_observations"`
	ReusedObservations int    `json:"reused_observations"`
	UnsupportedBlobs   int    `json:"unsupported_blobs"`
}

type Case struct {
	Name       string   `json:"name"`
	Assertions []string `json:"assertions"`
	Gates      []string `json:"gates"`
}

type Claims struct {
	SourceFree           bool `json:"source_free"`
	NoAccuracy           bool `json:"no_accuracy"`
	NoCompleteness       bool `json:"no_completeness"`
	NoSLO                bool `json:"no_slo"`
	NoReleaseAuthority   bool `json:"no_release_authority"`
	NoExtractorPromotion bool `json:"no_extractor_promotion"`
}

func Build() (Receipt, error) {
	observation, err := sourceobservation.Parse(context.Background(), sourceobservation.Input{
		Path: "neutral.go", Content: neutralSource,
	})
	if err != nil {
		return Receipt{}, err
	}
	grpc, err := sourceobservation.AdaptCallers(observation, "grpc", []sourceobservation.MethodDescriptor{{
		Protocol: "grpc", ImportPath: "example.com/repo/grpc", ClientType: "GreeterClient",
		Method: "SayHello", Operation: "/demo.Greeter/SayHello",
	}})
	if err != nil {
		return Receipt{}, err
	}
	thrift, err := sourceobservation.AdaptCallers(observation, "thrift", []sourceobservation.MethodDescriptor{{
		Protocol: "thrift", ImportPath: "example.com/repo/thrift", ClientType: "AgentClient",
		Method: "EmitBatch", Operation: "demo.Agent.EmitBatch",
	}})
	if err != nil {
		return Receipt{}, err
	}
	producer, err := sourceobservation.AdaptKafka(observation, "producer")
	if err != nil {
		return Receipt{}, err
	}
	consumer, err := sourceobservation.AdaptKafka(observation, "consumer")
	if err != nil {
		return Receipt{}, err
	}
	packs := []Pack{
		callerPack("grpc", grpc.Evidence), callerPack("thrift", thrift.Evidence),
		kafkaPack("kafka-producer", producer.Evidence), kafkaPack("kafka-consumer", consumer.Evidence),
	}
	return Receipt{
		Schema: Schema, Outcome: "completed",
		ObservationPolicyDigest: sourceobservation.PolicyDigest(),
		ContentDigest:           observation.ContentDigest, SharedParses: 1, Packs: packs,
		OperationReceipt: OperationReceiptShape{
			Schema: observationpublication.ReceiptSchema, InputBlobs: 1,
			SourceBlobReads: 1, ParsedObservations: 1,
		},
		Cases: []Case{
			{Name: "content_reuse", Assertions: []string{"exact_policy_and_content_identity", "aba_reactivation_without_schedule_resurrection"}, Gates: []string{"TestPublicationReusesOnlyExactContentAndReactivatesABA"}},
			{Name: "exhausted_recovery", Assertions: []string{"settled_schedule_not_resurrected", "distinct_recovery_identity_reuses_validated_members"}, Gates: []string{"TestRuntimeMintsDistinctRecoveryScheduleAfterExhaustion", "TestProgressShowsFailedScheduleAndDistinctRecovery"}},
			{Name: "progress_cost", Assertions: []string{"warm_control_reads_only", "corrupt_cold_member_refused"}, Gates: []string{"TestProgressReportsCurrentReceiptAndWarmReadsNoMembers"}},
			{Name: "authorization", Assertions: []string{"permission_before_counts", "authority_re_fenced_before_emission"}, Gates: []string{"TestObservationProgressAuthorizesBeforeReadingCounts"}},
			{Name: "transport_parity", Assertions: []string{"shared_projection_for_http_and_mcp", "bounded_source_free_response"}, Gates: []string{"TestObservationProgressHTTPUsesSharedBoundedProjection", "TestObservationProgressToolReturnsSharedProjection"}},
			{Name: "backup_lifecycle", Assertions: []string{"validated_archive_round_trip", "current_prior_and_lease_preserved"}, Gates: []string{"TestArchiveRoundTripPreservesExactCurrentPublication", "TestLifecyclePreservesCurrentRollbackFloorAndLease"}},
			{Name: "multi_pack", Assertions: []string{"one_parse_four_independent_projections", "no_adapter_reparse"}, Gates: []string{"TestSharedObservationDeterminismCitationsAndIndependentAdapters", "TestExistingGRPCAndThriftDirectCallerParity", "TestExistingKafkaPlaneParity"}},
		},
		Claims: Claims{
			SourceFree: true, NoAccuracy: true, NoCompleteness: true, NoSLO: true,
			NoReleaseAuthority: true, NoExtractorPromotion: true,
		},
	}, nil
}

func Marshal(receipt Receipt) ([]byte, error) {
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func Digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func callerPack(name string, evidence []sourceobservation.CallerEvidence) Pack {
	result := Pack{Name: name, Evidence: len(evidence)}
	for _, item := range evidence {
		incrementPack(&result, item.State)
	}
	return result
}

func kafkaPack(name string, evidence []sourceobservation.KafkaEvidence) Pack {
	result := Pack{Name: name, Evidence: len(evidence)}
	for _, item := range evidence {
		incrementPack(&result, item.State)
	}
	return result
}

func incrementPack(result *Pack, state string) {
	switch state {
	case "resolved":
		result.Resolved++
	case "dynamic":
		result.Dynamic++
	case "ambiguous":
		result.Ambiguous++
	default:
		result.Unsupported++
	}
}
