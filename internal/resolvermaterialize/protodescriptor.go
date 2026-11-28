package resolvermaterialize

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/idlpreflight"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolverinput"
)

// PHEBS-49: embedded-descriptor provenance. A protoc-gen-go-grpc (or legacy
// plugins=grpc) client file is related to a declared .proto only when exactly
// one protoc-gen-go descriptor in its directory and package clause is named
// by the client's `// source:` path S, that descriptor declares every
// operation the client calls, and exactly one declared .proto at S (or ending
// in /S) parses to an equivalent unlinked descriptor. Every other shape is an
// explicit abstention; nothing is inferred from names or directories.

// ProtoDescriptorProvenance names the derivation rule; it is pack-version
// identity.
const ProtoDescriptorProvenance = "protoc-gen-go-embedded-descriptor-equal-v1"

const (
	// MaxProtoDescriptorCandidates bounds the *.pb.go files one catalog reads.
	MaxProtoDescriptorCandidates = 4_096
	// MaxProtoDescriptorBytes bounds one decoded (and, for the legacy form,
	// decompressed) FileDescriptorProto.
	MaxProtoDescriptorBytes = 4 << 20
)

var (
	rawDescriptorName    = regexp.MustCompile(`^file_[A-Za-z0-9_]+_rawDesc$`)
	legacyDescriptorName = regexp.MustCompile(`^fileDescriptor(?:_[0-9a-f]+|[0-9]+)$`)
)

func isProtoGoCandidate(file extract.CandidateManifestFile) bool {
	return file.SourceLane == candidate.SourceLaneBase &&
		strings.HasSuffix(file.Path, ".pb.go")
}

type protoGoClient struct {
	file       extract.CandidateManifestFile
	digest     string
	importPath string
	symbols    []gocaller.GeneratedSymbol
	state      string
	reason     string
}

type embeddedDescriptor struct {
	file        extract.CandidateManifestFile
	digest      string
	packageName string
	descriptor  *descriptorpb.FileDescriptorProto
}

type protoDescriptorDir struct {
	descriptors []embeddedDescriptor
	problem     string
}

func (plan *generatedPlan) protoDescriptorGoverned(filePath string) bool {
	if _, explicit := plan.generatedPaths[filePath]; explicit {
		return true
	}
	if plan.generated == nil {
		return false
	}
	for root := range plan.invocationRoots {
		if pathWithinRoot(filePath, root) {
			return true
		}
	}
	return false
}

