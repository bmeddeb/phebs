package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestT421TailReadinessPollsBeforeSingleFinalAuthority(t *testing.T) {
	if got := t421TailReadinessLimits(); got != (readaccounting.Counts{
		ControlFileReads: 4, StoreReadAttempts: 4,
	}) {
		t.Fatalf("tail-readiness limits = %+v", got)
	}
	authService := newT421ExactReadAuthService(t)
	capture := &t421ExactReadTestCapture{}
	var tails, finals atomic.Uint64
	tail := t421ExactFinalAuthorityRead{
		Limits: t421TailReadinessLimits(),
		Read: func(ctx context.Context) ([]byte, func() error, error) {
			attempt := tails.Add(1)
			if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 4); err != nil {
				return nil, nil, err
			}
			if err := readaccounting.Charge(ctx, readaccounting.StoreReadAttempt, 4); err != nil {
				return nil, nil, err
			}
			status := "pending"
			if attempt != 2 {
				return t421TailReadinessMarshal(status, store.ServiceRuntimeSelector{}, relationshippublication.RootV3{}, nil)
			}
			return t421TailReadinessMarshal("ready",
				store.ServiceRuntimeSelector{Digest: "selected"},
				relationshippublication.RootV3{GenerationDigest: "relationship", Digest: "relationship-root"},
				&store.CallerGenerationPublicationSummary{
					Generation: store.CallerGenerationIdentity{Digest: "caller"}, ManifestDigest: "caller-root",
				},
			)
		},
	}
	final := t421ExactFinalAuthorityRead{
		Limits: readaccounting.Counts{},
		Read: func(context.Context) ([]byte, func() error, error) {
			finals.Add(1)
			return []byte("{\"schema\":\"final\"}\n"), nil, nil
		},
	}
	handler := authService.Require(t421ExactReadHandler(
		true, http.NotFoundHandler(), capture.report, capture.fail, final, tail,
	))

	wantBodies := []string{
		"{\"schema\":\"t421-tail-readiness-source-free-v1\",\"status\":\"pending\"}\n",
		"{\"schema\":\"t421-tail-readiness-source-free-v1\",\"status\":\"ready\",\"selected_runtime_sha256\":\"selected\",\"relationship_generation_sha256\":\"relationship\",\"relationship_root_sha256\":\"relationship-root\",\"caller_generation_sha256\":\"caller\",\"caller_root_sha256\":\"caller-root\"}\n",
	}
	for ordinal, wantBody := range wantBodies {
		response := serveT421ExactReadRequest(t, handler, exactT421ReadRequest(
			http.MethodGet, t421ExactTailReadinessPath, uint64(ordinal+1),
		))
		if response.status != http.StatusOK || string(response.body) != wantBody {
			t.Fatalf("tail %d = %d %q", ordinal+1, response.status, response.body)
		}
	}
	response := serveT421ExactReadRequest(t, handler, exactT421ReadRequest(
		http.MethodGet, t421ExactFinalAuthorityPath, 3,
	))
	if response.status != http.StatusOK || string(response.body) != "{\"schema\":\"final\"}\n" ||
		tails.Load() != 2 || finals.Load() != 1 {
		t.Fatalf("final = %d %q; tails=%d finals=%d", response.status, response.body, tails.Load(), finals.Load())
	}
	reports, failures := capture.snapshot()
	if len(reports) != 3 || len(failures) != 0 {
		t.Fatalf("reports=%q failures=%v", reports, failures)
	}
	for _, raw := range reports[:2] {
		var report t421ExactReadReport
		if err := json.Unmarshal(raw, &report); err != nil || report.Status != "complete" ||
			report.ControlFileReads != 4 || report.StoreReadAttempts != 4 ||
			report.MemberVisits != 0 || report.StoreWriteAttempts != 0 {
			t.Fatalf("tail accounting = %s, %v", raw, err)
		}
	}
}

func TestT421TailReadinessFailureIsSourceFreeAndTerminal(t *testing.T) {
	authService := newT421ExactReadAuthService(t)
	capture := &t421ExactReadTestCapture{}
	handler := authService.Require(t421ExactReadHandler(
		true, http.NotFoundHandler(), capture.report, capture.fail,
		t421ExactFinalAuthorityRead{Read: func(context.Context) ([]byte, func() error, error) {
			return []byte("{}"), nil, nil
		}},
		t421ExactFinalAuthorityRead{Limits: t421TailReadinessLimits(), Read: func(context.Context) ([]byte, func() error, error) {
			return nil, nil, errors.New("private tail cause")
		}},
	))
	response := serveT421ExactReadRequest(t, handler, exactT421ReadRequest(
		http.MethodGet, t421ExactTailReadinessPath, 1,
	))
	reports, failures := capture.snapshot()
	if response.status != http.StatusConflict || len(reports) != 1 ||
		!strings.Contains(string(reports[0]), `"status":"tail_readiness_refused"`) ||
		len(failures) != 1 || !errors.Is(failures[0], errT421ExactReadTail) ||
		strings.Contains(string(response.body)+string(reports[0]), "private") {
		t.Fatalf("response=%d %q reports=%q failures=%v", response.status, response.body, reports, failures)
	}
}

