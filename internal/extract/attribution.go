package extract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/resolverinput"
)

const (
	layoutSnapshotPath        = resolverinput.LayoutSnapshotPath
	unitSnapshotPath          = "unit-snapshot.json"
	generatedFromSnapshotPath = resolverinput.GeneratedFromSnapshotPath

	layoutSnapshotVersion        = resolverinput.LayoutSnapshotVersion
	unitSnapshotVersion          = "t20-unit-snapshot-v1"
	generatedFromSnapshotVersion = resolverinput.GeneratedFromSnapshotVersion

	maxAttributionRoots       = 128
	maxAttributionMappings    = 25_000
	maxAttributionValues      = 64
	maxAttributionValueBytes  = 4 << 10
	maxAttributionSourceLine  = 10_000_000
	attributionDigestDomain   = "phebs-attribution-source-v1\x00"
	attributionIdentityDomain = "phebs-attribution-candidate-v1\x00"
)

var attributionSnapshotPaths = []string{
	layoutSnapshotPath,
	unitSnapshotPath,
	generatedFromSnapshotPath,
}

type unitSnapshotInput struct {
	Version  string             `json:"version"`
	Mappings []unitMappingInput `json:"mappings"`
}

type unitMappingInput struct {
	CallID          string   `json:"call_id,omitempty"`
	SourcePath      string   `json:"source_path"`
	StartLine       int      `json:"start_line,omitempty"`
	BuildTargets    []string `json:"build_targets,omitempty"`
	Deployables     []string `json:"deployables,omitempty"`
	LogicalServices []string `json:"logical_services,omitempty"`
	Owners          []string `json:"owners,omitempty"`
	State           string   `json:"state,omitempty"`
	Source          string   `json:"source,omitempty"`
}

type attributionRoot struct {
	kind     string
	path     string
	protocol string
}

type unitRecord struct {
	line      int
	candidate sdk.ConsumerUnitCandidate
}

type invocationRecord struct {
	protocol        string
	generatedRoot   string
	declarationRoot string
}

type monorepoAttributionSource struct {
	available   bool
	digest      string
	repository  string
	provenance  []sdk.SnapshotProvenance
	roots       []attributionRoot
	units       map[string][]unitRecord
	generated   map[string][]sdk.GeneratedFromCandidate
	invocations []invocationRecord
	// planned is the bounded repository-wide candidate view for this domain,
	// not a retained copy of the complete Git tree. Dynamic invocation lookup
	// is limited to declaration candidates already admitted by that view;
	// snapshot validation uses the exact immutable-tree probes below.
	planned map[string]struct{}
	lookup  *verifiedCorpus

	lookupCtx   context.Context
	lookupMu    sync.Mutex
	lookupCache map[string]bool
	lookupErr   error
}

var (
	_ sdk.AttributionCorpus = (*verifiedCorpus)(nil)
	_ sdk.AttributionSource = (*monorepoAttributionSource)(nil)
)

