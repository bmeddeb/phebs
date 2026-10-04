package typedindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"testing"
)

func wire(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func fixture(t *testing.T) (Profile, Authority, Request, Inventory) {
	t.Helper()
	ctx := context.Background()
	data := wire(t, InventoryDefinition{InventorySchema, []BundleFile{{"tools/bin/go", 1, hash([]byte("x")), true}}})
	inv, err := DecodeInventory(ctx, data, hash(data))
	if err != nil {
		t.Fatal(err)
	}
	tool := Tool{Version: "1.0", Digest: hash([]byte("tool"))}
	d := ProfileDefinition{Schema: ProfileSchema, Name: "reduced", Provider: ProviderID, Tools: Tools{tool, tool, tool, tool, tool, tool, tool}, Config: ReducedConfig(), Policy: MeasuredPolicy(), BundleDigest: inv.Digest(), ImageDigest: hash([]byte("image"))}
	p, err := DecodeProfile(ctx, wire(t, d))
	if err != nil {
		t.Fatal(err)
	}
	source := Source{Repository: "example.test/team/repo", Incarnation: "repo-1", Generation: hash([]byte("source")), Commit: strings.Repeat("a", 40)}
	a := Authority{Enabled: true, Administrator: true, Source: source, Profile: Epoch{1, p.Digest()}, UniverseDigest: hash([]byte("universe"))}
	r := NewRequest(source, p, 1, a.UniverseDigest, "key-1")
	return p, a, r, inv
}
func admit(t *testing.T, p Profile, a Authority, r Request) Admission {
	t.Helper()
	got, err := Admit(context.Background(), a, p, wire(t, r))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAuthorityAndWireRefuseBeforeAdmission(t *testing.T) {
	p, a, r, _ := fixture(t)
	raw := wire(t, r)
	tests := []struct {
		name      string
		authority Authority
		raw       []byte
		want      error
	}{
		{"disabled first", Authority{}, []byte("bad"), Disabled},
		{"visibility is not administration", Authority{Enabled: true}, []byte("bad"), Forbidden},
		{"unknown", a, bytes.Replace(raw, []byte(`"schema":`), []byte(`"command":"sh","schema":`), 1), Invalid},
		{"duplicate", a, bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"x","schema":`), 1), Invalid},
		{"nested duplicate", a, bytes.Replace(raw, []byte(`"commit":`), []byte(`"commit":"a","commit":`), 1), Invalid},
		{"omitted empty field", a, bytes.Replace(raw, []byte(`,"plan_digest":""`), nil, 1), Invalid},
		{"case alias", a, bytes.Replace(raw, []byte(`"schema"`), []byte(`"Schema"`), 1), Invalid},
		{"null", a, []byte("null"), Invalid},
		{"second value", a, append(bytes.Clone(raw), []byte("{}")...), Invalid},
		{"oversize", a, bytes.Repeat([]byte(" "), MaxRequestBytes+1), Invalid},
		{"invalid UTF8", a, append(bytes.Clone(raw), 0xff), Invalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Admit(context.Background(), tt.authority, p, tt.raw)
			if !errors.Is(err, tt.want) || got.Digest() != "" {
				t.Fatalf("admit = %v %v", got, err)
			}
		})
	}
	// Positive control prevents malformed fixtures alone from proving refusal.
	if got := admit(t, p, a, r); got.Digest() == "" {
		t.Fatal("valid request refused")
	}
	mutations := []struct {
		name   string
		change func(*Request)
	}{
		{"historical commit", func(r *Request) { r.Source.Commit = strings.Repeat("b", 40) }},
		{"branch selector", func(r *Request) { r.Source.Commit = "main" }},
		{"incarnation", func(r *Request) { r.Source.Incarnation = "repo-2" }},
		{"generation", func(r *Request) { r.Source.Generation = hash([]byte("other")) }},
		{"repository", func(r *Request) { r.Source.Repository = "example.test/other" }},
		{"unsafe repository", func(r *Request) { r.Source.Repository = "../other" }},
		{"profile", func(r *Request) { r.ProfileDigest = hash([]byte("other")) }},
		{"epoch", func(r *Request) { r.ProfileEpoch++ }},
		{"config", func(r *Request) { r.ConfigDigest = hash([]byte("other")) }},
		{"tools", func(r *Request) { r.ToolsDigest = hash([]byte("other")) }},
		{"bundle", func(r *Request) { r.BundleDigest = hash([]byte("other")) }},
		{"policy", func(r *Request) { r.PolicyDigest = hash([]byte("other")) }},
		{"universe", func(r *Request) { r.UniverseDigest = hash([]byte("other")) }},
		{"provider", func(r *Request) { r.Provider = "shell" }},
		{"action", func(r *Request) { r.Action = "run-shell" }},
		{"empty key", func(r *Request) { r.IdempotencyKey = "" }},
		{"plan in planning", func(r *Request) { r.PlanDigest = hash([]byte("map")) }},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			bad := r
			tt.change(&bad)
			if got, err := Admit(context.Background(), a, p, wire(t, bad)); err == nil || got.Digest() != "" {
				t.Fatal("accepted mutated authority")
			}
		})
	}
}

func TestProfileReplacementsAndPlannedSuccessor(t *testing.T) {
	ctx := context.Background()
	p, a, r, _ := fixture(t)
	first := admit(t, p, a, r)
	same := admit(t, p, a, r)
	if same.Digest() != first.Digest() {
		t.Fatal("idempotent intent changed")
	}
	different := r
	different.IdempotencyKey = "key-2"
	if admit(t, p, a, different).Digest() == first.Digest() {
		t.Fatal("key aliases")
	}
	// A -> B -> A values retain distinct durable epochs and request identities.
	d := p.Definition()
	d.Tools.Driver.Digest = hash([]byte("new tool"))
	b, err := DecodeProfile(ctx, wire(t, d))
	if err != nil {
		t.Fatal(err)
	}
	eb, err := AdvanceProfile(ctx, a.Profile, b)
	if err != nil {
		t.Fatal(err)
	}
	ea, err := AdvanceProfile(ctx, eb, p)
	if err != nil {
		t.Fatal(err)
	}
	a.Profile = ea
	if _, err := Admit(ctx, a, p, wire(t, r)); !errors.Is(err, Stale) {
		t.Fatalf("old request not stale: %v", err)
	}
	r.ProfileEpoch = ea.Number
	last := admit(t, p, a, r)
	if last.Digest() == first.Digest() {
		t.Fatal("ABA aliases")
	}
	sameEpoch, err := AdvanceProfile(ctx, ea, p)
	if err != nil || sameEpoch.Number != ea.Number+1 {
		t.Fatal("equal-value replacement aliases")
	}
	if _, err := AdvanceProfile(ctx, Epoch{math.MaxUint64, p.Digest()}, p); !errors.Is(err, Invalid) {
		t.Fatal("epoch wrapped")
	}
	successor, err := PlannedSuccessor(ctx, last, hash([]byte("map")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Admit(ctx, a, p, wire(t, successor)); !errors.Is(err, Stale) {
		t.Fatal("unrecorded plan admitted")
	}
	a.ParentRequestDigest = last.Digest()
	a.PlanDigest = successor.PlanDigest
	execution := admit(t, p, a, successor)
	if execution.Digest() == last.Digest() {
		t.Fatal("plan not bound")
	}
	if _, err := PlannedSuccessor(ctx, execution, hash([]byte("other"))); !errors.Is(err, Invalid) {
		t.Fatal("recursive successor")
	}
	a.Administrator = false
	if _, err := Admit(ctx, a, p, wire(t, successor)); !errors.Is(err, Forbidden) {
		t.Fatal("nonadmin execution")
	}
}

func TestClosedProfileAndCommands(t *testing.T) {
	ctx := context.Background()
	p, _, _, _ := fixture(t)
	tests := []struct {
		name   string
		change func(*ProfileDefinition)
	}{
		{"tests", func(p *ProfileDefinition) { p.Config.SkipTests = false }},
		{"implementations", func(p *ProfileDefinition) { p.Config.SkipImplementations = false }},
		{"generated", func(p *ProfileDefinition) { p.Config.GeneratedDocuments = "read" }},
		{"network", func(p *ProfileDefinition) { p.Config.Network = "host" }},
		{"scratch", func(p *ProfileDefinition) { p.Config.Scratch = "tmpfs" }},
		{"wall", func(p *ProfileDefinition) { p.Policy.WallSeconds++ }},
		{"inodes", func(p *ProfileDefinition) { p.Policy.ScratchInodes++ }},
		{"unsafe version", func(p *ProfileDefinition) { p.Tools.Driver.Version = "../repo/driver" }},
		{"rc import", func(p *ProfileDefinition) { p.RCDigest = hash([]byte("import /etc/bazelrc\n")) }},
	}
	for _, schema := range []string{ProfileSchema, GeneratedProfileSchema} {
		for _, tt := range tests {
			t.Run(schema+"/"+tt.name, func(t *testing.T) {
				d := p.Definition()
				if schema == GeneratedProfileSchema {
					d.Schema, d.Config = schema, GeneratedConfig()
				}
				tt.change(&d)
				if _, err := DecodeProfile(ctx, wire(t, d)); err == nil {
					t.Fatal("accepted widened profile")
				}
			})
		}
	}
	raw := wire(t, p.Definition())
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"skip_tests":true`), []byte(`"skip_tests":true,"skip_tests":true`), 1),
		bytes.Replace(raw, []byte(`,"rc_digest":""`), nil, 1),
		bytes.Replace(raw, []byte(`"tools":{`), []byte(`"tools":{"executable":"/repo/driver",`), 1),
	} {
		if _, err := DecodeProfile(ctx, bad); err == nil {
			t.Fatal("accepted malformed profile")
		}
	}
	hostSchema, hostConfig := HostReducedIdentity()
	hostDef := p.Definition()
	hostDef.Schema, hostDef.Config = hostSchema, hostConfig
	host, err := DecodeProfile(ctx, wire(t, hostDef))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOARCH != "arm64" {
		if got, err := p.Commands(); err == nil || got.Driver != "" {
			t.Fatal("historical arm64 profile emitted commands on another host", err)
		}
		if host.Digest() == p.Digest() {
			t.Fatal("amd64 successor aliases the historical profile")
		}
	}
	commands, err := host.Commands()
	if err != nil {
		t.Fatal(err)
	}
	if commands.Driver != DriverPath || commands.Launcher != LauncherPath || commands.BazelStartup[3] != "--bazelrc=/dev/null" || !strings.Contains(strings.Join(commands.Environment, "\n"), "GOARCH="+runtime.GOARCH) {
		t.Fatal(commands)
	}
	commands.Environment[0] = "injected"
	again, _ := host.Commands()
	if again.Environment[0] == "injected" {
		t.Fatal("mutable recipe")
	}
	if err := p.VerifyRC(ctx, []byte(ResolvedRC)); err == nil {
		t.Fatal("unexpected rc accepted")
	}
	d := p.Definition()
	d.RCDigest = hash([]byte(ResolvedRC))
	withRC, err := DecodeProfile(ctx, wire(t, d))
	if err != nil {
		t.Fatal(err)
	}
	if err := withRC.VerifyRC(ctx, []byte(ResolvedRC)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{ResolvedRC + "import /tmp/x\n", "", ResolvedRC + "build --remote_cache=x\n"} {
		if withRC.VerifyRC(ctx, []byte(bad)) == nil {
			t.Fatal("rc override accepted")
		}
	}
	if Describe().ExecutionAvailable || Describe().Tests || Describe().Implementations || Describe().GeneratedDocuments {
		t.Fatal("claims unwritten executor/coverage")
	}
}

func TestPreparedCustodyCannotBeSkippedOrReused(t *testing.T) {
	ctx := context.Background()
	p, a, r, inv := fixture(t)
	parent := admit(t, p, a, r)
	r, err := PlannedSuccessor(ctx, parent, hash([]byte("map")))
	if err != nil {
		t.Fatal(err)
	}
	a.ParentRequestDigest = parent.Digest()
	a.PlanDigest = r.PlanDigest
	got := admit(t, p, a, r)
	custody, err := got.Custody()
	if err != nil {
		t.Fatal(err)
	}
	evidence := Preparation{ObservedPolicy: p.definition.Policy, ToolsDigest: p.toolsDigest, ImageDigest: p.definition.ImageDigest, RCDigest: p.definition.RCDigest, RequestDigest: got.Digest(), BundleDigest: inv.Digest(), InventoryFiles: len(inv.Files()), InventoryBytes: inv.Bytes(), PrivateInputs: custody.Inputs, CopiedAndVerified: true, ImmutableInputs: true, ToolsVerified: true, RCVerified: true, DirectIO: true, NetworkDenied: true, PrivateScratch: true, CapacityReserved: true}
	if err := got.ValidatePreparation(ctx, inv, evidence); err != nil {
		t.Fatal(err)
	}
	cases := []func(*Preparation){func(p *Preparation) { p.ObservedPolicy.MemoryBytes++ }, func(p *Preparation) { p.ToolsDigest = hash(nil) }, func(p *Preparation) { p.ImageDigest = hash(nil) }, func(p *Preparation) { p.RCDigest = hash(nil) }, func(p *Preparation) { p.CopiedAndVerified = false }, func(p *Preparation) { p.ImmutableInputs = false }, func(p *Preparation) { p.ToolsVerified = false }, func(p *Preparation) { p.RCVerified = false }, func(p *Preparation) { p.DirectIO = false }, func(p *Preparation) { p.NetworkDenied = false }, func(p *Preparation) { p.PrivateScratch = false }, func(p *Preparation) { p.CapacityReserved = false }, func(p *Preparation) { p.PrivateInputs = "shared/cache" }, func(p *Preparation) { p.RequestDigest = parent.Digest() }, func(p *Preparation) { p.InventoryBytes++ }, func(p *Preparation) { p.InventoryFiles++ }, func(p *Preparation) { p.BundleDigest = hash(nil) }}
	for i, mutate := range cases {
		bad := evidence
		mutate(&bad)
		if got.ValidatePreparation(ctx, inv, bad) == nil {
			t.Fatalf("accepted %d", i)
		}
	}
	if parent.ValidatePreparation(ctx, inv, evidence) == nil {
		t.Fatal("preparation from a different request accepted")
	}
	planCustody, err := parent.Custody()
	if err != nil {
		t.Fatal(err)
	}
	planEvidence := evidence
	planEvidence.RequestDigest = parent.Digest()
	planEvidence.PrivateInputs = planCustody.Inputs
	if err := parent.ValidatePreparation(ctx, inv, planEvidence); err != nil {
		t.Fatal("planning requires and accepts its own verified preparation", err)
	}
	different := r
	different.IdempotencyKey = "other"
	if _, err := Admit(ctx, a, p, wire(t, different)); !errors.Is(err, Stale) {
		t.Fatal("modified successor body accepted")
	}
	different.Action, different.ParentRequestDigest, different.PlanDigest = Plan, "", ""
	newParent := admit(t, p, a, different)
	different, err = PlannedSuccessor(ctx, newParent, hash([]byte("map")))
	if err != nil {
		t.Fatal(err)
	}
	a.ParentRequestDigest = newParent.Digest()
	other := admit(t, p, a, different)
	paths, _ := other.Custody()
	if paths.Root == custody.Root {
		t.Fatal("shared custody")
	}
}

func TestInventoryAndProgressBounds(t *testing.T) {
	ctx := context.Background()
	_, _, _, inv := fixture(t)
	files := inv.Files()
	files[0].Path = "mutated"
	if inv.Files()[0].Path == "mutated" {
		t.Fatal("inventory mutated")
	}
	base := BundleFile{Path: "tools/a", Bytes: 1, Digest: hash(nil)}
	cases := [][]BundleFile{
		{}, {base, base}, {{Path: "../escape", Bytes: 1, Digest: hash(nil)}},
		{base, {Path: "tools/a/b", Bytes: 1, Digest: hash(nil)}},
		{{Path: "tools/a", Bytes: MaxFileBytes + 1, Digest: hash(nil)}},
		{{Path: "tools/a", Bytes: -1, Digest: hash(nil)}},
		{{Path: "tools/a", Bytes: 1, Digest: "bad"}},
	}
	for i, fs := range cases {
		raw := wire(t, InventoryDefinition{InventorySchema, fs})
		if _, err := DecodeInventory(ctx, raw, hash(raw)); err == nil {
			t.Fatalf("bad inventory %d accepted", i)
		}
	}
	for _, name := range []string{".", "/absolute", "a\\b", "a:b", "a//b", "a/../b", "a/\x01b", strings.Repeat("a", 256)} {
		if bundlePath(name) {
			t.Fatal(name)
		}
	}
	raw := wire(t, InventoryDefinition{InventorySchema, inv.Files()})
	if _, err := DecodeInventory(ctx, raw, hash(nil)); err == nil {
		t.Fatal("manifest hash ignored")
	}
	p := Progress{Stage: StagePlanning, State: "running"}
	if _, err := DecodeProgress(ctx, wire(t, p)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Progress{{Stage: StagePlanning, State: "refused", Reason: "secret raw error"}, {Stage: "shell", State: "running"}, {Stage: StagePlanning, State: "complete", Reason: Stale}, {Stage: StagePlanning, State: "running", ElapsedMillis: -1}, {Stage: StagePlanning, State: "running", OutputBytes: 16777217}} {
		if _, err := DecodeProgress(ctx, wire(t, bad)); err == nil {
			t.Fatal("unsafe progress accepted")
		}
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := DecodeInventory(cancelCtx, raw, hash(raw)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInventoryCountAndAggregateCeilings(t *testing.T) {
	ctx := context.Background()
	// Ascending fixed-width paths make the final entry the only count violation.
	files := make([]BundleFile, MaxInventoryFiles)
	for i := range files {
		files[i] = BundleFile{Path: fmt.Sprintf("tools/%05d", i), Digest: hash(nil)}
	}
	raw := wire(t, InventoryDefinition{InventorySchema, files})
	if _, err := DecodeInventory(ctx, raw, hash(raw)); err != nil {
		t.Fatal(err)
	}
	files = append(files, BundleFile{Path: "tools/50000", Digest: hash(nil)})
	raw = wire(t, InventoryDefinition{InventorySchema, files})
	if _, err := DecodeInventory(ctx, raw, hash(raw)); err == nil {
		t.Fatal("file count overflow accepted")
	}
	files = files[:9]
	for i := range files {
		files[i].Bytes = MaxFileBytes
	}
	raw = wire(t, InventoryDefinition{InventorySchema, files[:8]})
	if _, err := DecodeInventory(ctx, raw, hash(raw)); err != nil {
		t.Fatal("exact byte ceiling", err)
	}
	raw = wire(t, InventoryDefinition{InventorySchema, files})
	if _, err := DecodeInventory(ctx, raw, hash(raw)); err == nil {
		t.Fatal("aggregate byte overflow accepted")
	}
	raw = bytes.Repeat([]byte(" "), MaxInventoryBytes+1)
	if _, err := DecodeInventory(ctx, raw, hash(raw)); err == nil {
		t.Fatal("manifest byte overflow accepted")
	}
	p := Progress{Stage: StageExecution, State: "refused", Reason: WallLimit, ElapsedMillis: 300050}
	if _, err := DecodeProgress(ctx, wire(t, p)); err != nil {
		t.Fatal("timeout overshoot observation refused", err)
	}
}

func TestDisabledAdmissionDoesNotAllocate(t *testing.T) {
	p, _, _, _ := fixture(t)
	raw := []byte(`malformed request with no authority`)
	allocations := testing.AllocsPerRun(100, func() {
		_, err := Admit(context.Background(), Authority{}, p, raw)
		if err != Disabled {
			panic("unexpected admission")
		}
	})
	if allocations != 0 {
		t.Fatalf("disabled allocations = %v", allocations)
	}
}

func TestPolicyMatchesSealedEvidence(t *testing.T) {
	raw, err := os.ReadFile("../../spike/t451b/phase2-direct-results.json")
	if err != nil {
		t.Fatal(err)
	}
	if hash(raw) != "sha256:371fe219b2d6734d59ab43305c97238eb6b874e8e9f010546278c8acc2163c13" {
		t.Fatal("sealed evidence changed")
	}
	var evidence struct {
		Caps struct {
			Policy
			CPUs int64 `json:"cpus"`
		} `json:"caps"`
	}
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	evidence.Caps.CPUQuotaMicros = evidence.Caps.CPUs * 100000
	evidence.Caps.CPUPeriodMicros = 100000
	if got := MeasuredPolicy(); got != evidence.Caps.Policy {
		t.Fatalf("policy differs from sealed caps: %+v", got)
	}
}

func TestGeneratedProfileIsExplicitAndReducedIdentityUnchanged(t *testing.T) {
	p, a, r, _ := fixture(t)
	reducedProfile := wire(t, p.Definition())
	reducedRequest := wire(t, r)
	// Captured independently from the unchanged fixture at 836575e4.
	if p.Digest() != "sha256:354e82ca33a8fe8d20fbe112502e2914bc545914321f26f10bc6c3881c051eb8" || hash(reducedRequest) != "sha256:83627cab55639e086b68483d1064082a3757b89dd7958a1f2a419c3199dccfc0" {
		t.Fatal("historical reduced profile/request identity changed")
	}
	for _, tt := range []struct {
		name, schema, mode string
		allowed            bool
	}{
		{"legacy omit", ProfileSchema, "omit", true},
		{"legacy cannot enable", ProfileSchema, "sealed", false},
		{"explicit sealed", GeneratedProfileSchema, "sealed", true},
		{"new schema cannot masquerade as legacy", GeneratedProfileSchema, "omit", false},
		{"unknown schema", "phebs-typed-profile-v3", "sealed", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := p.Definition()
			d.Schema = tt.schema
			d.Config.GeneratedDocuments = tt.mode
			got, err := DecodeProfile(t.Context(), wire(t, d))
			if (err == nil) != tt.allowed {
				t.Fatal(got, err)
			}
			if err != nil {
				return
			}
			if got.Definition().Policy != MeasuredPolicy() || !got.Definition().Config.SkipTests || !got.Definition().Config.SkipImplementations {
				t.Fatal("policy changed")
			}
			if tt.schema == ProfileSchema {
				if !bytes.Equal(reducedProfile, wire(t, got.Definition())) || p.Digest() != got.Digest() {
					t.Fatal("reduced profile changed")
				}
				old := admit(t, got, a, r)
				if !bytes.Equal(reducedRequest, wire(t, old.Request())) {
					t.Fatal("reduced request changed")
				}
			} else if got.Digest() == p.Digest() {
				t.Fatal("generated authority aliases reduced")
			}
		})
	}
	if Describe().ExecutionAvailable || Describe().GeneratedDocuments {
		t.Fatal("contract enabled runtime capability")
	}
	successor := p.Definition()
	successor.Schema, successor.Config = Amd64ProfileSchema, Amd64ReducedConfig()
	got, err := DecodeProfile(t.Context(), wire(t, successor))
	if err != nil || got.Digest() == p.Digest() || got.Definition().Config.GOARCH != "amd64" {
		t.Fatal("amd64 successor refused or aliased", err)
	}
	masquerade := successor
	masquerade.Schema = ProfileSchema
	if _, err = DecodeProfile(t.Context(), wire(t, masquerade)); err == nil {
		t.Fatal("historical schema accepted amd64 config")
	}
	masquerade = p.Definition()
	masquerade.Schema = Amd64ProfileSchema
	if _, err = DecodeProfile(t.Context(), wire(t, masquerade)); err == nil {
		t.Fatal("amd64 schema accepted the historical arm64 config")
	}
}

func TestManagedPurposeIdentity(t *testing.T) {
	p, auth, legacy, _ := fixture(t)
	if err := auth.Source.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Source{}).Validate(); !errors.Is(err, Invalid) {
		t.Fatal(err)
	}
	for _, bad := range []Request{NewManagedRequest(Source{}, p, 1, auth.UniverseDigest, Publish), NewManagedRequest(auth.Source, Profile{}, 1, auth.UniverseDigest, Publish), NewManagedRequest(auth.Source, p, 0, auth.UniverseDigest, Publish), NewManagedRequest(auth.Source, p, 1, "invalid", Publish), NewManagedRequest(auth.Source, p, 1, auth.UniverseDigest, "unknown")} {
		if bad != (Request{}) {
			t.Fatal("invalid factory shape returned authority")
		}
	}
	seen := map[string]bool{}
	for _, purpose := range []Purpose{Publish, Canary, DryRun} {
		t.Run(string(purpose), func(t *testing.T) {
			r := NewManagedRequest(auth.Source, p, auth.Profile.Number, auth.UniverseDigest, purpose)
			if r != NewManagedRequest(auth.Source, p, auth.Profile.Number, auth.UniverseDigest, purpose) || len(r.IdempotencyKey) != 64 {
				t.Fatal("unstable managed identity")
			}
			a := admit(t, p, auth, r)
			if a.Purpose() != purpose || seen[a.Digest()] {
				t.Fatal("purpose alias")
			}
			seen[a.Digest()] = true
			for _, mutate := range []func(*Request){func(v *Request) { v.Purpose = "" }, func(v *Request) { v.Purpose = "other" }, func(v *Request) {
				if v.Purpose == Publish {
					v.Purpose = Canary
				} else {
					v.Purpose = Publish
				}
			}, func(v *Request) { v.IdempotencyKey = "browser-retry" }, func(v *Request) { v.Schema = RequestSchema }, func(v *Request) { v.Schema = "unknown" }} {
				bad := r
				mutate(&bad)
				if _, e := Admit(t.Context(), auth, p, wire(t, bad)); e == nil {
					t.Fatal("mutated managed authority admitted", bad)
				}
			}
			raw := wire(t, r)
			for _, bad := range [][]byte{bytes.Replace(raw, []byte(`,"purpose":"`+string(purpose)+`"`), nil, 1), bytes.Replace(raw, []byte(`"purpose":`), []byte(`"purpose":"publish","purpose":`), 1), bytes.Replace(raw, []byte(`"purpose":"`+string(purpose)+`"`), []byte(`"purpose":null`), 1)} {
				if _, e := Admit(t.Context(), auth, p, bad); e == nil {
					t.Fatal("missing/duplicate purpose")
				}
			}
			next, e := PlannedSuccessor(t.Context(), a, hash([]byte("plan")))
			if e != nil {
				t.Fatal(e)
			}
			xauth := auth
			xauth.ParentRequestDigest = a.Digest()
			xauth.PlanDigest = next.PlanDigest
			x := admit(t, p, xauth, next)
			if x.Purpose() != purpose || next.IdempotencyKey != r.IdempotencyKey {
				t.Fatal("successor lost purpose")
			}
			// Legacy accepts arbitrary keys by design; stripping both version and
			// purpose creates DIFFERENT authority, never an alias or valid successor.
			downgraded := r
			downgraded.Schema = RequestSchema
			downgraded.Purpose = ""
			d := admit(t, p, auth, downgraded)
			if d.Digest() == a.Digest() {
				t.Fatal("downgrade aliases")
			}
			next.Schema = RequestSchema
			next.Purpose = ""
			if _, e = Admit(t.Context(), xauth, p, wire(t, next)); e == nil {
				t.Fatal("downgraded successor borrowed parent")
			}
			changed := auth.Source
			changed.Incarnation = "replacement"
			if NewManagedRequest(changed, p, auth.Profile.Number, auth.UniverseDigest, purpose).IdempotencyKey == r.IdempotencyKey || NewManagedRequest(auth.Source, p, auth.Profile.Number+1, auth.UniverseDigest, purpose).IdempotencyKey == r.IdempotencyKey || NewManagedRequest(auth.Source, p, auth.Profile.Number, hash([]byte("changed-universe")), purpose).IdempotencyKey == r.IdempotencyKey {
				t.Fatal("changed authority aliases")
			}
		})
	}
	legacyRaw := wire(t, legacy)
	for _, value := range []string{`null`, `""`, `"publish"`} {
		bad := append(bytes.Clone(legacyRaw[:len(legacyRaw)-1]), []byte(`,"purpose":`+value+`}`)...)
		if _, e := Admit(t.Context(), auth, p, bad); e == nil {
			t.Fatal("legacy explicit purpose accepted", value)
		}
	}
	if admit(t, p, auth, legacy).Purpose() != Publish {
		t.Fatal("legacy publish changed")
	}
	legacy.Purpose = Publish
	if _, e := Admit(t.Context(), auth, p, wire(t, legacy)); e == nil {
		t.Fatal("legacy purpose accepted")
	}
	if (Admission{}).Purpose() != "" {
		t.Fatal("empty admission publishes")
	}
}

func TestManagedRunOrdinal(t *testing.T) {
	p, auth, legacy, _ := fixture(t)
	base := NewManagedRequest(auth.Source, p, auth.Profile.Number, auth.UniverseDigest, Canary)
	if base.ManagedRun(0) != base || bytes.Contains(wire(t, base), []byte(`"run"`)) {
		t.Fatal("run zero changed the original request bytes")
	}
	seen := map[string]bool{admit(t, p, auth, base).Digest(): true}
	for _, run := range []uint64{1, 2} {
		r := base.ManagedRun(run)
		a := admit(t, p, auth, r)
		if r.Run != run || r.IdempotencyKey == base.IdempotencyKey || seen[a.Digest()] {
			t.Fatal("run did not create a distinct root", run)
		}
		seen[a.Digest()] = true
		next, e := PlannedSuccessor(t.Context(), a, hash([]byte("plan")))
		if e != nil || next.Run != run || next.ManagedRun(run) != (Request{}) {
			t.Fatal("successor lost its run or was rebased", e)
		}
		bad := r
		bad.Run++
		if _, e := Admit(t.Context(), auth, p, wire(t, bad)); e == nil {
			t.Fatal("run changed without its key")
		}
	}
	legacy.Run = 1
	if _, e := Admit(t.Context(), auth, p, wire(t, legacy)); e == nil {
		t.Fatal("legacy request admitted a run")
	}
	if legacy.ManagedRun(1) != (Request{}) {
		t.Fatal("legacy request rebased as managed")
	}
}
