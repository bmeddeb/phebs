package gocaller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const MaxDirectGeneratedSourceBytes = maxGeneratedBytes

// GeneratedSymbol is the syntax-only descriptor extracted from one generated
// base-lane Go source. Attribution and immutable blob provenance are added by
// the resolver materializer, which already owns those exact inputs.
type GeneratedSymbol struct {
	Protocol              string
	ImportPath            string
	Package               string
	ClientType            string
	Method                string
	Operation             string
	Constructors          []string
	GeneratedPath         string
	GeneratorRelativePath string
}

// DirectDescriptor is one exact resolver-catalog symbol projection consumed
// by direct caller-leaf syntax scanning. Non-resolved descriptors deliberately
// retain enough symbol identity to turn matching calls into abstentions.
type DirectDescriptor struct {
	State                 string
	Reason                string
	Protocol              string
	ImportPath            string
	Package               string
	ClientType            string
	Method                string
	Operation             string
	Constructors          []string
	GeneratedPath         string
	GeneratedObjectID     string
	GeneratedDigest       string
	GeneratorRelativePath string
	DeclarationPath       string
	DeclarationLineage    string
}

// GeneratedSourceIdentity is one exact catalog-owned generated input. The
// complete catalog-wide set is shared with every protocol resolver so no
// caller domain scans another adapter's generated code as consumer source.
type GeneratedSourceIdentity struct {
	Path     string
	ObjectID string
}

// DirectResolver is an immutable, reusable syntax lookup compiled once from a
// bounded resolver-catalog projection. Sharing it across every source file in
// a caller leaf avoids rebuilding a repository-wide descriptor index per blob.
type DirectResolver struct {
	protocol         string
	index            generatedSyntaxIndex
	generatedSources map[string]string
}

type directMethodIdentity struct {
	operation, lineage, reason                 string
	generatedPath, declarationPath, generation string
}

func directBindingIdentity(binding symbolBinding) directMethodIdentity {
	identity := directMethodIdentity{
		operation: binding.operation, lineage: binding.lineage, reason: binding.reason,
	}
	if binding.strictProvenance {
		identity.generatedPath = binding.generatedPath
		identity.declarationPath = binding.declarationPath
		identity.generation = binding.generatorRelative
	}
	return identity
}

// DirectScanRequest provides exactly one candidate-leaf source blob and the
// already validated resolver projection. It contains no corpus, tree-walk, or
// SCIP capability.
type DirectScanRequest struct {
	Protocol              string
	Path                  string
	Blob                  sdk.Blob
	Resolver              *DirectResolver
	Descriptors           []DirectDescriptor
	ResolverCatalogDigest string
	ConsumerUnits         func(string, int) sdk.ConsumerUnitAttribution
}

type DirectScanResult struct {
	Emitted     int
	Abstentions int
}

type syntaxAttribution struct{}

func (syntaxAttribution) GeneratedFrom(
	protocol, generatedPath, generatorRelativePath string,
) sdk.GeneratedFromAttribution {
	return sdk.GeneratedFromAttribution{
		State: sdk.AttributionStateResolved,
		Candidates: []sdk.GeneratedFromCandidate{{
			Protocol: protocol, GeneratedPath: generatedPath,
			GeneratorRelativePath: generatorRelativePath,
			DeclarationPath:       "__syntax__", DeclarationLineage: "__syntax__",
		}},
	}
}

