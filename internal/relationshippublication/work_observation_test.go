package relationshippublication

import (
	"context"
	"errors"
	"testing"

	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
)

func TestRelationshipBuildEntriesObserveBeforeValidation(t *testing.T) {
	for _, build := range []struct {
		name string
		call func(context.Context) error
	}{
		{"v1", func(ctx context.Context) error { _, err := Build(ctx, BuildRequest{}); return err }},
		{"v2", func(ctx context.Context) error { _, err := BuildV2(ctx, BuildRequestV2{}); return err }},
		{"v3", func(ctx context.Context) error { _, err := BuildV3(ctx, BuildRequestV3{}); return err }},
	} {
		for _, mode := range []string{"invalid_retry", "canceled", "sink"} {
			t.Run(build.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var events []readaccounting.RelationshipEvent
				ctx, err := readaccounting.WithRelationshipObserver(ctx, func(event readaccounting.RelationshipEvent, quantity uint64) error {
					events = append(events, event)
					if mode == "sink" {
						return readaccounting.ErrEvent
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if mode == "canceled" {
					cancel()
				}
				err = build.call(ctx)
				if err == nil || string(events) != "B" || mode == "sink" && !errors.Is(err, readaccounting.ErrEvent) ||
					mode == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal(events, err)
				}
				if mode == "invalid_retry" && (build.call(ctx) == nil || string(events) != "BB") {
					t.Fatal("second actual builder call was omitted")
				}
			})
		}
	}
}

func TestRelationshipProjectorEntriesObserveBeforeLookup(t *testing.T) {
	accumulator := &buildAccumulator{}
	for _, project := range []struct {
		name string
		call func(context.Context) (Projection, error)
	}{
		{"rpc", func(ctx context.Context) (Projection, error) {
			return accumulator.projectRPC(ctx, rpccallerposting.Posting{})
		}},
		{"kafka", func(ctx context.Context) (Projection, error) {
			return accumulator.projectKafka(ctx, kafkatopicposting.Posting{})
		}},
		{"rpc_v3", func(ctx context.Context) (Projection, error) {
			return accumulator.projectRPCV3(ctx, rpccallerposting.Posting{})
		}},
		{"kafka_v3", func(ctx context.Context) (Projection, error) {
			return accumulator.projectKafkaV3(ctx, kafkatopicposting.Posting{})
		}},
	} {
		t.Run(project.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var events []readaccounting.RelationshipEvent
			ctx, err := readaccounting.WithRelationshipObserver(ctx, func(event readaccounting.RelationshipEvent, quantity uint64) error {
				events = append(events, event)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := project.call(ctx); err == nil || string(events) != "P" {
				t.Fatal("failed lookup invocation not observed", events, err)
			}
			cancel()
			if _, err := project.call(ctx); !errors.Is(err, context.Canceled) || string(events) != "PP" {
				t.Fatal("canceled entered projector not retained", events, err)
			}
		})
	}
}

func TestRelationshipV3ObservesDuplicateAndFailedProjectorWork(t *testing.T) {
	for _, mode := range []string{"duplicates", "zero", "sink_prefix", "lookup_prefix", "installed_sink", "installed_canceled", "ordinary"} {
		t.Run(mode, func(t *testing.T) {
			repository := "example.com/acme/observed"
			catalog, generation := relationshipCatalogV3Test(t, repository, 1)
			states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
			upstream := relationshipUpstreamV3Test(t, repository)
			resolver := relationshipResolverV3Test(t, repository, upstream)
			posting := rpcPosting("a", "resolved", "grpc", "first.v1/Get", "services/00000/call.go", "")
			rpcValues := []rpccallerposting.Posting{posting, posting}
			kafkaValues := []kafkatopicposting.Posting{kafkaPosting("b", "producer", "literal", "topic", "services/00000/kafka.go")}
			if mode == "zero" {
				rpcValues, kafkaValues = nil, nil
			}
			rpc := fakeRPC{root: relationshipRPCV3Test(t, repository, resolver.root, upstream, rpcValues), values: rpcValues}
			kafka := fakeKafka{root: relationshipKafkaV3Test(t, repository, rpc.root.Authority, upstream, kafkaValues), values: kafkaValues}
			if mode == "lookup_prefix" {
				rpc.values[1].Path = "../invalid"
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var events []readaccounting.RelationshipEvent
			var references uint64
			if mode != "ordinary" {
				var err error
				ctx, err = readaccounting.WithRelationshipObserver(ctx, func(event readaccounting.RelationshipEvent, quantity uint64) error {
					events = append(events, event)
					if event == readaccounting.RelationshipReferences {
						references += quantity
						switch mode {
						case "installed_sink":
							return readaccounting.ErrEvent
						case "installed_canceled":
							cancel()
						}
					} else if quantity != 1 {
						t.Fatal(event, quantity)
					}
					if mode == "sink_prefix" && len(events) == 3 {
						return readaccounting.ErrEvent
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := BuildV3(ctx, BuildRequestV3{Root: t.TempDir(), Catalog: generation, States: states,
				ServiceSummary: summary, Resolver: resolver, RPC: rpc, Kafka: kafka, Upstream: upstream})
			if mode == "installed_sink" || mode == "installed_canceled" {
				if err == nil || prepared != nil || string(events) != "BPPPR" || references != 2 {
					t.Fatal("installed prefix lost on later refusal", events, references, err)
				}
				return
			}
			if mode == "sink_prefix" || mode == "lookup_prefix" {
				if err == nil || prepared != nil || string(events) != "BPP" {
					t.Fatal("actual failed prefix changed", events, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = prepared.abort() }()
			wantEvents, wantProjections := "BPPPR", 2
			switch mode {
			case "zero":
				wantEvents, wantProjections = "BR", 0
			case "ordinary":
				wantEvents = ""
			}
			if string(events) != wantEvents || prepared.Root().ProjectionCount != wantProjections {
				t.Fatalf("invocations=%s root=%d; want %s/%d", events, prepared.Root().ProjectionCount, wantEvents, wantProjections)
			}
			if mode != "ordinary" && references != uint64(prepared.Root().ServiceReferenceCount) {
				t.Fatal("installed quantities differ from retained deduplicated references", references, prepared.Root())
			}
		})
	}
}

func TestRelationshipRuntimeCurrentRedeliveryCountsActualRebuild(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	var events []readaccounting.RelationshipEvent
	ctx, err := readaccounting.WithRelationshipObserver(t.Context(), func(event readaccounting.RelationshipEvent, quantity uint64) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.Handle(ctx, fixture.chunk); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0] != readaccounting.RelationshipBuild {
		t.Fatal("production v2 builder entry was not observed")
	}
	builds := 0
	for _, event := range events {
		if event == readaccounting.RelationshipBuild {
			builds++
		}
	}
	if builds != 1 {
		t.Fatal("runtime/component wrappers double-counted builder entry", events)
	}
	prefix := string(events)
	// Current authority/no repin does not imply no builder work. This native
	// runtime route rebuilds the empty relationship stage on redelivery.
	if err := fixture.runtime.Handle(ctx, fixture.chunk); err != nil || string(events) != prefix+prefix {
		t.Fatal("exact-current redelivery omitted actual rebuild work", events, err)
	}
}
