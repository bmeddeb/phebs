package typedindex

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The literals below were captured from the committed PRE-CHANGE code (commit
// f241ffaf) for a fully deterministic Bazel fixture, before NewRequest/Admit began
// deriving the provider from the validated profile. They are golden bytes: if the
// leaf-7.5 derivation moved any canonical request byte, digest or managed key, the
// equality assertions fail. They are not recomputed from the post-change code, so
// the test cannot silently agree with itself.
const (
	goldenProfileDigest = "sha256:354e82ca33a8fe8d20fbe112502e2914bc545914321f26f10bc6c3881c051eb8"

	goldenV1JSON     = `{"schema":"phebs-typed-request-v1","action":"plan","source":{"repository":"example.test/team/repo","incarnation":"repo-1","generation":"sha256:41cf6794ba4200b839c53531555f0f3998df4cbb01a4d5cb0b94e3ca5e23947d","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"provider":"bazel-rules-go-scip-v1","profile_name":"reduced","profile_epoch":1,"profile_digest":"sha256:354e82ca33a8fe8d20fbe112502e2914bc545914321f26f10bc6c3881c051eb8","config_digest":"sha256:720f15bd4ec82b722fbb43de35fc0ac404bd46fe1ca73bd7f53a0bdac307b827","tools_digest":"sha256:0c2da4430844837f78c262ede0738e017354bfc16ac9fa1097c1fa974de4d996","universe_digest":"sha256:327a7380d2cc7cf09ed5820e1ecdb8abe585d696b5b5526986dfebe70acec59e","bundle_digest":"sha256:802312e6f5765775561eec89fd4066d9b7260bce9bb8a804a52e592157733f37","policy_digest":"sha256:f54a87ee98ddb03661801ed1b8ad42336752bd954a6a8cc1259828d598ff609a","idempotency_key":"key-1","parent_request_digest":"","plan_digest":""}`
	goldenV1Identity = "sha256:83627cab55639e086b68483d1064082a3757b89dd7958a1f2a419c3199dccfc0"

	goldenV2PublishKey      = "a4e0ca1dc4ab04e21bb46270b33652ff6ffbde2c75d790c9711464a616da3656"
	goldenV2PublishJSON     = `{"schema":"phebs-typed-request-v2","action":"plan","source":{"repository":"example.test/team/repo","incarnation":"repo-1","generation":"sha256:41cf6794ba4200b839c53531555f0f3998df4cbb01a4d5cb0b94e3ca5e23947d","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"provider":"bazel-rules-go-scip-v1","profile_name":"reduced","profile_epoch":1,"profile_digest":"sha256:354e82ca33a8fe8d20fbe112502e2914bc545914321f26f10bc6c3881c051eb8","config_digest":"sha256:720f15bd4ec82b722fbb43de35fc0ac404bd46fe1ca73bd7f53a0bdac307b827","tools_digest":"sha256:0c2da4430844837f78c262ede0738e017354bfc16ac9fa1097c1fa974de4d996","universe_digest":"sha256:327a7380d2cc7cf09ed5820e1ecdb8abe585d696b5b5526986dfebe70acec59e","bundle_digest":"sha256:802312e6f5765775561eec89fd4066d9b7260bce9bb8a804a52e592157733f37","policy_digest":"sha256:f54a87ee98ddb03661801ed1b8ad42336752bd954a6a8cc1259828d598ff609a","idempotency_key":"a4e0ca1dc4ab04e21bb46270b33652ff6ffbde2c75d790c9711464a616da3656","parent_request_digest":"","plan_digest":"","purpose":"publish"}`
	goldenV2PublishIdentity = "sha256:638e7387b3aa026dfe0998c7bf4e756098d97c6b94cbc96a53f722185ca03332"

	goldenV2CanaryKey      = "345db33fe146f9c2f9b0f4ce364cd293ac6bac27b97fa8273036c2ce31cd4e7c"
	goldenV2CanaryJSON     = `{"schema":"phebs-typed-request-v2","action":"plan","source":{"repository":"example.test/team/repo","incarnation":"repo-1","generation":"sha256:41cf6794ba4200b839c53531555f0f3998df4cbb01a4d5cb0b94e3ca5e23947d","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"provider":"bazel-rules-go-scip-v1","profile_name":"reduced","profile_epoch":1,"profile_digest":"sha256:354e82ca33a8fe8d20fbe112502e2914bc545914321f26f10bc6c3881c051eb8","config_digest":"sha256:720f15bd4ec82b722fbb43de35fc0ac404bd46fe1ca73bd7f53a0bdac307b827","tools_digest":"sha256:0c2da4430844837f78c262ede0738e017354bfc16ac9fa1097c1fa974de4d996","universe_digest":"sha256:327a7380d2cc7cf09ed5820e1ecdb8abe585d696b5b5526986dfebe70acec59e","bundle_digest":"sha256:802312e6f5765775561eec89fd4066d9b7260bce9bb8a804a52e592157733f37","policy_digest":"sha256:f54a87ee98ddb03661801ed1b8ad42336752bd954a6a8cc1259828d598ff609a","idempotency_key":"345db33fe146f9c2f9b0f4ce364cd293ac6bac27b97fa8273036c2ce31cd4e7c","parent_request_digest":"","plan_digest":"","purpose":"canary"}`
	goldenV2CanaryIdentity = "sha256:e5fb7204785fedd978758286f14c7d11566132d82f648ac9b7de33cb41e8471d"

	goldenV2DryRunKey      = "bf44767cecbe5026e7f2f43b9532070f258013c11ebc57e9f250967d6739cbf1"
	goldenV2DryRunJSON     = `{"schema":"phebs-typed-request-v2","action":"plan","source":{"repository":"example.test/team/repo","incarnation":"repo-1","generation":"sha256:41cf6794ba4200b839c53531555f0f3998df4cbb01a4d5cb0b94e3ca5e23947d","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"provider":"bazel-rules-go-scip-v1","profile_name":"reduced","profile_epoch":1,"profile_digest":"sha256:354e82ca33a8fe8d20fbe112502e2914bc545914321f26f10bc6c3881c051eb8","config_digest":"sha256:720f15bd4ec82b722fbb43de35fc0ac404bd46fe1ca73bd7f53a0bdac307b827","tools_digest":"sha256:0c2da4430844837f78c262ede0738e017354bfc16ac9fa1097c1fa974de4d996","universe_digest":"sha256:327a7380d2cc7cf09ed5820e1ecdb8abe585d696b5b5526986dfebe70acec59e","bundle_digest":"sha256:802312e6f5765775561eec89fd4066d9b7260bce9bb8a804a52e592157733f37","policy_digest":"sha256:f54a87ee98ddb03661801ed1b8ad42336752bd954a6a8cc1259828d598ff609a","idempotency_key":"bf44767cecbe5026e7f2f43b9532070f258013c11ebc57e9f250967d6739cbf1","parent_request_digest":"","plan_digest":"","purpose":"dry-run"}`
	goldenV2DryRunIdentity = "sha256:5b8afadbccd00220774f55cc0c1ee9256a5af00a07d63f7b423a3c244594d80f"
)

// goldenFixture rebuilds the exact deterministic Bazel profile/source/universe the
// golden literals were captured from. Every input is a fixed sha256 of a literal
// string, so the fixture is reproducible and the goldens are meaningful.
func goldenFixture(t *testing.T) (Profile, Source, string) {
	t.Helper()
	ctx := context.Background()
	invData := wire(t, InventoryDefinition{InventorySchema, []BundleFile{{"tools/bin/go", 1, hash([]byte("x")), true}}})
	inv, err := DecodeInventory(ctx, invData, hash(invData))
	if err != nil {
		t.Fatal(err)
	}
	tool := Tool{Version: "1.0", Digest: hash([]byte("tool"))}
	def := ProfileDefinition{Schema: ProfileSchema, Name: "reduced", Provider: ProviderID, Tools: Tools{tool, tool, tool, tool, tool, tool, tool}, Config: ReducedConfig(), Policy: MeasuredPolicy(), BundleDigest: inv.Digest(), ImageDigest: hash([]byte("image"))}
	p, err := DecodeProfile(ctx, wire(t, def))
	if err != nil {
		t.Fatal(err)
	}
	source := Source{Repository: "example.test/team/repo", Incarnation: "repo-1", Generation: hash([]byte("source")), Commit: strings.Repeat("a", 40)}
	return p, source, hash([]byte("universe"))
}

func TestProviderDerivationGolden(t *testing.T) {
	p, source, universe := goldenFixture(t)

	// The profile accessor exposes exactly the frozen Bazel discriminant.
	if p.Provider() != ProviderID {
		t.Fatalf("Profile.Provider() = %q, want %q", p.Provider(), ProviderID)
	}
	if p.Digest() != goldenProfileDigest {
		t.Fatalf("profile digest moved:\n got %s\nwant %s", p.Digest(), goldenProfileDigest)
	}

	// v1 plan request: provider derived from the profile equals the frozen
	// constant, and the whole canonical encoding plus identity is byte-identical
	// to the pre-change golden.
	v1 := NewRequest(source, p, 1, universe, "key-1")
	if v1.Provider != ProviderID || v1.Provider != p.Provider() {
		t.Fatalf("NewRequest provider = %q, want derived %q", v1.Provider, p.Provider())
	}
	if got := string(wire(t, v1)); got != goldenV1JSON {
		t.Fatalf("v1 canonical JSON moved:\n got %s\nwant %s", got, goldenV1JSON)
	}
	if got := identity(v1); got != goldenV1Identity {
		t.Fatalf("v1 identity moved:\n got %s\nwant %s", got, goldenV1Identity)
	}

	// v2 managed requests: the deterministic managed key, canonical encoding and
	// identity for every purpose are byte-identical to the pre-change goldens.
	// managedKey marshals the request including Provider, so an unchanged key
	// proves the derived provider did not perturb the managed-key derivation.
	for _, tc := range []struct {
		purpose  Purpose
		key      string
		json     string
		identity string
	}{
		{Publish, goldenV2PublishKey, goldenV2PublishJSON, goldenV2PublishIdentity},
		{Canary, goldenV2CanaryKey, goldenV2CanaryJSON, goldenV2CanaryIdentity},
		{DryRun, goldenV2DryRunKey, goldenV2DryRunJSON, goldenV2DryRunIdentity},
	} {
		v2 := NewManagedRequest(source, p, 1, universe, tc.purpose)
		if v2.Provider != p.Provider() {
			t.Fatalf("managed(%s) provider = %q, want derived %q", tc.purpose, v2.Provider, p.Provider())
		}
		if v2.IdempotencyKey != tc.key {
			t.Fatalf("managed(%s) key moved:\n got %s\nwant %s", tc.purpose, v2.IdempotencyKey, tc.key)
		}
		if got := string(wire(t, v2)); got != tc.json {
			t.Fatalf("managed(%s) canonical JSON moved:\n got %s\nwant %s", tc.purpose, got, tc.json)
		}
		if got := identity(v2); got != tc.identity {
			t.Fatalf("managed(%s) identity moved:\n got %s\nwant %s", tc.purpose, got, tc.identity)
		}
		// The managed key must still self-validate, proving the derivation did not
		// break the v2 purpose/key contract.
		if err := v2.ValidatePurpose(); err != nil {
			t.Fatalf("managed(%s) ValidatePurpose = %v, want nil", tc.purpose, err)
		}
	}
}

func TestCrossProviderReplayFence(t *testing.T) {
	ctx := context.Background()
	p, source, universe := goldenFixture(t)
	auth := Authority{Enabled: true, Administrator: true, Source: source, Profile: Epoch{1, p.Digest()}, UniverseDigest: universe}

	// Positive control: the untampered Bazel request still admits. Without this,
	// the refusals below could be an artifact of a broken fixture.
	base := NewRequest(source, p, 1, universe, "key-1")
	ok, err := Admit(ctx, auth, p, wire(t, base))
	if err != nil || ok.Digest() == "" {
		t.Fatalf("valid Bazel request refused: %v (digest %q)", err, ok.Digest())
	}

	// A request that claims a different closed provider than its profile carries is
	// a cross-provider replay and must be refused, even though every other field is
	// byte-identical to the admitted request. The provider is the only mutation.
	for _, alien := range []string{ModuleProviderID, ImportProviderID} {
		bad := base
		bad.Provider = alien
		got, err := Admit(ctx, auth, p, wire(t, bad))
		if err == nil || got.Digest() != "" {
			t.Fatalf("Admit accepted a %q request against a %q profile", alien, p.Provider())
		}
		if !errors.Is(err, Stale) {
			t.Fatalf("cross-provider replay(%s) = %v, want %v", alien, err, Stale)
		}
	}

	// An arbitrary non-closed provider string is likewise refused (this was already
	// true pre-change and must remain so).
	bad := base
	bad.Provider = "shell"
	if got, err := Admit(ctx, auth, p, wire(t, bad)); err == nil || got.Digest() != "" {
		t.Fatal("Admit accepted an arbitrary provider string")
	}
}