// DescribeGeneratedSource parses one mapped generated Go blob exactly once
// and returns deterministic wire-symbol descriptors. It performs no I/O and
// never consults SCIP.
func DescribeGeneratedSource(
	protocol, generatedPath, importPath, content string,
) ([]GeneratedSymbol, error) {
	if generatedPath == "" {
		return nil, errors.New("generated path is required")
	}
	if len(content) > maxGeneratedBytes {
		return nil, errors.New("generated source exceeds syntax bound")
	}
	if !utf8.ValidString(content) {
		return nil, errors.New("generated source is not valid UTF-8")
	}
	var (
		methods      []generatedMethod
		constructors map[string][]string
		err          error
	)
	switch protocol {
	case "grpc":
		methods, constructors, err = grpcSyntaxMethods(
			generatedPath, importPath, content, syntaxAttribution{},
		)
	case "thrift":
		methods, constructors, err = thriftSyntaxMethods(
			generatedPath, importPath, content, syntaxAttribution{},
		)
	default:
		return nil, fmt.Errorf("unsupported direct caller protocol %q", protocol)
	}
	if err != nil {
		return nil, err
	}
	constructorByClient := make(map[string][]string)
	for constructor, clientTypes := range constructors {
		for _, clientType := range clientTypes {
			constructorByClient[clientType] = appendUnique(
				constructorByClient[clientType], constructor,
			)
		}
	}
	result := make([]GeneratedSymbol, 0, len(methods))
	for _, method := range methods {
		values := append([]string(nil), constructorByClient[method.clientType]...)
		sort.Strings(values)
		result = append(result, GeneratedSymbol{
			Protocol: protocol, ImportPath: method.importPath,
			Package: method.packageName, ClientType: method.clientType,
			Method: method.method, Operation: method.binding.operation,
			Constructors: values, GeneratedPath: generatedPath,
			GeneratorRelativePath: method.binding.generatorRelative,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.ImportPath != right.ImportPath {
			return left.ImportPath < right.ImportPath
		}
		if left.ClientType != right.ClientType {
			return left.ClientType < right.ClientType
		}
		if left.Method != right.Method {
			return left.Method < right.Method
		}
		return left.Operation < right.Operation
	})
	return result, nil
}

type directAttribution struct {
	consumer func(string, int) sdk.ConsumerUnitAttribution
}

func (directAttribution) GeneratedFrom(_, _, _ string) sdk.GeneratedFromAttribution {
	return sdk.GeneratedFromAttribution{State: sdk.AttributionStateUnavailable}
}

func (value directAttribution) ConsumerUnits(
	path string,
	line int,
) sdk.ConsumerUnitAttribution {
	if value.consumer == nil {
		return sdk.ConsumerUnitAttribution{
			State:  sdk.AttributionStateUnavailable,
			Reason: "consumer_unit_projection_unavailable",
		}
	}
	return value.consumer(path, line)
}

// NewDirectResolver validates and compiles one protocol's exact descriptor
// projection. The returned resolver is immutable and safe for concurrent
// source-file scans.
func NewDirectResolver(
	ctx context.Context,
	protocol string,
	descriptors []DirectDescriptor,
) (*DirectResolver, error) {
	return NewDirectResolverWithGeneratedSources(ctx, protocol, descriptors, nil)
}

// NewDirectResolverWithGeneratedSources compiles one protocol's descriptors
// while retaining the catalog-wide generated input exclusion set.
func NewDirectResolverWithGeneratedSources(
	ctx context.Context,
	protocol string,
	descriptors []DirectDescriptor,
	generated []GeneratedSourceIdentity,
) (*DirectResolver, error) {
	if protocol != "grpc" && protocol != "thrift" {
		return nil, errors.New("unsupported direct caller protocol")
	}
	index := generatedSyntaxIndex{
		methods:        make(map[string][]generatedMethod),
		methodFallback: make(map[string][]generatedMethod),
		packageNames:   make(map[string]string),
		constructors:   make(map[string][]string),
	}
	packages := make(map[string]string)
	generatedSources := make(map[string]string)
	for position, source := range generated {
		if position%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if source.Path == "" || source.ObjectID == "" {
			return nil, errors.New("generated source identity is incomplete")
		}
		if prior, present := generatedSources[source.Path]; present &&
			prior != source.ObjectID {
			return nil, errors.New(
				"generated source identities disagree on immutable object",
			)
		}
		generatedSources[source.Path] = source.ObjectID
	}
	methodSeen := make(map[string]map[directMethodIdentity]struct{})
	constructorTypes := make(map[string]map[string]struct{})
	for position, descriptor := range descriptors {
		if position%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if err := validateDirectDescriptor(protocol, descriptor); err != nil {
			return nil, fmt.Errorf("descriptor %d: %w", position, err)
		}
		if prior, present := generatedSources[descriptor.GeneratedPath]; present &&
			prior != descriptor.GeneratedObjectID {
			return nil, errors.New(
				"direct descriptors disagree on generated source identity",
			)
		}
		generatedSources[descriptor.GeneratedPath] = descriptor.GeneratedObjectID
		if prior, present := packages[descriptor.ImportPath]; present &&
			prior != descriptor.Package {
			return nil, errors.New(
				"direct descriptors name multiple packages for one import path",
			)
		}
		packages[descriptor.ImportPath] = descriptor.Package
		reason := descriptor.Reason
		if descriptor.State != "resolved" && reason == "" {
			reason = "resolver_descriptor_" + descriptor.State
		}
		binding := symbolBinding{
			symbol:    descriptor.ImportPath + "." + descriptor.ClientType + "." + descriptor.Method,
			operation: descriptor.Operation, lineage: descriptor.DeclarationLineage,
			generatedPath:     descriptor.GeneratedPath,
			declarationPath:   descriptor.DeclarationPath,
			generatorRelative: descriptor.GeneratorRelativePath,
			reason:            reason,
			strictProvenance:  true,
		}
		key := syntaxMethodKey(
			descriptor.ImportPath, descriptor.ClientType, descriptor.Method,
		)
		identity := directBindingIdentity(binding)
		identities := methodSeen[key]
		if identities == nil {
			identities = make(map[directMethodIdentity]struct{})
			methodSeen[key] = identities
		}
		if _, duplicate := identities[identity]; !duplicate {
			identities[identity] = struct{}{}
			method := generatedMethod{
				importPath: descriptor.ImportPath, packageName: descriptor.Package,
				clientType: descriptor.ClientType, method: descriptor.Method,
				binding: binding,
			}
			index.methods[key] = append(index.methods[key], method)
			fallbackKey := syntaxFallbackKey(descriptor.ImportPath, descriptor.Method)
			index.methodFallback[fallbackKey] = append(
				index.methodFallback[fallbackKey], method,
			)
		}
		index.packageNames[descriptor.ImportPath] = descriptor.Package
		for _, constructor := range descriptor.Constructors {
			constructorKey := descriptor.ImportPath + "\x00" + constructor
			types := constructorTypes[constructorKey]
			if types == nil {
				types = make(map[string]struct{})
				constructorTypes[constructorKey] = types
			}
			types[descriptor.ClientType] = struct{}{}
		}
	}
	for key, types := range constructorTypes {
		values := make([]string, 0, len(types))
		for clientType := range types {
			values = append(values, clientType)
		}
		sort.Strings(values)
		index.constructors[key] = values
	}
	return &DirectResolver{
		protocol: protocol, index: index, generatedSources: generatedSources,
	}, nil
}

// GeneratedSource reports whether one exact candidate record is catalog-owned
// generated input. A path match with a different immutable object is stale
// authority, not ordinary consumer source.
func (resolver *DirectResolver) GeneratedSource(path, objectID string) (bool, error) {
	if resolver == nil {
		return false, errors.New("direct resolver is nil")
	}
	wanted, present := resolver.generatedSources[path]
	if !present {
		return false, nil
	}
	if wanted != objectID {
		return false, errors.New("generated source object disagrees with resolver catalog")
	}
	return true, nil
}

// ScanDirectSyntaxFile scans one base-lane source blob against exact catalog
// descriptors. Ambiguous or otherwise non-resolved descriptor matches emit
// UNRESOLVED_CALLER records; they are never promoted to calls or silently
// selected. The API has no typed-SCIP input by construction.
func ScanDirectSyntaxFile(
	ctx context.Context,
	request DirectScanRequest,
	emit sdk.Emit,
) (DirectScanResult, error) {
	if err := ctx.Err(); err != nil {
		return DirectScanResult{}, err
	}
	if request.Protocol != "grpc" && request.Protocol != "thrift" {
		return DirectScanResult{}, errors.New("unsupported direct caller protocol")
	}
	if request.Path == "" || request.Blob.Digest == "" || emit == nil {
		return DirectScanResult{}, errors.New("direct caller source input is incomplete")
	}
	if len(request.Blob.Content) > maxSourceBytes {
		return DirectScanResult{}, errors.New("direct caller source exceeds syntax bound")
	}
	if !utf8.ValidString(request.Blob.Content) {
		return DirectScanResult{}, errors.New("direct caller source is not valid UTF-8")
	}
	resolver := request.Resolver
	if resolver == nil {
		var err error
		resolver, err = NewDirectResolver(ctx, request.Protocol, request.Descriptors)
		if err != nil {
			return DirectScanResult{}, err
		}
	} else if len(request.Descriptors) != 0 {
		return DirectScanResult{}, errors.New(
			"direct caller request supplies both compiled and raw resolvers",
		)
	} else if resolver.protocol != request.Protocol {
		return DirectScanResult{}, errors.New("direct caller resolver protocol mismatch")
	}

	result := DirectScanResult{}
	wrappedEmit := func(fact sdk.Fact) error {
		if err := emit(fact); err != nil {
			return err
		}
		result.Emitted++
		if fact.Assertion.Predicate == "UNRESOLVED_CALLER" {
			result.Abstentions++
		}
		return nil
	}
	e := extractor{protocol: request.Protocol}
	_, err := e.scanSyntaxFile(
		request.Path, request.Blob, nil, resolver.index,
		directAttribution{consumer: request.ConsumerUnits},
		request.ResolverCatalogDigest, "direct-syntax", wrappedEmit,
	)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func validateDirectDescriptor(protocol string, descriptor DirectDescriptor) error {
	if descriptor.Protocol != protocol || descriptor.ImportPath == "" ||
		descriptor.Package == "" || descriptor.ClientType == "" ||
		descriptor.Method == "" || descriptor.Operation == "" ||
		descriptor.GeneratedPath == "" || descriptor.GeneratedObjectID == "" ||
		descriptor.GeneratedDigest == "" {
		return errors.New("direct descriptor identity is incomplete")
	}
	switch descriptor.State {
	case "resolved":
		if descriptor.Reason != "" || descriptor.DeclarationPath == "" ||
			descriptor.DeclarationLineage == "" {
			return errors.New("resolved direct descriptor lacks declaration authority")
		}
	case "unavailable", "ambiguous", "unsupported":
		if strings.TrimSpace(descriptor.Reason) == "" {
			return errors.New("non-resolved direct descriptor lacks a reason")
		}
	default:
		return errors.New("direct descriptor has an unsupported state")
	}
	for index, constructor := range descriptor.Constructors {
		if constructor == "" || index > 0 && descriptor.Constructors[index-1] >= constructor {
			return errors.New("direct descriptor constructors are not canonical")
		}
	}
	return nil
}