func emitProtoDescriptorProvenance(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	targets *declarationTargets,
	budget *materializationBudget,
	write func(json.RawMessage) error,
) error {
	if current.protocol != ProtocolGRPC || len(plan.protoGoCandidates) == 0 {
		return nil
	}
	base := generatedRecord{
		Schema:       resolvercatalog.RecordSchema,
		RecordSchema: GeneratedRecordSchema,
		Pack:         current.pack.Name, PackVersion: current.pack.Version,
		Kind: "proto_descriptor_provenance", Protocol: current.protocol,
	}
	if len(plan.protoGoCandidates) > MaxProtoDescriptorCandidates {
		record := base
		record.State, record.Reason = StateUnsupported, "proto_descriptor_candidate_limit_exceeded"
		return writeJSON(write, record)
	}
	candidates := slices.Clone(plan.protoGoCandidates)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })

	// 1. Find the client files; remember every directory's candidates.
	var clients []*protoGoClient
	byDir := make(map[string][]extract.CandidateManifestFile)
	oversizedDirs := make(map[string]bool)
	for _, file := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if plan.protoDescriptorGoverned(file.Path) {
			continue
		}
		dir := path.Dir(file.Path)
		byDir[dir] = append(byDir[dir], file)
		if file.DeclaredBytes > MaxGeneratedSymbolSourceBytes {
			oversizedDirs[dir] = true
			continue
		}
		content, err := plan.blobs.load(ctx, file, MaxGeneratedSymbolSourceBytes)
		if err != nil {
			return fmt.Errorf("read protobuf Go candidate %q: %w", file.Path, err)
		}
		text := string(content)
		if !strings.Contains(text, "grpc.ClientConn") {
			continue
		}
		client := &protoGoClient{
			file: file, digest: digestBytes(content),
			importPath: resolverinput.GoImportPath(plan.modules, file.Path),
		}
		symbols, parseErr := gocaller.DescribeGeneratedSource(
			string(ProtocolGRPC), file.Path, client.importPath, text,
		)
		switch {
		case parseErr != nil:
			client.state, client.reason = StateUnsupported, "generated_source_unparseable"
		case len(symbols) == 0:
			continue
		case repopath.Validate(symbols[0].GeneratorRelativePath) != nil:
			client.state, client.reason = StateUnsupported, "generated_source_unparseable"
			symbols = nil
		case client.importPath == "":
			client.state, client.reason = StateUnavailable, "module_path_unavailable"
		}
		client.symbols = symbols
		clients = append(clients, client)
	}
	if len(clients) == 0 {
		return nil
	}

	// 2. Decode the protoc-gen-go descriptors of the client directories.
	dirs := make(map[string]*protoDescriptorDir)
	for _, client := range clients {
		dir := path.Dir(client.file.Path)
		if _, done := dirs[dir]; done {
			continue
		}
		entry := &protoDescriptorDir{}
		dirs[dir] = entry
		if oversizedDirs[dir] {
			entry.problem = "proto_descriptor_source_too_large"
			continue
		}
		for _, file := range byDir[dir] {
			if err := ctx.Err(); err != nil {
				return err
			}
			content, err := plan.blobs.load(ctx, file, MaxGeneratedSymbolSourceBytes)
			if err != nil {
				return fmt.Errorf("read protobuf Go descriptor %q: %w", file.Path, err)
			}
			packageName, found, decodeErr := embeddedFileDescriptors(file.Path, content)
			if decodeErr != nil {
				entry.problem = "proto_descriptor_unparseable"
				break
			}
			for _, descriptor := range found {
				entry.descriptors = append(entry.descriptors, embeddedDescriptor{
					file: file, digest: digestBytes(content),
					packageName: packageName, descriptor: descriptor,
				})
			}
		}
	}

	// 3. Parse the declared .proto files a client's source path names.
	wanted := make(map[string]struct{})
	for _, client := range clients {
		if client.state == "" {
			wanted[client.symbols[0].GeneratorRelativePath] = struct{}{}
		}
	}
	declarations, err := protoDeclarationDescriptors(ctx, current, plan, targets, wanted)
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
		var source string
		if len(client.symbols) > 0 {
			source = client.symbols[0].GeneratorRelativePath
			record.GeneratorRelativePath = source
		}
		var declaration string
		if client.state == "" {
			var selected *embeddedDescriptor
			selected, client.state, client.reason = selectEmbeddedDescriptor(
				dirs[path.Dir(client.file.Path)], client.symbols,
			)
			if selected != nil {
				record.InputPath = selected.file.Path
				record.InputObjectID = selected.file.ObjectID
				record.InputDigest = selected.digest
				declaration, client.state, client.reason = selectProtoDeclaration(
					selected.descriptor, source, declarations,
				)
			}
		}
		if client.state == "" {
			lineages := make([]string, 0, len(targets.byPath[declaration]))
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
		if err := emitProtoDescriptorSymbols(
			ctx, current, client, record.Candidates, budget, write,
		); err != nil {
			return err
		}
	}
	return nil
}

func selectEmbeddedDescriptor(
	dir *protoDescriptorDir,
	symbols []gocaller.GeneratedSymbol,
) (*embeddedDescriptor, string, string) {
	if dir == nil {
		return nil, StateUnavailable, "proto_descriptor_missing"
	}
	if dir.problem != "" {
		return nil, StateUnsupported, dir.problem
	}
	source, packageName := symbols[0].GeneratorRelativePath, symbols[0].Package
	var matches []*embeddedDescriptor
	for index := range dir.descriptors {
		entry := &dir.descriptors[index]
		if entry.packageName == packageName && entry.descriptor.GetName() == source {
			matches = append(matches, entry)
		}
	}
	switch len(matches) {
	case 0:
		return nil, StateUnavailable, "proto_descriptor_missing"
	case 1:
	default:
		return nil, StateAmbiguous, "proto_descriptor_ambiguous"
	}
	operations := descriptorOperations(matches[0].descriptor)
	for _, symbol := range symbols {
		if _, declared := operations[symbol.Operation]; !declared {
			return nil, StateUnavailable, "proto_descriptor_operation_missing"
		}
	}
	return matches[0], "", ""
}

