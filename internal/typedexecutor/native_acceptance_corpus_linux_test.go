//go:build linux

package typedexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedbazel/provider"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"github.com/bmeddeb/phebs/spike/t451b"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

type acceptanceCorpusSymbolResult struct {
	Name                string `json:"name"`
	SymbolSHA256        string `json:"symbol_sha256"`
	QueryPoints         int    `json:"query_points"`
	DefinitionLocations int    `json:"definition_locations"`
	HoverPayloads       int    `json:"hover_payloads"`
	ReferencePoints     int    `json:"reference_points"`
}
type acceptanceCorpusResult struct {
	Cohort             string                         `json:"cohort"`
	Commit             string                         `json:"commit"`
	ArchiveSHA256      string                         `json:"archive_sha256"`
	OracleSHA256       string                         `json:"oracle_sha256"`
	RootDigest         string                         `json:"root_digest"`
	PlanDigest         string                         `json:"plan_digest"`
	MemberDigests      []string                       `json:"member_digests"`
	Documents          int                            `json:"documents"`
	Occurrences        int                            `json:"occurrences"`
	Definitions        int                            `json:"definitions"`
	GeneratedDocuments int                            `json:"generated_documents"`
	NavigationVerified bool                           `json:"navigation_verified"`
	SourceGitDrained   bool                           `json:"source_git_drained"`
	Symbols            []acceptanceCorpusSymbolResult `json:"symbols"`
	CrossCohort        string                         `json:"cross_cohort"`
	CostGate           string                         `json:"cost_gate"`
	CostMissing        []string                       `json:"cost_missing"`
	Costs              []acceptanceCorpusPhaseCost    `json:"costs,omitempty"`
}

type acceptanceCorpusPhaseCost struct {
	Phase             typedindex.Action            `json:"phase"`
	PlanningDigest    string                       `json:"planning_digest"`
	AttemptDigest     string                       `json:"attempt_digest"`
	RequestDigest     string                       `json:"request_digest"`
	SealDigest        string                       `json:"seal_digest"`
	StdoutSHA256      string                       `json:"stdout_sha256"`
	StderrSHA256      string                       `json:"stderr_sha256"`
	WorkerOutputBytes int64                        `json:"worker_output_bytes"`
	Metrics           *t451b.ManagedCost           `json:"metrics,omitempty"`
	HostWitness       *acceptanceCorpusHostWitness `json:"host_witness,omitempty"`
	HostWitnessSHA256 string                       `json:"host_witness_sha256,omitempty"`
	WorkerCache       *t451b.ManagedCache          `json:"worker_cache,omitempty"`
}

type acceptanceCorpusPhaseStop struct {
	Phase              string                `json:"phase"`
	PlanningDigest     string                `json:"planning_digest"`
	AttemptDigest      string                `json:"attempt_digest"`
	RequestDigest      string                `json:"request_digest"`
	SealDigest         string                `json:"seal_digest"`
	StdoutSHA256       string                `json:"stdout_sha256"`
	StderrSHA256       string                `json:"stderr_sha256"`
	ExitCode           int                   `json:"exit_code"`
	DiagnosticOnly     bool                  `json:"diagnostic_only"`
	CompletionVerified bool                  `json:"completion_verified"`
	Stop               t451b.ManagedCostStop `json:"stop"`
}

// A failed native invocation has no successful completion authority. Preserve
// only its bounded diagnostic and exact observed stream hashes beside the
// original nonpass; never add this record to successful Costs.
func acceptanceCorpusStop(options typedsandbox.Options, result typedsandbox.Result) (acceptanceCorpusPhaseStop, error) {
	var stopped acceptanceCorpusPhaseStop
	if result.ExitCode != 125 || options.Allowance.Validate() != nil || options.Control.Validate() != nil || options.Control.PlanningDigest != options.Allowance.PlanningDigest || options.Control.AttemptDigest != options.Allowance.AttemptDigest || int64(len(result.Stdout)+len(result.Stderr)) > typedsandbox.OutputBytes-options.Allowance.WorkerBytesUsed {
		return stopped, errors.New("invalid failed native diagnostic")
	}
	diagnostic, err := t451b.DecodeManagedCostStop(result.Stderr)
	if err != nil {
		return stopped, err
	}
	return acceptanceCorpusPhaseStop{Phase: options.Control.Phase, PlanningDigest: options.Control.PlanningDigest, AttemptDigest: options.Control.AttemptDigest, RequestDigest: options.Control.RequestDigest, SealDigest: options.Control.SealDigest, StdoutSHA256: acceptanceDigest(result.Stdout), StderrSHA256: acceptanceDigest(result.Stderr), ExitCode: result.ExitCode, DiagnosticOnly: true, Stop: diagnostic}, nil
}

