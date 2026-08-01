package resolvermaterialize

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
)

// CallerResolverView is the bounded, typed direct-caller projection built
// during resolvercatalog.OpenWithVisitor's descriptor-stable cold pass.
type CallerResolverView struct {
	protocol         Protocol
	generationDigest string
	manifestDigest   string
	descriptors      []gocaller.DirectDescriptor
	resolver         *gocaller.DirectResolver
}

func (view *CallerResolverView) Protocol() Protocol {
	if view == nil {
		return ""
	}
	return view.protocol
}

func (view *CallerResolverView) GenerationDigest() string {
	if view == nil {
		return ""
	}
	return view.generationDigest
}

func (view *CallerResolverView) ManifestDigest() string {
	if view == nil {
		return ""
	}
	return view.manifestDigest
}

func (view *CallerResolverView) Descriptors() []gocaller.DirectDescriptor {
	if view == nil {
		return nil
	}
	result := slices.Clone(view.descriptors)
	for index := range result {
		result[index].Constructors = slices.Clone(result[index].Constructors)
	}
	return result
}

// Resolver returns the immutable syntax index compiled once for all caller
// leaves and source blobs in this catalog generation.
func (view *CallerResolverView) Resolver() *gocaller.DirectResolver {
	if view == nil {
		return nil
	}
	return view.resolver
}

func ProtocolForCallerDomain(domain string) (Protocol, bool) {
	switch domain {
	case "grpc-caller":
		return ProtocolGRPC, true
	case "thrift-caller":
		return ProtocolThrift, true
	default:
		return "", false
	}
}

// OpenCallerResolver opens one exact catalog and builds the selected protocol
// projection during that same cold content-validation pass. No member is
// reopened or rehashed to produce the view.
func OpenCallerResolver(
	ctx context.Context,
	root string,
	expected resolvercatalog.State,
	protocol Protocol,
) (*resolvercatalog.Publication, *CallerResolverView, error) {
	publication, views, err := OpenCallerResolvers(
		ctx, root, expected, []Protocol{protocol},
	)
	if err != nil {
		return nil, nil, err
	}
	return publication, views[protocol], nil
}

