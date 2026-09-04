package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"gopkg.in/yaml.v3"

	apiresponse "github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/candidatejob"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/kafkatopicposting"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/rpccallerposting"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/spike/t401"
	t421fixture "github.com/bmeddeb/phebs/spike/t421"
)

const t421FinalAuthorityRegressionEnvironment = "PHEBS_T421_FINAL_AUTHORITY_REGRESSION"

const (
	t421FinalAuthorityHelperEnvironment = "PHEBS_T421_FINAL_AUTHORITY_HELPER"
	t421FinalAuthorityHelperConfig      = "PHEBS_T421_FINAL_AUTHORITY_HELPER_CONFIG"
	t421FinalAuthorityHelperContract    = "source-free-v1"
)

type t421BlackBoxServerProcess struct {
	command    *exec.Cmd
	done       chan struct{}
	waitErr    error
	ready      *os.File
	release    *os.File
	runtimePID int
	stopped    bool
}

func t421BlackBoxStartServer(
	helper, moduleRoot, zoekt, configPath string,
	logFile *os.File,
) (*t421BlackBoxServerProcess, error) {
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	releaseReader, releaseWriter, err := os.Pipe()
	if err != nil {
		_ = readyReader.Close()
		_ = readyWriter.Close()
		return nil, err
	}
	closePipes := func() {
		_ = readyReader.Close()
		_ = readyWriter.Close()
		_ = releaseReader.Close()
		_ = releaseWriter.Close()
	}
	command := exec.Command(
		helper, "-test.run=^TestT421FinalAuthorityServerHelper$", "-test.count=1",
	)
	command.Dir = moduleRoot
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Env = append(t421BlackBoxServerEnvironment(zoekt),
		t421FinalAuthorityHelperEnvironment+"="+t421FinalAuthorityHelperContract,
		t421FinalAuthorityHelperConfig+"="+configPath,
	)
	command.ExtraFiles = []*os.File{readyWriter, releaseReader}
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		closePipes()
		return nil, err
	}
	_ = readyWriter.Close()
	_ = releaseReader.Close()
	server := &t421BlackBoxServerProcess{
		command: command, done: make(chan struct{}), ready: readyReader, release: releaseWriter,
	}
	go func() {
		server.waitErr = command.Wait()
		close(server.done)
	}()
	return server, nil
}

func (server *t421BlackBoxServerProcess) stop(t *testing.T) error {
	t.Helper()
	if server == nil || server.stopped {
		if server == nil {
			return nil
		}
		select {
		case <-server.done:
			return server.waitErr
		default:
			return errors.New("real-server helper is still stopping")
		}
	}
	server.stopped = true
	_ = server.release.Close()
	exited := false
	select {
	case <-server.done:
		exited = true
	default:
		if server.command != nil && server.command.Process != nil {
			_ = syscall.Kill(-server.command.Process.Pid, syscall.SIGINT)
		}
		select {
		case <-server.done:
			exited = true
		case <-time.After(30 * time.Second):
			if server.command != nil && server.command.Process != nil {
				_ = syscall.Kill(-server.command.Process.Pid, syscall.SIGKILL)
			}
			select {
			case <-server.done:
				exited = true
			case <-time.After(5 * time.Second):
				t.Errorf("real-server helper process group did not exit")
			}
		}
	}
	_ = server.ready.Close()
	if server.runtimePID != 0 {
		t421BlackBoxRequireProcessExit(t, server.runtimePID)
	}
	if !exited {
		return errors.New("real-server helper process group did not exit")
	}
	return server.waitErr
}