func loadAttributionSource(ctx context.Context, corpus *verifiedCorpus) (sdk.AttributionSource, error) {
	corpus.mu.Lock()
	if corpus.closed {
		corpus.mu.Unlock()
		return nil, errors.New("corpus used after extractor returned")
	}
	if !corpus.inventoryComplete {
		corpus.mu.Unlock()
		return nil, errors.New("attribution read before complete corpus inventory")
	}
	// Inventory is immutable after completion, so attribution can share the
	// bounded planned-path index instead of duplicating it. In production this
	// is the candidate view, never the complete repository tree.
	planned := corpus.enumerated
	repository, commit := corpus.repo, corpus.commit
	corpus.mu.Unlock()

	source := &monorepoAttributionSource{
		repository:  repository,
		units:       make(map[string][]unitRecord),
		generated:   make(map[string][]sdk.GeneratedFromCandidate),
		planned:     planned,
		lookup:      corpus,
		lookupCtx:   ctx,
		lookupCache: make(map[string]bool),
	}
	type selectedBlob struct {
		path, digest, content string
	}
	var selected []selectedBlob
	for _, filePath := range attributionSnapshotPaths {
		present, err := corpus.containsRegular(ctx, filePath)
		if err != nil {
			return nil, fmt.Errorf("inspect attribution snapshot %q: %w", filePath, err)
		}
		if !present {
			continue
		}
		blob, err := corpus.Read(ctx, filePath)
		if err != nil {
			return nil, fmt.Errorf("read attribution snapshot %q: %w", filePath, err)
		}
		selected = append(selected, selectedBlob{
			path: filePath, digest: blob.Digest, content: blob.Content,
		})
		source.provenance = append(source.provenance, sdk.SnapshotProvenance{
			Kind: "repository_revision", Repository: repository, Commit: commit,
			Path: filePath, Digest: blob.Digest,
		})
	}
	if len(selected) == 0 {
		return source, nil
	}
	source.available = true

	for _, blob := range selected {
		switch blob.path {
		case layoutSnapshotPath:
			var input resolverinput.LayoutSnapshot
			if err := resolverinput.DecodeSnapshot(blob.content, &input); err != nil {
				return nil, fmt.Errorf("decode %s: %w", blob.path, err)
			}
			if input.Version != layoutSnapshotVersion {
				return nil, fmt.Errorf("%s has unsupported version %q", blob.path, input.Version)
			}
			if err := source.loadRoots(ctx, input.Roots); err != nil {
				return nil, fmt.Errorf("%s: %w", blob.path, err)
			}
		case unitSnapshotPath:
			var input unitSnapshotInput
			if err := resolverinput.DecodeSnapshot(blob.content, &input); err != nil {
				return nil, fmt.Errorf("decode %s: %w", blob.path, err)
			}
			if input.Version != unitSnapshotVersion {
				return nil, fmt.Errorf("%s has unsupported version %q", blob.path, input.Version)
			}
			if err := source.loadUnits(ctx, input.Mappings); err != nil {
				return nil, fmt.Errorf("%s: %w", blob.path, err)
			}
		case generatedFromSnapshotPath:
			var input resolverinput.GeneratedFromSnapshot
			if err := resolverinput.DecodeSnapshot(blob.content, &input); err != nil {
				return nil, fmt.Errorf("decode %s: %w", blob.path, err)
			}
			if input.Version != generatedFromSnapshotVersion {
				return nil, fmt.Errorf("%s has unsupported version %q", blob.path, input.Version)
			}
			if err := source.loadGeneratedFrom(ctx, input.Mappings, input.Invocations); err != nil {
				return nil, fmt.Errorf("%s: %w", blob.path, err)
			}
		}
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(attributionDigestDomain))
	writeAttributionHash(hash, repository)
	writeAttributionHash(hash, commit)
	for _, blob := range selected {
		writeAttributionHash(hash, blob.path)
		writeAttributionHash(hash, blob.digest)
	}
	source.digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return source, nil
}

type attributionHash interface {
	Write([]byte) (int, error)
}

func writeAttributionHash(hash attributionHash, value string) {
	_, _ = fmt.Fprintf(hash, "%d:", len(value))
	_, _ = hash.Write([]byte(value))
	_, _ = hash.Write([]byte{';'})
}

// declarationLineageID is the same provisional repository/path identity used
// by protodecl and thriftdecl. Generated-from provenance must join the
// declaration evidence key itself; exposing a separate repo:path spelling
// would make an exact Caller Map impossible even when both sides are proven.
func declarationLineageID(repository, declarationPath string) string {
	sum := sha256.Sum256([]byte(repository + "\x00" + declarationPath))
	return "provisional_repo_path_v1_" + hex.EncodeToString(sum[:])
}

