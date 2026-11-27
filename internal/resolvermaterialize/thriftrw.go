package resolvermaterialize

import (
	"context"
	"crypto/sha1" //nolint:gosec // thriftrw records a SHA-1 content fingerprint; it is compared, not trusted for security
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolverinput"
)

// PHEBS-46: hash-verified thriftrw provenance. A thriftrw-plugin-yarpc client
// is related to a declared .thrift file only when the thriftrw package its
// helpers go through embeds ThriftModule{Package: that import path, FilePath,
// SHA1} and exactly one declared .thrift file at FilePath (or ending in
// /FilePath) has that content SHA-1. Every other shape is an explicit
// abstention; nothing is inferred from directory names.

// ThriftRWProvenance names the derivation rule; it is pack-version identity.
const ThriftRWProvenance = "thriftrw-yarpc-module-package-sha1-embedded-v2"

// MaxThriftRWClients bounds the YARPC client candidates one catalog derives.
const MaxThriftRWClients = 4_096

// isThriftRWClientCandidate pre-filters by the plugin's fixed output name
// (<service>client/client.go); the header and provenance decide.
func isThriftRWClientCandidate(file extract.CandidateManifestFile) bool {
	return file.SourceLane == candidate.SourceLaneBase &&
		path.Base(file.Path) == "client.go" &&
		strings.HasSuffix(path.Base(path.Dir(file.Path)), "client")
}

type thriftRWModuleFile struct {
	file    extract.CandidateManifestFile
	digest  string
	module  gocaller.ThriftRWModule
	problem string
}

type thriftRWClient struct {
	file         extract.CandidateManifestFile
	content      string
	digest       string
	importPath   string
	parentImport string
	parentDir    string
	state        string
	reason       string
}

// importDirectory maps an import path to its repository directory through the
// repository's own go.mod modules (longest module path wins).
func importDirectory(modules map[string]string, importPath string) (string, bool) {
	bestRoot, bestPath := "", ""
	for root, modulePath := range modules {
		if importPath != modulePath && !strings.HasPrefix(importPath, modulePath+"/") {
			continue
		}
		if len(modulePath) > len(bestPath) {
			bestRoot, bestPath = root, modulePath
		}
	}
	if bestPath == "" {
		return "", false
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(importPath, bestPath), "/")
	if rest == "" {
		return bestRoot, true
	}
	if bestRoot == "." {
		return rest, true
	}
	return bestRoot + "/" + rest, true
}

