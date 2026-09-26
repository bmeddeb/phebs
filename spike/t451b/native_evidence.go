package t451b

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/spike/t451a"
	"github.com/bmeddeb/phebs/spike/t451a/launcher"
	"github.com/bmeddeb/phebs/spike/t451a/planner"
	"github.com/bmeddeb/phebs/spike/t451a/sandbox"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

type nativeFlat struct {
	Roots    []string
	Packages []struct {
		ID, PkgPath     string
		CompiledGoFiles []string
	}
}

type MemberDocument struct {
	PackageID      string `json:"package_id"`
	DocumentID     string `json:"document_id"`
	RawPath        string `json:"raw_path"`
	ProposedMember string `json:"proposed_member"`
}

type SymbolObservation struct {
	Path       string `json:"path"`
	Symbol     string `json:"symbol"`
	Line       int32  `json:"line"`
	Start      int32  `json:"start"`
	End        int32  `json:"end"`
	Definition bool   `json:"definition"`
	Hover      string `json:"hover"`
}

type NativeSCIPFacts struct {
	Counts                      SCIPFacts           `json:"counts"`
	Coverage                    NativeCoverage      `json:"coverage"`
	Members                     []MemberDocument    `json:"members"`
	PublicSymbols               []SymbolObservation `json:"public_symbols"`
	PublicOracleComplete        bool                `json:"public_oracle_complete"`
	CurrentAdmissionEstablished bool                `json:"current_admission_established"`
}

type NativeCoverage struct {
	ConfiguredTargets       int `json:"configured_targets"`
	ConfiguredUnits         int `json:"configured_units"`
	ToolUnits               int `json:"tool_units"`
	RootPackages            int `json:"root_packages"`
	RequiredNonToolPackages int `json:"required_non_tool_packages"`
	SDKPackages             int `json:"sdk_packages"`
	RootDocuments           int `json:"root_documents"`
	MemberEdges             int `json:"member_edges"`
	ExcludedControls        int `json:"excluded_controls"`
}

type NativeTiming struct {
	Stage       string `json:"stage"`
	Nanoseconds int64  `json:"nanoseconds"`
}

// NativeFailedLeg retains diagnostic identities, never raw failed output. A
// completed protocol call does not establish successful typing or indexing.
type NativeFailedLeg struct {
	Slot            string        `json:"slot"`
	Point           string        `json:"point"`
	ClientError     bool          `json:"client_error"`
	Protocol        string        `json:"protocol"`
	WallNanoseconds int64         `json:"wall_nanoseconds"`
	StdoutBytes     int           `json:"stdout_bytes"`
	StdoutSHA256    string        `json:"stdout_sha256"`
	StderrBytes     int           `json:"stderr_bytes"`
	StderrSHA256    string        `json:"stderr_sha256"`
	Call            *CallEvidence `json:"call,omitempty"`
}

type NativeEvidence struct {
	Version               string                  `json:"version"`
	Request               NativeRequest           `json:"request"`
	Decision              string                  `json:"decision"`
	Stage                 string                  `json:"stage"`
	WallNanoseconds       int64                   `json:"wall_nanoseconds"`
	Timings               []NativeTiming          `json:"timings"`
	Plan                  *planner.Plan           `json:"plan,omitempty"`
	GoFiles               *launcher.NativeGoFiles `json:"go_files,omitempty"`
	CompilerCacheEviction t451a.CacheEviction     `json:"compiler_cache_eviction"`
	Tools                 []ToolIdentity          `json:"tools"`
	SelectedSDK           *ToolIdentity           `json:"selected_sdk,omitempty"`
	Materialization       Materialization         `json:"materialization"`
	Observations          Observations            `json:"observations"`
	Cache                 PrivateCacheObservation `json:"cache"`
	Legs                  []LegEvidence           `json:"legs"`
	FailedLeg             *NativeFailedLeg        `json:"failed_leg,omitempty"`
	SCIP                  []byte                  `json:"scip"`
	SCIPSHA256            string                  `json:"scip_sha256"`
	Oracle                *NativeSCIPFacts        `json:"oracle,omitempty"`
	OmittedEvidenceBytes  int                     `json:"omitted_evidence_bytes,omitempty"`
	OmittedEvidenceSHA256 string                  `json:"omitted_evidence_sha256,omitempty"`
}