func TestNativeAcceptanceCorpusStop(t *testing.T) {
	control := typedsandbox.ControlIdentity{PlanningDigest: acceptanceDigest([]byte("plan")), AttemptDigest: acceptanceDigest([]byte("attempt")), Phase: "plan", SealDigest: acceptanceDigest([]byte("seal")), Device: 1, Inode: 2}
	control.RequestDigest = control.PlanningDigest
	options := typedsandbox.Options{Control: control, Allowance: typedsandbox.Allowance{Schema: "phebs-typed-allowance-v1", PlanningDigest: control.PlanningDigest, AttemptDigest: control.AttemptDigest, BootID: "12345678-1234-1234-1234-123456789abc", TimeDevice: 1, TimeInode: 2, Start: 1, Deadline: 1 + int64(typedsandbox.WallLimit)}}
	raw, err := t451b.EncodeManagedCostStop(t451b.ManagedCostStop{Schema: t451b.ManagedCostStopSchema, Stage: "observer_start"})
	if err != nil {
		t.Fatal(err)
	}
	failed := typedsandbox.Result{ExitCode: 125, Stderr: raw}
	stopped, err := acceptanceCorpusStop(options, failed)
	if err != nil || !stopped.DiagnosticOnly || stopped.CompletionVerified || stopped.StderrSHA256 != acceptanceDigest(raw) || stopped.ExitCode != 125 || stopped.SealDigest != control.SealDigest {
		t.Fatal("failed native diagnostic not preserved", err)
	}
	if _, err := acceptanceCorpusCost(t.Context(), options, failed); err == nil {
		t.Fatal("diagnostic minted successful completion")
	}
	for _, change := range []func(*typedsandbox.Options, *typedsandbox.Result){
		func(_ *typedsandbox.Options, r *typedsandbox.Result) { r.ExitCode = 0 },
		func(o *typedsandbox.Options, _ *typedsandbox.Result) {
			o.Control.AttemptDigest = acceptanceDigest([]byte("foreign"))
		},
		func(o *typedsandbox.Options, _ *typedsandbox.Result) {
			o.Allowance.WorkerBytesUsed = typedsandbox.OutputBytes
		},
		func(_ *typedsandbox.Options, r *typedsandbox.Result) { r.Stderr = append(slices.Clone(r.Stderr), '\n') },
	} {
		o, r := options, failed
		change(&o, &r)
		if _, err := acceptanceCorpusStop(o, r); err == nil {
			t.Fatal("invalid failed diagnostic admitted")
		}
	}
}

// Parse only source-free metadata from the existing success Result-v1. Full
// provider admission still runs unchanged in the controller. The completion
// token, rather than a copied scalar receipt, authenticates both exact streams.
func acceptanceCorpusCost(ctx context.Context, options typedsandbox.Options, result typedsandbox.Result) (acceptanceCorpusPhaseCost, error) {
	var cost acceptanceCorpusPhaseCost
	if err := typedsandbox.VerifyCompletion(options.Allowance, options.Control, result); err != nil {
		return cost, err
	}
	phase, err := acceptanceCorpusResultPhase(ctx, options, result)
	if err != nil {
		return cost, err
	}
	metrics, err := t451b.DecodeManagedCost(result.Stderr)
	if err != nil {
		return cost, err
	}
	return acceptanceCorpusPhaseCost{Phase: phase, PlanningDigest: options.Control.PlanningDigest, AttemptDigest: options.Control.AttemptDigest, RequestDigest: options.Control.RequestDigest, SealDigest: options.Control.SealDigest, StdoutSHA256: acceptanceDigest(result.Stdout), StderrSHA256: acceptanceDigest(result.Stderr), WorkerOutputBytes: int64(len(result.Stdout) + len(result.Stderr)), Metrics: &metrics}, nil
}