func (s *monorepoAttributionSource) loadRoots(
	ctx context.Context,
	inputs []resolverinput.LayoutRoot,
) error {
	if len(inputs) > maxAttributionRoots {
		return fmt.Errorf("more than %d layout roots", maxAttributionRoots)
	}
	roots := make([]attributionRoot, 0, len(inputs))
	for index, input := range inputs {
		if err := checkCorpusPath(input.Path); err != nil {
			return fmt.Errorf("root %d path: %w", index, err)
		}
		if input.Kind != "idl" && input.Kind != "generated" && input.Kind != "source" {
			return fmt.Errorf("root %d has unsupported kind %q", index, input.Kind)
		}
		switch input.Kind {
		case "source":
			if input.Protocol != "" {
				return fmt.Errorf("source root %q must not name a protocol", input.Path)
			}
		default:
			if !validToken(input.Protocol) {
				return fmt.Errorf("%s root %q requires a valid protocol", input.Kind, input.Path)
			}
		}
		matched, err := s.lookup.anyRegularUnder(ctx, input.Path)
		if err != nil {
			return fmt.Errorf("inspect root %q: %w", input.Path, err)
		}
		if !matched {
			return fmt.Errorf("root %q matches no regular corpus file", input.Path)
		}
		roots = append(roots, attributionRoot{
			kind: input.Kind, path: input.Path, protocol: input.Protocol,
		})
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].path != roots[j].path {
			return roots[i].path < roots[j].path
		}
		if roots[i].kind != roots[j].kind {
			return roots[i].kind < roots[j].kind
		}
		return roots[i].protocol < roots[j].protocol
	})
	for left := range roots {
		for right := left + 1; right < len(roots); right++ {
			if rootsOverlap(roots[left].path, roots[right].path) {
				return fmt.Errorf("layout roots %q and %q overlap", roots[left].path, roots[right].path)
			}
		}
	}
	s.roots = roots
	return nil
}

func (s *monorepoAttributionSource) loadUnits(
	ctx context.Context,
	inputs []unitMappingInput,
) error {
	if len(inputs) > maxAttributionMappings {
		return fmt.Errorf("more than %d unit mappings", maxAttributionMappings)
	}
	seenIDs := make(map[string]struct{}, len(inputs))
	seenCallIDs := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		if err := checkCorpusPath(input.SourcePath); err != nil {
			return fmt.Errorf("mapping %d source path: %w", index, err)
		}
		present, err := s.lookup.containsRegular(ctx, input.SourcePath)
		if err != nil {
			return fmt.Errorf(
				"mapping %d inspect source path %q: %w", index, input.SourcePath, err)
		}
		if !present {
			return fmt.Errorf("mapping %d source path %q is absent from the corpus", index, input.SourcePath)
		}
		if len(s.roots) > 0 {
			root, classified := s.classify(input.SourcePath)
			if !classified || root.kind != "source" {
				return fmt.Errorf("mapping %d source path %q is not classified as source",
					index, input.SourcePath)
			}
		}
		if input.StartLine < 0 || input.StartLine > maxAttributionSourceLine {
			return fmt.Errorf("mapping %d start line is outside [0,%d]", index, maxAttributionSourceLine)
		}
		if input.CallID != "" {
			if err := checkAttributionValue(input.CallID); err != nil {
				return fmt.Errorf("mapping %d call id: %w", index, err)
			}
			if _, duplicate := seenCallIDs[input.CallID]; duplicate {
				return fmt.Errorf("mapping %d repeats call id %q", index, input.CallID)
			}
			seenCallIDs[input.CallID] = struct{}{}
		}
		if input.Source != "" && input.Source != "unit-snapshot-v1" {
			return fmt.Errorf("mapping %d has unsupported source %q", index, input.Source)
		}
		var normalizeErr error
		candidate := sdk.ConsumerUnitCandidate{}
		if candidate.BuildTargets, normalizeErr = normalizeAttributionValues(input.BuildTargets); normalizeErr != nil {
			return fmt.Errorf("mapping %d build targets: %w", index, normalizeErr)
		}
		if candidate.Deployables, normalizeErr = normalizeAttributionValues(input.Deployables); normalizeErr != nil {
			return fmt.Errorf("mapping %d deployables: %w", index, normalizeErr)
		}
		if candidate.LogicalServices, normalizeErr = normalizeAttributionValues(input.LogicalServices); normalizeErr != nil {
			return fmt.Errorf("mapping %d logical services: %w", index, normalizeErr)
		}
		if candidate.Owners, normalizeErr = normalizeAttributionValues(input.Owners); normalizeErr != nil {
			return fmt.Errorf("mapping %d owners: %w", index, normalizeErr)
		}
		candidate.ID = attributionCandidateID(
			"unit", input.SourcePath, fmt.Sprintf("%d", input.StartLine),
			strings.Join(candidate.BuildTargets, "\x00"),
			strings.Join(candidate.Deployables, "\x00"),
			strings.Join(candidate.LogicalServices, "\x00"),
			strings.Join(candidate.Owners, "\x00"),
		)
		if _, duplicate := seenIDs[candidate.ID]; duplicate {
			return fmt.Errorf("mapping %d duplicates an earlier unit candidate", index)
		}
		seenIDs[candidate.ID] = struct{}{}
		derivedState := unitCandidateState(candidate)
		if input.State != "" && input.State != derivedState {
			return fmt.Errorf("mapping %d state %q does not match derived %q", index, input.State, derivedState)
		}
		s.units[input.SourcePath] = append(s.units[input.SourcePath], unitRecord{
			line: input.StartLine, candidate: candidate,
		})
	}
	for filePath := range s.units {
		sort.Slice(s.units[filePath], func(i, j int) bool {
			if s.units[filePath][i].line != s.units[filePath][j].line {
				return s.units[filePath][i].line < s.units[filePath][j].line
			}
			return s.units[filePath][i].candidate.ID < s.units[filePath][j].candidate.ID
		})
	}
	return nil
}

