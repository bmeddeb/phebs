package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
)

// The T57.2 MCP tests pin three things: that the two composed-inventory tools
// appear exactly where the HTTP routes do and nowhere else, that their schemas
// and honesty vocabulary cannot drift silently, and that a call reaches the same
// api boundary HTTP uses rather than reimplementing composition here.

const topicInventoryTestBundleID = "pb_" + "0000000000000000000000000000000000000000000000000000000000000000"

type stubTopicInventory struct {
	report   api.TopicInventoryReport
	listErr  error
	pageErr  error
	lists    int
	pages    int
	lastID   string
	lastSize int
	lastCur  string
}

func (s *stubTopicInventory) ListKafkaTopics(context.Context) (*api.ProofBundleEnvelope, error) {
	s.lists++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return &api.ProofBundleEnvelope{ID: topicInventoryTestBundleID}, nil
}

func (s *stubTopicInventory) BuildTopicInventory(
	_ context.Context,
	bundleID string,
	pageSize int,
	cursor string,
) (*api.TopicInventoryReport, error) {
	s.pages++
	s.lastID, s.lastSize, s.lastCur = bundleID, pageSize, cursor
	if s.pageErr != nil {
		return nil, s.pageErr
	}
	report := s.report
	report.BundleID = bundleID
	return &report, nil
}

func topicToolSession(t *testing.T, opts Options) (*sdk.ClientSession, map[string]*sdk.Tool) {
	t.Helper()
	ctx := t.Context()
	server := NewServer(opts)
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _, _ = server.Connect(ctx, serverTransport, nil) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "t57.2-topics", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	found := make(map[string]*sdk.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		found[tool.Name] = tool
	}
	return session, found
}

func callTopicTool(
	t *testing.T,
	session *sdk.ClientSession,
	name string,
	arguments map[string]any,
) (api.TopicInventoryReport, *sdk.CallToolResult) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	var report api.TopicInventoryReport
	if result.StructuredContent != nil && !result.IsError {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &report); err != nil {
			t.Fatalf("decode %s output: %v", name, err)
		}
	}
	return report, result
}

// TestTopicInventoryToolRegistration pins the admission boundary at the
// transport: the inventory tools appear only where the HTTP routes do, and a
// released-Kafka-plane restriction wins even over a caller that supplies the
// boundary anyway.
func TestTopicInventoryToolRegistration(t *testing.T) {
	stub := &stubTopicInventory{}
	for _, test := range []struct {
		name string
		opts Options
		want bool
	}{
		{name: "no boundary lists neither tool", opts: Options{Version: "test"}},
		{
			name: "the experimental boundary lists both",
			opts: Options{Version: "test", TopicInventory: stub},
			want: true,
		},
		{
			// Defence in depth: cmd/phebs already leaves the boundary nil under
			// a released plane, so this pairing cannot occur in production. If a
			// future caller creates it, the restriction must still win.
			name: "a released-plane restriction outranks a supplied boundary",
			opts: Options{Version: "test", TopicInventory: stub, KafkaTopicUsageOnly: true},
		},
		{
			// T57.3: cmd/phebs sets this only for a verified phebs.kafka.topics
			// record with both planes released.
			name: "a released Topics claim lists both beside the restriction",
			opts: Options{Version: "test", TopicInventory: stub, KafkaTopicUsageOnly: true, KafkaTopicInventoryReleased: true},
			want: true,
		},
		{
			name: "proof tools alone never bring the inventory",
			opts: Options{Version: "test", Proofs: schemaProofQueries{}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, found := topicToolSession(t, test.opts)
			_, hasList := found["list_kafka_topics"]
			_, hasRead := found["read_topic_inventory"]
			if hasList != test.want || hasRead != test.want {
				t.Fatalf("list_kafka_topics=%v read_topic_inventory=%v, want both %v",
					hasList, hasRead, test.want)
			}
			// The guard must not disturb the released plane's own tool.
			_, hasUsage := found["find_kafka_topic_usage"]
			wantUsage := test.opts.Proofs != nil
			if hasUsage != wantUsage {
				t.Fatalf("find_kafka_topic_usage=%v, want %v", hasUsage, wantUsage)
			}
		})
	}
}

