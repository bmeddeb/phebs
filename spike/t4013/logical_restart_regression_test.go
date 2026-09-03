package t4013

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/resolverinput"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const logicalRestartRegressionEnvironment = "PHEBS_T421_LOGICAL_RESTART_REGRESSION"
const declarationUpgradeRegressionEnvironment = "PHEBS_T421_DECLARATION_UPGRADE_REGRESSION"
const declarationUpgradePredecessor = "ea9dd555e5b19a752255fb099ae43721b4df971f"

// This is a small ordinary-server restart regression, not a signed ceremony,
// scale pass, or hermetic executable-admission proof. Its test-only PATH probe
// records Git arguments and execs the native Git without changing its output.
func TestLogicalCatalogRestartRetainsPhysicalReaders(t *testing.T) {
	if os.Getenv(logicalRestartRegressionEnvironment) != "1" {
		t.Skip("set " + logicalRestartRegressionEnvironment + "=1 for the bounded real-server restart regression")
	}
	logicalRestartRegression(t, false)
}

func TestPartitionedDeclarationUpgradeRepairsPreviousCatalog(t *testing.T) {
	if os.Getenv(declarationUpgradeRegressionEnvironment) != "1" {
		t.Skip("set " + declarationUpgradeRegressionEnvironment + "=1 for the bounded real-server upgrade regression")
	}
	if resolvermaterialize.PackVersion != "1.1.1" {
		t.Fatal("upgrade regression requires the approved 1.1.1 resolver pack")
	}
	logicalRestartRegression(t, true)
}