func (s *monorepoAttributionSource) loadGeneratedFrom(
	ctx context.Context,
	mappings []resolverinput.GeneratedFromMapping,
	invocations []resolverinput.GeneratorInvocation,
) error {
	if len(mappings) > maxAttributionMappings || len(invocations) > maxAttributionRoots {
		return errors.New("generated-from snapshot exceeds mapping or invocation-root limit")
	}
	seen := make(map[string]struct{}, len(mappings))
	for index, input := range mappings {
		for label, value := range map[string]string{
			"generated path": input.GeneratedPath, "declaration path": input.DeclarationPath,
		} {
			if err := checkCorpusPath(value); err != nil {
				return fmt.Errorf("mapping %d %s: %w", index, label, err)
			}
			present, err := s.lookup.containsRegular(ctx, value)
			if err != nil {
				return fmt.Errorf(
					"mapping %d inspect %s %q: %w", index, label, value, err)
			}
			if !present {
				return fmt.Errorf("mapping %d %s %q is absent from the corpus", index, label, value)
			}
		}
		protocol, err := generatedProtocol(input.Protocol, input.DeclarationPath)
		if err != nil {
			return fmt.Errorf("mapping %d: %w", index, err)
		}
		if len(s.roots) > 0 {
			generatedRoot, generated := s.classify(input.GeneratedPath)
			declarationRoot, declared := s.classify(input.DeclarationPath)
			if !generated || generatedRoot.kind != "generated" ||
				generatedRoot.protocol != protocol {
				return fmt.Errorf("mapping %d generated path %q has no matching generated/%s root",
					index, input.GeneratedPath, protocol)
			}
			if !declared || declarationRoot.kind != "idl" ||
				declarationRoot.protocol != protocol {
				return fmt.Errorf("mapping %d declaration path %q has no matching idl/%s root",
					index, input.DeclarationPath, protocol)
			}
		}
		if input.GeneratorRelativePath != "" {
			if err := checkCorpusPath(input.GeneratorRelativePath); err != nil {
				return fmt.Errorf("mapping %d generator-relative path: %w", index, err)
			}
		}
		if protocol == "grpc" && input.GeneratorRelativePath != "" &&
			!strings.HasSuffix(input.GeneratorRelativePath, ".proto") {
			return fmt.Errorf("mapping %d protobuf generator-relative path is not .proto", index)
		}
		if protocol == "thrift" && input.GeneratorRelativePath != "" {
			return fmt.Errorf("mapping %d Thrift relation cannot use a generator-relative path", index)
		}
		candidate := sdk.GeneratedFromCandidate{
			Protocol: protocol, GeneratedPath: input.GeneratedPath,
			GeneratorRelativePath: input.GeneratorRelativePath,
			DeclarationPath:       input.DeclarationPath,
			DeclarationLineage:    declarationLineageID(s.repository, input.DeclarationPath),
		}
		id := attributionCandidateID(
			"generated-from", protocol, input.GeneratedPath,
			input.GeneratorRelativePath, input.DeclarationPath,
		)
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("mapping %d duplicates an earlier generated-from relation", index)
		}
		seen[id] = struct{}{}
		key := generatedFromKey(protocol, input.GeneratedPath)
		s.generated[key] = append(s.generated[key], candidate)
	}
	for key := range s.generated {
		sortGeneratedCandidates(s.generated[key])
	}

	roots := make([]invocationRecord, 0, len(invocations))
	for index, input := range invocations {
		if input.Protocol != "grpc" {
			return fmt.Errorf("invocation %d protocol %q is unsupported; Thrift requires direct mappings",
				index, input.Protocol)
		}
		for label, value := range map[string]string{
			"generated root":            input.GeneratedRoot,
			"generator invocation root": input.GeneratorInvocationRoot,
		} {
			if err := checkCorpusPath(value); err != nil {
				return fmt.Errorf("invocation %d %s: %w", index, label, err)
			}
			matched, err := s.lookup.anyRegularUnder(ctx, value)
			if err != nil {
				return fmt.Errorf(
					"invocation %d inspect %s %q: %w", index, label, value, err)
			}
			if !matched {
				return fmt.Errorf("invocation %d %s %q matches no regular corpus file",
					index, label, value)
			}
		}
		if len(s.roots) > 0 {
			generatedRoot, generated := s.classify(input.GeneratedRoot)
			declarationRoot, declared := s.classify(input.GeneratorInvocationRoot)
			if !generated || generatedRoot.kind != "generated" ||
				generatedRoot.protocol != input.Protocol {
				return fmt.Errorf("invocation %d generated root %q has no matching generated/%s layout root",
					index, input.GeneratedRoot, input.Protocol)
			}
			if !declared || declarationRoot.kind != "idl" ||
				declarationRoot.protocol != input.Protocol {
				return fmt.Errorf("invocation %d declaration root %q has no matching idl/%s layout root",
					index, input.GeneratorInvocationRoot, input.Protocol)
			}
		}
		roots = append(roots, invocationRecord{
			protocol: input.Protocol, generatedRoot: input.GeneratedRoot,
			declarationRoot: input.GeneratorInvocationRoot,
		})
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].generatedRoot != roots[j].generatedRoot {
			return roots[i].generatedRoot < roots[j].generatedRoot
		}
		return roots[i].declarationRoot < roots[j].declarationRoot
	})
	for left := range roots {
		for right := left + 1; right < len(roots); right++ {
			if rootsOverlap(roots[left].generatedRoot, roots[right].generatedRoot) {
				return fmt.Errorf("generated invocation roots %q and %q overlap",
					roots[left].generatedRoot, roots[right].generatedRoot)
			}
		}
	}
	s.invocations = roots
	return nil
}