func nativeSCIPArguments(cohort string, patterns []string) []string {
	module, version, remote := PublicModule, PublicCommit, "https://github.com/bazelbuild/remote-apis-sdks"
	if cohort == "neutral" {
		module, version, remote = ModulePath, ModuleVersion, "https://example.test/phebs-neutral"
	}
	return append([]string{"index", "--module-root=" + launcher.Workspace, "--module-path=" + module, "--module-version=" + version, "--repository-remote=" + remote, "--go-version=go1.25.0", "--skip-tests", "--skip-implementations", "--output=/scratch/t451b-native-index.scip"}, patterns...)
}

func verifyNativeProbe(data, response []byte) error {
	report, err := decode[loadReport](data, maxClientBytes, false)
	if err != nil {
		return err
	}
	var flat nativeFlat
	if err = json.Unmarshal(response, &flat); err != nil {
		return err
	}
	if report.Mode != launcher.CompatibilityMode || len(report.Roots) != len(flat.Roots) || len(report.Roots) == 0 || len(report.Packages) != len(flat.Packages) || len(flat.Packages) > 8192 {
		return errors.New("native probe closure mismatch")
	}
	want := map[string]loadFact{}
	roots := map[string]bool{}
	for _, id := range flat.Roots {
		if roots[id] {
			return errors.New("native duplicate root")
		}
		roots[id] = true
	}
	for _, p := range flat.Packages {
		if _, ok := want[p.ID]; ok {
			return errors.New("native duplicate package")
		}
		want[p.ID] = loadFact{ID: p.ID, Path: p.PkgPath, Syntax: len(p.CompiledGoFiles)}
	}
	observed := map[string]loadFact{}
	previous := ""
	for _, p := range report.Packages {
		w, ok := want[p.ID]
		if !ok || p.ID <= previous || p.Path != w.Path || !p.Types || p.Errors != 0 || p.IllTyped || p.Syntax < 0 || p.Syntax > planner.MaxDocuments || roots[p.ID] && (!p.TypeInfo || p.Syntax != w.Syntax || p.Syntax == 0) {
			return errors.New("native typed package incomplete")
		}
		previous = p.ID
		observed[p.ID] = p
	}
	previous = ""
	for _, p := range report.Roots {
		if !roots[p.ID] || p.ID <= previous || observed[p.ID] != p || !p.TypeInfo || p.Syntax == 0 {
			return errors.New("native typed root mismatch")
		}
		previous = p.ID
	}
	return nil
}

func rootMembers(plan planner.Plan, response []byte, cohort string) ([]MemberDocument, error) {
	var flat nativeFlat
	if err := json.Unmarshal(response, &flat); err != nil {
		return nil, err
	}
	docs := map[string]string{}
	for _, d := range plan.Documents {
		base := launcher.ExecRoot
		switch d.Kind {
		case "source":
			base = launcher.Workspace
		case "external":
			base = launcher.OutputBase
		case "generated":
		default:
			return nil, errors.New("native member source kind")
		}
		name := base + "/" + d.ExecPath
		if old, ok := docs[name]; ok && old != d.ID {
			return nil, errors.New("ambiguous member document")
		}
		docs[name] = d.ID
	}
	roots := map[string]bool{}
	for _, id := range flat.Roots {
		roots[id] = true
	}
	var out []MemberDocument
	for _, p := range flat.Packages {
		if !roots[p.ID] {
			continue
		}
		for _, name := range p.CompiledGoFiles {
			id, ok := docs[name]
			if !ok {
				return nil, errors.New("root compiled file outside plan documents")
			}
			rel, err := filepath.Rel(launcher.Workspace, name)
			if err != nil {
				return nil, err
			}
			out = append(out, MemberDocument{p.ID, id, filepath.ToSlash(rel), cohort})
		}
	}
	slices.SortFunc(out, func(a, b MemberDocument) int {
		if n := strings.Compare(a.PackageID, b.PackageID); n != 0 {
			return n
		}
		return strings.Compare(a.DocumentID, b.DocumentID)
	})
	if len(out) == 0 || len(out) > planner.MaxDocuments {
		return nil, errors.New("native member count")
	}
	return out, nil
}