func logicalRestartRegression(t *testing.T, upgrade bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	moduleRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp("", "phebs-t421-logical-restart-")
	if err != nil {
		t.Fatal(err)
	}
	defer retainFailedDiagnosticWorkspace(t, workspace)
	toolchain, err := buildWorkingTreeToolchain(ctx, moduleRoot, workspace)
	if err != nil {
		t.Fatal(err)
	}
	profile := logicalRestartProfile(t, ctx, workspace)
	probeDir := logicalRestartGitProbe(t, ctx, workspace, toolchain)
	tracePath := filepath.Join(workspace, "git-cold.ndjson")
	toolchain.extraEnvironment = []string{
		"PATH=" + probeDir + string(os.PathListSeparator) + toolchain.controls.GitExecPath,
		"PHEBS_T421_GIT_TRACE=" + tracePath,
		"PHEBS_T421_NATIVE_GIT=" + toolchain.host.git.path,
	}
	var server *privateServer
	defer func() {
		if err := server.stop(30 * time.Second); err != nil {
			t.Errorf("stop logical restart regression: %v", err)
		}
	}()
	inspector, err := newProfileInspector(profile, profileInspectionV32)
	if err != nil {
		t.Fatal(err)
	}
	var predecessor *logicalRestartUpgradeSnapshot
	if upgrade {
		oldWorkspace := filepath.Join(workspace, "predecessor")
		if err := os.Mkdir(oldWorkspace, 0o700); err != nil {
			t.Fatal(err)
		}
		oldSource := filepath.Join(oldWorkspace, "source")
		if err := exportReviewedSource(ctx, moduleRoot, declarationUpgradePredecessor, oldSource); err != nil {
			t.Fatal(err)
		}
		oldToolchain, err := buildWorkingTreeToolchain(ctx, oldSource, oldWorkspace)
		if err != nil {
			t.Fatal(err)
		}
		oldToolchain.extraEnvironment = []string{
			"PATH=" + probeDir + string(os.PathListSeparator) + oldToolchain.controls.GitExecPath,
			"PHEBS_T421_GIT_TRACE=" + filepath.Join(workspace, "git-predecessor.ndjson"),
			"PHEBS_T421_NATIVE_GIT=" + oldToolchain.host.git.path,
		}
		server, err = launchPrivateServer(ctx, profile, oldToolchain, "pre-reader-1-1-0")
		if err != nil {
			t.Fatal(err)
		}
		value := logicalRestartAwaitPredecessor(t, ctx, inspector, profile, server)
		predecessor = &value
		t.Logf("ordinary predecessor %s produced current 1.1.0 native resolver with zero declaration operations", declarationUpgradePredecessor)
		if err := server.stop(30 * time.Second); err != nil {
			t.Fatal(err)
		}
	}
	server, err = launchPrivateServer(ctx, profile, toolchain, "logical-cold")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := logicalRestartAwaitEndpoint(t, ctx, inspector, profile, server)
	t.Log("ordinary cold resolver declaration is current; endpoint discovered without Contract Atlas")
	before := logicalRestartAwaitReads(t, ctx, inspector, profile, endpoint, "a", server)
	t.Log("ordinary cold authorized search, caller, and relationship reads passed")
	if predecessor != nil {
		current, err := logicalRestartReadResolver(ctx, inspector, profile, resolvermaterialize.PackVersion)
		if err != nil {
			t.Fatal(err)
		}
		logicalRestartCheckUpgrade(t, *predecessor, before, current, logicalRestartReadTrace(t, tracePath), server)
	}
	if err := server.stop(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	logicalRestartWriteConfiguration(t, profile, "b")
	tracePath = filepath.Join(workspace, "git-restart.ndjson")
	toolchain.extraEnvironment[1] = "PHEBS_T421_GIT_TRACE=" + tracePath
	server, err = launchPrivateServer(ctx, profile, toolchain, "logical-changed-selection")
	if err != nil {
		t.Fatal(err)
	}
	after := logicalRestartAwaitReads(t, ctx, inspector, profile, endpoint, "b", server)
	t.Log("logical catalog restart authorized search, caller, and relationship reads passed")
	if before.source != after.source || before.search != after.search ||
		before.caller.GenerationDigest != after.caller.GenerationDigest ||
		before.caller.ManifestDigest != after.caller.ManifestDigest ||
		before.caller.ResolverManifest != after.caller.ResolverManifest ||
		!reflect.DeepEqual(before.relationship.Authority.Upstream, after.relationship.Authority.Upstream) {
		t.Fatal("logical-only restart replaced physical, caller, resolver, or extraction/observation authority")
	}
	if before.catalog == after.catalog || before.relationship.RootDigest == after.relationship.RootDigest {
		t.Fatal("changed operator catalog did not replace its selected catalog and relationship authority")
	}
	settledRows := logicalRestartAwaitQuietGit(t, ctx, tracePath, profile)
	// The post-settlement window covers two watcher intervals and repeats the
	// authorized readers. Citation resolution intentionally is not requested:
	// resolving source content has a separate, nonzero Git-blob cost.
	for range 12 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
		value, err := logicalRestartRead(ctx, inspector, profile, endpoint, "b")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, after) {
			t.Fatal("settled authorized readers changed exact authority")
		}
	}
	rows := logicalRestartReadTrace(t, tracePath)
	counts := logicalRestartCheckTrace(t, rows, profile, false)
	postCounts := logicalRestartCheckTrace(t, rows[len(settledRows):], profile, true)
	if counts["catalog_census"] != 1 || postCounts["watcher"] < 1 {
		t.Fatalf("logical restart work lacks one native census or post-settlement watcher coverage: all=%v settled=%v", counts, postCounts)
	}
	_, _, indexChildren, _, sampleErr := server.sampler.metrics()
	if sampleErr != nil || indexChildren != 0 {
		t.Fatalf("logical restart index-child proof: children=%d err=%v", indexChildren, sampleErr)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+profile.Address+apiresponse.SearchPath+"?q=Orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := inspector.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized search status=%d, want 401", response.StatusCode)
	}
	t.Logf("logical V3 restart: native Git work=%v; settled authorized-read window=%v; source-blob commands=0; index children=0", counts, postCounts)
}

type logicalRestartReadSnapshot struct {
	source, search, catalog string
	caller                  apiresponse.CallerMapGeneration
	relationship            apiresponse.RelationshipRootReceipt
}

type logicalRestartResolverSnapshot struct {
	manifest   resolvercatalog.Manifest
	caller     apiresponse.CallerMapGeneration
	endpoint   apiresponse.CallerMapEndpoint
	operations int
}

type logicalRestartUpgradeSnapshot struct {
	read     logicalRestartReadSnapshot
	resolver logicalRestartResolverSnapshot
}

