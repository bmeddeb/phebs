package resolvermaterialize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolverinput"
	"github.com/bmeddeb/phebs/internal/store"
)

const assertionPageSize = 1000

type AssertionReader interface {
	ListAssertions(context.Context, store.AssertionQuery) ([]store.Assertion, error)
}

type BlobReader func(
	context.Context, string, string, int64,
) ([]byte, error)

type DeclarationInput struct {
	Protocol         Protocol
	Domain           string
	RunID            string
	GenerationDigest string
	AuthoritySchema  string
	PlanDigest       string
	RootDigest       string
}

type BuildRequest struct {
	Root          string
	RepositoryDir string
	Identity      resolvercatalog.Identity
	Registry      *Registry
	Manifest      extract.CandidateManifest
	Declarations  []DeclarationInput
	Assertions    AssertionReader
	ReadBlob      BlobReader
}

type inputBlob struct {
	file    extract.CandidateManifestFile
	digest  string
	content string
	present bool
	reason  string
}

type generatedPlan struct {
	layout           *resolverinput.LayoutSnapshot
	generated        *resolverinput.GeneratedFromSnapshot
	layoutInput      inputBlob
	generatedInput   inputBlob
	layoutProblem    string
	generatedProblem string
	generatedPaths   map[string]bool
	generatedFiles   map[string]extract.CandidateManifestFile
	invocationRoots  map[string]bool
	modules          map[string]string
	parsedSymbols    map[string]generatedSymbolParse
	blobs            *blobBudget
}

type generatedSymbolParse struct {
	protocol Protocol
	file     extract.CandidateManifestFile
	digest   string
	symbols  []gocaller.GeneratedSymbol
	state    string
	reason   string
}

type blobBudget struct {
	read  BlobReader
	dir   string
	reads int
	bytes int64
}

// materializationBudget is shared by every adapter in one Build. The frozen
// policy commits these limits as catalog-wide ceilings, so initializing any of
// them inside an adapter member would silently multiply the admitted work when
// more than one protocol is enabled.
type materializationBudget struct {
	declarationRecords           int
	declarationPathBytes         int
	generatedCandidateExpansions int
	generatedCandidateBytes      int
	generatedSymbolDescriptors   int
	generatedSymbolIdentityBytes int
	generatedSourceObjects       map[string]string
	generatedSymbolRecords       map[string]string
}

func (budget *blobBudget) load(
	ctx context.Context,
	file extract.CandidateManifestFile,
	limit int64,
) ([]byte, error) {
	if err := validateInputFile(file); err != nil {
		return nil, err
	}
	if file.DeclaredBytes > limit {
		return nil, gitobj.ErrTooLarge
	}
	if budget.reads >= MaxInputBlobReads ||
		file.DeclaredBytes > MaxInputBlobBytes-budget.bytes {
		return nil, resolvercatalog.ErrLimit
	}
	// Bind the immutable read to the candidate-declared size, not merely the
	// broader per-input ceiling. A corrupted envelope therefore cannot consume
	// the remaining aggregate budget plus one full per-file allowance before
	// the post-read length check rejects it.
	content, err := budget.read(
		ctx, budget.dir, file.ObjectID, file.DeclaredBytes,
	)
	if err != nil {
		return nil, err
	}
	budget.reads++
	budget.bytes += int64(len(content))
	if int64(len(content)) != file.DeclaredBytes {
		return nil, fmt.Errorf(
			"resolver input %q length changed: candidate=%d blob=%d",
			file.Path, file.DeclaredBytes, len(content),
		)
	}
	return content, nil
}

