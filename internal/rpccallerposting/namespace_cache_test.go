package rpccallerposting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

func newTestNamespaceCache(t *testing.T, resolver *countingResolver) *namespaceCache {
	t.Helper()
	root := resolver.Root()
	if err := resolvernamespace.ValidateRoot(root); err != nil {
		t.Fatal(err)
	}
	return &namespaceCache{resolver: resolver, namespaces: root.Namespaces, values: make(map[string][]resolvernamespace.Record)}
}

func TestNamespaceCacheSkipsAbsentKeysWithoutGrowth(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("mixed=%t", mixed), func(t *testing.T) {
			grpc := grpcDescriptor("example.test/shared", "Client", "Call", "test.Service/Call", "grpc")
			thrift := grpcDescriptor("example.test/shared", "Client", "Call", "test.Service.Call", "thrift")
			thrift.Protocol = "thrift"
			thrift.GeneratorRelativePath = ""
			thrift.DeclarationPath = strings.TrimSuffix(thrift.DeclarationPath, ".proto") + ".thrift"
			resolver := &countingResolver{publication: resolverPublication(t, t.TempDir(), []gocaller.DirectDescriptor{grpc, thrift})}
			cache := newTestNamespaceCache(t, resolver)
			for index := range MaxNamespaceReads + 1 {
				for _, protocol := range []string{"grpc", "thrift"} {
					values, err := cache.namespace(t.Context(), protocol, fmt.Sprintf("example.test/absent-%05d", index))
					if err != nil || len(values) != 0 {
						t.Fatalf("absent %s namespace %d = %v, %v", protocol, index, values, err)
					}
					if mixed {
						values, err = cache.namespace(t.Context(), protocol, "example.test/shared")
						if err != nil || len(values) != 1 || values[0].Protocol != protocol {
							t.Fatalf("present %s namespace = %v, %v", protocol, values, err)
						}
					}
				}
			}
			want := 0
			if mixed {
				want = 2
			}
			if cache.reads != want || resolver.reads != want || len(cache.values) != want || resolver.rootReads != 1 {
				t.Fatalf("cache/member/entries/root = %d/%d/%d/%d, want %d/%d/%d/1", cache.reads, resolver.reads, len(cache.values), resolver.rootReads, want, want, want)
			}
		})
	}
}

func TestNamespaceCachePositiveReadLimitStillRefusesBeforeRead(t *testing.T) {
	descriptors := []gocaller.DirectDescriptor{
		grpcDescriptor("example.test/a", "Client", "Call", "test.A/Call", "a"),
		grpcDescriptor("example.test/b", "Client", "Call", "test.B/Call", "b"),
	}
	resolver := &countingResolver{publication: resolverPublication(t, t.TempDir(), descriptors)}
	cache := newTestNamespaceCache(t, resolver)
	// Isolate the defensive counter boundary. A genuinely validated root cannot
	// contain more than MaxNamespaceReads distinct admitted namespaces itself.
	cache.reads = MaxNamespaceReads - 1
	if values, err := cache.namespace(t.Context(), "grpc", descriptors[0].ImportPath); err != nil || len(values) != 1 {
		t.Fatalf("last admitted positive read = %v, %v", values, err)
	}
	if _, err := cache.namespace(t.Context(), "grpc", descriptors[1].ImportPath); !errors.Is(err, ErrLimit) {
		t.Fatalf("positive limit error = %v", err)
	}
	if values, err := cache.namespace(t.Context(), "grpc", descriptors[0].ImportPath); err != nil || len(values) != 1 {
		t.Fatalf("cached positive at read cap = %v, %v", values, err)
	}
	if values, err := cache.namespace(t.Context(), "grpc", "example.test/absent"); err != nil || len(values) != 0 {
		t.Fatalf("proven absence at read cap = %v, %v", values, err)
	}
	if cache.reads != MaxNamespaceReads || resolver.reads != 1 || len(cache.values) != 1 {
		t.Fatalf("read cap changed work: cache=%d reads=%d entries=%d", cache.reads, resolver.reads, len(cache.values))
	}
}

type namespaceRootResolver struct {
	resolverSource
	root resolvernamespace.Root
}

func (resolver namespaceRootResolver) Root() resolvernamespace.Root { return resolver.root }

func TestBuildRejectsOversizedNamespaceRootBeforeLookup(t *testing.T) {
	root := t.TempDir()
	resolver := &countingResolver{publication: resolverPublication(t, root, nil)}
	snapshot := resolver.Root()
	snapshot.Namespaces = make([]resolvernamespace.NamespaceReceipt, resolvernamespace.MaxNamespaces+1)
	snapshot.NamespaceCount = len(snapshot.Namespaces)
	fixture := observationFixture(t, "cmd/plain.go", "package app\n", []sourcepartition.Placement{{
		Path: "cmd/plain.go", Mode: "100644", Revisions: []int{0},
	}})
	_, err := buildSources(t.Context(), root, fakeSource(t, []observedFixture{fixture}), namespaceRootResolver{
		resolverSource: resolver, root: snapshot,
	})
	if !errors.Is(err, resolvernamespace.ErrInvalid) || resolver.reads != 0 {
		t.Fatalf("oversized root bypassed admission: %v, reads=%d", err, resolver.reads)
	}
}