func verifyNativeSCIP(data []byte, plan planner.Plan, response []byte, cohort string, patterns []string) (NativeSCIPFacts, error) {
	var facts NativeSCIPFacts
	if len(data) == 0 || len(data) > maxClientBytes {
		return facts, errors.New("native SCIP byte bound")
	}
	var index scip.Index
	if err := proto.Unmarshal(data, &index); err != nil {
		return facts, err
	}
	m := index.Metadata
	if m == nil || m.ProjectRoot != "file:///scratch/workspace" || m.TextDocumentEncoding != scip.TextEncoding_UTF8 || m.ToolInfo == nil || m.ToolInfo.Name != "scip-go" || m.ToolInfo.Version != "0.2.7" || !slices.Equal(m.ToolInfo.Arguments, nativeSCIPArguments(cohort, patterns)) {
		return facts, errors.New("native SCIP metadata mismatch")
	}
	members, err := rootMembers(plan, response, cohort)
	if err != nil {
		return facts, err
	}
	facts.Members = members
	want := map[string]bool{}
	for _, member := range members {
		want[member.RawPath] = true
	}
	if len(index.Documents) != len(want) || len(index.ExternalSymbols) > 8192 {
		return facts, errors.New("native SCIP exact root documents mismatch")
	}
	seen := map[string]bool{}
	for _, doc := range index.Documents {
		if doc == nil || doc.Language != "go" || !want[doc.RelativePath] || seen[doc.RelativePath] || len(doc.Occurrences) > 65536 || len(doc.Symbols) > 65536 {
			return facts, errors.New("native SCIP document identity/count")
		}
		seen[doc.RelativePath] = true
		facts.Counts.Documents++
		facts.Counts.Occurrences += len(doc.Occurrences)
		facts.Counts.Symbols += len(doc.Symbols)
		if facts.Counts.Occurrences > 65536 || facts.Counts.Symbols > 65536 {
			return facts, errors.New("native SCIP aggregate count")
		}
		for _, o := range doc.Occurrences {
			if o == nil {
				return facts, errors.New("nil native occurrence")
			}
			r, ok := o.SourceRange()
			if !ok || r.Start.Line < 0 || r.Start.Character < 0 || r.End.Line < r.Start.Line || r.End.Character < 0 || r.Start.Line == r.End.Line && r.End.Character <= r.Start.Character {
				return facts, errors.New("native SCIP occurrence range")
			}
			if cohort != "neutral" {
				collectPublicSymbol(&facts, doc, o, r)
			}
		}
		for _, s := range doc.Symbols {
			if s == nil {
				return facts, errors.New("nil native symbol")
			}
		}
	}
	facts.Counts.ExternalSymbols = len(index.ExternalSymbols)
	var flat nativeFlat
	if err = json.Unmarshal(response, &flat); err != nil {
		return facts, err
	}
	facts.Coverage = NativeCoverage{ConfiguredTargets: len(plan.Targets), ConfiguredUnits: len(plan.Units), RootPackages: len(flat.Roots), RootDocuments: len(want), MemberEdges: len(members), ExcludedControls: 1}
	for _, unit := range plan.Units {
		if unit.Tool {
			facts.Coverage.ToolUnits++
		}
	}
	sdkIDs := map[string]bool{}
	for _, sdk := range plan.SDKs {
		for _, pkg := range sdk.Packages {
			sdkIDs[pkg.ID] = true
		}
	}
	for _, pkg := range flat.Packages {
		if sdkIDs[pkg.ID] {
			facts.Coverage.SDKPackages++
		} else {
			facts.Coverage.RequiredNonToolPackages++
		}
	}
	for _, s := range index.ExternalSymbols {
		if s == nil {
			return facts, errors.New("nil external native symbol")
		}
	}
	if cohort == "neutral" {
		// Reuse the independently frozen semantic oracle over an owned view of
		// its original two documents. The raw full index remains untouched.
		view := proto.Clone(&index).(*scip.Index)
		view.Documents = nil
		view.Metadata.ToolInfo.Arguments = scipArguments([]string{"example.test/neutral/lib"})
		for _, d := range index.Documents {
			if d.RelativePath == "lib/a.go" || d.RelativePath == "lib/lib.go" {
				view.Documents = append(view.Documents, d)
			}
		}
		b, err := proto.Marshal(view)
		if err != nil {
			return facts, err
		}
		neutral, err := VerifySCIP(b)
		if err != nil {
			return facts, err
		}
		facts.Counts.Definitions, facts.Counts.Hovers, facts.Counts.CrossFileReferences, facts.Counts.ExternalReferences = neutral.Definitions, neutral.Hovers, neutral.CrossFileReferences, neutral.ExternalReferences
	}
	return facts, nil
}