// Build streams one deterministic stage. It never walks a repository tree:
// candidate replay supplies every admitted path/OID, and ReadBlob is invoked
// only for go.mod, the two fixed generated-attribution inputs, and mapped
// generated base-lane Go sources retained by the v1.1 symbol projection.
func Build(ctx context.Context, request BuildRequest) (*resolvercatalog.Prepared, error) {
	if err := validateBuildRequest(request); err != nil {
		return nil, err
	}
	if request.ReadBlob == nil {
		request.ReadBlob = gitobj.ReadBlob
	}
	stage, err := resolvercatalog.NewStage(request.Root, request.Identity)
	if err != nil {
		return nil, err
	}
	// Discard is idempotent after Seal/Publish and prevents runtime failures
	// from accumulating prior-process stages until the next restart.
	defer func() { _ = stage.Discard() }()

	blobs := &blobBudget{read: request.ReadBlob, dir: request.RepositoryDir}
	materialization := &materializationBudget{}
	plan, err := discoverGeneratedInputs(ctx, request, blobs)
	if err != nil {
		return nil, err
	}

	metadata, err := encodeMetadata(memberMetadata{
		Schema: MemberMetadataSchema, Pack: GoModulePackName,
		PackVersion: PackVersion, RecordSchema: GoModuleRecordSchema,
		Policy: FrozenMaterializationPolicy(),
	})
	if err != nil {
		return nil, err
	}
	caller := request.Registry.adapters[0]
	if err := stage.AddMember(
		ctx, "go-module-v2.ndjson", metadata,
		func(write func(json.RawMessage) error) error {
			return emitModulesAndValidateGeneratedPaths(
				ctx, request.Manifest, caller, blobs, plan, write,
			)
		},
	); err != nil {
		return nil, fmt.Errorf("materialize Go modules: %w", err)
	}

	declarations := declarationInputsByProtocol(request.Declarations)
	for _, current := range request.Registry.adapters {
		input, present := declarations[current.protocol]
		metadata, err := encodeMetadata(memberMetadata{
			Schema: MemberMetadataSchema, Pack: current.pack.Name,
			PackVersion: current.pack.Version, Protocol: current.protocol,
			// Protocol members intentionally contain declaration-target rows
			// followed by generated-attribution rows. Their union schema keeps
			// lifecycle routing truthful while each row retains its exact schema.
			RecordSchema: ProtocolMemberRecordSchema,
			Policy:       FrozenMaterializationPolicy(),
		})
		if err != nil {
			return nil, err
		}
		name := current.pack.Name + "-v2.ndjson"
		if err := stage.AddMember(
			ctx, name, metadata,
			func(write func(json.RawMessage) error) error {
				var selected *DeclarationInput
				if present {
					selected = &input
				}
				targets, err := emitDeclarationTargets(
					ctx, request, current, selected, materialization, write,
				)
				if err != nil {
					return err
				}
				return emitGeneratedAttribution(
					ctx, current, plan, targets, materialization, write,
				)
			},
		); err != nil {
			return nil, fmt.Errorf("materialize %s: %w", current.pack.Name, err)
		}
	}
	prepared, err := stage.Seal(ctx)
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

func validateBuildRequest(request BuildRequest) error {
	if err := request.Registry.validate(); err != nil {
		return err
	}
	if request.Manifest == nil || request.Assertions == nil {
		return errors.New("resolver materialization inputs are incomplete")
	}
	if request.Root == "" || request.RepositoryDir == "" {
		return errors.New("resolver materialization roots are required")
	}
	if request.Identity.Repository == "" || request.Identity.Commit == "" ||
		request.Manifest.Identity() != request.Identity.CandidateManifestDigest {
		return errors.New("resolver materialization identity does not match candidate input")
	}
	wanted := request.Registry.Packs()
	if !slices.Equal(request.Identity.ResolverPacks, wanted) {
		return errors.New("resolver materialization identity has a stale pack set")
	}
	seen := make(map[Protocol]struct{}, len(request.Declarations))
	expectedDeclarations := make(
		[]resolvercatalog.DeclarationPublication, len(request.Declarations),
	)
	for index, declaration := range request.Declarations {
		if declaration.Protocol != ProtocolGRPC && declaration.Protocol != ProtocolThrift ||
			declaration.Domain == "" || declaration.RunID == "" ||
			declaration.GenerationDigest == "" {
			return errors.New("resolver declaration input is invalid")
		}
		matched := false
		for _, current := range request.Registry.adapters {
			if current.protocol == declaration.Protocol &&
				current.declarationDomain == declaration.Domain {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("resolver declaration input has no enabled adapter")
		}
		if _, duplicate := seen[declaration.Protocol]; duplicate {
			return errors.New("resolver declaration protocols are duplicated")
		}
		if index > 0 && request.Declarations[index-1].Domain >= declaration.Domain {
			return errors.New("resolver declaration inputs are not strictly ordered")
		}
		seen[declaration.Protocol] = struct{}{}
		expectedDeclarations[index] = resolvercatalog.DeclarationPublication{
			Domain: declaration.Domain, RunID: declaration.RunID,
			GenerationDigest: declaration.GenerationDigest,
			AuthoritySchema:  declaration.AuthoritySchema,
			PlanDigest:       declaration.PlanDigest,
			RootDigest:       declaration.RootDigest,
		}
	}
	if !slices.Equal(expectedDeclarations, request.Identity.Declarations) {
		return errors.New("resolver declaration inputs do not match generation identity")
	}
	return nil
}

func declarationInputsByProtocol(
	inputs []DeclarationInput,
) map[Protocol]DeclarationInput {
	result := make(map[Protocol]DeclarationInput, len(inputs))
	for _, input := range inputs {
		result[input.Protocol] = input
	}
	return result
}

func discoverGeneratedInputs(
	ctx context.Context,
	request BuildRequest,
	budget *blobBudget,
) (*generatedPlan, error) {
	plan := &generatedPlan{
		generatedPaths:  make(map[string]bool),
		generatedFiles:  make(map[string]extract.CandidateManifestFile),
		invocationRoots: make(map[string]bool),
		modules:         make(map[string]string),
		parsedSymbols:   make(map[string]generatedSymbolParse),
		blobs:           budget,
	}
	fixed := make(map[string]extract.CandidateManifestFile, 2)
	caller := request.Registry.adapters[0]
	err := request.Manifest.ForEachRepositoryFile(
		ctx, caller.callerDomain, caller.callerVersion,
		func(file extract.CandidateManifestFile) error {
			switch file.Path {
			case resolverinput.LayoutSnapshotPath,
				resolverinput.GeneratedFromSnapshotPath:
				if err := validateInputFile(file); err != nil {
					return err
				}
				if _, duplicate := fixed[file.Path]; duplicate {
					return fmt.Errorf("resolver input %q is duplicated", file.Path)
				}
				fixed[file.Path] = file
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("discover resolver inputs: %w", err)
	}
	plan.layoutInput, err = loadFixedInput(
		ctx, fixed[resolverinput.LayoutSnapshotPath], budget, extract.MaxBlobBytes,
	)
	if err != nil {
		return nil, err
	}
	plan.generatedInput, err = loadFixedInput(
		ctx, fixed[resolverinput.GeneratedFromSnapshotPath], budget, extract.MaxBlobBytes,
	)
	if err != nil {
		return nil, err
	}
	if plan.layoutInput.present {
		value, decodeErr := resolverinput.DecodeLayoutSnapshot(
			plan.layoutInput.content,
			resolverinput.SnapshotLimits{LayoutRoots: MaxLayoutRoots},
		)
		switch {
		case plan.layoutInput.reason != "":
			plan.layoutProblem = plan.layoutInput.reason
		case errors.Is(decodeErr, resolverinput.ErrLayoutRootLimit):
			plan.layoutProblem = "layout_root_limit_exceeded"
		case decodeErr != nil:
			plan.layoutProblem = "malformed_layout_snapshot"
		case value.Version != resolverinput.LayoutSnapshotVersion:
			plan.layoutProblem = "unsupported_layout_version"
		default:
			problem := validateLayout(value)
			if problem != "" {
				plan.layoutProblem = problem
			} else {
				plan.layout = &value
			}
		}
	}
	if plan.generatedInput.present {
		value, decodeErr := resolverinput.DecodeGeneratedFromSnapshot(
			plan.generatedInput.content,
			resolverinput.SnapshotLimits{
				GeneratedMappings:    MaxGeneratedMappings,
				GeneratorInvocations: MaxGeneratorInvocations,
			},
		)
		switch {
		case plan.generatedInput.reason != "":
			plan.generatedProblem = plan.generatedInput.reason
		case errors.Is(decodeErr, resolverinput.ErrGeneratedMappingLimit):
			plan.generatedProblem = "generated_mapping_limit_exceeded"
		case errors.Is(decodeErr, resolverinput.ErrGeneratorInvocationLimit):
			plan.generatedProblem = "generator_invocation_limit_exceeded"
		case decodeErr != nil:
			plan.generatedProblem = "malformed_generated_snapshot"
		case value.Version != resolverinput.GeneratedFromSnapshotVersion:
			plan.generatedProblem = "unsupported_generated_version"
		default:
			for _, invocation := range value.Invocations {
				if invocation.Protocol != string(ProtocolGRPC) {
					plan.generatedProblem = "unsupported_invocation_protocol"
					break
				}
			}
			if plan.generatedProblem != "" {
				break
			}
			plan.generated = &value
			for _, mapping := range value.Mappings {
				if repopath.Validate(mapping.GeneratedPath) == nil {
					plan.generatedPaths[mapping.GeneratedPath] = false
				}
			}
			for _, invocation := range value.Invocations {
				if repopath.Validate(invocation.GeneratedRoot) == nil {
					plan.invocationRoots[invocation.GeneratedRoot] = false
				}
			}
		}
	}
	return plan, nil
}

func loadFixedInput(
	ctx context.Context,
	file extract.CandidateManifestFile,
	budget *blobBudget,
	limit int64,
) (inputBlob, error) {
	if file.Path == "" {
		return inputBlob{}, nil
	}
	result := inputBlob{file: file, present: true}
	if file.DeclaredBytes > limit {
		result.reason = "input_too_large"
		return result, nil
	}
	content, err := budget.load(ctx, file, limit)
	if err != nil {
		return inputBlob{}, fmt.Errorf("read resolver input %q: %w", file.Path, err)
	}
	result.content = string(content)
	result.digest = digestBytes(content)
	return result, nil
}

func validateInputFile(file extract.CandidateManifestFile) error {
	if repopath.Validate(file.Path) != nil || !gitobj.IsObjectID(file.ObjectID) ||
		file.DeclaredBytes < 0 || file.SourceLane != candidate.SourceLaneBase ||
		file.Mode != "" && file.Mode != "100644" && file.Mode != "100755" {
		return fmt.Errorf("resolver input %q has an invalid candidate envelope", file.Path)
	}
	return nil
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func encodeMetadata(metadata memberMetadata) (json.RawMessage, error) {
	value, err := json.Marshal(metadata)
	return json.RawMessage(value), err
}

func writeJSON(write func(json.RawMessage) error, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return write(json.RawMessage(raw))
}

func pathWithinRoot(filePath, root string) bool {
	return filePath == root || strings.HasPrefix(filePath, root+"/")
}

func validateLayout(layout resolverinput.LayoutSnapshot) string {
	roots := slices.Clone(layout.Roots)
	for _, root := range roots {
		if repopath.Validate(root.Path) != nil {
			return "invalid_layout_path"
		}
		switch root.Kind {
		case "source":
			if root.Protocol != "" {
				return "source_layout_names_protocol"
			}
		case "idl", "generated":
			if root.Protocol != string(ProtocolGRPC) &&
				root.Protocol != string(ProtocolThrift) {
				return "unsupported_layout_protocol"
			}
		default:
			return "unsupported_layout_kind"
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].Path != roots[j].Path {
			return roots[i].Path < roots[j].Path
		}
		if roots[i].Kind != roots[j].Kind {
			return roots[i].Kind < roots[j].Kind
		}
		return roots[i].Protocol < roots[j].Protocol
	})
	for left := range roots {
		for right := left + 1; right < len(roots); right++ {
			if pathWithinRoot(roots[left].Path, roots[right].Path) ||
				pathWithinRoot(roots[right].Path, roots[left].Path) {
				return "overlapping_layout_roots"
			}
		}
	}
	return ""
}

func findLayoutRoot(
	layout *resolverinput.LayoutSnapshot,
	filePath, kind string,
	protocol Protocol,
) bool {
	if layout == nil {
		return true
	}
	for _, root := range layout.Roots {
		if root.Kind == kind && root.Protocol == string(protocol) &&
			pathWithinRoot(filePath, root.Path) {
			return true
		}
	}
	return false
}

func moduleRoot(filePath string) string { return path.Dir(filePath) }

func emitModulesAndValidateGeneratedPaths(
	ctx context.Context,
	manifest extract.CandidateManifest,
	caller adapter,
	budget *blobBudget,
	plan *generatedPlan,
	write func(json.RawMessage) error,
) error {
	moduleCount := 0
	err := manifest.ForEachRepositoryFile(
		ctx, caller.callerDomain, caller.callerVersion,
		func(file extract.CandidateManifestFile) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if file.SourceLane == candidate.SourceLaneBase &&
				strings.HasSuffix(file.Path, ".go") {
				if _, wanted := plan.generatedPaths[file.Path]; wanted {
					if err := validateInputFile(file); err != nil {
						return err
					}
					plan.generatedPaths[file.Path] = true
					plan.generatedFiles[file.Path] = file
				}
				for root := range plan.invocationRoots {
					if pathWithinRoot(file.Path, root) {
						plan.invocationRoots[root] = true
					}
				}
			}
			if file.Path != "go.mod" && !strings.HasSuffix(file.Path, "/go.mod") {
				return nil
			}
			if err := validateInputFile(file); err != nil {
				return err
			}
			moduleCount++
			if moduleCount > resolvercatalog.MaxRecordsPerMember {
				return resolvercatalog.ErrLimit
			}
			record := moduleRecord{
				Schema:       resolvercatalog.RecordSchema,
				RecordSchema: GoModuleRecordSchema,
				Pack:         GoModulePackName, PackVersion: PackVersion,
				Kind: "go_module", Path: file.Path, ObjectID: file.ObjectID,
				ModuleRoot: moduleRoot(file.Path),
			}
			if file.DeclaredBytes > int64(resolverinput.MaxGoModuleBytes) {
				record.State = StateUnsupported
				record.Reason = string(resolverinput.ModuleOversized)
				return writeJSON(write, record)
			}
			content, err := budget.load(
				ctx, file, int64(resolverinput.MaxGoModuleBytes),
			)
			if err != nil {
				return fmt.Errorf("read module %q: %w", file.Path, err)
			}
			record.ContentDigest = digestBytes(content)
			classification := resolverinput.ClassifyGoModule(string(content))
			record.ModulePath = classification.ModulePath
			switch classification.State {
			case resolverinput.ModuleResolved:
				record.State = StateResolved
				plan.modules[record.ModuleRoot] = classification.ModulePath
			case resolverinput.ModuleUnavailable:
				record.State = StateUnavailable
			case resolverinput.ModuleAmbiguous:
				record.State = StateAmbiguous
			case resolverinput.ModuleUnsupported:
				record.State = StateUnsupported
			default:
				return errors.New("module classifier returned an unknown state")
			}
			record.Reason = string(classification.Reason)
			return writeJSON(write, record)
		},
	)
	if err != nil {
		return err
	}
	if moduleCount == 0 {
		return writeJSON(write, moduleRecord{
			Schema:       resolvercatalog.RecordSchema,
			RecordSchema: GoModuleRecordSchema,
			Pack:         GoModulePackName, PackVersion: PackVersion,
			Kind: "go_module_input", State: StateUnavailable,
			Reason: "no_go_module",
		})
	}
	return nil
}

type declarationTargets struct {
	byPath map[string]map[string]struct{}
}

func emitDeclarationTargets(
	ctx context.Context,
	request BuildRequest,
	current adapter,
	input *DeclarationInput,
	budget *materializationBudget,
	write func(json.RawMessage) error,
) (*declarationTargets, error) {
	targets := &declarationTargets{byPath: make(map[string]map[string]struct{})}
	if input == nil {
		if err := writeJSON(write, generatedRecord{
			Schema:       resolvercatalog.RecordSchema,
			RecordSchema: GeneratedRecordSchema,
			Pack:         current.pack.Name, PackVersion: current.pack.Version,
			Kind: "declaration_input", State: StateUnavailable,
			Reason: "declaration_publication_missing", Protocol: current.protocol,
		}); err != nil {
			return nil, err
		}
		return targets, nil
	}
	if input.Domain != current.declarationDomain || input.Protocol != current.protocol {
		return nil, errors.New("declaration input does not match resolver adapter")
	}
	var after *store.AssertionCursor
	for {
		rows, err := request.Assertions.ListAssertions(ctx, store.AssertionQuery{
			Repo: request.Identity.Repository, RunID: input.RunID,
			Predicate: "DECLARES_OPERATION", Limit: assertionPageSize,
			After: after, AllowTruncate: true,
		})
		if err != nil {
			return nil, fmt.Errorf("read declaration operations: %w", err)
		}
		hasMore := len(rows) > assertionPageSize
		if hasMore {
			rows = rows[:assertionPageSize]
		}
		for _, assertion := range rows {
			if assertion.RunID != input.RunID ||
				assertion.Repo != request.Identity.Repository ||
				assertion.Predicate != "DECLARES_OPERATION" ||
				assertion.Tier != "exact" ||
				repopath.Validate(assertion.Subject) != nil ||
				assertion.Lineage == "" || assertion.Object == "" {
				return nil, errors.New("published declaration assertion is malformed")
			}
			budget.declarationRecords++
			if budget.declarationRecords > MaxDeclarationRecords {
				return nil, resolvercatalog.ErrLimit
			}
			lineages := targets.byPath[assertion.Subject]
			if lineages == nil {
				if len(assertion.Subject) >
					MaxDeclarationPathBytes-budget.declarationPathBytes {
					return nil, resolvercatalog.ErrLimit
				}
				budget.declarationPathBytes += len(assertion.Subject)
				lineages = make(map[string]struct{})
				targets.byPath[assertion.Subject] = lineages
			}
			lineages[assertion.Lineage] = struct{}{}
			if err := writeJSON(write, declarationRecord{
				Schema:       resolvercatalog.RecordSchema,
				RecordSchema: DeclarationRecordSchema,
				Pack:         current.pack.Name, PackVersion: current.pack.Version,
				Kind: "declaration_operation", State: StateResolved,
				Protocol: current.protocol, Path: assertion.Subject,
				Lineage: assertion.Lineage, Operation: assertion.Object,
			}); err != nil {
				return nil, err
			}
		}
		if !hasMore {
			break
		}
		last := rows[len(rows)-1]
		after = &store.AssertionCursor{
			Predicate: last.Predicate, Subject: last.Subject,
			Object: last.Object, ID: last.ID, RunID: last.RunID,
		}
	}
	return targets, nil
}

func emitGeneratedAttribution(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	targets *declarationTargets,
	budget *materializationBudget,
	write func(json.RawMessage) error,
) error {
	status := generatedRecord{
		Schema:       resolvercatalog.RecordSchema,
		RecordSchema: GeneratedRecordSchema,
		Pack:         current.pack.Name, PackVersion: current.pack.Version,
		Kind: "generated_attribution_input", Protocol: current.protocol,
	}
	if plan.generatedInput.present {
		status.InputPath = plan.generatedInput.file.Path
		status.InputObjectID = plan.generatedInput.file.ObjectID
		status.InputDigest = plan.generatedInput.digest
	}
	switch {
	case !plan.generatedInput.present:
		status.State = StateUnavailable
		status.Reason = "generated_snapshot_missing"
	case plan.generatedProblem != "":
		status.State = StateUnsupported
		status.Reason = plan.generatedProblem
	case plan.layoutProblem != "":
		status.State = StateUnsupported
		status.Reason = plan.layoutProblem
	default:
		status.State = StateResolved
	}
	if err := writeJSON(write, status); err != nil {
		return err
	}
	if status.State != StateResolved || plan.generated == nil {
		return nil
	}
	if err := emitGeneratedMappings(
		ctx, current, plan, targets, budget, write,
	); err != nil {
		return err
	}
	return emitGeneratorInvocations(ctx, current, plan, targets, write)
}

type mappingKey struct {
	generated string
	relative  string
}

type mappingGroup struct {
	key            mappingKey
	candidates     []generatedCandidate
	candidateBytes int
	state          string
	reason         string
	seenRows       map[string]struct{}
	limited        bool
}

type declarationCandidateSet struct {
	lineages      []string
	identityBytes int
}

func emitGeneratedMappings(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	targets *declarationTargets,
	budget *materializationBudget,
	write func(json.RawMessage) error,
) error {
	mappings := slices.Clone(plan.generated.Mappings)
	sort.Slice(mappings, func(i, j int) bool {
		left, right := mappings[i], mappings[j]
		if left.GeneratedPath != right.GeneratedPath {
			return left.GeneratedPath < right.GeneratedPath
		}
		if left.GeneratorRelativePath != right.GeneratorRelativePath {
			return left.GeneratorRelativePath < right.GeneratorRelativePath
		}
		if left.DeclarationPath != right.DeclarationPath {
			return left.DeclarationPath < right.DeclarationPath
		}
		return left.Protocol < right.Protocol
	})
	groups := make(map[mappingKey]*mappingGroup)
	candidateSets := make(map[string]declarationCandidateSet)
	var order []mappingKey
	for _, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return err
		}
		protocol, protocolErr := generatedProtocol(mapping.Protocol, mapping.DeclarationPath)
		if protocolErr == nil && protocol != current.protocol {
			continue
		}
		key := mappingKey{
			generated: mapping.GeneratedPath,
			relative:  mapping.GeneratorRelativePath,
		}
		group := groups[key]
		if group == nil {
			group = &mappingGroup{
				key: key, seenRows: make(map[string]struct{}),
			}
			groups[key] = group
			order = append(order, key)
		}
		if group.limited {
			continue
		}
		if protocolErr != nil {
			group.state, group.reason = StateUnsupported, "unsupported_protocol"
			continue
		}
		rowIdentity := mapping.DeclarationPath
		if _, duplicate := group.seenRows[rowIdentity]; duplicate {
			group.state, group.reason = StateUnsupported, "duplicate_mapping"
			continue
		}
		group.seenRows[rowIdentity] = struct{}{}
		if repopath.Validate(mapping.GeneratedPath) != nil ||
			repopath.Validate(mapping.DeclarationPath) != nil ||
			mapping.GeneratorRelativePath != "" &&
				repopath.Validate(mapping.GeneratorRelativePath) != nil {
			group.state, group.reason = StateUnsupported, "invalid_mapping_path"
			continue
		}
		if protocol == ProtocolGRPC && mapping.GeneratorRelativePath != "" &&
			!strings.HasSuffix(mapping.GeneratorRelativePath, ".proto") ||
			protocol == ProtocolThrift && mapping.GeneratorRelativePath != "" {
			group.state, group.reason = StateUnsupported, "unsupported_relative_path"
			continue
		}
		if !plan.generatedPaths[mapping.GeneratedPath] {
			if group.state == "" {
				group.state, group.reason = StateUnavailable, "generated_path_missing"
			}
			continue
		}
		if !findLayoutRoot(plan.layout, mapping.GeneratedPath, "generated", protocol) ||
			!findLayoutRoot(plan.layout, mapping.DeclarationPath, "idl", protocol) {
			group.state, group.reason = StateUnsupported, "layout_classification_mismatch"
			continue
		}
		lineages := targets.byPath[mapping.DeclarationPath]
		if len(lineages) == 0 {
			if group.state == "" {
				group.state, group.reason = StateUnavailable, "declaration_not_in_target_set"
			}
			continue
		}
		set, known := candidateSets[mapping.DeclarationPath]
		if !known {
			set.lineages = make([]string, 0, len(lineages))
			for lineage := range lineages {
				if len(set.lineages)%256 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				set.lineages = append(set.lineages, lineage)
				set.identityBytes += len(mapping.DeclarationPath) + len(lineage)
			}
			slices.Sort(set.lineages)
			candidateSets[mapping.DeclarationPath] = set
		}
		if len(set.lineages) > MaxGeneratedCandidateExpansions-
			budget.generatedCandidateExpansions ||
			set.identityBytes > MaxGeneratedCandidateBytes-
				budget.generatedCandidateBytes {
			return resolvercatalog.ErrLimit
		}
		budget.generatedCandidateExpansions += len(set.lineages)
		budget.generatedCandidateBytes += set.identityBytes
		if len(set.lineages) > MaxGeneratedCandidatesPerSelector-len(group.candidates) ||
			set.identityBytes > MaxGeneratedCandidateBytesPerSelector-group.candidateBytes {
			group.candidates = nil
			group.candidateBytes = 0
			group.state = StateUnsupported
			group.reason = "candidate_expansion_limit_exceeded"
			group.limited = true
			continue
		}
		for index, lineage := range set.lineages {
			if index%256 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			group.candidates = append(group.candidates, generatedCandidate{
				DeclarationPath:    mapping.DeclarationPath,
				DeclarationLineage: lineage,
			})
		}
		group.candidateBytes += set.identityBytes
	}
	for _, key := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		group := groups[key]
		sort.Slice(group.candidates, func(i, j int) bool {
			if group.candidates[i].DeclarationPath != group.candidates[j].DeclarationPath {
				return group.candidates[i].DeclarationPath < group.candidates[j].DeclarationPath
			}
			return group.candidates[i].DeclarationLineage < group.candidates[j].DeclarationLineage
		})
		state, reason := group.state, group.reason
		if state == "" {
			switch len(group.candidates) {
			case 0:
				state, reason = StateUnavailable, "no_declaration_candidate"
			case 1:
				state = StateResolved
			default:
				state, reason = StateAmbiguous, "multiple_declaration_paths"
			}
		} else if state == StateUnavailable && len(group.candidates) > 0 {
			// A valid and an unavailable row with the same selector cannot be
			// reduced by input order; retain every candidate and mark ambiguity.
			state, reason = StateAmbiguous, "mixed_mapping_candidates"
		}
		mappingRecord := generatedRecord{
			Schema:       resolvercatalog.RecordSchema,
			RecordSchema: GeneratedRecordSchema,
			Pack:         current.pack.Name, PackVersion: current.pack.Version,
			Kind: "generated_mapping", State: state, Reason: reason,
			Protocol:              current.protocol,
			InputPath:             plan.generatedInput.file.Path,
			InputObjectID:         plan.generatedInput.file.ObjectID,
			InputDigest:           plan.generatedInput.digest,
			GeneratedPath:         key.generated,
			GeneratorRelativePath: key.relative,
			Candidates:            group.candidates,
		}
		if err := writeJSON(write, mappingRecord); err != nil {
			return err
		}
		if err := emitGeneratedSymbols(
			ctx, current, plan, key, state, reason, group.candidates, budget, write,
		); err != nil {
			return err
		}
	}
	return nil
}