func TestT421FinalAuthorityServerHelper(t *testing.T) {
	if os.Getenv(t421FinalAuthorityHelperEnvironment) != t421FinalAuthorityHelperContract {
		t.Skip("real-server helper is inactive")
	}
	configPath := os.Getenv(t421FinalAuthorityHelperConfig)
	ready := os.NewFile(3, "t421-final-authority-ready")
	release := os.NewFile(4, "t421-final-authority-release")
	if configPath == "" || ready == nil || release == nil {
		t.Fatal("real-server helper is incomplete")
	}
	defer func() {
		_ = ready.Close()
		_ = release.Close()
	}()
	attempt := 0
	t421FinalAuthorityBeforeConfirm = func(ctx context.Context) error {
		attempt++
		if attempt < 3 {
			return nil
		}
		if attempt != 3 {
			return errors.New("real-server final-authority barrier repeated")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := ready.Write([]byte{'R'}); err != nil {
			return err
		}
		var signal [1]byte
		if _, err := io.ReadFull(release, signal[:]); err != nil {
			return err
		}
		if signal[0] != 'G' {
			return errors.New("real-server final-authority barrier release is invalid")
		}
		return ctx.Err()
	}
	if err := serve([]string{"-config", configPath}); err != nil {
		t.Fatal(err)
	}
}

// This opt-in test is the smallest production-shaped witness for the exact F
// reader. It authors T42.1's 31,602-file addition corpus plus the three frozen
// structural fixtures required by the profile; the already-proven
// two-million-file structural plane is not repeated here.
func TestT421FinalAuthorityRealServerRegression(t *testing.T) {
	if os.Getenv(t421FinalAuthorityRegressionEnvironment) != "1" {
		t.Skip("set " + t421FinalAuthorityRegressionEnvironment + "=1 for the real-server F regression")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	if _, err := store.FindSurrealBinary(); err != nil {
		t.Fatal(err)
	}

	moduleRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	repository := filepath.Join(workspace, "combined.git")
	corpus, err := t421fixture.BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := t421fixture.BuildIndependentOracle()
	if err != nil {
		t.Fatal(err)
	}
	commit := t421BlackBoxRepository(
		t, ctx, repository, corpus.Profile.Pipeline.ExtractionDomains,
	)
	repositoryName, err := phebssync.RepoName(repository)
	if err != nil {
		t.Fatal(err)
	}

	catalogPath := filepath.Join(workspace, "catalog.json")
	catalog, err := json.Marshal(corpus.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, catalog, 0o600); err != nil {
		t.Fatal(err)
	}
	address := t421BlackBoxAddress(t)
	dataDir := filepath.Join(workspace, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credential := "t421-final-authority-regression-key"
	configPath := filepath.Join(workspace, "phebs.yaml")
	t421BlackBoxConfig(t, configPath, repository, repositoryName, catalogPath,
		dataDir, address, credential, corpus.Catalog)

	binDir := filepath.Join(workspace, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	zoekt := filepath.Join(binDir, "zoekt-git-index")
	t421BlackBoxBuild(t, ctx, moduleRoot, zoekt,
		"github.com/sourcegraph/zoekt/cmd/zoekt-git-index")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(workspace, "server.log")
	logFile, err := os.OpenFile(
		logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := t421BlackBoxStartServer(helper, moduleRoot, zoekt, configPath, logFile)
	if err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.stop(t)
		_ = logFile.Close()
	})

	client := &http.Client{Timeout: 10 * time.Minute}
	baseURL := "http://" + address
	t421BlackBoxWaitHealth(t, ctx, client, baseURL, server.done, func() error {
		return server.waitErr
	}, logPath)
	runtime, err := store.ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	server.runtimePID = runtime.PID
	ordinal := uint64(1)
	request := func(path string) t421BlackBoxResponse {
		result := t421BlackBoxExactRequest(
			t, ctx, client, baseURL+path, credential, ordinal,
		)
		ordinal++
		return result
	}

	progressPath := apiresponse.ExtractionProgressPath + "?repository=" +
		url.QueryEscape(repositoryName)
	t421BlackBoxWait(t, ctx, 20*time.Minute, func() (bool, error) {
		response := request(progressPath)
		if response.Report.Status != "complete" {
			return false, fmt.Errorf("extraction accounting status %q", response.Report.Status)
		}
		if response.Status != http.StatusOK {
			if !isT421BlackBoxExtractionRetry(response.Status, response.Body) {
				return false, fmt.Errorf("extraction progress status %d: %s", response.Status, response.Body)
			}
			return false, nil
		}
		var progress extractionpublication.Progress
		if err := json.Unmarshal(response.Body, &progress); err != nil {
			return false, err
		}
		if err := extractionpublication.ValidateProgress(progress); err != nil {
			return false, err
		}
		return progress.State == "current", nil
	})
	t421BlackBoxWait(t, ctx, 10*time.Minute, func() (bool, error) {
		response := request(t421ExactTailReadinessPath)
		if response.Status != http.StatusOK || response.Report.Status != "complete" {
			return false, fmt.Errorf("tail readiness status %d/%s",
				response.Status, response.Report.Status)
		}
		var ready t421TailReadinessResponse
		if err := json.Unmarshal(response.Body, &ready); err != nil {
			return false, err
		}
		if ready.Schema != t421TailReadinessSchema ||
			(ready.Status != "pending" && ready.Status != "ready") {
			return false, fmt.Errorf("invalid tail readiness: %+v", ready)
		}
		if ready.Status == "ready" && response.Report != (t421ExactReadReport{
			Schema: t421ExactReadReportSchema, RequestOrdinal: response.Report.RequestOrdinal,
			Status: "complete", ControlFileReads: 4, StoreReadAttempts: 4,
		}) {
			return false, fmt.Errorf("ready tail accounting: %+v", response.Report)
		}
		return ready.Status == "ready", nil
	})

	// The first helper authors the publication from empty custody. Restarting on
	// that exact custody makes Searcher.Open capture the published whole-search
	// generation as its startup shared-current baseline before F,Q,F begins.
	if err := server.stop(t); err != nil {
		t.Fatalf("stop publication helper: %v\n%s", err, t421BlackBoxLogTail(logPath))
	}
	server, err = t421BlackBoxStartServer(helper, moduleRoot, zoekt, configPath, logFile)
	if err != nil {
		t.Fatalf("restart exact witness helper: %v\n%s", err, t421BlackBoxLogTail(logPath))
	}
	t421BlackBoxWaitHealth(t, ctx, client, baseURL, server.done, func() error {
		return server.waitErr
	}, logPath)
	runtime, err = store.ReadLocalRuntime(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	server.runtimePID = runtime.PID
	ordinal = 1

	cold := request(t421ExactFinalAuthorityPath)
	if cold.Status != http.StatusOK || cold.Report.Status != "complete" {
		t.Fatalf("cold F = status %d report %+v\n%s", cold.Status, cold.Report,
			t421BlackBoxLogTail(logPath))
	}
	var authority t421FinalAuthorityResponse
	if err := json.Unmarshal(cold.Body, &authority); err != nil ||
		authority.Schema != t421FinalAuthoritySchema || !authority.Authority.Current ||
		authority.Authority.PhysicalCommit != commit {
		t.Fatalf("invalid F authority: %+v, %v", authority.Authority, err)
	}

	state, err := store.Open(ctx, runtime.Endpoint, "root", "root", "phebs", "phebs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := state.Close(closeCtx); err != nil {
			t.Errorf("close F oracle store: %v", err)
		}
	})
	selected, err := state.GetServiceRuntimeSelector(ctx, repositoryName)
	if err != nil {
		t.Fatal(err)
	}
	accounting := t421BlackBoxFinalAccounting(
		t, ctx, state, dataDir, repositoryName, selected, corpus, oracle, authority,
	)
	coldCounts := t421BlackBoxFinalReportCounts(cold.Report)
	coldMatches := 0
	var coldIndicators [3]uint64
	for candidateMiss := uint64(0); candidateMiss <= 1; candidateMiss++ {
		for relationshipMiss := uint64(0); relationshipMiss <= 1; relationshipMiss++ {
			for callerMiss := uint64(0); callerMiss <= 1; callerMiss++ {
				if coldCounts == accounting.counts(
					1, candidateMiss, relationshipMiss, 1, callerMiss,
				) {
					coldMatches++
					coldIndicators = [3]uint64{candidateMiss, relationshipMiss, callerMiss}
				}
			}
		}
	}
	if coldMatches != 1 {
		t.Fatalf("cold F accounting %+v matches %d cache postures", coldCounts, coldMatches)
	}
	t.Logf("F cold shared-cache misses: candidate=%d relationship=%d caller=%d",
		coldIndicators[0], coldIndicators[1], coldIndicators[2])

	plan, err := t421fixture.BuildPlan(commit)
	if err != nil {
		t.Fatal(err)
	}
	queryAccounting := t421BlackBoxOpenQueryAccounting(
		t, ctx, state, dataDir, repositoryName, selected,
	)
	t421BlackBoxRunProductQueries(
		t, ctx, client, baseURL, credential, &ordinal, plan.Oracle.QueryCases,
		repositoryName, corpus.Catalog, queryAccounting,
	)

	warm := request(t421ExactFinalAuthorityPath)
	t.Logf("F accounting: cold=%+v warm=%+v", cold.Report, warm.Report)
	if warm.Status != http.StatusOK || warm.Report.Status != "complete" {
		t.Fatalf("warm F = status %d report %+v\n%s", warm.Status, warm.Report,
			t421BlackBoxLogTail(logPath))
	}
	if !bytes.Equal(cold.Body, warm.Body) {
		t.Fatal("cold and warm F responses differ")
	}
	if got, want := t421BlackBoxFinalReportCounts(warm.Report), accounting.counts(0, 0, 0, 0, 0); got != want {
		t.Fatalf("warm F accounting = %+v, want %+v", got, want)
	}
	target := store.ServiceRuntimeTarget{
		CatalogGenerationDigest:      selected.CatalogGenerationDigest,
		CatalogRootDigest:            selected.CatalogRootDigest,
		CatalogControlRevision:       selected.CatalogControlRevision,
		StateControlRevision:         selected.StateControlRevision,
		StateSummaryDigest:           selected.StateSummaryDigest,
		SearchGenerationDigest:       selected.SearchGenerationDigest,
		RelationshipGenerationDigest: selected.RelationshipGenerationDigest,
		RelationshipRootDigest:       selected.RelationshipRootDigest,
	}

	type exactResult struct {
		response t421BlackBoxResponse
		err      error
	}
	late := make(chan exactResult, 1)
	lateOrdinal := ordinal
	go func() {
		response, err := t421BlackBoxExactRequestRaw(
			ctx, client, baseURL+t421ExactFinalAuthorityPath, credential, lateOrdinal,
		)
		late <- exactResult{response: response, err: err}
	}()
	barrier := make(chan error, 1)
	go func() {
		var signal [1]byte
		_, err := io.ReadFull(server.ready, signal[:])
		if err == nil && signal[0] != 'R' {
			err = errors.New("real-server final-authority barrier signal is invalid")
		}
		barrier <- err
	}()
	select {
	case result := <-late:
		t.Fatalf("warm F completed before the late-authority barrier: status %d, %v",
			result.response.Status, result.err)
	case err := <-barrier:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	superseded, err := state.SelectServiceRuntimeV3(ctx, store.ServiceRuntimeSelectionRequest{
		Repository: repositoryName, ExpectedControlRevision: selected.ControlRevision,
		ExpectedDigest: selected.Digest, Target: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if superseded.ControlRevision != selected.ControlRevision+1 ||
		superseded.Digest == selected.Digest {
		t.Fatalf("selector supersession = %+v, prior %+v", superseded, selected)
	}
	if _, err := server.release.Write([]byte{'G'}); err != nil {
		t.Fatal(err)
	}
	result := <-late
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.response.Status != http.StatusConflict ||
		result.response.Report.Status != "final_authority_refused" {
		t.Fatalf("late-supersession F returned stale success: status %d report %+v",
			result.response.Status, result.response.Report)
	}
	wantLate := t421BlackBoxFinalReportCounts(warm.Report)
	if wantLate.StoreReadAttempts == 0 {
		t.Fatal("warm F store accounting is empty")
	}
	wantLate.StoreReadAttempts-- // final repository authorization follows selector confirmation
	if got := t421BlackBoxFinalReportCounts(result.response.Report); got != wantLate {
		t.Fatalf("late-selector F accounting = %+v, want %+v", got, wantLate)
	}
	select {
	case <-server.done:
		if server.waitErr == nil {
			t.Fatal("terminal exact-read refusal exited successfully")
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("terminal exact-read refusal did not stop the server\n%s",
			t421BlackBoxLogTail(logPath))
	}
}

type t421BlackBoxFinalAccountingInput struct {
	domains, typedControls, relationshipFiles, resolverFiles, componentFiles uint64
	adapters, catalogQueries                                                 uint64
	relationshipRecords, extractionRecords                                   uint64
	resolverRecords, componentRecords                                        uint64
	sourceOwners, candidateRecords, catalogRecords                           uint64
	callerRecords                                                            uint64
}

func (input t421BlackBoxFinalAccountingInput) counts(
	sourceMiss, candidateMiss, relationshipMiss, catalogMiss, callerMiss uint64,
) readaccounting.Counts {
	return readaccounting.Counts{
		ControlFileReads: 29 + 3*input.domains + input.typedControls + input.relationshipFiles +
			input.resolverFiles + input.componentFiles + sourceMiss + candidateMiss + relationshipMiss + 5*callerMiss,
		StoreReadAttempts: 30 + 6*input.domains + 3*input.adapters +
			input.catalogQueries*catalogMiss,
		MemberVisits: input.relationshipRecords + input.extractionRecords + input.resolverRecords +
			input.componentRecords + 2*input.sourceOwners*sourceMiss +
			input.candidateRecords*candidateMiss + input.catalogRecords*catalogMiss +
			input.callerRecords*callerMiss,
	}
}

func t421BlackBoxFinalReportCounts(report t421ExactReadReport) readaccounting.Counts {
	return readaccounting.Counts{
		ControlFileReads: report.ControlFileReads, StoreReadAttempts: report.StoreReadAttempts,
		MemberVisits: report.MemberVisits, StoreWriteAttempts: report.StoreWriteAttempts,
	}
}

func t421BlackBoxFinalAccounting(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	dataDir, repository string,
	selector store.ServiceRuntimeSelector,
	corpus t421fixture.CombinedCorpus,
	oracle t421fixture.Oracle,
	authority t421FinalAuthorityResponse,
) t421BlackBoxFinalAccountingInput {
	t.Helper()
	profile := corpus.Profile
	input := t421BlackBoxFinalAccountingInput{domains: uint64(len(profile.Pipeline.ExtractionDomains))}
	if len(authority.ExtractionRoots) != len(profile.Pipeline.ExtractionDomains) ||
		len(authority.Projection.ExtractionRoots) != len(profile.Pipeline.ExtractionDomains) {
		t.Fatal("F extraction authority does not match the fixture domain set")
	}
	for index, domain := range profile.Pipeline.ExtractionDomains {
		root := authority.ExtractionRoots[index]
		projection := authority.Projection.ExtractionRoots[index]
		typed := domain.TypedPartitions > 0
		rootTyped := root.TypedScopeSHA256 != "" && root.TypedScopeContentSHA256 != ""
		projectionTyped := projection.TypedScopeSHA256 != "" && projection.TypedScopeContentSHA256 != ""
		if root.Domain != domain.Domain || projection.Domain != domain.Domain ||
			root.TypedPartitions != domain.TypedPartitions ||
			projection.TypedPartitions != domain.TypedPartitions ||
			rootTyped != typed || projectionTyped != typed {
			t.Fatalf("F extraction domain %q differs from fixture", domain.Domain)
		}
		if typed {
			input.typedControls++
		}
	}
	pointer, err := state.GetCandidateManifestPublication(ctx, repository)
	if err != nil || pointer == nil {
		t.Fatalf("read F candidate publication: %v", err)
	}
	candidateState := candidate.State{
		Schema: candidate.StateSchema, Repository: pointer.Repository,
		Commit: pointer.HeadCommit, UnitDigest: pointer.UnitDigest,
		PolicyDigest: pointer.PolicyDigest, GenerationDigest: pointer.GenerationDigest,
		ManifestDigest: pointer.ManifestDigest, Manifest: pointer.ManifestPath,
	}
	publication, err := candidate.OpenStateContext(
		ctx, candidatejob.CandidateRoot(dataDir), candidateState,
	)
	if err != nil || candidateState.GenerationDigest != authority.Authority.CandidateGenerationSHA256 {
		t.Fatalf("open F candidate publication: %v", err)
	}
	input.extractionRecords = t421BlackBoxExtractionRecords(
		t, profile.Pipeline.ExtractionDomains, publication.Manifest(),
	)

	currentState, err := state.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil || !t421FinalSelectedStateMatches(selector, currentState) {
		t.Fatalf("F fixture did not select the current state summary: %+v, %v", currentState, err)
	}
	catalogRoot, err := state.ReadServiceCatalogV3Root(ctx, repository, selector.CatalogRootDigest)
	if err != nil || catalogRoot.Digest != authority.Authority.CatalogRootSHA256 {
		t.Fatalf("read F catalog root: %v", err)
	}
	input.catalogQueries = uint64(len(catalogRoot.ServiceMembers) + len(catalogRoot.PlacementMembers))
	catalogCtx, catalogLedger, err := readaccounting.Start(ctx, t421FinalAuthorityReadLimits())
	if err != nil {
		t.Fatal(err)
	}
	decodedCatalog, readErr := servicecatalogv3.ReadCatalogContext(
		catalogCtx, state, catalogRoot,
	)
	catalogCounts, finishErr := catalogLedger.Finish()
	if readErr != nil || finishErr != nil ||
		catalogCounts.ControlFileReads != 0 || catalogCounts.StoreWriteAttempts != 0 ||
		catalogCounts.StoreReadAttempts != input.catalogQueries ||
		len(decodedCatalog.Services) != catalogRoot.Services ||
		len(decodedCatalog.Memberships) != catalogRoot.Memberships {
		t.Fatalf("derive F catalog accounting: counts=%+v read=%v finish=%v",
			catalogCounts, readErr, finishErr)
	}
	input.catalogRecords = catalogCounts.MemberVisits

	relationship, err := relationshippublication.OpenGenerationV3(
		ctx, filepath.Join(dataDir, "relationships"), repository,
		authority.Authority.RelationshipGenerationSHA256,
		authority.Authority.RelationshipRootSHA256,
	)
	if err != nil {
		t.Fatal(err)
	}
	relationshipRoot := relationship.Root()
	if uint64(relationshipRoot.ServiceCount) != profile.Logical.AcceptedServices ||
		uint64(relationshipRoot.ProjectionFragmentCount) != profile.Pipeline.RelationshipProjections ||
		len(relationshipRoot.ServiceMembers) != 20 ||
		len(relationshipRoot.RepositoryMembers) != relationshippublication.RepositoryBuckets {
		t.Fatal("F relationship root differs from fixture")
	}
	input.relationshipRecords = uint64(
		relationshipRoot.ServiceCount + relationshipRoot.ProjectionFragmentCount,
	)
	input.relationshipFiles = uint64(
		len(relationshipRoot.ServiceMembers) + len(relationshipRoot.RepositoryMembers),
	)

	resolver, err := resolvernamespace.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-resolver-namespaces"), repository,
		relationshipRoot.Authority.ResolverGenerationDigest,
		relationshipRoot.Authority.ResolverRootDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolverRoot := resolver.Root()
	if resolverRoot.NamespaceCount != len(resolverRoot.Namespaces) ||
		uint64(resolverRoot.NamespaceCount) != profile.Pipeline.GeneratedMappings ||
		uint64(resolverRoot.RecordCount) != profile.Pipeline.GeneratedDescriptors {
		t.Fatal("F resolver root differs from fixture")
	}
	input.resolverFiles = uint64(len(resolverRoot.Namespaces))
	input.resolverRecords = uint64(resolverRoot.RecordCount)

	rpc, err := rpccallerposting.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-rpc-postings"), repository,
		relationshipRoot.Authority.RPCGenerationDigest,
		relationshipRoot.Authority.RPCRootDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	kafka, err := kafkatopicposting.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-kafka-postings"), repository,
		relationshipRoot.Authority.KafkaGenerationDigest,
		relationshipRoot.Authority.KafkaRootDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	rpcRoot, kafkaRoot := rpc.Root(), kafka.Root()
	if uint64(rpcRoot.PostingCount) != profile.Pipeline.RPCCallPostings ||
		uint64(kafkaRoot.ProducerCount) != profile.Pipeline.KafkaProducerPostings ||
		uint64(kafkaRoot.ConsumerCount) != profile.Pipeline.KafkaConsumerPostings {
		t.Fatal("F component roots differ from fixture")
	}
	input.componentFiles = uint64(len(rpcRoot.Members) + len(kafkaRoot.Members))
	input.componentRecords = uint64(rpcRoot.PostingCount + kafkaRoot.PostingCount)

	registry, err := callerexecute.NewRegistry(evidenceExtractors(true, true, false, true))
	if err != nil {
		t.Fatal(err)
	}
	input.adapters = uint64(len(registry.Adapters()))
	input.sourceOwners = authority.Authority.SearchInventory.Records
	wantOwners := profile.Overlay.RegularFiles + profile.GeneratedMapping.RegularFiles +
		profile.TypedIndex.RegularFiles + 3
	if input.sourceOwners != wantOwners {
		t.Fatalf("F source owners = %d, want %d", input.sourceOwners, wantOwners)
	}
	manifest := publication.Manifest()
	var repositoryRecords, callerRecords, localRecords uint64
	for _, member := range manifest.RepositoryMembers {
		repositoryRecords += uint64(member.RecordCount)
	}
	for _, leaf := range manifest.CallerLeaves {
		callerRecords += uint64(leaf.RecordCount)
	}
	for _, projection := range manifest.LocalProjections {
		for _, member := range projection.Members {
			localRecords += uint64(member.RecordCount)
		}
	}
	mergeRecords := t421BlackBoxProjectionMergeVisits(repositoryRecords + callerRecords)
	input.candidateRecords = t421BlackBoxCandidateOpenRecords(manifest)
	if repositoryRecords != 31_601 || callerRecords != 21_603 || localRecords != 0 ||
		mergeRecords != 364_324 || input.candidateRecords != 470_732 {
		t.Fatalf(
			"F candidate records repository/caller/local/merge/total = %d/%d/%d/%d/%d",
			repositoryRecords, callerRecords, localRecords, mergeRecords, input.candidateRecords,
		)
	}
	caller, err := state.GetCallerGenerationPublicationSummary(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	input.callerRecords = uint64(
		caller.ResultCount + caller.AbstentionCount + caller.CoverageRecordCount,
	)
	var wantResults, wantAbstentions, wantCallerRecords uint64
	for _, leaf := range oracle.ProductRelationships.CallerLeaves {
		wantResults += leaf.ResolvedPostings
		wantAbstentions += leaf.Abstentions
		wantCallerRecords += leaf.Records
	}
	// The second caller adapter is Thrift. This fixture deliberately has no
	// Thrift resolver descriptors, so each leaf contributes one coverage row.
	wantCoverage := uint64(len(oracle.ProductRelationships.CallerLeaves))
	wantCallerRecords += wantCoverage
	if uint64(caller.ResultCount) != wantResults ||
		uint64(caller.AbstentionCount) != wantAbstentions ||
		uint64(caller.CoverageRecordCount) != wantCoverage ||
		input.callerRecords != wantCallerRecords {
		t.Fatalf("F caller records = %d, want %d", input.callerRecords, wantCallerRecords)
	}
	return input
}

func t421BlackBoxCandidateOpenRecords(manifest candidate.Manifest) uint64 {
	var records, local uint64
	for _, member := range manifest.RepositoryMembers {
		records += uint64(member.RecordCount)
	}
	for _, leaf := range manifest.CallerLeaves {
		records += uint64(leaf.RecordCount)
	}
	for _, projection := range manifest.LocalProjections {
		for _, member := range projection.Members {
			local += uint64(member.RecordCount)
		}
	}
	return 2*records + local + t421BlackBoxProjectionMergeVisits(records)
}

func t421BlackBoxProjectionMergeVisits(records uint64) uint64 {
	const chunkRecords = 512
	levels := make([]uint64, 0, 16)
	var visits uint64
	for remaining := records; remaining > 0; {
		run := min(remaining, uint64(chunkRecords))
		remaining -= run
		for level := 0; ; level++ {
			if level == len(levels) {
				levels = append(levels, run)
				break
			}
			if levels[level] == 0 {
				levels[level] = run
				break
			}
			run += levels[level]
			visits += run
			levels[level] = 0
		}
	}
	var result uint64
	for _, run := range levels {
		if run == 0 {
			continue
		}
		if result == 0 {
			result = run
			continue
		}
		result += run
		visits += result
	}
	return visits
}

func t421BlackBoxExtractionRecords(
	t *testing.T,
	domains []t421fixture.ExtractionDomainProfile,
	manifest candidate.Manifest,
) uint64 {
	t.Helper()
	planes := make(map[string]candidate.Plane, len(manifest.Policies))
	for _, policy := range manifest.Policies {
		planes[policy.Domain] = policy.Plane
	}
	var records uint64
	for _, domain := range domains {
		var admitted, memberPartitions uint64
		for _, partition := range domain.Partitions {
			if partition.Kind == candidate.PartitionKindTypedInput {
				if partition.MemberOrdinal != -1 || partition.AdmittedRecords != 0 {
					t.Fatalf("invalid typed partition in %q", domain.Domain)
				}
				continue
			}
			if partition.Kind != candidate.PartitionKindCandidateMember || partition.MemberOrdinal < 0 {
				t.Fatalf("invalid candidate partition in %q", domain.Domain)
			}
			memberPartitions++
			admitted += partition.AdmittedRecords
			records += t421BlackBoxCandidateMemberRecords(
				t, manifest, planes[domain.Domain], domain.Domain, partition.MemberOrdinal,
			)
		}
		if admitted != domain.CandidateRecords ||
			memberPartitions != domain.MemberPartitions {
			t.Fatalf("candidate partitions in %q do not reconstruct the fixture scope", domain.Domain)
		}
	}
	return records
}

func TestT421BlackBoxExtractionRecordsCountsSplitMemberReads(t *testing.T) {
	domain := t421fixture.ExtractionDomainProfile{
		Domain: "proto-contract", CandidateRecords: 3, MemberPartitions: 2,
		Partitions: []t421fixture.ExtractionPartitionProfile{
			{Kind: candidate.PartitionKindCandidateMember, MemberOrdinal: 0, AdmittedRecords: 1},
			{Kind: candidate.PartitionKindCandidateMember, MemberOrdinal: 0, AdmittedRecords: 2},
		},
	}
	manifest := candidate.Manifest{
		Policies:          []candidate.PolicyIdentity{{Domain: domain.Domain, Plane: candidate.PlaneLocal}},
		RepositoryMembers: []candidate.Artifact{{RecordCount: 3}},
	}
	if got := t421BlackBoxExtractionRecords(t, []t421fixture.ExtractionDomainProfile{domain}, manifest); got != 6 {
		t.Fatalf("split member decoded records = %d, want 6", got)
	}
}

func TestT421BlackBoxCandidateOpenRecordsIncludesLocalProjections(t *testing.T) {
	manifest := candidate.Manifest{
		RepositoryMembers: []candidate.Artifact{{RecordCount: 1}},
		CallerLeaves:      []candidate.CallerLeaf{{Artifact: candidate.Artifact{RecordCount: 2}}},
		LocalProjections: []candidate.LocalProjection{{
			Members: []candidate.Artifact{{RecordCount: 3}},
		}},
	}
	if got := t421BlackBoxCandidateOpenRecords(manifest); got != 9 {
		t.Fatalf("candidate strict-open visits = %d, want 9", got)
	}
}

func TestT421BlackBoxProjectionMergeVisits(t *testing.T) {
	for _, test := range []struct {
		records uint64
		visits  uint64
	}{
		{512, 0}, {513, 513}, {1_025, 2_049}, {53_204, 364_324},
	} {
		if got := t421BlackBoxProjectionMergeVisits(test.records); got != test.visits {
			t.Fatalf("projection merge visits for %d records = %d, want %d", test.records, got, test.visits)
		}
	}
}

func t421BlackBoxCandidateMemberRecords(
	t *testing.T,
	manifest candidate.Manifest,
	plane candidate.Plane,
	domain string,
	ordinal int64,
) uint64 {
	t.Helper()
	if ordinal < 0 {
		t.Fatalf("negative candidate member ordinal in %q", domain)
	}
	index := int(ordinal)
	switch plane {
	case candidate.PlaneRepository:
		if index >= len(manifest.RepositoryMembers) {
			t.Fatalf("repository candidate member %d in %q is absent", ordinal, domain)
		}
		return uint64(manifest.RepositoryMembers[index].RecordCount)
	case candidate.PlaneCaller:
		if index >= len(manifest.CallerLeaves) {
			t.Fatalf("caller candidate member %d in %q is absent", ordinal, domain)
		}
		return uint64(manifest.CallerLeaves[index].RecordCount)
	case candidate.PlaneLocal:
		if manifest.UnitDigest == "" {
			if index >= len(manifest.RepositoryMembers) {
				t.Fatalf("unscoped local candidate member %d in %q is absent", ordinal, domain)
			}
			return uint64(manifest.RepositoryMembers[index].RecordCount)
		}
		for _, projection := range manifest.LocalProjections {
			if projection.Domain == domain {
				if index >= len(projection.Members) {
					t.Fatalf("local candidate member %d in %q is absent", ordinal, domain)
				}
				return uint64(projection.Members[index].RecordCount)
			}
		}
	}
	t.Fatalf("candidate plane %q in %q is unavailable", plane, domain)
	return 0
}

type t421BlackBoxResponse struct {
	Status      int
	ContentType string
	Body        []byte
	Report      t421ExactReadReport
}

type t421BlackBoxQueryAccounting struct {
	catalog      servicecatalogv3.Root
	relationship relationshippublication.RootV3
	rpc          rpccallerposting.Root
	kafka        kafkatopicposting.Root
}

type t421BlackBoxQueryTransport struct {
	pages           [][]byte
	records         uint64
	expectedMembers uint64
	paths           map[string]struct{}
	reports         []t421ExactReadReport
}

type t421BlackBoxQueryPage struct {
	semantic     []byte
	records      uint64
	paths        []string
	nextCursor   string
	serviceKey   string
	relationship *apiresponse.RelationshipPage
}

func t421BlackBoxOpenQueryAccounting(
	t *testing.T,
	ctx context.Context,
	state *store.Surreal,
	dataDir, repository string,
	selector store.ServiceRuntimeSelector,
) t421BlackBoxQueryAccounting {
	t.Helper()
	catalog, err := state.ReadServiceCatalogV3Root(
		ctx, repository, selector.CatalogRootDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	relationship, err := relationshippublication.OpenGenerationV3(
		ctx, filepath.Join(dataDir, "relationships"), repository,
		selector.RelationshipGenerationDigest, selector.RelationshipRootDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	root := relationship.Root()
	rpc, err := rpccallerposting.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-rpc-postings"), repository,
		root.Authority.RPCGenerationDigest, root.Authority.RPCRootDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	kafka, err := kafkatopicposting.OpenGeneration(
		ctx, filepath.Join(dataDir, "relationship-kafka-postings"), repository,
		root.Authority.KafkaGenerationDigest, root.Authority.KafkaRootDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	return t421BlackBoxQueryAccounting{
		catalog: catalog, relationship: root, rpc: rpc.Root(), kafka: kafka.Root(),
	}
}

func t421BlackBoxRunProductQueries(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	baseURL, credential string,
	ordinal *uint64,
	queries []t421fixture.QueryCase,
	repository string,
	catalog servicecatalog.Catalog,
	accounting t421BlackBoxQueryAccounting,
) {
	t.Helper()
	replacements := t421BlackBoxQueryReplacements(t, repository, catalog)
	httpResults := make(map[string]t421BlackBoxQueryTransport, len(queries))
	mcpResults := make(map[string]t421BlackBoxQueryTransport, len(queries))

	for _, query := range queries {
		httpResults[query.Name] = t421BlackBoxRunHTTPQuery(
			t, ctx, client, baseURL, credential, ordinal, query,
			replacements, accounting,
		)
	}
	t421BlackBoxPrepareMCP(t, ctx, client, baseURL+"/api/mcp", credential)
	for _, query := range queries {
		mcpResults[query.Name] = t421BlackBoxRunMCPQuery(
			t, ctx, client, baseURL, credential, ordinal, query,
			replacements, accounting,
		)
	}

	var totals readaccounting.Counts
	var expectedMembers uint64
	var reports int
	for _, query := range queries {
		httpResult, mcpResult := httpResults[query.Name], mcpResults[query.Name]
		if len(httpResult.pages) != len(mcpResult.pages) {
			t.Fatalf("Q %s HTTP/MCP pages = %d/%d", query.Name, len(httpResult.pages), len(mcpResult.pages))
		}
		for index := range httpResult.pages {
			if !bytes.Equal(httpResult.pages[index], mcpResult.pages[index]) {
				t.Fatalf("Q %s page %d HTTP/MCP semantics differ\nHTTP %s\nMCP %s",
					query.Name, index+1, httpResult.pages[index], mcpResult.pages[index])
			}
		}
		for _, result := range []t421BlackBoxQueryTransport{httpResult, mcpResult} {
			if result.records != query.ExpectedRecords || uint64(len(result.paths)) != query.ExpectedPaths {
				t.Fatalf("Q %s records/paths = %d/%d, want %d/%d",
					query.Name, result.records, len(result.paths), query.ExpectedRecords, query.ExpectedPaths)
			}
			for _, report := range result.reports {
				totals.ControlFileReads += report.ControlFileReads
				totals.StoreReadAttempts += report.StoreReadAttempts
				totals.MemberVisits += report.MemberVisits
				totals.StoreWriteAttempts += report.StoreWriteAttempts
				reports++
			}
			if result.expectedMembers > math.MaxUint64-expectedMembers {
				t.Fatal("Q expected member total overflow")
			}
			expectedMembers += result.expectedMembers
		}
	}
	if reports != 38 || totals != (readaccounting.Counts{
		ControlFileReads: 160, StoreReadAttempts: 164, MemberVisits: expectedMembers,
	}) {
		t.Fatalf("Q accounting reports=%d totals=%+v, want reports=38 C/S/M/W=160/164/%d/0", reports, totals, expectedMembers)
	}
	t.Logf("Q accounting: reports=%d C=%d S=%d M=%d W=%d",
		reports, totals.ControlFileReads, totals.StoreReadAttempts,
		totals.MemberVisits, totals.StoreWriteAttempts)
}

func t421BlackBoxRunHTTPQuery(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	baseURL, credential string,
	ordinal *uint64,
	query t421fixture.QueryCase,
	replacements map[string]string,
	accounting t421BlackBoxQueryAccounting,
) t421BlackBoxQueryTransport {
	t.Helper()
	if query.HTTP.Method != http.MethodGet {
		t.Fatalf("Q %s HTTP method = %q", query.Name, query.HTTP.Method)
	}
	result := t421BlackBoxQueryTransport{paths: make(map[string]struct{})}
	pages := t421BlackBoxQueryPages(query)
	cursor := ""
	for pageIndex := uint64(0); pageIndex < pages; pageIndex++ {
		path := t421BlackBoxExpandHTTPQuery(query.HTTP.Path, replacements)
		if cursor != "" {
			parsed, err := url.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			values := parsed.Query()
			values.Set("cursor", cursor)
			parsed.RawQuery = values.Encode()
			path = parsed.String()
		}
		response := t421BlackBoxExactRequest(
			t, ctx, client, baseURL+path, credential, *ordinal,
		)
		*ordinal = *ordinal + 1
		if response.Report.Status != "complete" || response.Status != int(query.ExpectedStatus) {
			t.Fatalf("Q %s HTTP page %d status=%d report=%+v",
				query.Name, pageIndex+1, response.Status, response.Report)
		}
		page := t421BlackBoxParseHTTPQueryPage(t, query, response.Body)
		wantMembers := accounting.memberVisits(t, query, "http", pageIndex, page)
		if response.Report.MemberVisits != wantMembers || response.Report.StoreWriteAttempts != 0 {
			t.Fatalf("Q %s HTTP page %d M/W=%d/%d, want %d/0",
				query.Name, pageIndex+1, response.Report.MemberVisits,
				response.Report.StoreWriteAttempts, wantMembers)
		}
		if wantMembers > math.MaxUint64-result.expectedMembers {
			t.Fatalf("Q %s HTTP expected member total overflow", query.Name)
		}
		result.expectedMembers += wantMembers
		t.Logf("Q %s HTTP page %d accounting C/S/M/W=%d/%d/%d/%d",
			query.Name, pageIndex+1, response.Report.ControlFileReads,
			response.Report.StoreReadAttempts, response.Report.MemberVisits,
			response.Report.StoreWriteAttempts)
		result.pages = append(result.pages, page.semantic)
		result.records += page.records
		for _, path := range page.paths {
			result.paths[path] = struct{}{}
		}
		result.reports = append(result.reports, response.Report)
		cursor = t421BlackBoxRequireQueryCursor(t, query, pageIndex, pages, page.nextCursor)
	}
	return result
}

func t421BlackBoxRunMCPQuery(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	baseURL, credential string,
	ordinal *uint64,
	query t421fixture.QueryCase,
	replacements map[string]string,
	accounting t421BlackBoxQueryAccounting,
) t421BlackBoxQueryTransport {
	t.Helper()
	result := t421BlackBoxQueryTransport{paths: make(map[string]struct{})}
	pages := t421BlackBoxQueryPages(query)
	cursor := ""
	for pageIndex := uint64(0); pageIndex < pages; pageIndex++ {
		arguments := t421BlackBoxMCPArguments(t, query.Parameters, replacements)
		if cursor != "" {
			arguments["cursor"] = cursor
		}
		response, structured, code := t421BlackBoxExactMCPRequest(
			t, ctx, client, baseURL+"/api/mcp", credential, *ordinal,
			query.MCPTool, arguments,
		)
		*ordinal = *ordinal + 1
		if response.Status != http.StatusOK || response.Report.Status != "complete" ||
			code != query.ExpectedMCPCode {
			t.Fatalf("Q %s MCP page %d status=%d code=%q report=%+v",
				query.Name, pageIndex+1, response.Status, code, response.Report)
		}
		page := t421BlackBoxParseMCPQueryPage(t, query, structured, code)
		wantMembers := accounting.memberVisits(t, query, "mcp", pageIndex, page)
		if response.Report.MemberVisits != wantMembers || response.Report.StoreWriteAttempts != 0 {
			t.Fatalf("Q %s MCP page %d M/W=%d/%d, want %d/0",
				query.Name, pageIndex+1, response.Report.MemberVisits,
				response.Report.StoreWriteAttempts, wantMembers)
		}
		if wantMembers > math.MaxUint64-result.expectedMembers {
			t.Fatalf("Q %s MCP expected member total overflow", query.Name)
		}
		result.expectedMembers += wantMembers
		t.Logf("Q %s MCP page %d accounting C/S/M/W=%d/%d/%d/%d",
			query.Name, pageIndex+1, response.Report.ControlFileReads,
			response.Report.StoreReadAttempts, response.Report.MemberVisits,
			response.Report.StoreWriteAttempts)
		result.pages = append(result.pages, page.semantic)
		result.records += page.records
		for _, path := range page.paths {
			result.paths[path] = struct{}{}
		}
		result.reports = append(result.reports, response.Report)
		cursor = t421BlackBoxRequireQueryCursor(t, query, pageIndex, pages, page.nextCursor)
	}
	return result
}

func t421BlackBoxQueryReplacements(
	t *testing.T,
	repository string,
	catalog servicecatalog.Catalog,
) map[string]string {
	t.Helper()
	services := make([]string, 0, len(catalog.Services))
	for _, service := range catalog.Services {
		if service.Disposition == servicecatalog.DispositionAccepted {
			services = append(services, service.Key)
		}
	}
	sort.Strings(services)
	replacements := map[string]string{
		"$authorized_repository": repository,
		"$hidden_repository":     "github.com/t421/hidden",
	}
	for index, service := range services {
		replacements[fmt.Sprintf("$accepted_service_%05d", index)] = service
	}
	return replacements
}

func t421BlackBoxQueryPages(query t421fixture.QueryCase) uint64 {
	if query.PageSize == 0 {
		return 1
	}
	pages := query.ExpectedRecords / query.PageSize
	if query.ExpectedRecords%query.PageSize != 0 {
		pages++
	}
	return max(uint64(1), pages)
}

func t421BlackBoxExpandHTTPQuery(path string, replacements map[string]string) string {
	for placeholder, value := range replacements {
		path = strings.ReplaceAll(path, placeholder, url.QueryEscape(value))
	}
	return path
}

func t421BlackBoxMCPArguments(
	t *testing.T,
	parameters []t421fixture.QueryParameter,
	replacements map[string]string,
) map[string]any {
	t.Helper()
	arguments := make(map[string]any, len(parameters))
	for _, parameter := range parameters {
		value := parameter.Value
		for placeholder, replacement := range replacements {
			value = strings.ReplaceAll(value, placeholder, replacement)
		}
		switch parameter.Name {
		case "max_matches", "context_lines", "page_size":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				t.Fatalf("Q MCP argument %q = %q: %v", parameter.Name, value, err)
			}
			arguments[parameter.Name] = parsed
		case "repositories":
			var repositories []string
			if err := json.Unmarshal([]byte(value), &repositories); err != nil {
				t.Fatalf("Q MCP repositories = %q: %v", value, err)
			}
			arguments[parameter.Name] = repositories
		default:
			arguments[parameter.Name] = value
		}
	}
	return arguments
}

func t421BlackBoxRequireQueryCursor(
	t *testing.T,
	query t421fixture.QueryCase,
	pageIndex, pages uint64,
	cursor string,
) string {
	t.Helper()
	if pageIndex+1 < pages {
		if cursor == "" {
			t.Fatalf("Q %s page %d omitted its continuation cursor", query.Name, pageIndex+1)
		}
		return cursor
	}
	if cursor != "" {
		t.Fatalf("Q %s final page retained a continuation cursor", query.Name)
	}
	return ""
}

func t421BlackBoxParseHTTPQueryPage(
	t *testing.T,
	query t421fixture.QueryCase,
	body []byte,
) t421BlackBoxQueryPage {
	t.Helper()
	if query.ExpectedStatus != http.StatusOK {
		var problem struct {
			Status int `json:"status"`
		}
		if err := json.Unmarshal(body, &problem); err != nil || problem.Status != int(query.ExpectedStatus) {
			t.Fatalf("Q %s HTTP refusal body = %s: %v", query.Name, body, err)
		}
		return t421BlackBoxDeniedQueryPage(t)
	}
	return t421BlackBoxParseSuccessfulQueryPage(t, query, body)
}

func t421BlackBoxParseMCPQueryPage(
	t *testing.T,
	query t421fixture.QueryCase,
	structured []byte,
	code string,
) t421BlackBoxQueryPage {
	t.Helper()
	if code != "ok" {
		if len(structured) != 0 {
			t.Fatalf("Q %s MCP refusal retained structured content", query.Name)
		}
		return t421BlackBoxDeniedQueryPage(t)
	}
	return t421BlackBoxParseSuccessfulQueryPage(t, query, structured)
}

func t421BlackBoxDeniedQueryPage(t *testing.T) t421BlackBoxQueryPage {
	t.Helper()
	semantic, err := json.Marshal(struct {
		Code string `json:"code"`
	}{Code: "unknown_repository"})
	if err != nil {
		t.Fatal(err)
	}
	return t421BlackBoxQueryPage{semantic: semantic}
}

func t421BlackBoxParseSuccessfulQueryPage(
	t *testing.T,
	query t421fixture.QueryCase,
	raw []byte,
) t421BlackBoxQueryPage {
	t.Helper()
	page := t421BlackBoxQueryPage{}
	switch query.Surface {
	case "all_code_search", "service_search":
		var result struct {
			Schema string `json:"$schema,omitempty"`
			search.Result
		}
		t421BlackBoxDecodeExactJSON(t, raw, &result)
		if result.Stats.MatchCount < 0 {
			t.Fatalf("Q %s returned a negative match count", query.Name)
		}
		page.records = uint64(result.Stats.MatchCount)
		for _, file := range result.Files {
			page.paths = append(page.paths, file.Path)
		}
		result.Stats.DurationMS = 0
		page.semantic = t421BlackBoxMarshalJSON(t, result.Result)
	case "service_detail":
		var detail struct {
			Schema string `json:"$schema,omitempty"`
			apiresponse.ServiceDetail
		}
		t421BlackBoxDecodeExactJSON(t, raw, &detail)
		page.records = uint64(len(detail.Memberships))
		page.serviceKey = detail.Service.Key
		for _, membership := range detail.Memberships {
			page.paths = append(page.paths, membership.Path)
		}
		page.semantic = t421BlackBoxMarshalJSON(t, detail.ServiceDetail)
	case "service_relationships":
		var relationship struct {
			Schema string `json:"$schema,omitempty"`
			apiresponse.RelationshipPage
		}
		t421BlackBoxDecodeExactJSON(t, raw, &relationship)
		page.records = uint64(len(relationship.Rows))
		page.nextCursor = relationship.Pagination.NextCursor
		for index := range relationship.Rows {
			row := &relationship.Rows[index]
			page.paths = append(page.paths, row.Source.Path, row.Evidence.Path)
			if row.Target != nil {
				page.paths = append(page.paths, row.Target.Path)
			}
			row.Citation = ""
		}
		relationship.Pagination.NextCursor = ""
		page.relationship = &relationship.RelationshipPage
		page.semantic = t421BlackBoxMarshalJSON(t, relationship.RelationshipPage)
	default:
		t.Fatalf("Q %s has unsupported surface %q", query.Name, query.Surface)
	}
	return page
}

func TestT421ExactQueryParserAcceptsHumaSchema(t *testing.T) {
	query := t421fixture.QueryCase{Name: "schema", Surface: "all_code_search"}
	plain := []byte(`{"files":[],"stats":{"match_count":0,"file_count":0,"duration_ms":7}}`)
	withSchema := []byte(`{"$schema":"https://example.invalid/Result.json","files":[],"stats":{"match_count":0,"file_count":0,"duration_ms":7}}`)
	if got, want := t421BlackBoxParseSuccessfulQueryPage(t, query, withSchema),
		t421BlackBoxParseSuccessfulQueryPage(t, query, plain); !bytes.Equal(got.semantic, want.semantic) {
		t.Fatalf("Huma schema changed Q semantics: %s / %s", got.semantic, want.semantic)
	}
	detail := t421BlackBoxParseSuccessfulQueryPage(t,
		t421fixture.QueryCase{Name: "detail", Surface: "service_detail"},
		[]byte(`{"$schema":"https://example.invalid/ServiceDetail.json","memberships":[{},{}]}`),
	)
	if detail.records != 2 {
		t.Fatalf("service-detail records = %d; want membership rows 2", detail.records)
	}
}

func t421BlackBoxDecodeExactJSON(t *testing.T, raw []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode Q response: %v\n%s", err, raw)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("Q response has trailing JSON: %v", err)
	}
}

func t421BlackBoxMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (accounting t421BlackBoxQueryAccounting) memberVisits(
	t *testing.T,
	query t421fixture.QueryCase,
	transport string,
	pageIndex uint64,
	page t421BlackBoxQueryPage,
) uint64 {
	t.Helper()
	switch {
	case query.Name == "first_service":
		if transport == "mcp" {
			return 0
		}
		for _, receipt := range accounting.catalog.ServiceMembers {
			if receipt.First <= page.serviceKey && page.serviceKey <= receipt.Last {
				return uint64(receipt.Records + receipt.Memberships)
			}
		}
		t.Fatalf("Q %s catalog member receipt is absent for %q", query.Name, page.serviceKey)
	case query.Surface == "service_relationships":
		if page.relationship == nil {
			t.Fatalf("Q %s relationship page is absent", query.Name)
		}
		var visits uint64
		if pageIndex == 0 {
			serviceKey := page.relationship.Query.ServiceKey
			for _, receipt := range accounting.relationship.ServiceMembers {
				if receipt.FirstKey <= serviceKey && serviceKey <= receipt.LastKey {
					visits += uint64(receipt.ServiceCount)
					serviceKey = ""
					break
				}
			}
			if serviceKey != "" {
				t.Fatalf("Q %s relationship service receipt is absent", query.Name)
			}
		}
		projectionBuckets := make(map[int]struct{})
		componentBuckets := make(map[string]struct{})
		for _, row := range page.relationship.Rows {
			projectionBuckets[t421BlackBoxDigestBucket(t, row.ProjectionDigest)] = struct{}{}
			lookup := row.LookupKey
			if lookup == "" {
				lookup = "__unresolved__"
			}
			switch row.Kind {
			case "rpc":
				bucket := sha256.Sum256([]byte("phebs-rpc-caller-operation-v1\x00" + lookup))
				componentBuckets[fmt.Sprintf("rpc\x00%s\x00%d", row.Plane, bucket[0])] = struct{}{}
			case "kafka":
				bucket := sha256.Sum256([]byte("phebs-kafka-topic-bucket-v1\x00" + lookup))
				componentBuckets[fmt.Sprintf("kafka\x00%s\x00%d", row.Plane, bucket[0])] = struct{}{}
			default:
				t.Fatalf("Q %s relationship kind = %q", query.Name, row.Kind)
			}
		}
		for bucket := range projectionBuckets {
			found := false
			for _, receipt := range accounting.relationship.RepositoryMembers {
				if receipt.Bucket == bucket {
					visits += uint64(receipt.FragmentCount)
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("Q %s projection bucket %d is absent", query.Name, bucket)
			}
		}
		for key := range componentBuckets {
			parts := strings.Split(key, "\x00")
			bucket, err := strconv.Atoi(parts[2])
			if err != nil {
				t.Fatal(err)
			}
			found := false
			switch parts[0] {
			case "rpc":
				for _, receipt := range accounting.rpc.Members {
					if receipt.Protocol == parts[1] && receipt.Bucket == bucket {
						visits += uint64(receipt.PostingCount)
						found = true
						break
					}
				}
			case "kafka":
				for _, receipt := range accounting.kafka.Members {
					if receipt.Plane == parts[1] && receipt.Bucket == bucket {
						visits += uint64(receipt.PostingCount)
						found = true
						break
					}
				}
			}
			if !found {
				t.Fatalf("Q %s evidence member %q is absent", query.Name, key)
			}
		}
		return visits
	}
	return 0
}

func t421BlackBoxDigestBucket(t *testing.T, digest string) int {
	t.Helper()
	if !strings.HasPrefix(digest, "sha256:") || len(digest) < len("sha256:")+2 {
		t.Fatalf("Q projection digest = %q", digest)
	}
	decoded, err := strconv.ParseUint(digest[len("sha256:"):len("sha256:")+2], 16, 8)
	if err != nil {
		t.Fatalf("Q projection digest = %q: %v", digest, err)
	}
	return int(decoded)
}

func t421BlackBoxPrepareMCP(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	target, credential string,
) {
	t.Helper()
	requests := []struct {
		id     string
		method string
		params any
	}{
		{id: "setup-initialize", method: "initialize", params: map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name": "t421-final-authority-regression", "version": "1",
			},
		}},
		{id: "setup-tools-list", method: "tools/list", params: map[string]any{}},
	}
	for _, setup := range requests {
		body := t421BlackBoxMCPRequestBody(t, setup.id, setup.method, setup.params)
		response, err := t421BlackBoxHTTPRaw(
			ctx, client, http.MethodPost, target, credential, body, 0,
		)
		if err != nil {
			t.Fatal(err)
		}
		if response.Status != http.StatusOK || response.Report != (t421ExactReadReport{}) {
			t.Fatalf("unmarked MCP %s status=%d report=%+v", setup.method, response.Status, response.Report)
		}
		message := t421BlackBoxMCPMessage(t, response.ContentType, response.Body)
		decoded, err := jsonrpc.DecodeMessage(message)
		if err != nil {
			t.Fatalf("decode unmarked MCP %s: %v", setup.method, err)
		}
		reply, ok := decoded.(*jsonrpc.Response)
		if !ok || reply.Error != nil || reply.ID.Raw() != setup.id {
			t.Fatalf("unmarked MCP %s response = %#v", setup.method, decoded)
		}
	}
}

func t421BlackBoxExactMCPRequest(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	target, credential string,
	ordinal uint64,
	tool string,
	arguments map[string]any,
) (t421BlackBoxResponse, []byte, string) {
	t.Helper()
	id := fmt.Sprintf("q-%d", ordinal)
	body := t421BlackBoxMCPRequestBody(t, id, "tools/call", map[string]any{
		"name": tool, "arguments": arguments,
	})
	response, err := t421BlackBoxHTTPRaw(
		ctx, client, http.MethodPost, target, credential, body, ordinal,
	)
	if err != nil {
		t.Fatal(err)
	}
	message := t421BlackBoxMCPMessage(t, response.ContentType, response.Body)
	decoded, err := jsonrpc.DecodeMessage(message)
	if err != nil {
		t.Fatalf("decode Q MCP response: %v\n%s", err, message)
	}
	reply, ok := decoded.(*jsonrpc.Response)
	if !ok || reply.ID.Raw() != id || reply.Error != nil {
		t.Fatalf("Q MCP JSON-RPC response = %#v", decoded)
	}
	var result struct {
		Meta              json.RawMessage   `json:"_meta,omitempty"`
		Content           []json.RawMessage `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
		IsError           bool              `json:"isError,omitempty"`
	}
	t421BlackBoxDecodeExactJSON(t, reply.Result, &result)
	if !result.IsError {
		if len(result.StructuredContent) == 0 || string(result.StructuredContent) == "null" {
			t.Fatal("Q MCP success omitted structured content")
		}
		return response, result.StructuredContent, "ok"
	}
	if len(result.StructuredContent) != 0 && string(result.StructuredContent) != "null" {
		t.Fatal("Q MCP refusal retained structured content")
	}
	var text strings.Builder
	for _, content := range result.Content {
		var item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(content, &item) == nil && item.Type == "text" {
			text.WriteString(item.Text)
			text.WriteByte('\n')
		}
	}
	if !strings.Contains(strings.ToLower(text.String()), "not found") {
		t.Fatalf("Q MCP refusal text = %q", text.String())
	}
	return response, nil, "unknown_repository"
}

func t421BlackBoxMCPRequestBody(
	t *testing.T,
	id, method string,
	params any,
) []byte {
	t.Helper()
	return t421BlackBoxMarshalJSON(t, struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{JSONRPC: "2.0", ID: id, Method: method, Params: params})
}

func t421BlackBoxMCPMessage(t *testing.T, contentType string, body []byte) []byte {
	t.Helper()
	mediaType := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	if mediaType == "application/json" {
		return body
	}
	if mediaType != "text/event-stream" {
		t.Fatalf("Q MCP content type = %q", contentType)
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 1024), 4<<20)
	var message []byte
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "" || strings.HasPrefix(line, ":") || line == "event: message":
		case strings.HasPrefix(line, "data: ") && len(message) == 0:
			message = append([]byte(nil), strings.TrimPrefix(line, "data: ")...)
		default:
			t.Fatalf("Q MCP SSE line = %q", line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(message) == 0 {
		t.Fatalf("Q MCP SSE omitted a message: %s", body)
	}
	return message
}

func t421BlackBoxRepository(
	t *testing.T,
	ctx context.Context,
	repository string,
	domains []t421fixture.ExtractionDomainProfile,
) string {
	t.Helper()
	structuralPath, structuralContent := t421BlackBoxStructuralFixture(t)
	t421BlackBoxValidateFixtureScope(t, domains, structuralPath)
	t421BlackBoxRun(t, ctx, "", "git", "init", "--bare", "--initial-branch=main", repository)
	command := exec.CommandContext(ctx, "git", "--git-dir", repository, "fast-import", "--quiet")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	writer := bufio.NewWriterSize(input, 1<<20)
	const message = "T42.1 F fixture\n"
	_, err = fmt.Fprintf(writer, "commit refs/heads/main\nmark :1\nauthor Neutral <neutral@example.invalid> 946684801 +0000\ncommitter Neutral <neutral@example.invalid> 946684801 +0000\ndata %d\n%s", len(message), message)
	files := 0
	if err == nil {
		err = t421fixture.WalkCombinedAdditions(func(path string, content []byte) error {
			if strings.ContainsAny(path, "\r\n") {
				return errors.New("T42.1 fixture path contains a line break")
			}
			if _, err := fmt.Fprintf(writer, "M 100644 inline %s\ndata %d\n", strconv.Quote(path), len(content)); err != nil {
				return err
			}
			if _, err := writer.Write(content); err != nil {
				return err
			}
			if err := writer.WriteByte('\n'); err != nil {
				return err
			}
			files++
			return nil
		})
	}
	if err == nil {
		const profile = "t401-neutral-scale-envelope-v1\n"
		_, err = fmt.Fprintf(writer,
			"M 100644 inline %s\ndata %d\n%s",
			strconv.Quote(".phebs/t401-profile.txt"), len(profile), profile,
		)
		files++
	}
	if err == nil {
		const module = "module example.invalid/t401-neutral\n\ngo 1.26\n"
		_, err = fmt.Fprintf(writer,
			"M 100644 inline %s\ndata %d\n%s",
			strconv.Quote("go.mod"), len(module), module,
		)
		files++
	}
	if err == nil {
		_, err = fmt.Fprintf(writer,
			"M 100644 inline %s\ndata %d\n%s",
			strconv.Quote(structuralPath), len(structuralContent), structuralContent,
		)
		files++
	}
	if err == nil && files != 31_605 {
		err = fmt.Errorf("T42.1 reduced fixture files = %d, want 31605", files)
	}
	if err == nil {
		_, err = io.WriteString(writer, "done\n")
	}
	err = errors.Join(err, writer.Flush(), input.Close())
	if waitErr := command.Wait(); waitErr != nil {
		err = errors.Join(err, fmt.Errorf("git fast-import: %w: %s", waitErr, output.String()))
	}
	if err != nil {
		t.Fatal(err)
	}
	t421BlackBoxRun(t, ctx, "", "git", "--git-dir", repository,
		"symbolic-ref", "HEAD", "refs/heads/main")
	return strings.TrimSpace(t421BlackBoxOutput(t, ctx, "", "git", "--git-dir", repository,
		"rev-parse", "HEAD"))
}

func TestT421BlackBoxFixtureScopeMatchesFrozenProfile(t *testing.T) {
	corpus, err := t421fixture.BuildCombinedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	path, _ := t421BlackBoxStructuralFixture(t)
	t421BlackBoxValidateFixtureScope(t, corpus.Profile.Pipeline.ExtractionDomains, path)
}

func t421BlackBoxStructuralFixture(t *testing.T) (string, []byte) {
	t.Helper()
	profiles, err := t401.FrozenProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if profile.Name != t401.StructuralProfileName {
			continue
		}
		path, content, err := t401.FrozenStructuralGoFixture(profile, 0)
		if err != nil {
			t.Fatal(err)
		}
		return path, content
	}
	t.Fatal("frozen structural profile is absent")
	return "", nil
}

func t421BlackBoxValidateFixtureScope(
	t *testing.T,
	domains []t421fixture.ExtractionDomainProfile,
	structuralPath string,
) {
	t.Helper()
	policies, err := extract.CandidatePolicies(evidenceExtractors(true, true, false, true))
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]uint64, len(policies))
	count := func(path string) {
		for _, policy := range policies {
			if policy.Enumerate(path) {
				counts[policy.Domain]++
			}
		}
	}
	if err := t421fixture.WalkCombinedAdditions(func(path string, _ []byte) error {
		count(path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	count(".phebs/t401-profile.txt")
	count("go.mod")
	count(structuralPath)
	if len(policies) != len(domains) {
		t.Fatalf("fixture policies = %d, want %d", len(policies), len(domains))
	}
	for _, domain := range domains {
		if counts[domain.Domain] != domain.CandidateRecords {
			t.Fatalf("fixture candidates in %q = %d, want %d",
				domain.Domain, counts[domain.Domain], domain.CandidateRecords)
		}
	}
}

func t421BlackBoxConfig(
	t *testing.T,
	path, repository, repositoryName, catalogPath, dataDir, address, credential string,
	catalog servicecatalog.Catalog,
) {
	t.Helper()
	if catalog.Authority.Kind != servicecatalog.AuthorityOperator ||
		catalog.Authority.ID == "" || catalog.Authority.Version == "" {
		t.Fatalf("unexpected T42.1 catalog authority: %+v", catalog.Authority)
	}
	insecure := false
	enabled := true
	value := config.Config{
		Server:      config.Server{Addr: address, DataDir: dataDir},
		Auth:        config.Auth{APIKey: credential, CookieSecure: &insecure},
		Sync:        config.Sync{PollInterval: "250ms", ResyncInterval: "0"},
		Diagnostics: config.Diagnostics{Jobs: true, Candidates: true, Extraction: true},
		Lifecycle:   config.Lifecycle{Enabled: &enabled},
		Experimental: config.Experimental{
			ProvisionalProtoExtraction: true, ProvisionalThriftExtraction: true,
			ProvisionalThriftFieldExtraction: false, ProvisionalKafkaExtraction: true,
		},
		Connections: []config.Connection{{
			Name: "t421-final-authority", Type: "git", URL: repository, Watch: true,
		}},
		ServiceCatalogs: map[string]config.ServiceCatalog{repositoryName: {
			Kind: catalog.Authority.Kind, ID: catalog.Authority.ID,
			Version: catalog.Authority.Version, Path: catalogPath,
			Runtime: config.ServiceCatalogRuntimeV3,
		}},
	}
	raw, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func t421BlackBoxBuild(t *testing.T, ctx context.Context, root, output, target string) {
	t.Helper()
	t421BlackBoxRun(t, ctx, root, "go", "build", "-trimpath", "-o", output, target)
}

func t421BlackBoxAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func t421BlackBoxServerEnvironment(zoekt string) []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "PHEBS_") && key != "PHEBS_SURREAL" &&
			key != "PHEBS_SURREAL_SHA256" {
			continue
		}
		result = append(result, value)
	}
	return append(result,
		t421ExactReadsEnvironment+"="+t421ExactReadsContract,
		"PHEBS_ZOEKT_GIT_INDEX="+zoekt,
	)
}

func t421BlackBoxWaitHealth(
	t *testing.T, ctx context.Context, client *http.Client, baseURL string,
	serverDone <-chan struct{}, serverErr func() error, logPath string,
) {
	t.Helper()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err == nil {
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil {
				return
			}
		}
		select {
		case <-serverDone:
			t.Fatalf("server exited before health: %v\n%s", serverErr(), t421BlackBoxLogTail(logPath))
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func t421BlackBoxWait(
	t *testing.T, ctx context.Context, timeout time.Duration,
	ready func() (bool, error),
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		ok, err := ready()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatal("bounded readiness did not converge")
}

func isT421BlackBoxExtractionRetry(status int, raw []byte) bool {
	var problem struct {
		Status int    `json:"status"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &problem) != nil || problem.Status != status {
		return false
	}
	return status == http.StatusNotFound && problem.Title == "Not Found" &&
		problem.Detail == "extraction progress not found" ||
		status == http.StatusConflict &&
			(problem.Detail == apiresponse.ExtractionProgressDetailStale ||
				problem.Detail == apiresponse.ExtractionProgressDetailAuthority)
}

func t421BlackBoxExactRequest(
	t *testing.T, ctx context.Context, client *http.Client, target, credential string,
	ordinal uint64,
) t421BlackBoxResponse {
	t.Helper()
	response, err := t421BlackBoxExactRequestRaw(ctx, client, target, credential, ordinal)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func t421BlackBoxExactRequestRaw(
	ctx context.Context, client *http.Client, target, credential string,
	ordinal uint64,
) (t421BlackBoxResponse, error) {
	return t421BlackBoxHTTPRaw(
		ctx, client, http.MethodGet, target, credential, nil, ordinal,
	)
}

func t421BlackBoxHTTPRaw(
	ctx context.Context,
	client *http.Client,
	method, target, credential string,
	body []byte,
	ordinal uint64,
) (t421BlackBoxResponse, error) {
	var input io.Reader
	if body != nil {
		input = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, input)
	if err != nil {
		return t421BlackBoxResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
	}
	if ordinal != 0 {
		request.Header.Set(t421ExactReadActivationHeader, t421ExactReadsContract)
		request.Header.Set(t421ExactReadOrdinalHeader, strconv.FormatUint(ordinal, 10))
	}
	response, err := client.Do(request)
	if err != nil {
		return t421BlackBoxResponse{}, err
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return t421BlackBoxResponse{}, errors.Join(readErr, closeErr)
	}
	result := t421BlackBoxResponse{
		Status: response.StatusCode, ContentType: response.Header.Get("Content-Type"), Body: body,
	}
	encoded := response.Trailer.Get(t421ExactReadTrailer)
	if ordinal == 0 {
		if encoded != "" {
			return t421BlackBoxResponse{}, errors.New("unmarked request returned an exact-read report")
		}
		return result, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return t421BlackBoxResponse{}, fmt.Errorf("decode exact-read report: %w", err)
	}
	var report t421ExactReadReport
	if err := json.Unmarshal(raw, &report); err != nil ||
		report.Schema != t421ExactReadReportSchema || report.RequestOrdinal != ordinal {
		return t421BlackBoxResponse{}, fmt.Errorf(
			"invalid exact-read report %q: %+v: %w", raw, report,
			errors.Join(err, errors.New("report identity mismatch")),
		)
	}
	result.Report = report
	return result, nil
}

func t421BlackBoxRun(t *testing.T, ctx context.Context, dir, name string, args ...string) {
	t.Helper()
	_ = t421BlackBoxOutput(t, ctx, dir, name, args...)
}

func t421BlackBoxOutput(t *testing.T, ctx context.Context, dir, name string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return string(output)
}

func t421BlackBoxLogTail(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err.Error()
	}
	if info.Size() > 32<<10 {
		if _, err := file.Seek(-(32 << 10), io.SeekEnd); err != nil {
			return err.Error()
		}
	}
	raw, err := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	if err != nil {
		return err.Error()
	}
	return string(raw)
}

func t421BlackBoxRequireProcessExit(t *testing.T, pid int) {
	t.Helper()
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	alive := func() bool { return process.Signal(syscall.Signal(0)) == nil }
	deadline := time.Now().Add(5 * time.Second)
	for alive() && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if alive() {
		t.Errorf("SurrealDB process %d survived real-server regression cleanup", pid)
	}
}