func collectPublicSymbol(facts *NativeSCIPFacts, doc *scip.Document, o *scip.Occurrence, r scip.Range) {
	if len(facts.PublicSymbols) >= 32 {
		return
	}
	s, err := scip.ParseSymbol(o.Symbol)
	if err != nil || s.Scheme != "scip-go" || s.Package == nil || s.Package.Manager != "gomod" || s.Package.Name != PublicModule || s.Package.Version != PublicCommit || len(s.Descriptors) != 2 {
		return
	}
	pkg, name := s.Descriptors[0].Name, s.Descriptors[1].Name
	wanted := pkg == PublicModule+"/go/pkg/outerr" && (name == "NewOutWriter" || name == "NewErrWriter") || pkg == PublicModule+"/go/pkg/command" && name == "NewRemoteErrorResult" || pkg == PublicModule+"/go/api/command" && name == "Command"
	if !wanted {
		return
	}
	hover := ""
	for _, info := range doc.Symbols {
		if info != nil && info.Symbol == o.Symbol && info.SignatureDocumentation != nil {
			hover = info.SignatureDocumentation.Text
		}
	}
	if len(hover) > 4096 {
		hover = hover[:4096]
	}
	facts.PublicSymbols = append(facts.PublicSymbols, SymbolObservation{doc.RelativePath, o.Symbol, r.Start.Line, r.Start.Character, r.End.Character, scip.SymbolRole_Definition.Matches(o), hover})
}

func nativeStage(stage string) bool {
	return slices.Contains([]string{"profile", "planning", "package-load/typecheck", "indexer-unlocalized", "validation", "containment/measurement", "complete"}, stage)
}