func emitGeneratedSymbols(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	key mappingKey,
	state, reason string,
	candidates []generatedCandidate,
	budget *materializationBudget,
	write func(json.RawMessage) error,
) error {
	// Unit tests that exercise mapping reduction in isolation predate the
	// generated-source projection. Production Build always fills this map from
	// the same candidate pass that proves generatedPaths.
	file, present := plan.generatedFiles[key.generated]
	if !present {
		return nil
	}
	if err := validateInputFile(file); err != nil {
		return err
	}
	if err := reserveGeneratedSourceIdentity(
		budget, file.Path, file.ObjectID,
	); err != nil {
		return err
	}
	parsed, err := loadGeneratedSymbols(ctx, current.protocol, plan, file)
	if err != nil {
		return err
	}
	base := generatedSymbolRecord{
		Schema:       resolvercatalog.RecordSchema,
		RecordSchema: GeneratedSymbolRecordSchema,
		Pack:         current.pack.Name, PackVersion: current.pack.Version,
		Protocol: current.protocol, GeneratedPath: file.Path,
		GeneratedObjectID: file.ObjectID, GeneratedDigest: parsed.digest,
		GeneratorRelativePath: key.relative,
	}
	if parsed.reason != "" {
		base.Kind, base.State, base.Reason = "generated_symbol_input", parsed.state, parsed.reason
		return writeGeneratedSymbol(write, budget, base)
	}
	matched := make([]gocaller.GeneratedSymbol, 0, len(parsed.symbols))
	for _, symbol := range parsed.symbols {
		if current.protocol == ProtocolGRPC && key.relative != "" &&
			symbol.GeneratorRelativePath != key.relative {
			continue
		}
		matched = append(matched, symbol)
	}
	if len(matched) == 0 {
		base.Kind, base.State, base.Reason = "generated_symbol_input", StateUnavailable,
			"no_generated_client_symbols"
		return writeGeneratedSymbol(write, budget, base)
	}
	for _, symbol := range matched {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := base
		record.Kind = "generated_symbol"
		record.State, record.Reason = state, reason
		record.ImportPath = symbol.ImportPath
		record.Package = symbol.Package
		record.ClientType = symbol.ClientType
		record.Method = symbol.Method
		record.Operation = symbol.Operation
		record.Constructors = slices.Clone(symbol.Constructors)
		record.GeneratorRelativePath = symbol.GeneratorRelativePath
		if state == StateResolved && len(candidates) == 1 {
			record.DeclarationPath = candidates[0].DeclarationPath
			record.DeclarationLineage = candidates[0].DeclarationLineage
		} else if record.Reason == "" {
			record.Reason = "generated_mapping_" + state
		}
		if err := writeGeneratedSymbol(write, budget, record); err != nil {
			return err
		}
	}
	return nil
}