func emitThriftRWProvenance(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	targets *declarationTargets,
	budget *materializationBudget,
	write func(json.RawMessage) error,
) error {
	if current.protocol != ProtocolThrift || len(plan.thriftRWCandidates) == 0 {
		return nil
	}
	base := generatedRecord{
		Schema:       resolvercatalog.RecordSchema,
		RecordSchema: GeneratedRecordSchema,
		Pack:         current.pack.Name, PackVersion: current.pack.Version,
		Kind: "thriftrw_provenance", Protocol: current.protocol,
	}
	if len(plan.thriftRWCandidates) > MaxThriftRWClients {
		record := base
		record.State, record.Reason = StateUnsupported, "thriftrw_client_limit_exceeded"
		return writeJSON(write, record)
	}
	candidates := append([]extract.CandidateManifestFile(nil), plan.thriftRWCandidates...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })

	// 1. Read each candidate; keep YARPC clients and locate their module package.
	var clients []*thriftRWClient
	neededDirs := make(map[string]struct{})
	for _, file := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, explicit := plan.generatedPaths[file.Path]; explicit {
			continue // an explicit generated-from mapping governs this file
		}
		client := &thriftRWClient{file: file}
		if file.DeclaredBytes > MaxGeneratedSymbolSourceBytes {
			client.state, client.reason = StateUnsupported, generatedSourceTooLargeReason
			clients = append(clients, client)
			continue
		}
		content, err := plan.blobs.load(ctx, file, MaxGeneratedSymbolSourceBytes)
		if err != nil {
			return fmt.Errorf("read YARPC client candidate %q: %w", file.Path, err)
		}
		if !gocaller.IsThriftRWYARPCClient(string(content)) {
			continue
		}
		client.content, client.digest = string(content), digestBytes(content)
		client.importPath = resolverinput.GoImportPath(plan.modules, file.Path)
		parent, found, parseErr := gocaller.YARPCClientModuleImport(file.Path, client.content)
		switch {
		case parseErr != nil:
			client.state, client.reason = StateUnsupported, "generated_source_unparseable"
		case client.importPath == "":
			client.state, client.reason = StateUnavailable, "module_path_unavailable"
		case !found:
			client.state, client.reason = StateUnavailable, "thriftrw_module_import_unavailable"
		default:
			client.parentImport = parent
			dir, inside := importDirectory(plan.modules, parent)
			if !inside {
				client.state, client.reason = StateUnavailable, "thriftrw_module_outside_repository"
				break
			}
			client.parentDir = dir
			neededDirs[dir] = struct{}{}
		}
		clients = append(clients, client)
	}
	if len(clients) == 0 {
		return nil
	}

	// 2. Parse the thriftrw module files in the needed package directories.
	modulesByDir := make(map[string][]thriftRWModuleFile)
	if len(neededDirs) > 0 {
		var files []extract.CandidateManifestFile
		err := plan.manifest.ForEachRepositoryFile(
			ctx, current.callerDomain, current.callerVersion,
			func(file extract.CandidateManifestFile) error {
				if file.SourceLane != candidate.SourceLaneBase || !strings.HasSuffix(file.Path, ".go") ||
					strings.HasSuffix(file.Path, "_test.go") {
					return nil
				}
				if _, wanted := neededDirs[path.Dir(file.Path)]; wanted {
					files = append(files, file)
				}
				return nil
			},
		)
		if err != nil {
			return fmt.Errorf("discover thriftrw module sources: %w", err)
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry := thriftRWModuleFile{file: file}
			if file.DeclaredBytes > MaxGeneratedSymbolSourceBytes {
				entry.problem = generatedSourceTooLargeReason
				modulesByDir[path.Dir(file.Path)] = append(modulesByDir[path.Dir(file.Path)], entry)
				continue
			}
			content, err := plan.blobs.load(ctx, file, MaxGeneratedSymbolSourceBytes)
			if err != nil {
				return fmt.Errorf("read thriftrw module candidate %q: %w", file.Path, err)
			}
			if !gocaller.IsThriftRWModuleSource(string(content)) {
				continue
			}
			module, ok, parseErr := gocaller.ParseThriftRWModule(file.Path, string(content))
			if parseErr != nil {
				entry.problem = "thriftrw_module_unparseable"
			} else if !ok {
				continue
			}
			entry.digest, entry.module = digestBytes(content), module
			modulesByDir[path.Dir(file.Path)] = append(modulesByDir[path.Dir(file.Path)], entry)
		}
	}

	// 3. Read the declared .thrift files a module names, once each.
	wantedSuffixes := make(map[string]struct{})
	for _, entries := range modulesByDir {
		for _, entry := range entries {
			if entry.problem == "" {
				wantedSuffixes[entry.module.FilePath] = struct{}{}
			}
		}
	}
	declarationHashes, err := thriftRWDeclarationHashes(ctx, current, plan, targets, wantedSuffixes)
	if err != nil {
		return err
	}

	// 4. Relate each client and emit its provenance and symbols.
	for _, client := range clients {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := base
		record.GeneratedPath = client.file.Path
		var module *thriftRWModuleFile
		var declaration string
		if client.state == "" {
			module, client.state, client.reason = selectThriftRWModule(
				modulesByDir[client.parentDir], client.parentImport,
			)
		}
		if module != nil {
			record.InputPath = module.file.Path
			record.InputObjectID = module.file.ObjectID
			record.InputDigest = module.digest
			declaration, client.state, client.reason = selectThriftRWDeclaration(
				module.module, declarationHashes,
			)
			// PHEBS-47: with no committed .thrift at FilePath, thrift-contract
			// declares the module's digest-valid embedded IDL under the module
			// file itself, which is then the declaration.
			if client.reason == "thriftrw_declaration_not_declared" &&
				len(targets.byPath[module.file.Path]) > 0 {
				declaration, client.state, client.reason = module.file.Path, "", ""
			}
		}
		var lineages []string
		if client.state == "" {
			for lineage := range targets.byPath[declaration] {
				lineages = append(lineages, lineage)
			}
			sort.Strings(lineages)
			for _, lineage := range lineages {
				record.Candidates = append(record.Candidates, generatedCandidate{
					DeclarationPath: declaration, DeclarationLineage: lineage,
				})
			}
			switch len(lineages) {
			case 0:
				client.state, client.reason = StateUnavailable, "declaration_not_in_target_set"
			case 1:
				client.state = StateResolved
			default:
				client.state, client.reason = StateAmbiguous, "multiple_declaration_paths"
			}
		}
		record.State, record.Reason = client.state, client.reason
		if err := writeJSON(write, record); err != nil {
			return err
		}
		if err := emitThriftRWSymbols(
			ctx, current, client, module, record.Candidates, targets, budget, write,
		); err != nil {
			return err
		}
	}
	return nil
}

func selectThriftRWModule(
	entries []thriftRWModuleFile,
	parentImport string,
) (*thriftRWModuleFile, string, string) {
	var matches []*thriftRWModuleFile
	for index := range entries {
		entry := &entries[index]
		if entry.problem != "" {
			continue
		}
		if entry.module.Package == parentImport {
			matches = append(matches, entry)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], "", ""
	case 0:
		for _, entry := range entries {
			if entry.problem != "" {
				return nil, StateUnsupported, entry.problem
			}
		}
		return nil, StateUnavailable, "thriftrw_module_missing"
	default:
		return nil, StateAmbiguous, "thriftrw_module_ambiguous"
	}
}