func generatedProtocol(declared, declarationPath string) (string, error) {
	derived := ""
	switch {
	case strings.HasSuffix(declarationPath, ".proto"):
		derived = "grpc"
	case strings.HasSuffix(declarationPath, ".thrift"):
		derived = "thrift"
	default:
		return "", fmt.Errorf("declaration path %q has no supported protocol suffix", declarationPath)
	}
	if declared != "" && declared != derived {
		return "", fmt.Errorf("declared protocol %q conflicts with %q", declared, derived)
	}
	return derived, nil
}

func normalizeAttributionValues(values []string) ([]string, error) {
	if len(values) > maxAttributionValues {
		return nil, fmt.Errorf("more than %d values", maxAttributionValues)
	}
	normalized := append([]string(nil), values...)
	sort.Strings(normalized)
	for index, value := range normalized {
		if err := checkAttributionValue(value); err != nil {
			return nil, err
		}
		if index > 0 && normalized[index-1] == value {
			return nil, fmt.Errorf("duplicate value %q", value)
		}
	}
	return normalized, nil
}

func checkAttributionValue(value string) error {
	if value == "" || len(value) > maxAttributionValueBytes || !utf8.ValidString(value) {
		return errors.New("value is not bounded non-empty UTF-8")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return errors.New("value contains a control character")
		}
	}
	return nil
}

func attributionCandidateID(kind string, fields ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(attributionIdentityDomain))
	writeAttributionHash(hash, kind)
	for _, field := range fields {
		writeAttributionHash(hash, field)
	}
	return "au_" + hex.EncodeToString(hash.Sum(nil))
}

