package store

import (
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedStoredRequestClosedProviders(t *testing.T) {
	h := typedDigest([]byte("fixture"))
	r := typedindex.Request{Schema: typedindex.RequestSchema, Action: typedindex.Plan, Source: typedindex.Source{Repository: "example.test/repo", Incarnation: "one", Generation: h, Commit: strings.Repeat("a", 40)}, ProfileName: "one", ProfileEpoch: 1, ProfileDigest: h, ConfigDigest: h, ToolsDigest: h, UniverseDigest: h, BundleDigest: h, PolicyDigest: h, IdempotencyKey: "one"}
	for _, provider := range typedindex.ProviderOrder() {
		r.Provider = provider
		if !typedStoredRequest(r) {
			t.Fatal("known custody refused", provider)
		}
	}
	for _, provider := range []string{"", "shell", typedindex.ModuleProviderID + " ", "bazel-rules-go-scip-v2"} {
		r.Provider = provider
		if typedStoredRequest(r) {
			t.Fatal("unknown custody accepted", provider)
		}
	}
}