// descriptorOperations returns the gRPC full method names a descriptor
// declares ("/package.Service/Method").
func descriptorOperations(file *descriptorpb.FileDescriptorProto) map[string]struct{} {
	result := make(map[string]struct{})
	prefix := ""
	if file.GetPackage() != "" {
		prefix = file.GetPackage() + "."
	}
	for _, service := range file.GetService() {
		for _, method := range service.GetMethod() {
			result["/"+prefix+service.GetName()+"/"+method.GetName()] = struct{}{}
		}
	}
	return result
}

type protoDeclaration struct {
	descriptor *descriptorpb.FileDescriptorProto
	problem    string
}

func selectProtoDeclaration(
	generated *descriptorpb.FileDescriptorProto,
	source string,
	declarations map[string]protoDeclaration,
) (string, string, string) {
	var named, matched []string
	for declarationPath, declaration := range declarations {
		if declarationPath != source && !strings.HasSuffix(declarationPath, "/"+source) {
			continue
		}
		named = append(named, declarationPath)
		if declaration.problem != "" {
			return "", StateUnsupported, declaration.problem
		}
		if descriptorsEquivalent(generated, declaration.descriptor) {
			matched = append(matched, declarationPath)
		}
	}
	switch {
	case len(matched) == 1:
		return matched[0], "", ""
	case len(matched) > 1:
		return "", StateAmbiguous, "proto_declaration_ambiguous"
	case len(named) > 0:
		return "", StateUnavailable, "proto_descriptor_mismatch"
	default:
		return "", StateUnavailable, "proto_declaration_not_declared"
	}
}

// protoDeclarationDescriptors reads, from the declaration domain's candidate
// view, every declared .proto whose path is or ends with a wanted source path
// and parses it without linking.
func protoDeclarationDescriptors(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	targets *declarationTargets,
	wanted map[string]struct{},
) (map[string]protoDeclaration, error) {
	result := make(map[string]protoDeclaration)
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
		return nil, fmt.Errorf("discover declared proto files: %w", err)
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		content, err := plan.blobs.load(ctx, file, extract.MaxBlobBytes)
		if err != nil {
			return nil, fmt.Errorf("read declared proto file %q: %w", file.Path, err)
		}
		descriptor, parseErr := parseDeclaredProto(ctx, file.Path, string(content))
		if parseErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			result[file.Path] = protoDeclaration{problem: "proto_declaration_unparseable"}
			continue
		}
		result[file.Path] = protoDeclaration{descriptor: descriptor}
	}
	return result, nil
}

func parseDeclaredProto(
	ctx context.Context, filePath, content string,
) (*descriptorpb.FileDescriptorProto, error) {
	if err := idlpreflight.Validate(ctx, idlpreflight.Protobuf, content); err != nil {
		return nil, err
	}
	handler := reporter.NewHandler(nil)
	node, err := parser.Parse(filePath, strings.NewReader(content), handler)
	if err != nil {
		return nil, err
	}
	result, err := parser.ResultFromAST(node, true, handler)
	if err != nil {
		return nil, err
	}
	descriptor := result.FileDescriptorProto()
	if descriptor == nil {
		return nil, errors.New("parser returned no descriptor")
	}
	return descriptor, nil
}

// embeddedFileDescriptors returns the package clause and the protoc-gen-go
// FileDescriptorProtos a Go file embeds as top-level literals. A file with no
// such literal returns none; a literal that does not decode is an error.
func embeddedFileDescriptors(
	filePath string, content []byte,
) (string, []*descriptorpb.FileDescriptorProto, error) {
	text := string(content)
	if !strings.Contains(text, "_rawDesc") && !strings.Contains(text, "fileDescriptor") {
		return "", nil, nil
	}
	file, err := goparser.ParseFile(
		token.NewFileSet(), filePath, content, goparser.SkipObjectResolution,
	)
	if err != nil {
		return "", nil, err
	}
	var result []*descriptorpb.FileDescriptorProto
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR && gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			name := value.Names[0].Name
			legacy := legacyDescriptorName.MatchString(name)
			if !legacy && !rawDescriptorName.MatchString(name) {
				continue
			}
			raw, err := descriptorLiteralBytes(value.Values[0])
			if err != nil {
				return "", nil, fmt.Errorf("descriptor %s: %w", name, err)
			}
			if legacy {
				raw, err = gunzipDescriptor(raw)
				if err != nil {
					return "", nil, fmt.Errorf("descriptor %s: %w", name, err)
				}
			}
			descriptor := &descriptorpb.FileDescriptorProto{}
			if err := proto.Unmarshal(raw, descriptor); err != nil {
				return "", nil, fmt.Errorf("descriptor %s: %w", name, err)
			}
			if descriptor.GetName() == "" {
				return "", nil, fmt.Errorf("descriptor %s has no name", name)
			}
			result = append(result, descriptor)
		}
	}
	return file.Name.Name, result, nil
}