func unitCandidateState(candidate sdk.ConsumerUnitCandidate) string {
	total := len(candidate.BuildTargets) + len(candidate.Deployables) +
		len(candidate.LogicalServices) + len(candidate.Owners)
	if total == 0 {
		return sdk.AttributionStateUnavailable
	}
	if len(candidate.BuildTargets) > 1 || len(candidate.Deployables) > 1 ||
		len(candidate.LogicalServices) > 1 || len(candidate.Owners) > 1 {
		return sdk.AttributionStateAmbiguous
	}
	return sdk.AttributionStateResolved
}

func pathWithinRoot(filePath, root string) bool {
	return filePath == root || strings.HasPrefix(filePath, root+"/")
}

func rootsOverlap(left, right string) bool {
	return pathWithinRoot(left, right) || pathWithinRoot(right, left)
}

func generatedFromKey(protocol, generatedPath string) string {
	return protocol + "\x00" + generatedPath
}

func sortGeneratedCandidates(candidates []sdk.GeneratedFromCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].DeclarationPath != candidates[j].DeclarationPath {
			return candidates[i].DeclarationPath < candidates[j].DeclarationPath
		}
		return candidates[i].GeneratorRelativePath < candidates[j].GeneratorRelativePath
	})
}

func (s *monorepoAttributionSource) Available() bool { return s.available }
func (s *monorepoAttributionSource) Digest() string  { return s.digest }

func (s *monorepoAttributionSource) Provenance() []sdk.SnapshotProvenance {
	return append([]sdk.SnapshotProvenance(nil), s.provenance...)
}

func (s *monorepoAttributionSource) ClassifyPath(filePath string) sdk.PathClassification {
	if checkCorpusPath(filePath) != nil {
		return sdk.PathClassification{
			State: sdk.AttributionStateUnavailable, Reason: "invalid_path",
		}
	}
	if root, ok := s.classify(filePath); ok {
		return sdk.PathClassification{
			State: sdk.AttributionStateResolved, Kind: root.kind,
			Root: root.path, Protocol: root.protocol,
		}
	}
	return sdk.PathClassification{
		State: sdk.AttributionStateUnavailable, Reason: "no_layout_root",
	}
}

func (s *monorepoAttributionSource) classify(filePath string) (attributionRoot, bool) {
	for _, root := range s.roots {
		if pathWithinRoot(filePath, root.path) {
			return root, true
		}
	}
	return attributionRoot{}, false
}

func (s *monorepoAttributionSource) ConsumerUnits(
	filePath string,
	line int,
) sdk.ConsumerUnitAttribution {
	if checkCorpusPath(filePath) != nil || line < 1 || line > maxAttributionSourceLine {
		return sdk.ConsumerUnitAttribution{
			State: sdk.AttributionStateUnavailable, Reason: "invalid_source_location",
		}
	}
	records := s.units[filePath]
	selected := make([]unitRecord, 0, len(records))
	for _, record := range records {
		if record.line == line {
			selected = append(selected, record)
		}
	}
	if len(selected) == 0 {
		for _, record := range records {
			if record.line == 0 {
				selected = append(selected, record)
			}
		}
	}
	if len(selected) == 0 {
		return sdk.ConsumerUnitAttribution{
			State: sdk.AttributionStateUnavailable, Reason: "no_unit_mapping",
		}
	}
	candidates := make([]sdk.ConsumerUnitCandidate, 0, len(selected))
	state := sdk.AttributionStateResolved
	for _, record := range selected {
		candidate := cloneUnitCandidate(record.candidate)
		candidates = append(candidates, candidate)
		candidateState := unitCandidateState(candidate)
		if candidateState == sdk.AttributionStateUnavailable && len(selected) == 1 {
			state = sdk.AttributionStateUnavailable
		}
		if candidateState == sdk.AttributionStateAmbiguous || len(selected) > 1 {
			state = sdk.AttributionStateAmbiguous
		}
	}
	reason := ""
	switch state {
	case sdk.AttributionStateUnavailable:
		reason = "mapping_has_no_attribution"
	case sdk.AttributionStateAmbiguous:
		reason = "multiple_unit_candidates"
	}
	return sdk.ConsumerUnitAttribution{
		State: state, Reason: reason, Candidates: candidates,
	}
}