func logicalRestartAwaitPredecessor(t *testing.T, ctx context.Context, inspector *profileInspector, profile PreparedProfile, server *privateServer) logicalRestartUpgradeSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	var last error
	for time.Now().Before(deadline) && ctx.Err() == nil {
		resolver, err := logicalRestartReadResolver(ctx, inspector, profile, "1.1.0")
		if err == nil {
			if resolver.operations != 0 || resolver.endpoint.Lineage != "" {
				t.Fatal("ordinary predecessor did not reproduce the missing-declaration defect")
			}
			read, readErr := logicalRestartReadBase(ctx, inspector, profile, "a", false)
			if readErr == nil {
				confirmed, confirmErr := logicalRestartReadResolver(ctx, inspector, profile, "1.1.0")
				if confirmErr == nil && reflect.DeepEqual(resolver, confirmed) {
					read.caller = resolver.caller
					return logicalRestartUpgradeSnapshot{read: read, resolver: resolver}
				}
				readErr = fmt.Errorf("predecessor resolver changed during physical authority read: %v", confirmErr)
			}
			err = readErr
		}
		last = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("ordinary predecessor did not converge: %v; %s", last, rehearsalLogTail(server.logPath))
	return logicalRestartUpgradeSnapshot{}
}

func logicalRestartCheckUpgrade(t *testing.T, old logicalRestartUpgradeSnapshot, current logicalRestartReadSnapshot, resolver logicalRestartResolverSnapshot, trace []logicalRestartGitCommand, server *privateServer) {
	t.Helper()
	if old.read.source != current.source || old.read.search != current.search || old.read.catalog != current.catalog ||
		old.read.caller.Commit != current.caller.Commit || old.read.caller.UnitDigest != current.caller.UnitDigest ||
		old.read.caller.CandidateManifest != current.caller.CandidateManifest ||
		!reflect.DeepEqual(old.resolver.manifest.Identity.Declarations, resolver.manifest.Identity.Declarations) ||
		!reflect.DeepEqual(old.read.relationship.Authority.Upstream, current.relationship.Authority.Upstream) {
		t.Fatal("pack upgrade replaced physical, catalog, candidate, or extraction/observation authority")
	}
	if old.read.caller.GenerationDigest == current.caller.GenerationDigest ||
		old.read.caller.ManifestDigest == current.caller.ManifestDigest ||
		old.read.caller.ResolverManifest == current.caller.ResolverManifest ||
		old.resolver.manifest.Identity.GenerationDigest == resolver.manifest.Identity.GenerationDigest ||
		resolver.operations != 1 || resolver.endpoint.Lineage == "" ||
		current.caller.GenerationDigest != resolver.caller.GenerationDigest || current.caller.ManifestDigest != resolver.caller.ManifestDigest ||
		current.caller.ResolverManifest != resolver.caller.ResolverManifest {
		t.Fatal("pack upgrade did not replace both resolver/caller publications with the real declaration")
	}
	blobReads := 0
	for _, row := range trace {
		args := logicalRestartGitArguments(row.Args)
		if len(args) == 3 && args[0] == "cat-file" && args[1] == "blob" {
			blobReads++
		}
	}
	_, _, indexChildren, _, sampleErr := server.sampler.metrics()
	if blobReads == 0 || sampleErr != nil || indexChildren != 0 {
		t.Fatalf("upgrade work lacks expected materialization or rebuilt physical index: blobs=%d index_children=%d err=%v", blobReads, indexChildren, sampleErr)
	}
	t.Logf("ordinary 1.1.0 -> 1.1.1 upgrade: declaration operations 0 -> %d; resolver %s -> %s; caller %s -> %s; source-blob commands=%d; index children=0; physical/candidate/extraction authority unchanged",
		resolver.operations, old.resolver.manifest.Identity.GenerationDigest, resolver.manifest.Identity.GenerationDigest,
		old.read.caller.GenerationDigest, current.caller.GenerationDigest, blobReads)
}