func loadGeneratedSymbols(
	ctx context.Context,
	protocol Protocol,
	plan *generatedPlan,
	file extract.CandidateManifestFile,
) (generatedSymbolParse, error) {
	if err := validateInputFile(file); err != nil {
		return generatedSymbolParse{}, err
	}
	if file.DeclaredBytes > MaxGeneratedSymbolSourceBytes {
		return generatedSymbolParse{
			protocol: protocol, file: file, state: StateUnsupported,
			reason: generatedSourceTooLargeReason,
		}, nil
	}
	if plan.parsedSymbols == nil {
		plan.parsedSymbols = make(map[string]generatedSymbolParse)
	}
	if parsed, present := plan.parsedSymbols[file.Path]; present {
		if parsed.protocol != protocol {
			return generatedSymbolParse{
				protocol: protocol, file: file, digest: parsed.digest,
				state: StateUnsupported, reason: "generated_path_protocol_conflict",
			}, nil
		}
		return parsed, nil
	}
	if plan.blobs == nil {
		return generatedSymbolParse{}, errors.New("generated symbol blob budget is unavailable")
	}
	if len(plan.parsedSymbols) >= MaxGeneratedSymbolSources {
		return generatedSymbolParse{}, resolvercatalog.ErrLimit
	}
	content, err := plan.blobs.load(ctx, file, MaxGeneratedSymbolSourceBytes)
	if err != nil {
		return generatedSymbolParse{}, fmt.Errorf("read generated symbol source %q: %w", file.Path, err)
	}
	parsed := generatedSymbolParse{
		protocol: protocol, file: file, digest: digestBytes(content),
	}
	importPath := resolverinput.GoImportPath(plan.modules, file.Path)
	parsed.symbols, err = gocaller.DescribeGeneratedSource(
		string(protocol), file.Path, importPath, string(content),
	)
	if err != nil {
		parsed.state, parsed.reason = StateUnsupported, "generated_source_unparseable"
		parsed.symbols = nil
	}
	for _, symbol := range parsed.symbols {
		if protocol == ProtocolGRPC &&
			(symbol.GeneratorRelativePath == "" ||
				repopath.Validate(symbol.GeneratorRelativePath) != nil ||
				!strings.HasSuffix(symbol.GeneratorRelativePath, ".proto")) ||
			protocol == ProtocolThrift && symbol.GeneratorRelativePath != "" {
			parsed.state, parsed.reason = StateUnsupported, "generated_source_unparseable"
			parsed.symbols = nil
			break
		}
	}
	if importPath == "" && parsed.reason == "" {
		parsed.state, parsed.reason = StateUnavailable, "module_path_unavailable"
	}
	plan.parsedSymbols[file.Path] = parsed
	return parsed, nil
}