func (s *monorepoAttributionSource) GeneratedFrom(
	protocol, generatedPath, generatorRelativePath string,
) sdk.GeneratedFromAttribution {
	if !validToken(protocol) || checkCorpusPath(generatedPath) != nil ||
		generatorRelativePath != "" && checkCorpusPath(generatorRelativePath) != nil {
		return sdk.GeneratedFromAttribution{
			State: sdk.AttributionStateUnavailable, Reason: "invalid_generated_query",
		}
	}
	var candidates []sdk.GeneratedFromCandidate
	seen := make(map[string]struct{})
	for _, direct := range s.generated[generatedFromKey(protocol, generatedPath)] {
		if direct.GeneratorRelativePath != "" &&
			direct.GeneratorRelativePath != generatorRelativePath {
			continue
		}
		candidate := direct
		if candidate.GeneratorRelativePath == "" {
			candidate.GeneratorRelativePath = generatorRelativePath
		}
		id := candidate.DeclarationLineage + "\x00" + candidate.GeneratorRelativePath
		if _, duplicate := seen[id]; !duplicate {
			seen[id] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	for _, invocation := range s.invocations {
		if invocation.protocol != protocol ||
			!pathWithinRoot(generatedPath, invocation.generatedRoot) ||
			generatorRelativePath == "" ||
			!strings.HasSuffix(generatorRelativePath, ".proto") {
			continue
		}
		declarationPath := path.Join(invocation.declarationRoot, generatorRelativePath)
		if !pathWithinRoot(declarationPath, invocation.declarationRoot) {
			continue
		}
		if !s.dynamicDeclarationPresent(declarationPath) {
			continue
		}
		candidate := sdk.GeneratedFromCandidate{
			Protocol: protocol, GeneratedPath: generatedPath,
			GeneratorRelativePath: generatorRelativePath,
			DeclarationPath:       declarationPath,
			DeclarationLineage:    declarationLineageID(s.repository, declarationPath),
		}
		id := candidate.DeclarationLineage + "\x00" + candidate.GeneratorRelativePath
		if _, duplicate := seen[id]; !duplicate {
			seen[id] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	sortGeneratedCandidates(candidates)
	switch len(candidates) {
	case 0:
		return sdk.GeneratedFromAttribution{
			State: sdk.AttributionStateUnavailable, Reason: "no_snapshot_mapping",
		}
	case 1:
		return sdk.GeneratedFromAttribution{
			State: sdk.AttributionStateResolved, Candidates: cloneGeneratedCandidates(candidates),
		}
	default:
		return sdk.GeneratedFromAttribution{
			State: sdk.AttributionStateAmbiguous, Reason: "multiple_declaration_paths",
			Candidates: cloneGeneratedCandidates(candidates),
		}
	}
}

func (s *monorepoAttributionSource) dynamicDeclarationPresent(
	declarationPath string,
) bool {
	if _, present := s.planned[declarationPath]; present {
		return true
	}
	s.lookupMu.Lock()
	defer s.lookupMu.Unlock()
	if present, cached := s.lookupCache[declarationPath]; cached {
		return present
	}
	if s.lookupErr != nil {
		return false
	}
	present, err := s.lookup.containsRegular(s.lookupCtx, declarationPath)
	if err != nil {
		s.lookupErr = fmt.Errorf(
			"inspect dynamic declaration %q: %w", declarationPath, err)
		return false
	}
	if len(s.lookupCache) >= maxCorpusFiles {
		s.lookupErr = fmt.Errorf(
			"dynamic declaration lookup exceeds %d-path limit", maxCorpusFiles)
		return false
	}
	s.lookupCache[declarationPath] = present
	return present
}

func (s *monorepoAttributionSource) validationError() error {
	s.lookupMu.Lock()
	defer s.lookupMu.Unlock()
	return s.lookupErr
}

func cloneUnitCandidate(candidate sdk.ConsumerUnitCandidate) sdk.ConsumerUnitCandidate {
	candidate.BuildTargets = append([]string(nil), candidate.BuildTargets...)
	candidate.Deployables = append([]string(nil), candidate.Deployables...)
	candidate.LogicalServices = append([]string(nil), candidate.LogicalServices...)
	candidate.Owners = append([]string(nil), candidate.Owners...)
	return candidate
}

func cloneGeneratedCandidates(
	candidates []sdk.GeneratedFromCandidate,
) []sdk.GeneratedFromCandidate {
	return append([]sdk.GeneratedFromCandidate(nil), candidates...)
}