// DecodeNativeEvidence accepts bounded partial STOP evidence. Completeness is
// required only for COHORT_OBSERVED, which itself is not a feasibility GO.
func DecodeNativeEvidence(data []byte) (NativeEvidence, error) {
	e, err := decode[NativeEvidence](data, sandbox.OutputBytes, false)
	if err != nil {
		return e, err
	}
	if _, err = DecodeNativeRequest(nativeRequestBytes(e.Request)); err != nil {
		return e, err
	}
	if e.Version != "phebs-t451b-native-evidence-v1" || !nativeStage(e.Stage) || e.WallNanoseconds <= 0 || !slices.Contains([]string{"STOP", "COHORT_OBSERVED"}, e.Decision) || len(e.Legs) > 2 || len(e.SCIP) > maxClientBytes || len(e.SCIP) > 0 && e.SCIPSHA256 != t451a.Digest(e.SCIP) {
		return e, errors.New("native evidence shape")
	}
	if e.OmittedEvidenceBytes != 0 || e.OmittedEvidenceSHA256 != "" {
		if e.Decision != "STOP" || e.OmittedEvidenceBytes < sandbox.OutputBytes || !digest(e.OmittedEvidenceSHA256) || e.Plan != nil {
			return e, errors.New("native omitted evidence identity")
		}
	}
	var elapsed int64
	if len(e.Timings) > 16 {
		return e, errors.New("native stage count")
	}
	for _, timing := range e.Timings {
		if !nativeStage(timing.Stage) || timing.Stage == "complete" || timing.Nanoseconds <= 0 || timing.Nanoseconds > e.WallNanoseconds-elapsed {
			return e, errors.New("native stage timing")
		}
		elapsed += timing.Nanoseconds
	}
	if len(e.Tools) > 0 {
		if err = validateToolProfiles(nativeToolProfiles(e.Request), e.Tools); err != nil {
			return e, err
		}
	}
	if e.FailedLeg != nil {
		if err = verifyNativeFailedLeg(e); err != nil {
			return e, err
		}
	}
	if e.Plan == nil {
		if e.GoFiles != nil || e.SelectedSDK != nil || len(e.Legs) != 0 || e.Oracle != nil || len(e.SCIP) != 0 {
			return e, errors.New("native evidence without plan")
		}
	} else {
		roots, rootErr := nativeRoots(*e.Plan, e.Request.Cohort)
		if rootErr != nil {
			return e, rootErr
		}
		if e.GoFiles != nil {
			if _, err = launcher.PrepareNativeCompatibility(*e.Plan, roots, "load", *e.GoFiles); err != nil {
				return e, err
			}
		}
		for i, leg := range e.Legs {
			slot := []string{"load", "scip"}[i]
			if e.GoFiles == nil || leg.Slot != slot || leg.WallNanoseconds <= 0 || len(leg.Stdout)+len(leg.Stderr) > maxClientBytes {
				return e, errors.New("native leg shape")
			}
			if err = verifyNativeCall(*e.Plan, roots, *e.GoFiles, e.Request.Cohort, slot, leg.Call); err != nil {
				return e, err
			}
			executable, args := NativeProbePath, leg.Call.Launcher.Arguments
			if slot == "scip" {
				executable, args = SCIPPath, nativeSCIPArguments(e.Request.Cohort, args)
			}
			if !slices.Equal(leg.ClientArgv, append([]string{executable}, args...)) || !slices.Equal(leg.Environment, leg.Call.Environment) {
				return e, errors.New("native client invocation")
			}
			if slot == "load" {
				if err = verifyNativeProbe(leg.Stdout, leg.Call.Result.Response); err != nil {
					return e, err
				}
			}
		}
		if e.Oracle != nil {
			if len(e.Legs) != 2 {
				return e, errors.New("native oracle before completed clients")
			}
			got, err := verifyNativeSCIP(e.SCIP, *e.Plan, e.Legs[1].Call.Result.Response, e.Request.Cohort, e.Legs[1].Call.Launcher.Arguments)
			if err != nil {
				return e, err
			}
			if !reflect.DeepEqual(got, *e.Oracle) {
				return e, errors.New("native oracle facts mismatch")
			}
		}
	}
	if e.Decision == "COHORT_OBSERVED" {
		if e.Stage != "complete" || len(e.Timings) == 0 || e.Plan == nil || e.GoFiles == nil || len(e.Tools) == 0 || e.SelectedSDK == nil || len(e.Legs) != 2 || e.Oracle == nil || !e.Materialization.OriginalsUnchanged || !e.Materialization.ExcludedControlAbsent || e.Materialization.ArchiveSHA256 != PublicArchiveDigest || e.Materialization.AspectSHA256 != e.Request.AspectSHA256 || !digest(e.Materialization.SourceSHA256) || !digest(e.Materialization.OwnedSHA256) || e.Observations.Unavailable || e.Observations.Samples == 0 || !e.Cache.Complete {
			return e, errors.New("native complete evidence missing")
		}
		if err = validateSelectedSDK(*e.Plan, *e.SelectedSDK); err != nil {
			return e, err
		}
		if err = validateNativeMeasurements(e); err != nil {
			return e, err
		}
		if e.Request.Cohort == "neutral" {
			if !digest(e.Materialization.PlanningLockSHA256) {
				return e, errors.New("native neutral planning lock seal absent")
			}
			if err = validateNativeNeutral(*e.Plan, *e.GoFiles); err != nil {
				return e, err
			}
		} else if e.Materialization.PlanningLockSHA256 != "" {
			return e, errors.New("public planning lock cannot refresh")
		}
		if e.Request.Cohort == "neutral" && (e.Materialization.SourceFiles != 0 || e.Materialization.SourceBytes != 0) || e.Request.Cohort != "neutral" && (e.Materialization.SourceFiles != 128 || e.Materialization.SourceBytes != 1079184) {
			return e, errors.New("native original inventory mismatch")
		}
	}
	return e, nil
}