func TestT421TailCallerMustMatchSelectedRelationship(t *testing.T) {
	root := relationshippublication.RootV3{Authority: relationshippublication.AuthorityV3{
		Repository: "example.test/monorepo", ResolverGenerationDigest: "namespace",
		ResolverRootDigest: "namespace-root", UpstreamDigest: "all-domains",
	}}
	resolver := resolvernamespace.Root{Authority: resolvernamespace.Authority{
		Repository: "example.test/monorepo", ResolverGenerationDigest: "resolver",
		ResolverManifestDigest: "resolver-manifest",
	}}
	summary := store.CallerGenerationPublicationSummary{Generation: store.CallerGenerationIdentity{
		Repository: "example.test/monorepo", ResolverGenerationDigest: "resolver",
		ResolverManifestDigest: "resolver-manifest", UpstreamDigest: "caller-domains",
	}}
	if !t421TailCallerMatchesRelationship(summary, root, resolver) {
		t.Fatal("matching caller and selected relationship were refused")
	}
	summary.Generation.ResolverGenerationDigest = root.Authority.ResolverGenerationDigest
	if t421TailCallerMatchesRelationship(summary, root, resolver) {
		t.Fatal("namespace generation was accepted as resolver catalog generation")
	}
	summary.Generation.ResolverGenerationDigest = resolver.Authority.ResolverGenerationDigest
	summary.Generation.ResolverManifestDigest = "other"
	if t421TailCallerMatchesRelationship(summary, root, resolver) {
		t.Fatal("other resolver manifest was accepted")
	}
}

func TestT421TailRelationshipMustMatchSelectedRuntime(t *testing.T) {
	selector := store.ServiceRuntimeSelector{
		Repository: "example.test/monorepo", CatalogRootDigest: "catalog",
		CatalogControlRevision: 3, StateSummaryDigest: "state", StateControlRevision: 4,
		RelationshipGenerationDigest: "relationship", RelationshipRootDigest: "root",
	}
	root := relationshippublication.RootV3{
		GenerationDigest: "relationship", Digest: "root",
		Authority: relationshippublication.AuthorityV3{
			Repository: "example.test/monorepo", CatalogRootDigest: "catalog",
			CatalogControlRevision: 3, ServiceStateSummaryDigest: "state",
			ServiceStateControlRevision: 4,
		},
	}
	if !t421TailRelationshipMatchesSelector(selector, root) {
		t.Fatal("selected relationship coherence was refused")
	}
	root.Authority.ServiceStateSummaryDigest = "other"
	if t421TailRelationshipMatchesSelector(selector, root) {
		t.Fatal("relationship from another selected state was accepted")
	}
}

func TestT421TailRelationshipRetirementDistinguishesSupersessionFromCorruption(
	t *testing.T,
) {
	const repository = "example.test/t421-tail-retirement"
	pointer := func(fill string) relationshippublication.PointerV3 {
		value := relationshippublication.PointerV3{
			Schema: relationshippublication.PointerSchemaV3, Repository: repository,
			GenerationDigest: "sha256:" + strings.Repeat(fill, 64),
			RootDigest:       "sha256:" + strings.Repeat(fill, 64), RootName: "root.json",
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		value.Digest = "sha256:" + hex.EncodeToString(digest[:])
		return value
	}
	writeCurrent := func(t *testing.T, root string, value relationshippublication.PointerV3) {
		t.Helper()
		base, err := relationshippublication.RepositoryRootV3(root, repository)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(base, 0o700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "current.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	old := pointer("1")
	t.Run("advanced pointer is pending", func(t *testing.T) {
		root := t.TempDir()
		writeCurrent(t, root, pointer("2"))
		reader := &t421FinalAuthorityReader{repository: repository}
		value, pending, err := reader.readTailRelationship(t.Context(), root, old)
		if err != nil || !pending || value.GenerationDigest != "" {
			t.Fatalf("supersession = value=%+v pending=%t err=%v", value, pending, err)
		}
	})

	t.Run("unchanged pointer is corruption", func(t *testing.T) {
		root := t.TempDir()
		writeCurrent(t, root, old)
		reader := &t421FinalAuthorityReader{repository: repository}
		value, pending, err := reader.readTailRelationship(t.Context(), root, old)
		if !errors.Is(err, relationshippublication.ErrNotFound) || pending ||
			value.GenerationDigest != "" {
			t.Fatalf("corruption = value=%+v pending=%t err=%v", value, pending, err)
		}
	})

	relationship := relationshippublication.RootV3{Authority: relationshippublication.AuthorityV3{
		ResolverGenerationDigest: old.GenerationDigest, ResolverRootDigest: old.RootDigest,
	}}
	t.Run("advanced pointer after resolver retirement is pending", func(t *testing.T) {
		root := t.TempDir()
		writeCurrent(t, root, pointer("2"))
		reader := &t421FinalAuthorityReader{repository: repository, dataDir: t.TempDir()}
		value, pending, err := reader.readTailResolver(t.Context(), root, old, relationship)
		if err != nil || !pending || value.GenerationDigest != "" {
			t.Fatalf("resolver supersession = value=%+v pending=%t err=%v", value, pending, err)
		}
	})

	t.Run("unchanged pointer after resolver retirement is corruption", func(t *testing.T) {
		root := t.TempDir()
		writeCurrent(t, root, old)
		reader := &t421FinalAuthorityReader{repository: repository, dataDir: t.TempDir()}
		value, pending, err := reader.readTailResolver(t.Context(), root, old, relationship)
		if !errors.Is(err, resolvernamespace.ErrNotFound) || pending || value.GenerationDigest != "" {
			t.Fatalf("resolver corruption = value=%+v pending=%t err=%v", value, pending, err)
		}
	})
}