func acceptanceCorpusResultPhase(ctx context.Context, options typedsandbox.Options, result typedsandbox.Result) (typedindex.Action, error) {
	var header struct {
		Schema        string            `json:"schema"`
		RequestDigest string            `json:"request_digest"`
		Phase         typedindex.Action `json:"phase"`
	}
	wire, err := provider.DecodeResultWire(ctx, result.Stdout, typedsandbox.OutputBytes-options.Allowance.WorkerBytesUsed)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(wire, &header); err != nil || header.Schema != "phebs-bazel-worker-result-v1" || string(header.Phase) != options.Control.Phase || header.RequestDigest != options.Control.RequestDigest {
		return "", errors.New("cost result metadata differs from authenticated native control")
	}
	return header.Phase, nil
}

func acceptanceCorpusSetCost(r *acceptanceCorpusResult, costs []acceptanceCorpusPhaseCost) error {
	if r == nil {
		return errors.New("missing corpus proof for cost")
	}
	if len(costs) == 0 {
		if r.CostGate != "unavailable" || len(r.Costs) != 0 || !slices.Equal(r.CostMissing, []string{"sampled_child_lifetimes", "sampled_fd_counts", "private_cache_inventory"}) {
			return errors.New("missing cost cannot establish completion")
		}
		return nil
	}
	if r.CostGate == "pass" && len(r.CostMissing) != 0 {
		return errors.New("completed cost contradicts missing measurements")
	}
	if len(costs) != 2 {
		return errors.New("both cold phases require complete cost measurements")
	}
	var output, duration int64
	for i, cost := range costs {
		if (cost.HostWitness == nil) != (costs[0].HostWitness == nil) {
			return errors.New("mixed cold phase measurement methods")
		}
		if cost.Phase != [2]typedindex.Action{typedindex.Plan, typedindex.Execute}[i] || cost.PlanningDigest != costs[0].PlanningDigest || cost.AttemptDigest != costs[0].AttemptDigest || (cost.RequestDigest == cost.PlanningDigest) != (i == 0) || cost.WorkerOutputBytes <= 0 || cost.WorkerOutputBytes > typedsandbox.OutputBytes-output {
			return errors.New("cost phase, owner or shared output differs")
		}
		for _, digest := range []string{cost.PlanningDigest, cost.AttemptDigest, cost.RequestDigest, cost.SealDigest, cost.StdoutSHA256, cost.StderrSHA256} {
			if !acceptanceHash(digest) {
				return errors.New("cost control digest")
			}
		}
		var raw []byte
		var err error
		if cost.HostWitness != nil {
			if cost.Metrics != nil || cost.WorkerCache == nil || acceptanceValidateHostCost(cost) != nil {
				return errors.New("host cost witness differs from completed phase")
			}
			a, first := cost.HostWitness.Allowance, costs[0].HostWitness.Allowance
			if a.Start != first.Start || a.Deadline != first.Deadline || a.BootID != first.BootID || a.TimeDevice != first.TimeDevice || a.TimeInode != first.TimeInode || a.WorkerBytesUsed != output || (a.WireBytesUsed == 0) != (i == 0) {
				return errors.New("host cost shared allowance differs")
			}
			raw, err = t451b.EncodeManagedCache(*cost.WorkerCache)
			duration += cost.HostWitness.Observations.DurationNanoseconds
		} else {
			if cost.Metrics == nil || cost.WorkerCache != nil || cost.HostWitnessSHA256 != "" {
				return errors.New("missing or mixed cost method")
			}
			raw, err = t451b.EncodeManagedCost(*cost.Metrics)
			duration += cost.Metrics.Observations.DurationNanoseconds
		}
		if err != nil {
			return err
		}
		if acceptanceDigest(raw) != cost.StderrSHA256 || int64(len(raw)) >= cost.WorkerOutputBytes {
			return errors.New("cost stderr identity or physical output")
		}
		output += cost.WorkerOutputBytes
	}
	if duration > int64(typedsandbox.WallLimit) {
		return errors.New("sampled cost duration exceeds shared wall")
	}
	r.CostGate, r.CostMissing, r.Costs = "pass", []string{}, slices.Clone(costs)
	return nil
}