// TestTopicInventoryToolSchemas pins both tools' input and output schema
// digests, so a silent field change shows up as a digest move, and checks that
// the descriptions and output schema keep the vocabulary the honesty of the
// composed answer depends on.
func TestTopicInventoryToolSchemas(t *testing.T) {
	digests := map[string]string{
		"list_kafka_topics":    "sha256:ca8961d780943fac6a06f5c1a91675b10281eb61358fd59b31a1819f334c42ab",
		"read_topic_inventory": "sha256:6b206548e78f618116fe52a057666f29df5105f0304ddbf0a3e043e6ee5b6fbd",
	}
	_, found := topicToolSession(t, Options{Version: "test", TopicInventory: &stubTopicInventory{}})
	for name, want := range digests {
		tool, ok := found[name]
		if !ok {
			t.Fatalf("%s is not listed", name)
		}
		input, _ := json.Marshal(tool.InputSchema)
		output, _ := json.Marshal(tool.OutputSchema)
		sum := sha256.Sum256(append(append(input, '\n'), output...))
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s input/output schema digest = %s, want %s", name, got, want)
		}
		// The composed contract must be visible in the output schema, or a
		// client cannot tell a measured zero from an unmeasured one.
		for _, field := range []string{
			`"planes"`, `"topics"`, `"pagination"`, `"unresolved_census"`, `"truncated"`, `"caveat"`,
		} {
			if !strings.Contains(string(output), field) {
				t.Errorf("%s output schema omits %s: %s", name, field, output)
			}
		}
		description := strings.ToLower(tool.Description)
		for _, phrase := range []string{"source spelling", "unavailable", "unpublished", "not proven absent"} {
			if !strings.Contains(description, phrase) {
				t.Errorf("%s description lost %q: %s", name, phrase, tool.Description)
			}
		}
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint && name == "list_kafka_topics" {
			t.Errorf("list_kafka_topics persists a bundle and must not claim to be read-only")
		}
	}
	if read := found["read_topic_inventory"]; read.Annotations == nil || !read.Annotations.ReadOnlyHint {
		t.Error("read_topic_inventory is a pure read and must be annotated read-only")
	}
	// Only the paging tool takes an inventory identity.
	listInput, _ := json.Marshal(found["list_kafka_topics"].InputSchema)
	if strings.Contains(string(listInput), "bundle_id") {
		t.Errorf("list_kafka_topics must not require an existing bundle: %s", listInput)
	}
	readInput, _ := json.Marshal(found["read_topic_inventory"].InputSchema)
	if !strings.Contains(string(readInput), `"bundle_id"`) || !strings.Contains(string(readInput), `"cursor"`) {
		t.Errorf("read_topic_inventory input lost its identity or cursor: %s", readInput)
	}
}

// TestTopicInventoryToolCalls pins that both tools reach the same api boundary
// HTTP uses, pass their paging arguments through unchanged, and surface a
// composition refusal as a tool error rather than an empty answer.
func TestTopicInventoryToolCalls(t *testing.T) {
	stub := &stubTopicInventory{
		report: api.TopicInventoryReport{
			SchemaVersion: "kafka-topic-inventory-v1",
			Planes: []api.TopicPlaneState{
				{Plane: "consumer", Domain: "kafka-consumer", State: "unpublished"},
				{Plane: "producer", Domain: "kafka-producer", State: "served", PublishedRuns: 1},
			},
			Topics: []api.TopicInventoryRow{{
				Topic:    "orders-v1",
				Producer: api.TopicRowPlane{State: "served", Sites: 2, Files: 1},
				Consumer: api.TopicRowPlane{State: "unpublished"},
			}},
			Pagination: api.TopicInventoryPagination{
				Order: "topic", PageSize: 2, Returned: 1, Total: 3, Complete: true,
				NextCursor: "cursor-1",
			},
			Caveat: "test caveat",
		},
	}
	session, _ := topicToolSession(t, Options{Version: "test", TopicInventory: stub})

	report, result := callTopicTool(t, session, "list_kafka_topics", map[string]any{"page_size": 2})
	if result.IsError {
		t.Fatalf("list_kafka_topics returned a tool error: %+v", result.Content)
	}
	if stub.lists != 1 || stub.pages != 1 {
		t.Fatalf("compose=%d page=%d, want one of each", stub.lists, stub.pages)
	}
	// The compose step hands its own content address to the paging step, and an
	// omitted page size stays zero so the api default applies.
	if stub.lastID != topicInventoryTestBundleID || stub.lastSize != 2 || stub.lastCur != "" {
		t.Fatalf("list passed id=%q size=%d cursor=%q", stub.lastID, stub.lastSize, stub.lastCur)
	}
	if report.BundleID != topicInventoryTestBundleID || len(report.Topics) != 1 ||
		report.Pagination.NextCursor != "cursor-1" {
		t.Fatalf("list report = %+v", report)
	}
	// The plane states survive the wire, including the unpublished one, so a
	// client cannot read the consumer's zero as measured.
	if len(report.Planes) != 2 || report.Planes[0].State != "unpublished" || report.Planes[1].State != "served" {
		t.Fatalf("plane states did not survive the wire: %+v", report.Planes)
	}
	if report.Topics[0].Consumer.State != "unpublished" {
		t.Fatalf("row plane state did not survive the wire: %+v", report.Topics[0].Consumer)
	}

	_, result = callTopicTool(t, session, "read_topic_inventory", map[string]any{
		"bundle_id": report.BundleID, "page_size": 2, "cursor": report.Pagination.NextCursor,
	})
	if result.IsError {
		t.Fatalf("read_topic_inventory returned a tool error: %+v", result.Content)
	}
	if stub.pages != 2 {
		t.Fatalf("paging calls = %d, want 2", stub.pages)
	}
	if stub.lastID != topicInventoryTestBundleID || stub.lastSize != 2 || stub.lastCur != "cursor-1" {
		t.Fatalf("read passed id=%q size=%d cursor=%q", stub.lastID, stub.lastSize, stub.lastCur)
	}

	// A refusal is a tool error, never an empty report that reads as no topics.
	stub.listErr = errors.New("composition refused")
	if _, result := callTopicTool(t, session, "list_kafka_topics", nil); !result.IsError {
		t.Fatal("a failed composition returned a normal result")
	}
	stub.listErr, stub.pageErr = nil, errors.New("cursor refused")
	if _, result := callTopicTool(t, session, "read_topic_inventory", map[string]any{
		"bundle_id": topicInventoryTestBundleID, "cursor": "foreign",
	}); !result.IsError {
		t.Fatal("a refused cursor returned a normal result")
	}
}