// descriptorLiteralBytes evaluates the literal forms protoc-gen-go emits:
// []byte{0x0a, ...}, a string literal, a concatenation of string literals,
// and string([]byte{...}) or []byte("...") conversions.
func descriptorLiteralBytes(expression ast.Expr) ([]byte, error) {
	var out []byte
	pending := []ast.Expr{expression}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		switch value := current.(type) {
		case *ast.ParenExpr:
			pending = append(pending, value.X)
		case *ast.BinaryExpr:
			if value.Op != token.ADD {
				return nil, errors.New("unsupported descriptor expression")
			}
			pending = append(pending, value.Y, value.X)
		case *ast.CallExpr:
			if len(value.Args) != 1 || !descriptorConversion(value.Fun) {
				return nil, errors.New("unsupported descriptor conversion")
			}
			pending = append(pending, value.Args[0])
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				return nil, errors.New("unsupported descriptor literal")
			}
			text, err := strconv.Unquote(value.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, text...)
		case *ast.CompositeLit:
			for _, element := range value.Elts {
				literal, ok := element.(*ast.BasicLit)
				if !ok || literal.Kind != token.INT {
					return nil, errors.New("unsupported descriptor byte")
				}
				number, err := strconv.ParseUint(literal.Value, 0, 8)
				if err != nil {
					return nil, err
				}
				out = append(out, byte(number))
			}
		default:
			return nil, errors.New("unsupported descriptor expression")
		}
		if len(out) > MaxProtoDescriptorBytes {
			return nil, errors.New("descriptor exceeds bound")
		}
	}
	return out, nil
}

func descriptorConversion(function ast.Expr) bool {
	switch value := function.(type) {
	case *ast.Ident:
		return value.Name == "string"
	case *ast.ArrayType:
		element, ok := value.Elt.(*ast.Ident)
		return ok && value.Len == nil && element.Name == "byte"
	}
	return false
}

func gunzipDescriptor(compressed []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxProtoDescriptorBytes+1))
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxProtoDescriptorBytes {
		return nil, errors.New("descriptor exceeds bound")
	}
	return raw, nil
}

func emitProtoDescriptorSymbols(
	ctx context.Context,
	current adapter,
	client *protoGoClient,
	candidates []generatedCandidate,
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
	if len(client.symbols) == 0 {
		base.Kind, base.State, base.Reason = "generated_symbol_input", client.state, client.reason
		if base.State == StateResolved || base.State == "" {
			return errors.New("protobuf Go client without symbols cannot resolve")
		}
		return writeGeneratedSymbol(write, budget, base)
	}
	for _, symbol := range client.symbols {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := base
		record.Kind = "generated_symbol"
		record.State, record.Reason = client.state, client.reason
		record.ImportPath, record.Package = symbol.ImportPath, symbol.Package
		record.ClientType, record.Method = symbol.ClientType, symbol.Method
		record.Operation = symbol.Operation
		record.Constructors = slices.Clone(symbol.Constructors)
		record.GeneratorRelativePath = symbol.GeneratorRelativePath
		if client.state == StateResolved && len(candidates) == 1 {
			record.DeclarationPath = candidates[0].DeclarationPath
			record.DeclarationLineage = candidates[0].DeclarationLineage
		} else if record.Reason == "" {
			record.Reason = "proto_descriptor_provenance_" + client.state
		}
		if err := writeGeneratedSymbol(write, budget, record); err != nil {
			return err
		}
	}
	return nil
}