func TestNativeAcceptanceCorpusCost(t *testing.T) {
	metrics := t451b.ManagedCost{Schema: t451b.ManagedCostSchema,
		Observations: t451b.Observations{Version: "phebs-t451b-sampled-observations-v1", IntervalNanoseconds: 50000000, DurationNanoseconds: 1, Samples: 2, SampledChildLifetimes: 1, ChildLifetimesLowerBound: true, SampledProcessFDPeak: 2, SampledAggregateFDPeak: 2, FDCountsNonAtomic: true},
		Cache:        t451b.PrivateCacheObservation{Version: "phebs-t451b-private-cache-v1", Roots: []string{"/scratch/bazel-user", "/scratch/bazel-output", "/scratch/repository-cache", "/scratch/gocache", "/scratch/gomodcache", "/scratch/cache"}, MissingRoots: []string{}, Entries: 6, Directories: 6, UniqueInodes: 6, Complete: true}}
	raw, err := t451b.EncodeManagedCost(metrics)
	if err != nil {
		t.Fatal(err)
	}
	planning, attempt := acceptanceDigest([]byte("planning")), acceptanceDigest([]byte("attempt"))
	costs := []acceptanceCorpusPhaseCost{}
	for _, phase := range []typedindex.Action{typedindex.Plan, typedindex.Execute} {
		request := planning
		if phase == typedindex.Execute {
			request = acceptanceDigest([]byte("execute"))
		}
		costs = append(costs, acceptanceCorpusPhaseCost{Phase: phase, PlanningDigest: planning, AttemptDigest: attempt, RequestDigest: request, SealDigest: acceptanceDigest([]byte(phase)), StdoutSHA256: acceptanceDigest([]byte("result")), StderrSHA256: acceptanceDigest(raw), WorkerOutputBytes: 4096, Metrics: &metrics})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*acceptanceCorpusResult, *[]acceptanceCorpusPhaseCost)
		valid  bool
	}{
		{"both-phases", func(*acceptanceCorpusResult, *[]acceptanceCorpusPhaseCost) {}, true},
		{"unavailable", func(_ *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) { *c = nil }, true},
		{"empty-pass", func(r *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) { r.CostGate = "pass"; *c = nil }, false},
		{"partial", func(_ *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) { *c = (*c)[:1] }, false},
		{"phase", func(_ *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) { (*c)[1].Phase = typedindex.Plan }, false},
		{"owner", func(_ *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) {
			(*c)[1].AttemptDigest = acceptanceDigest([]byte("foreign"))
		}, false},
		{"stderr-hash", func(_ *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) {
			(*c)[1].StderrSHA256 = acceptanceDigest([]byte("foreign"))
		}, false},
		{"request", func(_ *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) { (*c)[1].RequestDigest = planning }, false},
		{"output", func(_ *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) {
			(*c)[1].WorkerOutputBytes = typedsandbox.OutputBytes
		}, false},
		{"qualified", func(_ *acceptanceCorpusResult, c *[]acceptanceCorpusPhaseCost) {
			(*c)[1].Metrics.Observations.FDCountsNonAtomic = false
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &acceptanceCorpusResult{CostGate: "unavailable", CostMissing: []string{"sampled_child_lifetimes", "sampled_fd_counts", "private_cache_inventory"}}
			input := slices.Clone(costs)
			tc.mutate(r, &input)
			if err := acceptanceCorpusSetCost(r, input); (err == nil) != tc.valid {
				t.Fatal("cost completion classification", err)
			}
		})
	}
	// Exported healthy-looking scalars plus genuine cost JSON cannot mint the
	// private successful native completion token from another invocation.
	fake := typedsandbox.Result{ExitCode: 0, Removed: true, Stdout: []byte(`{"schema":"phebs-bazel-worker-result-v1","request_digest":"` + planning + `","phase":"plan"}`), Stderr: raw}
	if _, err := acceptanceCorpusCost(t.Context(), typedsandbox.Options{}, fake); err == nil {
		t.Fatal("copied result bypassed native completion token")
	}
}