func selectThriftRWDeclaration(
	module gocaller.ThriftRWModule,
	hashes map[string]string,
) (string, string, string) {
	var named, matched []string
	for declarationPath, sum := range hashes {
		if declarationPath != module.FilePath && !strings.HasSuffix(declarationPath, "/"+module.FilePath) {
			continue
		}
		named = append(named, declarationPath)
		if sum == module.SHA1 {
			matched = append(matched, declarationPath)
		}
	}
	switch {
	case len(matched) == 1:
		return matched[0], "", ""
	case len(matched) > 1:
		return "", StateAmbiguous, "thriftrw_declaration_ambiguous"
	case len(named) > 0:
		return "", StateUnavailable, "thriftrw_declaration_hash_mismatch"
	default:
		return "", StateUnavailable, "thriftrw_declaration_not_declared"
	}
}

// thriftRWDeclarationHashes reads, from the declaration domain's candidate
// view, every declared .thrift file whose path ends with a wanted FilePath and
// returns its content SHA-1.
func thriftRWDeclarationHashes(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	targets *declarationTargets,
	wanted map[string]struct{},
) (map[string]string, error) {
	result := make(map[string]string)
	if len(wanted) == 0 {
		return result, nil
	}
	named := func(declarationPath string) bool {
		if _, declared := targets.byPath[declarationPath]; !declared {
			return false
		}
		for suffix := range wanted {
			if declarationPath == suffix || strings.HasSuffix(declarationPath, "/"+suffix) {
				return true
			}
		}
		return false
	}
	var files []extract.CandidateManifestFile
	err := plan.manifest.ForEachRepositoryFile(
		ctx, current.declarationDomain, current.declarationVersion,
		func(file extract.CandidateManifestFile) error {
			if file.SourceLane == candidate.SourceLaneBase && named(file.Path) {
				files = append(files, file)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("discover declared thrift files: %w", err)
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		content, err := plan.blobs.load(ctx, file, extract.MaxBlobBytes)
		if err != nil {
			return nil, fmt.Errorf("read declared thrift file %q: %w", file.Path, err)
		}
		sum := sha1.Sum(content) //nolint:gosec // content fingerprint comparison
		result[file.Path] = hex.EncodeToString(sum[:])
	}
	return result, nil
}

func emitThriftRWSymbols(
	ctx context.Context,
	current adapter,
	client *thriftRWClient,
	module *thriftRWModuleFile,
	candidates []generatedCandidate,
	targets *declarationTargets,
	budget *materializationBudget,
	write func(json.RawMessage) error,
) error {
	base := generatedSymbolRecord{
		Schema:       resolvercatalog.RecordSchema,
		RecordSchema: GeneratedSymbolRecordSchema,
		Pack:         current.pack.Name, PackVersion: current.pack.Version,
		Protocol: current.protocol, GeneratedPath: client.file.Path,
		GeneratedObjectID: client.file.ObjectID, GeneratedDigest: client.digest,
	}
	if client.digest == "" {
		// Oversized client: no content was read, so no digest is claimed.
		base.Kind, base.State, base.Reason = "generated_symbol_input", client.state, client.reason
		return writeGeneratedSymbol(write, budget, base)
	}
	if module == nil {
		base.Kind, base.State, base.Reason = "generated_symbol_input", client.state, client.reason
		if base.State == StateResolved || base.State == "" {
			return errors.New("thriftrw client without a module cannot resolve")
		}
		return writeGeneratedSymbol(write, budget, base)
	}
	symbols, err := gocaller.DescribeThriftRWYARPCClient(
		client.file.Path, client.importPath, client.parentImport, client.content, module.module,
	)
	if err != nil {
		base.Kind, base.State, base.Reason = "generated_symbol_input", StateUnsupported, "generated_source_unparseable"
		return writeGeneratedSymbol(write, budget, base)
	}
	if len(symbols) == 0 {
		base.Kind, base.State, base.Reason = "generated_symbol_input", StateUnavailable, "no_generated_client_symbols"
		return writeGeneratedSymbol(write, budget, base)
	}
	for _, symbol := range symbols {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := base
		record.Kind = "generated_symbol"
		record.State, record.Reason = client.state, client.reason
		record.ImportPath, record.Package = symbol.ImportPath, symbol.Package
		record.ClientType, record.Method = symbol.ClientType, symbol.Method
		record.Operation = symbol.Operation
		record.Constructors = append([]string(nil), symbol.Constructors...)
		if client.state == StateResolved && len(candidates) == 1 {
			record.DeclarationPath = candidates[0].DeclarationPath
			record.DeclarationLineage = candidates[0].DeclarationLineage
			operation, bound, unboundReason := targets.bindThriftOperation(
				record.DeclarationLineage, symbol.Operation,
			)
			if bound {
				record.Operation = operation
			} else {
				record.State, record.Reason = StateUnavailable, unboundReason
				record.DeclarationPath, record.DeclarationLineage = "", ""
			}
		} else if record.Reason == "" {
			record.Reason = "thriftrw_provenance_" + client.state
		}
		if err := writeGeneratedSymbol(write, budget, record); err != nil {
			return err
		}
	}
	return nil
}