// OpenCallerResolvers builds every requested protocol projection during one
// descriptor-stable catalog open. A repository with both caller domains never
// rereads or rehashes the catalog once per domain.
func OpenCallerResolvers(
	ctx context.Context,
	root string,
	expected resolvercatalog.State,
	protocols []Protocol,
) (*resolvercatalog.Publication, map[Protocol]*CallerResolverView, error) {
	if len(protocols) == 0 {
		return nil, nil, errors.New("caller resolver protocols are empty")
	}
	type route struct {
		protocol Protocol
		pack     resolvercatalog.ResolverPack
		view     *CallerResolverView
	}
	routes := make(map[string]route, len(protocols))
	views := make(map[Protocol]*CallerResolverView, len(protocols))
	for _, protocol := range protocols {
		if _, duplicate := views[protocol]; duplicate {
			return nil, nil, errors.New("caller resolver protocols are duplicated")
		}
		pack, err := callerPack(protocol)
		if err != nil {
			return nil, nil, err
		}
		if !slices.Contains(expected.ResolverPacks, pack) {
			return nil, nil, errors.New("resolver catalog does not contain the caller pack")
		}
		view := &CallerResolverView{
			protocol: protocol, generationDigest: expected.GenerationDigest,
			manifestDigest: expected.ManifestDigest,
		}
		views[protocol] = view
		routes[pack.Name+"-v2.ndjson"] = route{
			protocol: protocol, pack: pack, view: view,
		}
	}
	seen := make(map[[sha256.Size]byte]struct{})
	packages := make(map[string]string)
	generatedSources := make(map[string]string)
	identityBytes := 0
	descriptorCount := 0
	publication, err := resolvercatalog.OpenWithVisitor(
		ctx, root, expected,
		func(
			member resolvercatalog.MemberReceipt,
			_ int,
			raw json.RawMessage,
		) error {
			current, selected := routes[member.Name]
			if !selected {
				return nil
			}
			var envelope struct {
				RecordSchema string `json:"record_schema"`
				Pack         string `json:"pack"`
				PackVersion  string `json:"pack_version"`
				Kind         string `json:"kind"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return err
			}
			if envelope.Pack != current.pack.Name || envelope.PackVersion != PackVersion {
				return errors.New("caller resolver record has a stale pack identity")
			}
			if envelope.Kind != "generated_symbol" &&
				envelope.Kind != "generated_symbol_input" {
				return nil
			}
			if envelope.RecordSchema != GeneratedSymbolRecordSchema {
				return errors.New("caller resolver symbol has a stale schema")
			}
			var record generatedSymbolRecord
			if err := decodeExactRecord(raw, &record); err != nil {
				return err
			}
			if err := validateGeneratedSourceRecord(
				record, current.protocol, current.pack,
			); err != nil {
				return err
			}
			if prior, present := generatedSources[record.GeneratedPath]; present &&
				prior != record.GeneratedObjectID {
				return errors.New("caller resolver generated path names multiple objects")
			} else if !present {
				generatedSources[record.GeneratedPath] = record.GeneratedObjectID
				identityBytes += len(record.GeneratedPath) +
					len(record.GeneratedObjectID)
				if len(generatedSources) > MaxGeneratedSymbolSources ||
					identityBytes > MaxGeneratedSymbolIdentityBytes {
					return resolvercatalog.ErrLimit
				}
			}
			if record.Kind == "generated_symbol_input" {
				return nil
			}
			if err := validateGeneratedSymbolRecord(
				record, current.protocol, current.pack,
			); err != nil {
				return err
			}
			descriptor := directDescriptorFromRecord(record)
			if prior, present := packages[descriptor.ImportPath]; present &&
				prior != descriptor.Package {
				return errors.New("caller resolver import path names multiple packages")
			}
			packages[descriptor.ImportPath] = descriptor.Package
			key := sha256.Sum256([]byte(directDescriptorKey(descriptor)))
			if _, duplicate := seen[key]; duplicate {
				return errors.New("caller resolver symbol identity is duplicated")
			}
			seen[key] = struct{}{}
			identityBytes += generatedSymbolIdentityBytes(record)
			descriptorCount++
			if descriptorCount > MaxGeneratedSymbolDescriptors ||
				identityBytes > MaxGeneratedSymbolIdentityBytes {
				return resolvercatalog.ErrLimit
			}
			current.view.descriptors = append(current.view.descriptors, descriptor)
			return nil
		},
	)
	if err != nil {
		return nil, nil, err
	}
	members := publication.Manifest().Members
	for memberName, current := range routes {
		found := false
		for _, member := range members {
			if member.Name != memberName {
				continue
			}
			found = true
			if err := validateCallerMemberMetadata(
				member.Metadata, current.protocol, current.pack,
			); err != nil {
				return nil, nil, err
			}
			break
		}
		if !found {
			return nil, nil, errors.New("resolver catalog caller member is missing")
		}
	}
	sources := make([]gocaller.GeneratedSourceIdentity, 0, len(generatedSources))
	for path, objectID := range generatedSources {
		sources = append(sources, gocaller.GeneratedSourceIdentity{
			Path: path, ObjectID: objectID,
		})
	}
	slices.SortFunc(sources, func(left, right gocaller.GeneratedSourceIdentity) int {
		return strings.Compare(left.Path+"\x00"+left.ObjectID, right.Path+"\x00"+right.ObjectID)
	})
	for protocol, view := range views {
		view.resolver, err = gocaller.NewDirectResolverWithGeneratedSources(
			ctx, string(protocol), view.descriptors, sources,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("compile caller resolver %q: %w", protocol, err)
		}
	}
	return publication, views, nil
}

func validateGeneratedSourceRecord(
	record generatedSymbolRecord,
	protocol Protocol,
	pack resolvercatalog.ResolverPack,
) error {
	validDigest := validSHA256(record.GeneratedDigest)
	if record.Kind == "generated_symbol_input" &&
		record.State == StateUnsupported &&
		record.Reason == generatedSourceTooLargeReason &&
		record.GeneratedDigest == "" {
		validDigest = true
	}
	if record.Schema != resolvercatalog.RecordSchema ||
		record.RecordSchema != GeneratedSymbolRecordSchema ||
		record.Pack != pack.Name || record.PackVersion != pack.Version ||
		record.Protocol != protocol || repopath.Validate(record.GeneratedPath) != nil ||
		!gitobj.IsObjectID(record.GeneratedObjectID) ||
		!validDigest {
		return errors.New("caller resolver generated source identity is invalid")
	}
	if protocol == ProtocolGRPC && record.GeneratorRelativePath != "" &&
		(repopath.Validate(record.GeneratorRelativePath) != nil ||
			!strings.HasSuffix(record.GeneratorRelativePath, ".proto")) ||
		protocol == ProtocolThrift && record.GeneratorRelativePath != "" {
		return errors.New("caller resolver generated source selector is invalid")
	}
	if record.Kind == "generated_symbol_input" {
		if record.State != StateUnavailable && record.State != StateUnsupported ||
			strings.TrimSpace(record.Reason) == "" || record.ImportPath != "" ||
			record.Package != "" || record.ClientType != "" || record.Method != "" ||
			record.Operation != "" || len(record.Constructors) != 0 ||
			record.DeclarationPath != "" || record.DeclarationLineage != "" {
			return errors.New("caller resolver generated input abstention is invalid")
		}
		return nil
	}
	if record.Kind != "generated_symbol" {
		return errors.New("caller resolver generated source kind is invalid")
	}
	return nil
}

func callerPack(protocol Protocol) (resolvercatalog.ResolverPack, error) {
	switch protocol {
	case ProtocolGRPC:
		return resolvercatalog.ResolverPack{
			Name: GRPCGeneratedPackName, Version: PackVersion,
		}, nil
	case ProtocolThrift:
		return resolvercatalog.ResolverPack{
			Name: ThriftGeneratedPackName, Version: PackVersion,
		}, nil
	default:
		return resolvercatalog.ResolverPack{}, errors.New("unsupported caller resolver protocol")
	}
}

func decodeExactRecord(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("caller resolver record has trailing JSON")
	}
	return nil
}

func validateCallerMemberMetadata(
	raw json.RawMessage,
	protocol Protocol,
	pack resolvercatalog.ResolverPack,
) error {
	var metadata memberMetadata
	if err := decodeExactRecord(raw, &metadata); err != nil {
		return fmt.Errorf("decode caller resolver member metadata: %w", err)
	}
	if metadata.Schema != MemberMetadataSchema || metadata.Pack != pack.Name ||
		metadata.PackVersion != pack.Version || metadata.Protocol != protocol ||
		metadata.RecordSchema != ProtocolMemberRecordSchema ||
		!reflect.DeepEqual(metadata.Policy, FrozenMaterializationPolicy()) {
		return errors.New("caller resolver member metadata is stale")
	}
	return nil
}

func validateGeneratedSymbolRecord(
	record generatedSymbolRecord,
	protocol Protocol,
	pack resolvercatalog.ResolverPack,
) error {
	if record.Schema != resolvercatalog.RecordSchema ||
		record.RecordSchema != GeneratedSymbolRecordSchema ||
		record.Pack != pack.Name || record.PackVersion != pack.Version ||
		record.Kind != "generated_symbol" || record.Protocol != protocol ||
		record.ImportPath == "" || record.Package == "" ||
		record.ClientType == "" || record.Method == "" ||
		record.Operation == "" || repopath.Validate(record.GeneratedPath) != nil ||
		!gitobj.IsObjectID(record.GeneratedObjectID) ||
		!validSHA256(record.GeneratedDigest) {
		return errors.New("caller resolver symbol identity is invalid")
	}
	if protocol == ProtocolGRPC && (record.GeneratorRelativePath == "" ||
		repopath.Validate(record.GeneratorRelativePath) != nil ||
		!strings.HasSuffix(record.GeneratorRelativePath, ".proto")) ||
		protocol == ProtocolThrift && record.GeneratorRelativePath != "" {
		return errors.New("caller resolver symbol selector is invalid")
	}
	switch record.State {
	case StateResolved:
		if record.Reason != "" || repopath.Validate(record.DeclarationPath) != nil ||
			record.DeclarationLineage == "" {
			return errors.New("resolved caller resolver symbol lacks declaration authority")
		}
	case StateUnavailable, StateAmbiguous, StateUnsupported:
		if strings.TrimSpace(record.Reason) == "" ||
			record.DeclarationPath != "" || record.DeclarationLineage != "" {
			return errors.New("non-resolved caller resolver symbol is not an abstention")
		}
	default:
		return errors.New("caller resolver symbol has an unsupported state")
	}
	for index, constructor := range record.Constructors {
		if constructor == "" ||
			index > 0 && record.Constructors[index-1] >= constructor {
			return errors.New("caller resolver constructors are not canonical")
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func directDescriptorFromRecord(record generatedSymbolRecord) gocaller.DirectDescriptor {
	return gocaller.DirectDescriptor{
		State: record.State, Reason: record.Reason, Protocol: string(record.Protocol),
		ImportPath: record.ImportPath, Package: record.Package,
		ClientType: record.ClientType, Method: record.Method,
		Operation: record.Operation, Constructors: slices.Clone(record.Constructors),
		GeneratedPath:         record.GeneratedPath,
		GeneratedObjectID:     record.GeneratedObjectID,
		GeneratedDigest:       record.GeneratedDigest,
		GeneratorRelativePath: record.GeneratorRelativePath,
		DeclarationPath:       record.DeclarationPath,
		DeclarationLineage:    record.DeclarationLineage,
	}
}

func directDescriptorKey(value gocaller.DirectDescriptor) string {
	return strings.Join([]string{
		value.Protocol, value.ImportPath, value.ClientType, value.Method,
		value.Operation, value.GeneratedPath, value.GeneratorRelativePath,
		value.DeclarationPath, value.DeclarationLineage, value.State, value.Reason,
	}, "\x00")
}