func writeGeneratedSymbol(
	write func(json.RawMessage) error,
	budget *materializationBudget,
	record generatedSymbolRecord,
) error {
	if err := reserveGeneratedSourceIdentity(
		budget, record.GeneratedPath, record.GeneratedObjectID,
	); err != nil {
		return err
	}
	if record.Kind != "generated_symbol" {
		return writeJSON(write, record)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	key := directDescriptorKey(directDescriptorFromRecord(record))
	if budget.generatedSymbolRecords == nil {
		budget.generatedSymbolRecords = make(map[string]string)
	}
	if prior, present := budget.generatedSymbolRecords[key]; present {
		if prior != string(raw) {
			return errors.New("generated symbol identity has conflicting records")
		}
		return nil
	}
	identityBytes := generatedSymbolIdentityBytes(record)
	if budget.generatedSymbolDescriptors >= MaxGeneratedSymbolDescriptors ||
		identityBytes > MaxGeneratedSymbolIdentityBytes-
			budget.generatedSymbolIdentityBytes {
		return resolvercatalog.ErrLimit
	}
	budget.generatedSymbolDescriptors++
	budget.generatedSymbolIdentityBytes += identityBytes
	budget.generatedSymbolRecords[key] = string(raw)
	return write(json.RawMessage(raw))
}

func reserveGeneratedSourceIdentity(
	budget *materializationBudget,
	generatedPath, objectID string,
) error {
	if budget.generatedSourceObjects == nil {
		budget.generatedSourceObjects = make(map[string]string)
	}
	if prior, present := budget.generatedSourceObjects[generatedPath]; present {
		if prior != objectID {
			return errors.New("generated source path names multiple objects")
		}
		return nil
	}
	identityBytes := len(generatedPath) + len(objectID)
	if len(budget.generatedSourceObjects) >= MaxGeneratedSymbolSources ||
		identityBytes > MaxGeneratedSymbolIdentityBytes-
			budget.generatedSymbolIdentityBytes {
		return resolvercatalog.ErrLimit
	}
	budget.generatedSourceObjects[generatedPath] = objectID
	budget.generatedSymbolIdentityBytes += identityBytes
	return nil
}

func generatedSymbolIdentityBytes(record generatedSymbolRecord) int {
	result := len(record.State) + len(record.Reason) + len(record.Protocol) +
		len(record.ImportPath) + len(record.Package) + len(record.ClientType) +
		len(record.Method) + len(record.Operation) + len(record.GeneratedPath) +
		len(record.GeneratedObjectID) + len(record.GeneratedDigest) +
		len(record.GeneratorRelativePath) + len(record.DeclarationPath) +
		len(record.DeclarationLineage)
	for _, constructor := range record.Constructors {
		result += len(constructor)
	}
	return result
}

func emitGeneratorInvocations(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	targets *declarationTargets,
	write func(json.RawMessage) error,
) error {
	invocations := slices.Clone(plan.generated.Invocations)
	sort.Slice(invocations, func(i, j int) bool {
		if invocations[i].GeneratedRoot != invocations[j].GeneratedRoot {
			return invocations[i].GeneratedRoot < invocations[j].GeneratedRoot
		}
		if invocations[i].GeneratorInvocationRoot !=
			invocations[j].GeneratorInvocationRoot {
			return invocations[i].GeneratorInvocationRoot <
				invocations[j].GeneratorInvocationRoot
		}
		return invocations[i].Protocol < invocations[j].Protocol
	})
	type invocationState struct {
		value  resolverinput.GeneratorInvocation
		state  string
		reason string
	}
	values := make([]invocationState, 0, len(invocations))
	seen := make(map[string]int)
	for _, invocation := range invocations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if invocation.Protocol != string(current.protocol) {
			continue
		}
		state := invocationState{value: invocation, state: StateResolved}
		key := invocation.GeneratedRoot + "\x00" + invocation.GeneratorInvocationRoot
		if current.protocol != ProtocolGRPC {
			state.state, state.reason = StateUnsupported, "invocation_protocol_unsupported"
		} else if repopath.Validate(invocation.GeneratedRoot) != nil ||
			repopath.Validate(invocation.GeneratorInvocationRoot) != nil {
			state.state, state.reason = StateUnsupported, "invalid_invocation_path"
		} else if prior, duplicate := seen[key]; duplicate {
			state.state, state.reason = StateUnsupported, "duplicate_invocation"
			values[prior].state, values[prior].reason = StateUnsupported, "duplicate_invocation"
		} else if !plan.invocationRoots[invocation.GeneratedRoot] {
			state.state, state.reason = StateUnavailable, "generated_root_missing"
		} else if present, err := targetPathUnder(
			ctx, targets, invocation.GeneratorInvocationRoot,
		); err != nil {
			return err
		} else if !present {
			state.state, state.reason = StateUnavailable, "declaration_root_not_in_target_set"
		} else if !findLayoutRoot(
			plan.layout, invocation.GeneratedRoot, "generated", current.protocol,
		) || !findLayoutRoot(
			plan.layout, invocation.GeneratorInvocationRoot, "idl", current.protocol,
		) {
			state.state, state.reason = StateUnsupported, "layout_classification_mismatch"
		}
		if _, known := seen[key]; !known {
			seen[key] = len(values)
		}
		values = append(values, state)
	}
	for left := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		for right := left + 1; right < len(values); right++ {
			if values[left].value.GeneratedRoot == values[right].value.GeneratedRoot &&
				values[left].value.GeneratorInvocationRoot ==
					values[right].value.GeneratorInvocationRoot {
				continue
			}
			if pathWithinRoot(values[left].value.GeneratedRoot, values[right].value.GeneratedRoot) ||
				pathWithinRoot(values[right].value.GeneratedRoot, values[left].value.GeneratedRoot) {
				if values[left].state != StateUnsupported {
					values[left].state, values[left].reason = StateAmbiguous, "overlapping_generated_roots"
				}
				if values[right].state != StateUnsupported {
					values[right].state, values[right].reason = StateAmbiguous, "overlapping_generated_roots"
				}
			}
		}
	}
	for _, invocation := range values {
		if err := writeJSON(write, generatedRecord{
			Schema:       resolvercatalog.RecordSchema,
			RecordSchema: GeneratedRecordSchema,
			Pack:         current.pack.Name, PackVersion: current.pack.Version,
			Kind: "generator_invocation", State: invocation.state,
			Reason: invocation.reason, Protocol: current.protocol,
			InputPath:               plan.generatedInput.file.Path,
			InputObjectID:           plan.generatedInput.file.ObjectID,
			InputDigest:             plan.generatedInput.digest,
			GeneratedRoot:           invocation.value.GeneratedRoot,
			GeneratorInvocationRoot: invocation.value.GeneratorInvocationRoot,
		}); err != nil {
			return err
		}
	}
	return nil
}

func generatedProtocol(declared, declarationPath string) (Protocol, error) {
	var derived Protocol
	switch {
	case strings.HasSuffix(declarationPath, ".proto"):
		derived = ProtocolGRPC
	case strings.HasSuffix(declarationPath, ".thrift"):
		derived = ProtocolThrift
	default:
		return "", errors.New("declaration path has no supported protocol suffix")
	}
	if declared != "" && declared != string(derived) {
		return "", errors.New("declared protocol conflicts with declaration path")
	}
	return derived, nil
}

func targetPathUnder(
	ctx context.Context,
	targets *declarationTargets,
	root string,
) (bool, error) {
	index := 0
	for filePath := range targets.byPath {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		index++
		if pathWithinRoot(filePath, root) {
			return true, nil
		}
	}
	return false, nil
}