func TestNamespaceCachePreservesLookupInputValidation(t *testing.T) {
	resolver := &countingResolver{publication: resolverPublication(t, t.TempDir(), nil)}
	for _, test := range []struct {
		name, protocol, namespace string
	}{
		{"empty protocol", "", "example.test/absent"},
		{"unknown protocol", "http", "example.test/absent"},
		{"empty namespace", "grpc", ""},
		{"overlong namespace", "grpc", strings.Repeat("a", resolvernamespace.MaxTextBytes+1)},
		{"invalid utf8", "grpc", "example.test/\xff"},
		{"nul", "grpc", "example.test/\x00absent"},
		{"newline", "grpc", "example.test/\nabsent"},
		{"tab", "grpc", "example.test/\tabsent"},
		{"delete", "grpc", "example.test/\x7fabsent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache := newTestNamespaceCache(t, resolver)
			// A preexisting key must not bypass validation either.
			cache.values[test.protocol+"\x00"+test.namespace] = []resolvernamespace.Record{}
			_, directErr := resolver.publication.LookupNamespace(t.Context(), resolvernamespace.LanguageGo, test.protocol, test.namespace)
			_, cachedErr := cache.namespace(t.Context(), test.protocol, test.namespace)
			if !errors.Is(directErr, resolvernamespace.ErrInvalid) || !errors.Is(cachedErr, resolvernamespace.ErrInvalid) || cachedErr.Error() != directErr.Error() {
				t.Fatalf("validation diverged: cached=%v direct=%v", cachedErr, directErr)
			}
			if cache.reads != 0 || resolver.reads != 0 {
				t.Fatal("invalid key performed a namespace read")
			}
		})
	}
	if MaxTextBytes != resolvernamespace.MaxTextBytes {
		t.Fatal("RPC and resolver namespace text bounds diverged")
	}
}

func TestNamespaceCacheCancellationOnEveryPath(t *testing.T) {
	for _, mode := range []string{"absent", "present", "cached"} {
		t.Run(mode, func(t *testing.T) {
			descriptor := grpcDescriptor("example.test/present", "Client", "Call", "test.Service/Call", mode)
			resolver := &countingResolver{publication: resolverPublication(t, t.TempDir(), []gocaller.DirectDescriptor{descriptor})}
			cache := newTestNamespaceCache(t, resolver)
			namespace := descriptor.ImportPath
			if mode == "absent" {
				namespace = "example.test/absent"
			}
			if mode == "cached" {
				if _, err := cache.namespace(t.Context(), "grpc", namespace); err != nil {
					t.Fatal(err)
				}
			}
			beforeReads, beforeEntries := resolver.reads, len(cache.values)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := cache.namespace(ctx, "grpc", namespace); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled %s lookup = %v", mode, err)
			}
			if resolver.reads != beforeReads || cache.reads != beforeReads || len(cache.values) != beforeEntries {
				t.Fatal("canceled lookup changed cache or read work")
			}
		})
	}
}

func TestNamespaceCachePresentMemberMustRemainReadable(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			descriptor := grpcDescriptor("example.test/present", "Client", "Call", "test.Service/Call", mode)
			resolver := &countingResolver{publication: resolverPublication(t, root, []gocaller.DirectDescriptor{descriptor})}
			cache := newTestNamespaceCache(t, resolver)
			authority := resolver.publication.Root()
			repository := sha256.Sum256([]byte(authority.Authority.Repository))
			path := filepath.Join(root, "resolver-namespaces", hex.EncodeToString(repository[:]),
				"generation-"+strings.TrimPrefix(authority.GenerationDigest, "sha256:"), authority.Namespaces[0].Member)
			if mode == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := cache.namespace(t.Context(), "grpc", descriptor.ImportPath); err == nil {
				t.Fatal("present unreadable member became an empty namespace")
			}
			if resolver.reads != 1 || cache.reads != 0 || len(cache.values) != 0 {
				t.Fatal("failed member read was cached or counted as successful")
			}
		})
	}
}

type namespaceV2Source struct {
	fakeObservationSource
	authority observationpublication.DownstreamAuthority
}

func (source namespaceV2Source) DownstreamAuthority() observationpublication.DownstreamAuthority {
	return source.authority
}

func TestBuildV2UsesExactNamespaceInventory(t *testing.T) {
	root := t.TempDir()
	descriptor := grpcDescriptor("example.test/present", "Client", "Call", "test.Service/Call", "v2")
	resolver := resolverPublication(t, root, []gocaller.DirectDescriptor{descriptor})
	fixture := observationFixture(t, "cmd/call.go", `package app
import pb "example.test/present"
import other "example.test/absent"
func call(client pb.Client) { client.Call(nil); other.Unknown() }
`, []sourcepartition.Placement{{Path: "cmd/call.go", Mode: "100644", Revisions: []int{0}}})
	source := namespaceV2Source{fakeObservationSource: fakeSource(t, []observedFixture{fixture})}
	manifest := source.Manifest()
	source.authority = observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: manifest.Repository,
		SourceGenerationDigest: manifest.SourceGenerationDigest, SourceRootDigest: manifest.PartitionManifestDigest,
		ObservationGenerationDigest: manifest.GenerationDigest, ObservationRootDigest: manifest.Digest,
		PartitionPolicyDigest: manifest.PartitionPolicyDigest, ObservationPolicyDigest: manifest.ObservationPolicyDigest,
		InventoryPolicyDigest: "sha256:" + strings.Repeat("a", 64), RecordCount: 1, ObservedCount: 1,
	}
	upstream, err := downstreamauthority.Build(source.authority, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := BuildV2(t.Context(), BuildRequestV2{Root: root, Observations: source, Resolver: resolver, Upstream: upstream})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	value := publication.Root()
	if value.Schema != RootSchemaV2 || value.PostingCount != 1 || value.ResolvedCount != 1 || value.Policy != FrozenPolicy() ||
		value.Authority.ResolverRootDigest != resolver.Root().Digest {
		t.Fatalf("v2 posting changed authority or semantics: %+v", value)
	}
}