func logicalRestartProfile(t *testing.T, ctx context.Context, workspace string) PreparedProfile {
	t.Helper()
	repository, parent, _ := t40r1DescriptorGitBlobRepository(t, ctx)
	// Reuse the existing descriptor fixture, supplying the message definitions
	// needed by an ordinary production extractor rather than seeding a store.
	content := t40r1DescriptorGitBlobFiles[t40r1DescriptorDeclaration] + "message GetRequest {}\nmessage GetResponse {}\n"
	input := fmt.Sprintf("blob\nmark :1\ndata %d\n%s\ncommit refs/heads/main\nauthor Neutral <neutral@example.invalid> 946684801 +0000\ncommitter Neutral <neutral@example.invalid> 946684801 +0000\ndata 15\nlogical fixture\nfrom %s\nM 100644 :1 %s\n\ndone\n", len(content), content, parent, t40r1DescriptorDeclaration)
	command := exec.CommandContext(ctx, "git", "--git-dir", repository, "fast-import", "--quiet")
	command.Stdin = strings.NewReader(input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("complete neutral fixture: %v: %s", err, output)
	}
	t40r1CallerPartitionedGit(t, ctx, repository, "symbolic-ref", "HEAD", "refs/heads/main")
	commit := strings.TrimSpace(t40r1CallerPartitionedGit(t, ctx, repository, "rev-parse", "HEAD"))
	name, err := phebssync.RepoName(repository)
	if err != nil {
		t.Fatal(err)
	}
	address, err := reserveLoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}
	profile := PreparedProfile{
		Name: "logical-restart-small-v3", Repository: repository, RepositoryName: name,
		Config: filepath.Join(workspace, "phebs.yaml"), Catalog: filepath.Join(workspace, "catalog.json"),
		Credential: filepath.Join(workspace, "api-key"), DataDir: filepath.Join(workspace, "data"),
		Address: address, Revisions: map[string]string{"a": commit},
	}
	if err := os.Mkdir(profile.DataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credential, err := randomCredential()
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateNew(profile.Credential, []byte(credential+"\n")); err != nil {
		t.Fatal(err)
	}
	logicalRestartWriteConfiguration(t, profile, "a")
	return profile
}