func acceptanceCorpusLocation(c nativeAcceptanceConfig, p acceptanceOraclePoint, l *codenav.Location) bool {
	return l != nil && l.Repo == c.Source.Repository && l.Revision == c.Source.Commit && l.Path == p.Path && l.Encoding == codenav.EncodingUTF8 && l.Range == acceptanceCorpusRange(p)
}
func acceptanceCorpusRange(p acceptanceOraclePoint) codenav.CodeRange {
	return codenav.CodeRange{Start: codenav.CodePosition{Line: p.Range[0], Character: p.Range[1]}, End: codenav.CodePosition{Line: p.Range[0], Character: p.Range[2]}}
}

// Every query/open/final check re-reads real exact current authority. There is
// no mutable binding map, imported source authority or Git fallback here.
type acceptanceCorpusResolver struct {
	store *store.Surreal
	base  string
	conf  nativeAcceptanceConfig
}

func (r *acceptanceCorpusResolver) current(ctx context.Context) (typedworkspace.RoutedAuthority, codenav.RoutedBinding, error) {
	c, err := r.store.ReadTypedIndexCurrentCustody(ctx, r.conf.Source.Repository)
	if err != nil {
		return typedworkspace.RoutedAuthority{}, codenav.RoutedBinding{}, err
	}
	id := typedworkspace.OwnerIdentity{PlanningDigest: c.PlanningDigest, AttemptDigest: c.AttemptDigest, ChunkIdentity: c.ChunkIdentity, LeaseDigest: c.LeaseDigest, Request: c.Parent.Request()}
	a := typedworkspace.RoutedAuthority{Owner: id, ManifestDigest: c.Custody.ManifestDigest, DirectoryDevice: c.Custody.DirectoryDevice, DirectoryInode: c.Custody.DirectoryInode, PublicationReceiptDigest: c.Custody.PublicationReceiptDigest, Parent: c.Parent, Execution: c.Admission, RootDigest: c.Pointer.RootDigest}
	if a.Identity() == "" || c.Parent.Request().Source != r.conf.Source || c.Pointer.Epoch == 0 || c.Custody.Revision != 3 || c.Custody.PublicationRootDigest != c.Pointer.RootDigest {
		return typedworkspace.RoutedAuthority{}, codenav.RoutedBinding{}, codenav.ErrTypedIndexBinding
	}
	identity := acceptanceDigest(acceptanceJSON(struct {
		Custody string
		Epoch   uint64
	}{a.Identity(), c.Pointer.Epoch}))
	return a, codenav.RoutedBinding{Selected: true, Identity: identity, RootDigest: c.Pointer.RootDigest, Source: r.conf.Source}, nil
}
func (r *acceptanceCorpusResolver) ResolveRoutedIndex(ctx context.Context, repo, commit string) (codenav.RoutedBinding, error) {
	if repo != r.conf.Source.Repository || commit != r.conf.Source.Commit {
		return codenav.RoutedBinding{}, codenav.ErrTypedIndexBinding
	}
	_, b, err := r.current(ctx)
	return b, err
}
func (r *acceptanceCorpusResolver) OpenRoutedIndex(ctx context.Context, b codenav.RoutedBinding, cached codenav.RoutedMetadata) (codenav.RoutedReader, codenav.RoutedMetadata, error) {
	a, actual, err := r.current(ctx)
	if err != nil {
		return nil, nil, err
	}
	if actual != b {
		return nil, nil, codenav.ErrBindingChanged
	}
	var m *typedworkspace.RoutedMetadata
	if cached != nil {
		var ok bool
		m, ok = cached.(*typedworkspace.RoutedMetadata)
		if !ok {
			return nil, nil, codenav.ErrTypedIndexBinding
		}
	}
	p, err := typedworkspace.OpenRoutedPublication(ctx, r.base, a, m)
	if err != nil {
		return nil, nil, err
	}
	return p, p.Metadata(), nil
}