func verifyNativeFailedLeg(e NativeEvidence) error {
	f := e.FailedLeg
	if e.Decision != "STOP" || f == nil || f.WallNanoseconds <= 0 || f.WallNanoseconds > e.WallNanoseconds || f.StdoutBytes < 0 || f.StderrBytes < 0 || f.StdoutBytes > maxClientBytes || f.StderrBytes > maxClientBytes-f.StdoutBytes || !digest(f.StdoutSHA256) || !digest(f.StderrSHA256) || f.StdoutBytes == 0 && f.StdoutSHA256 != t451a.Digest(nil) || f.StderrBytes == 0 && f.StderrSHA256 != t451a.Digest(nil) || !slices.Contains([]string{"client", "protocol", "files", "probe"}, f.Point) || (f.Point == "client") != f.ClientError || f.Point == "probe" && f.Slot != "load" {
		return errors.New("native failed-leg identity")
	}
	if _, _, err := nativeSlotPaths(f.Slot); err != nil {
		return err
	}
	stage := "package-load/typecheck"
	if f.Slot == "scip" {
		stage = "indexer-unlocalized"
	}
	if e.Stage != stage {
		return errors.New("native failed leg stage mismatch")
	}
	if e.OmittedEvidenceBytes > 0 {
		if f.Protocol != "unproven" || f.Call != nil {
			return errors.New("omitted native protocol cannot be substantiated")
		}
		return nil
	}
	if e.Plan == nil || e.GoFiles == nil || len(e.Legs) > 1 || f.Slot != []string{"load", "scip"}[len(e.Legs)] {
		return errors.New("native failed leg ordering")
	}
	switch f.Protocol {
	case "unproven":
		if f.Call != nil || f.Point != "client" && f.Point != "protocol" {
			return errors.New("native unproven protocol shape")
		}
	case "completed":
		if f.Call == nil || f.Point == "protocol" {
			return errors.New("native completed protocol shape")
		}
		roots, err := nativeRoots(*e.Plan, e.Request.Cohort)
		if err != nil {
			return err
		}
		if err = verifyNativeCall(*e.Plan, roots, *e.GoFiles, e.Request.Cohort, f.Slot, *f.Call); err != nil {
			return err
		}
	default:
		return errors.New("unknown native protocol evidence")
	}
	return nil
}

func validateNativeMeasurements(e NativeEvidence) error {
	o, c := e.Observations, e.Cache
	if o.Version != "phebs-t451b-sampled-observations-v1" || o.IntervalNanoseconds != observationInterval.Nanoseconds() || o.DurationNanoseconds <= 0 || o.DurationNanoseconds > e.WallNanoseconds || !o.ChildLifetimesLowerBound || !o.FDCountsNonAtomic || o.Unavailable || o.UnexpectedErrors != 0 || o.Failure != "" || o.SampledChildLifetimes > maxObservedLifetimes || o.SampledProcessFDPeak > sandbox.DescriptorLimit || o.SampledAggregateFDPeak > sandbox.DescriptorLimit*sandbox.TaskLimit {
		return errors.New("native observation contract")
	}
	roots := []string{"/scratch/bazel-user", "/scratch/bazel-output", "/scratch/repository-cache", "/scratch/gocache", "/scratch/gomodcache", "/scratch/cache"}
	if c.Version != "phebs-t451b-private-cache-v1" || !c.Complete || !slices.Equal(c.Roots, roots) || c.Entries > sandbox.ScratchInodes || c.LogicalBytes > sandbox.ScratchBytes || c.AllocatedBytes > sandbox.ScratchBytes || c.RegularFiles > c.Entries || c.Directories > c.Entries-c.RegularFiles || c.Symlinks != c.Entries-c.RegularFiles-c.Directories || c.UniqueInodes > c.Entries {
		return errors.New("native private cache contract")
	}
	missing := map[string]bool{}
	for _, name := range c.MissingRoots {
		if !slices.Contains(roots, name) || missing[name] {
			return errors.New("native missing cache root")
		}
		missing[name] = true
	}
	return nil
}