func logicalRestartWriteConfiguration(t *testing.T, profile PreparedProfile, version string) {
	t.Helper()
	catalog := servicecatalog.Catalog{
		Schema:    servicecatalog.Schema,
		Authority: servicecatalog.Authority{Kind: servicecatalog.AuthorityOperator, ID: "logical-restart-neutral", Version: version},
		Services:  []servicecatalog.Service{{Key: "orders", DisplayName: "Orders " + version, Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase}},
		Memberships: []servicecatalog.Membership{
			{ServiceKey: "orders", Path: "consumer", Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase},
			{ServiceKey: "orders", Path: "gen/grpc", Role: servicecatalog.RoleGenerated, Origin: servicecatalog.OriginBase},
			{ServiceKey: "orders", Path: "idl/proto", Role: servicecatalog.RoleSupporting, Origin: servicecatalog.OriginBase},
		},
	}
	for _, path := range []string{"go.mod", resolverinput.LayoutSnapshotPath, resolverinput.GeneratedFromSnapshotPath, "unit-snapshot.json"} {
		catalog.Unowned = append(catalog.Unowned, servicecatalog.UnownedPlacement{Path: path, Origin: servicecatalog.OriginBase})
	}
	raw, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile.Catalog, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := os.ReadFile(profile.Credential)
	if err != nil {
		t.Fatal(err)
	}
	base, err := configFor(profile.Repository, profile.RepositoryName, profile.Catalog, profile.DataDir, profile.Address, strings.TrimSpace(string(credential)))
	if err != nil {
		t.Fatal(err)
	}
	var parsed config.Config
	if err := yaml.Unmarshal(base, &parsed); err != nil {
		t.Fatal(err)
	}
	parsed.ServiceCatalogs[profile.RepositoryName] = config.ServiceCatalog{
		Kind: catalog.Authority.Kind, ID: catalog.Authority.ID, Version: version,
		Path: profile.Catalog, Runtime: config.ServiceCatalogRuntimeV3,
	}
	raw, err = yaml.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile.Config, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func logicalRestartAwaitEndpoint(t *testing.T, ctx context.Context, inspector *profileInspector, profile PreparedProfile, server *privateServer) apiresponse.CallerMapEndpoint {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	var last error
	for time.Now().Before(deadline) && ctx.Err() == nil {
		endpoint, err := logicalRestartResolverEndpoint(ctx, inspector, profile)
		if err == nil {
			return endpoint
		}
		last = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("ordinary cold declaration did not converge: %v; %s", last, rehearsalLogTail(server.logPath))
	return apiresponse.CallerMapEndpoint{}
}

// Discovery uses the real immutable resolver output and its authorized caller
// authority, not fixture-seeded lineages. This is not a Contract Atlas test:
// that independent product reader still uses legacy published-run visibility.
func logicalRestartResolverEndpoint(ctx context.Context, inspector *profileInspector, profile PreparedProfile) (apiresponse.CallerMapEndpoint, error) {
	resolver, err := logicalRestartReadResolver(ctx, inspector, profile, resolvermaterialize.PackVersion)
	if err != nil {
		return apiresponse.CallerMapEndpoint{}, err
	}
	if resolver.operations != 1 || resolver.endpoint.Lineage == "" {
		return apiresponse.CallerMapEndpoint{}, errors.New("native resolver declaration is absent or duplicated")
	}
	return resolver.endpoint, nil
}

func logicalRestartReadResolver(ctx context.Context, inspector *profileInspector, profile PreparedProfile, packVersion string) (logicalRestartResolverSnapshot, error) {
	progressPath := apiresponse.CallerGenerationProgressPath + "?repository=" + url.QueryEscape(profile.RepositoryName)
	var before apiresponse.CallerGenerationProgress
	if err := inspector.get(ctx, profile, progressPath, &before); err != nil {
		return logicalRestartResolverSnapshot{}, err
	}
	if before.Generation.State != "current" {
		return logicalRestartResolverSnapshot{}, errors.New("native caller generation is not current")
	}
	root := filepath.Join(profile.DataDir, "resolver-catalogs")
	raw, err := readAtomicRegular(filepath.Join(root, resolvercatalogid.ManifestName(profile.RepositoryName)), resolvercatalog.MaxManifestBytes)
	if err != nil {
		return logicalRestartResolverSnapshot{}, err
	}
	var manifest resolvercatalog.Manifest
	if err := decodeStrict(raw, &manifest); err != nil {
		return logicalRestartResolverSnapshot{}, err
	}
	if manifest.Identity.Repository != profile.RepositoryName || manifest.Identity.Commit != profile.Revisions["a"] ||
		manifest.AuthorityDigest != before.Generation.ResolverManifest ||
		manifest.Identity.CandidateManifestDigest != before.Generation.CandidateManifest ||
		manifest.Identity.DeclarationSetDigest != before.Generation.DeclarationSetDigest {
		return logicalRestartResolverSnapshot{}, errors.New("resolver output differs from authorized caller authority")
	}
	if len(manifest.Identity.ResolverPacks) == 0 {
		return logicalRestartResolverSnapshot{}, errors.New("resolver output has no pack identities")
	}
	for _, pack := range manifest.Identity.ResolverPacks {
		if pack.Version != packVersion {
			return logicalRestartResolverSnapshot{}, fmt.Errorf("resolver pack version %q, want %q", pack.Version, packVersion)
		}
	}
	nativeDeclaration := false
	for _, declaration := range manifest.Identity.Declarations {
		if declaration.Domain == "proto-contract" && declaration.AuthoritySchema == store.PartitionedExtractionDomainSchema {
			nativeDeclaration = true
		}
	}
	if !nativeDeclaration {
		return logicalRestartResolverSnapshot{}, errors.New("resolver has no partitioned-native declaration authority")
	}
	var endpoint apiresponse.CallerMapEndpoint
	operations := 0
	publication, err := resolvercatalog.OpenWithVisitor(ctx, root, manifest.State(), func(member resolvercatalog.MemberReceipt, _ int, raw json.RawMessage) error {
		if member.Name != resolvermaterialize.GRPCGeneratedPackName+"-v2.ndjson" {
			return nil
		}
		var kind struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &kind); err != nil || kind.Kind != "declaration_operation" {
			return err
		}
		var declaration struct {
			Schema       string `json:"schema"`
			RecordSchema string `json:"record_schema"`
			Pack         string `json:"pack"`
			PackVersion  string `json:"pack_version"`
			Kind         string `json:"kind"`
			State        string `json:"state"`
			Protocol     string `json:"protocol"`
			Path         string `json:"path"`
			Lineage      string `json:"lineage"`
			Operation    string `json:"operation"`
		}
		if err := decodeStrict(raw, &declaration); err != nil {
			return err
		}
		if declaration.Schema != resolvercatalog.RecordSchema || declaration.RecordSchema != resolvermaterialize.DeclarationRecordSchema ||
			declaration.Pack != resolvermaterialize.GRPCGeneratedPackName || declaration.PackVersion != packVersion ||
			declaration.State != resolvermaterialize.StateResolved || declaration.Protocol != string(resolvermaterialize.ProtocolGRPC) || declaration.Lineage == "" {
			return errors.New("native resolver declaration record is invalid")
		}
		operations++
		if declaration.Path == t40r1DescriptorDeclaration && "/"+declaration.Operation == t40r1DescriptorOperation {
			if endpoint.Lineage != "" {
				return errors.New("native resolver operation is duplicated")
			}
			endpoint = apiresponse.CallerMapEndpoint{Protocol: "protobuf", Repository: manifest.Identity.Repository, Lineage: declaration.Lineage, Operation: "/" + declaration.Operation}
		}
		return nil
	})
	if err != nil {
		return logicalRestartResolverSnapshot{}, err
	}
	var after apiresponse.CallerGenerationProgress
	if err := inspector.get(ctx, profile, progressPath, &after); err != nil {
		return logicalRestartResolverSnapshot{}, err
	}
	if !reflect.DeepEqual(before.Generation, after.Generation) || !publication.Current() {
		return logicalRestartResolverSnapshot{}, errors.New("native resolver changed during discovery")
	}
	return logicalRestartResolverSnapshot{manifest: manifest, caller: before.Generation, endpoint: endpoint, operations: operations}, nil
}

func logicalRestartAwaitReads(t *testing.T, ctx context.Context, inspector *profileInspector, profile PreparedProfile, endpoint apiresponse.CallerMapEndpoint, version string, server *privateServer) logicalRestartReadSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	var last error
	for time.Now().Before(deadline) && ctx.Err() == nil {
		value, err := logicalRestartRead(ctx, inspector, profile, endpoint, version)
		if err == nil {
			return value
		}
		last = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("ordinary version-%s authorized readers did not converge: %v\n%s", version, last, rehearsalLogTail(server.logPath))
	return logicalRestartReadSnapshot{}
}

func logicalRestartRead(ctx context.Context, inspector *profileInspector, profile PreparedProfile, endpoint apiresponse.CallerMapEndpoint, version string) (logicalRestartReadSnapshot, error) {
	value, err := logicalRestartReadBase(ctx, inspector, profile, version, true)
	if err != nil {
		return logicalRestartReadSnapshot{}, err
	}
	query := url.Values{"repository": {endpoint.Repository}, "protocol": {endpoint.Protocol}, "lineage": {endpoint.Lineage}, "operation": {endpoint.Operation}, "page_size": {"100"}}
	var callers apiresponse.CallerMapPage
	if err := inspector.get(ctx, profile, "/api/contract_callers?"+query.Encode(), &callers); err != nil {
		return logicalRestartReadSnapshot{}, err
	}
	if callers.Generation == nil || callers.Generation.State != "current" || len(callers.Rows) != 1 || callers.Rows[0].Operation != endpoint.Operation {
		return logicalRestartReadSnapshot{}, errors.New("authorized exact caller read is not current with one real row")
	}
	value.caller = *callers.Generation
	return value, nil
}

func logicalRestartReadBase(ctx context.Context, inspector *profileInspector, profile PreparedProfile, version string, requireRelationshipRows bool) (logicalRestartReadSnapshot, error) {
	var inventory apiresponse.ServiceInventory
	if err := inspector.get(ctx, profile, "/api/services?repository="+url.QueryEscape(profile.RepositoryName), &inventory); err != nil {
		return logicalRestartReadSnapshot{}, err
	}
	if inventory.Repository.Authority.Version != version || len(inventory.Services) != 1 ||
		inventory.Services[0].DisplayName != "Orders "+version || inventory.Services[0].Status != "current" {
		return logicalRestartReadSnapshot{}, errors.New("changed catalog is not the current selected service")
	}
	var found search.Result
	searchQuery := url.Values{
		"q": {"Orders"}, "scope": {search.ScopeService}, "repository": {profile.RepositoryName},
		"service_key": {"orders"}, "max_matches": {"10"},
	}
	if err := inspector.get(ctx, profile, apiresponse.SearchPath+"?"+searchQuery.Encode(), &found); err != nil {
		return logicalRestartReadSnapshot{}, err
	}
	if len(found.Files) == 0 || found.Scope == nil || found.Scope.Kind != search.ScopeService ||
		found.Scope.Repository != profile.RepositoryName || found.Scope.ServiceKey != "orders" ||
		found.Scope.ServiceStatus != "current" || found.Scope.Authority == nil {
		return logicalRestartReadSnapshot{}, errors.New("authorized service search lacks real rows and current service authority")
	}
	var relationships apiresponse.RelationshipPage
	if err := inspector.get(ctx, profile, "/api/service-relationships?repository="+url.QueryEscape(profile.RepositoryName)+"&service_key=orders&page_size=100", &relationships); err != nil {
		return logicalRestartReadSnapshot{}, err
	}
	if len(relationships.Roots) != 1 || relationships.Roots[0].Authority == nil || relationships.Roots[0].Authority.Upstream == nil ||
		(requireRelationshipRows && len(relationships.Rows) == 0) {
		return logicalRestartReadSnapshot{}, errors.New("authorized relationship read lacks real rows and exact authority")
	}
	source, err := repositoryindex.ReadSourceManifest(filepath.Join(profile.DataDir, "index"), profile.RepositoryName)
	if err != nil {
		return logicalRestartReadSnapshot{}, err
	}
	manifest, err := repositoryindex.ReadSearchManifest(filepath.Join(profile.DataDir, "index"), profile.RepositoryName)
	if err != nil {
		return logicalRestartReadSnapshot{}, err
	}
	return logicalRestartReadSnapshot{source: source.Digest, search: manifest.Digest, catalog: inventory.Repository.CatalogGeneration, relationship: relationships.Roots[0]}, nil
}

type logicalRestartGitCommand struct {
	Args []string `json:"args"`
	Dir  string   `json:"dir"`
}

func logicalRestartAwaitQuietGit(t *testing.T, ctx context.Context, path string, profile PreparedProfile) []logicalRestartGitCommand {
	t.Helper()
	// The first three-second watcher tick enqueues startup sync even for an
	// unchanged HEAD. Account for that turn before opening the watcher-only
	// query window; the complete restart trace still refuses every blob read.
	deadline := time.Now().Add(time.Minute)
	quietSince := time.Now()
	seen := 0
	for time.Now().Before(deadline) && ctx.Err() == nil {
		rows := logicalRestartReadTrace(t, path)
		if len(rows) < seen {
			t.Fatal("Git trace shortened during logical restart")
		}
		counts := logicalRestartCheckTrace(t, rows[seen:], profile, false)
		if counts["watcher"] != len(rows)-seen {
			quietSince = time.Now()
		}
		seen = len(rows)
		all := logicalRestartCheckTrace(t, rows, profile, false)
		if all["watcher"] > 0 && time.Since(quietSince) >= 3*time.Second {
			return rows
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatal("logical restart Git work did not reach one quiet watcher interval")
	return nil
}

func logicalRestartReadTrace(t *testing.T, path string) []logicalRestartGitCommand {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > 8<<20 || raw[len(raw)-1] != '\n' {
		t.Fatalf("bounded complete Git trace unavailable: %v", err)
	}
	var result []logicalRestartGitCommand
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 16<<10)
	for scanner.Scan() {
		var row logicalRestartGitCommand
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil || len(row.Args) == 0 || len(result) >= 4096 {
			t.Fatalf("invalid bounded Git trace: %v", err)
		}
		result = append(result, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func logicalRestartCheckTrace(t *testing.T, rows []logicalRestartGitCommand, profile PreparedProfile, settled bool) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, row := range rows {
		args := logicalRestartGitArguments(row.Args)
		kind := ""
		switch {
		case reflect.DeepEqual(args, []string{"rev-parse", "HEAD"}) && row.Dir == profile.Repository:
			kind = "watcher"
		case !settled && reflect.DeepEqual(args, []string{"rev-parse", "HEAD"}):
			kind = "startup_index_head"
		case !settled && reflect.DeepEqual(args, []string{"ls-tree", "-rz", "--full-tree", profile.Revisions["a"]}):
			kind = "catalog_census"
		case !settled && reflect.DeepEqual(args, []string{"--version"}):
			kind = "startup_version"
		case !settled && len(args) == 3 && args[0] == "cat-file" && args[1] == "-e":
			kind = "startup_object_presence"
		case !settled && reflect.DeepEqual(args, []string{"cat-file", "-t", profile.Revisions["a"]}):
			kind = "startup_commit_type"
		case !settled && len(args) == 3 && args[0] == "config" && args[1] == "--get-all" && slices.Contains([]string{"remote.origin.url", "remote.origin.pushurl"}, args[2]):
			kind = "startup_origin"
		case !settled && reflect.DeepEqual(args, []string{"remote", "set-url", "origin", profile.Repository}):
			kind = "startup_sync_origin"
		case !settled && reflect.DeepEqual(args, []string{"fetch", "--prune", "origin"}):
			kind = "startup_sync_fetch"
		case !settled && (reflect.DeepEqual(args, []string{"symbolic-ref", "HEAD"}) || reflect.DeepEqual(args, []string{"symbolic-ref", "--short", "HEAD"}) || reflect.DeepEqual(args, []string{"symbolic-ref", "HEAD", "refs/heads/main"})):
			kind = "startup_sync_head"
		default:
			t.Fatalf("unexpected Git work in settled=%t window: %q", settled, row.Args)
		}
		counts[kind]++
	}
	return counts
}

func logicalRestartGitArguments(args []string) []string {
	for len(args) > 0 {
		if args[0] == "--no-replace-objects" {
			args = args[1:]
		} else if len(args) > 1 && (args[0] == "-c" || args[0] == "-C" || args[0] == "--git-dir") {
			args = args[2:]
		} else {
			break
		}
	}
	return args
}

func TestLogicalRestartGitArgumentClassification(t *testing.T) {
	profile := PreparedProfile{Repository: "/neutral/repository", Revisions: map[string]string{"a": strings.Repeat("a", 40)}}
	for _, test := range []struct {
		name    string
		row     logicalRestartGitCommand
		want    string
		settled bool
	}{
		{"watcher", logicalRestartGitCommand{Args: []string{"rev-parse", "HEAD"}, Dir: profile.Repository}, "watcher", true},
		{"census", logicalRestartGitCommand{Args: []string{"--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-C", "/neutral/mirror", "ls-tree", "-rz", "--full-tree", profile.Revisions["a"]}}, "catalog_census", false},
		{"index_head", logicalRestartGitCommand{Args: []string{"--git-dir", "/neutral/mirror", "rev-parse", "HEAD"}}, "startup_index_head", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := slices.Clone(test.row.Args)
			counts := logicalRestartCheckTrace(t, []logicalRestartGitCommand{test.row}, profile, test.settled)
			if len(counts) != 1 || counts[test.want] != 1 || !slices.Equal(original, test.row.Args) {
				t.Fatalf("Git classification changed command or attribution: %v", counts)
			}
		})
	}
	args := logicalRestartGitArguments([]string{"--no-replace-objects", "-C", "/neutral/mirror", "cat-file", "blob", strings.Repeat("b", 40)})
	if len(args) != 3 || args[0] != "cat-file" || args[1] != "blob" {
		t.Fatal("upgrade materialization is not recognized as a blob command")
	}
}

func logicalRestartGitProbe(t *testing.T, ctx context.Context, workspace string, toolchain privateToolchain) string {
	t.Helper()
	directory := filepath.Join(workspace, "git-probe")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "main.go")
	if err := os.WriteFile(path, []byte(logicalRestartGitProbeSource), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, toolchain.host.goDriver.path, "build", "-o", filepath.Join(directory, "git"), path)
	command.Env = executionEnvironment(false)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build test-only native Git exec probe: %v: %s", err, output)
	}
	return directory
}

const logicalRestartGitProbeSource = `package main
import("encoding/json";"os";"syscall")
func main(){
 args:=os.Args[1:];dir,err:=os.Getwd();if err!=nil{os.Exit(125)}
 raw,err:=json.Marshal(struct{Args []string ` + "`json:\"args\"`" + `;Dir string ` + "`json:\"dir\"`" + `}{args,dir});if err!=nil||len(raw)>16382{os.Exit(125)}
 f,err:=os.OpenFile(os.Getenv("PHEBS_T421_GIT_TRACE"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{os.Exit(125)}
 info,err:=f.Stat();if err!=nil||info.Size()>8<<20{os.Exit(125)}
 raw=append(raw,'\n');n,err:=f.Write(raw);if err!=nil||n!=len(raw){os.Exit(125)};if f.Close()!=nil{os.Exit(125)}
 real:=os.Getenv("PHEBS_T421_NATIVE_GIT");if real==""{os.Exit(125)}
 if syscall.Exec(real,append([]string{real},args...),os.Environ())!=nil{os.Exit(125)}
}
`