// Read-only original Git objects supply real product range conversion. The
// source tree must equal every frozen archive file, including executable modes.
// This private test mirror is removed after proof; it never enters worker input.
func acceptanceCorpusPrepareGit(ctx context.Context, c nativeAcceptanceConfig) (data string, err error) {
	raw, err := acceptanceRead(filepath.Join(c.root(), "source-git.tar"), acceptanceCorpusGitMax)
	if err != nil || acceptanceDigest(raw) != c.SourceGitSHA256 {
		return "", errors.New("source Git input identity")
	}
	files, err := acceptanceCorpusGitFiles(raw)
	if err != nil {
		return "", err
	}
	data = filepath.Join(c.caseRoot(*acceptanceCase), "source-data")
	if err = os.Mkdir(data, 0700); err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(data))
		}
	}()
	repo := filepath.Join(data, "repos", c.Source.Repository+".git")
	if err = os.MkdirAll(filepath.Join(repo, "objects/pack"), 0700); err != nil {
		return data, err
	}
	if err = os.Mkdir(filepath.Join(repo, "refs"), 0700); err != nil {
		return data, err
	}
	for name, body := range files {
		f, e := os.OpenFile(filepath.Join(repo, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
		if e != nil {
			return data, e
		}
		_, e = f.Write(body)
		if e = errors.Join(e, f.Close()); e != nil {
			return data, e
		}
	}
	archive, err := acceptanceRead(filepath.Join(c.root(), "bundle/tools/corpus/remote-apis-sdks.tar.gz"), 249496)
	if err != nil {
		return data, err
	}
	expected, err := acceptanceCorpusFiles(ctx, archive)
	if err != nil {
		return data, err
	}
	if err = acceptanceCorpusVerifyGit(ctx, repo, expected); err != nil {
		return data, err
	}
	return data, nil
}

func acceptanceCorpusPublication(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal, p *typedworkspace.Publication, m typedindex.AttemptManifest) (_ *acceptanceCorpusResult, err error) {
	if !m.Complete || len(m.Members) == 0 || len(m.Generated) != 0 {
		return nil, errors.New("corpus publication incomplete or widened")
	}
	r := &acceptanceCorpusResult{Cohort: c.Cohort, Commit: c.Source.Commit, ArchiveSHA256: acceptanceCorpusArchive, OracleSHA256: acceptanceCorpusOracle, RootDigest: acceptanceDigest(acceptanceJSON(p.Root())), PlanDigest: p.Root().Binding.PlanDigest, MemberDigests: []string{}, Symbols: []acceptanceCorpusSymbolResult{}, CrossCohort: "pending", CostGate: "unavailable", CostMissing: []string{"sampled_child_lifetimes", "sampled_fd_counts", "private_cache_inventory"}}
	documents := map[string]*scip.Document{}
	for _, member := range m.Members {
		b, e := p.ReadMember(ctx, member.Name)
		if e != nil {
			return nil, e
		}
		var index scip.Index
		if proto.Unmarshal(b, &index) != nil {
			return nil, errors.New("corpus member SCIP")
		}
		r.MemberDigests = append(r.MemberDigests, acceptanceDigest(b))
		for _, d := range index.Documents {
			if d == nil || documents[d.RelativePath] != nil || !strings.HasPrefix(d.RelativePath, "go/") {
				return nil, errors.New("corpus member document")
			}
			documents[d.RelativePath] = d
			r.Documents++
			r.Occurrences += len(d.Occurrences)
			for _, occurrence := range d.Occurrences {
				if occurrence != nil && occurrence.Symbol != "" && occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
					r.Definitions++
				}
			}
		}
	}
	slices.Sort(r.MemberDigests)
	want := map[string][3]int{"ordinary": {3, 535, 148}, "proto": {1, 1309, 241}, "fanout": {13, 13278, 2350}}[c.Cohort]
	if [3]int{r.Documents, r.Occurrences, r.Definitions} != want {
		return nil, errors.New("corpus retained counts differ from frozen gate")
	}
	data, err := acceptanceCorpusPrepareGit(ctx, c)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup := os.RemoveAll(data)
		r.SourceGitDrained = cleanup == nil
		err = errors.Join(err, cleanup)
	}()
	resolver := &acceptanceCorpusResolver{s, filepath.Join(c.caseRoot(*acceptanceCase), "workspace"), c}
	service := codenav.New(codenav.Options{DataDir: data, RoutedResolver: resolver})
	defer func() { err = errors.Join(err, service.Remove(c.Source.Repository)) }()
	for _, symbol := range acceptanceCorpusSymbols {
		points := symbol.References
		if c.Cohort == symbol.Definition.Cohort {
			points = []acceptanceOraclePoint{symbol.Definition}
		} else if c.Cohort != "fanout" {
			continue
		}
		proof := acceptanceCorpusSymbolResult{Name: symbol.Name}
		for _, point := range points {
			rawSymbol := acceptanceCorpusPointSymbol(documents[point.Path], point, c.Cohort != "fanout")
			if rawSymbol == "" || proof.SymbolSHA256 != "" && proof.SymbolSHA256 != acceptanceDigest([]byte(rawSymbol)) {
				return nil, errors.New("corpus frozen source point symbol")
			}
			proof.SymbolSHA256 = acceptanceDigest([]byte(rawSymbol))
			query := codenav.Query{Repo: c.Source.Repository, Revision: c.Source.Commit, Path: point.Path, Line: point.Range[0], Character: point.Range[1], Encoding: codenav.EncodingUTF8}
			definition, e := service.Definition(ctx, query)
			if e != nil || !definition.Available || definition.Symbol != rawSymbol {
				return nil, errors.Join(e, errors.New("corpus Definition oracle"))
			}
			hover, e := service.Hover(ctx, query)
			if e != nil || !hover.Available {
				return nil, errors.Join(e, errors.New("corpus Hover availability"))
			}
			refs, e := service.References(ctx, query)
			if e != nil || !refs.Available || refs.Symbol != rawSymbol || refs.Truncated {
				return nil, errors.Join(e, errors.New("corpus References oracle"))
			}
			if c.Cohort == "fanout" {
				if definition.Location != nil || hover.Hover != nil || !acceptanceCorpusReferences(c, symbol.References, refs.Locations) {
					return nil, errors.New("corpus scoped reference set or explicit cross-cohort gap")
				}
				proof.ReferencePoints++
			} else {
				if !acceptanceCorpusLocation(c, symbol.Definition, definition.Location) || hover.Hover == nil || hover.Hover.Symbol != rawSymbol || hover.Hover.Range != acceptanceCorpusRange(point) || hover.Hover.Encoding != codenav.EncodingUTF8 || hover.Hover.Signature != symbol.Signature || !strings.Contains(strings.Join(strings.Fields(strings.Join(hover.Hover.Documentation, " ")), " "), symbol.Documentation) {
					return nil, errors.New("corpus definition range/signature/documentation")
				}
				proof.DefinitionLocations++
				proof.HoverPayloads++
			}
			proof.QueryPoints++
		}
		r.Symbols = append(r.Symbols, proof)
	}
	r.NavigationVerified = true
	return r, nil
}

func acceptanceCorpusPointSymbol(d *scip.Document, p acceptanceOraclePoint, definition bool) string {
	if d == nil {
		return ""
	}
	match := ""
	for _, occurrence := range d.Occurrences {
		if occurrence == nil || occurrence.Symbol == "" || (occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) != 0) != definition {
			continue
		}
		span, ok := occurrence.SourceRange()
		if ok && span.Start.Line == p.Range[0] && span.Start.Character == p.Range[1] && span.End.Line == p.Range[0] && span.End.Character == p.Range[2] {
			if match != "" {
				return ""
			}
			match = occurrence.Symbol
		}
	}
	return match
}
func acceptanceCorpusReferences(c nativeAcceptanceConfig, expected []acceptanceOraclePoint, actual []codenav.Location) bool {
	points := map[acceptanceOraclePoint]bool{}
	for _, location := range actual {
		if location.Path != "go/pkg/rexec/rexec.go" {
			continue
		}
		matched := false
		for _, point := range expected {
			if acceptanceCorpusLocation(c, point, &location) && !points[point] {
				points[point], matched = true, true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return len(points) == len(expected)
}
