package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

// TopicInventoryQueries is the composed Topics overview boundary (T57.2).
//
// It is deliberately not folded into ProofQueries. A released Kafka plane
// restricts the proof tools to find_kafka_topic_usage through
// KafkaTopicUsageOnly, and the composed inventory has its own measured claim
// (T57.3), so sharing the proof interface would advertise it on a released
// binary whose plane releases alone do not describe it. Callers supply this on
// the experimental surface or beside a released phebs.kafka.topics claim,
// mirroring the HTTP registration boundary in registerTopicInventoryAPI.
type TopicInventoryQueries interface {
	ListKafkaTopics(ctx context.Context) (*api.ProofBundleEnvelope, error)
	BuildTopicInventory(
		ctx context.Context,
		bundleID string,
		pageSize int,
		cursor string,
	) (*api.TopicInventoryReport, error)
}

// registerTopicInventoryTools exposes the composed topic inventory. Both tools
// return the same bounded api.TopicInventoryReport rather than the raw
// immutable bundle: an agent's context window is better served by one page of
// at most 100 rows plus the content address and cursor it needs to continue,
// and the report already carries every plane state, the census, the coverage
// certificate and the caveat. HTTP keeps the two-step bundle-then-page shape
// for clients that want the exact persisted bytes.
func registerTopicInventoryTools(server *sdk.Server, opts Options) {
	// KafkaTopicUsageOnly is checked here as well as at the wiring boundary
	// that leaves TopicInventory nil, so a released-plane admission cannot
	// reach these tools even if a future caller supplies both.
	if opts.TopicInventory == nil || opts.KafkaTopicUsageOnly && !opts.KafkaTopicInventoryReleased {
		return
	}
	type listInput struct {
		PageSize int `json:"page_size,omitempty" jsonschema:"rows returned, default 50, cap 100"`
	}
	// No ReadOnlyHint: composing an inventory persists one immutable proof
	// bundle, exactly as the other proof-bundle-creating tools do.
	sdk.AddTool(server, &sdk.Tool{
		Name:        "list_kafka_topics",
		Description: "Compose the source-spelled Kafka topic inventory across the admitted producer and consumer planes and return its first page. This answers which topics exist in scope, which find_kafka_topic_usage cannot because that tool needs a spelling you already know. Each plane is reported separately with its own publication, coverage and quality state: unavailable means no admitted recipe publishes that plane, unpublished means nothing has published it yet, and only a served plane's zero is a measured zero. Rows cite exact assertion identities and the pinned evidence commit, never a mutable HEAD link, and carry no per-site spans — read one spelling with find_kafka_topic_usage for those. The result is a page of one immutable content-addressed inventory, so a clipped scan or a clipped row list is named in truncated and the counts are then lower bounds. A topic is a source spelling with no cluster, environment or runtime identity, and an absent topic is not proven absent.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, input listInput) (*sdk.CallToolResult, api.TopicInventoryReport, error) {
		envelope, err := opts.TopicInventory.ListKafkaTopics(ctx)
		if err != nil {
			return nil, api.TopicInventoryReport{}, err
		}
		report, err := opts.TopicInventory.BuildTopicInventory(ctx, envelope.ID, input.PageSize, "")
		if err != nil {
			return nil, api.TopicInventoryReport{}, err
		}
		return nil, *report, nil
	})

	type pageInput struct {
		BundleID string `json:"bundle_id" jsonschema:"exact content address returned by list_kafka_topics"`
		PageSize int    `json:"page_size,omitempty" jsonschema:"rows returned, default 50, cap 100"`
		Cursor   string `json:"cursor,omitempty" jsonschema:"opaque continuation cursor from the preceding page of this same inventory"`
	}
	sdk.AddTool(server, &sdk.Tool{
		Name:        "read_topic_inventory",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(bool)},
		Description: "Read one page of an already-composed immutable Kafka topic inventory. Re-authorizes every repository the bundle cites and re-verifies its canonical bytes on each call, so losing access to a cited repository returns not-found rather than its rows, and a cursor minted for another inventory is refused. Paging re-reads the persisted bundle and never rescans the corpus. The plane states, unresolved census, coverage certificate and caveat repeat exactly, so a later page cannot quietly drop an unavailable or unpublished plane's qualification. A topic remains a source spelling with no cluster, environment or runtime identity, rows cite exact assertion identities rather than per-site spans, and an absent topic is not proven absent.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, input pageInput) (*sdk.CallToolResult, api.TopicInventoryReport, error) {
		report, err := opts.TopicInventory.BuildTopicInventory(ctx, input.BundleID, input.PageSize, input.Cursor)
		if err != nil {
			return nil, api.TopicInventoryReport{}, err
		}
		return nil, *report, nil
	})
}
