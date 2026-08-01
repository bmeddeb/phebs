// Package resolvermaterialize builds the bounded resolver catalog consumed by
// repository-overlay caller workers. It reads only immutable candidate rows,
// exact published declaration assertions, and the committed module/generated
// attribution blobs named by those rows.
package resolvermaterialize

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolverinput"
)

const (
	PackVersion = "1.1.0"

	GoModulePackName            = "go-module"
	GRPCGeneratedPackName       = "grpc-generated-attribution"
	ThriftGeneratedPackName     = "thrift-generated-attribution"
	GoModuleRecordSchema        = "phebs-resolver-go-module-v1"
	GeneratedRecordSchema       = "phebs-resolver-generated-attribution-v2"
	GeneratedSymbolRecordSchema = "phebs-resolver-generated-go-symbol-v1"
	DeclarationRecordSchema     = "phebs-resolver-declaration-target-v1"
	ProtocolMemberRecordSchema  = "phebs-resolver-protocol-member-v2"
	MemberMetadataSchema        = "phebs-resolver-member-metadata-v2"
	MaterializationPolicyName   = "phebs-resolver-materialization-policy-v2"

	StateResolved    = "resolved"
	StateUnavailable = "unavailable"
	StateAmbiguous   = "ambiguous"
	StateUnsupported = "unsupported"

	generatedSourceTooLargeReason = "generated_source_too_large"

	// The output lifecycle independently caps member and catalog bytes. These
	// limits bound the immutable source work performed before that output can
	// be known, and are pack-version identity below.
	MaxInputBlobReads                           = 100_000
	MaxInputBlobBytes                     int64 = 512 << 20
	MaxLayoutRoots                              = 128
	MaxGeneratedMappings                        = 25_000
	MaxGeneratorInvocations                     = 128
	MaxGeneratedCandidatesPerSelector           = 1_024
	MaxGeneratedCandidateBytesPerSelector       = 128 << 10
	MaxGeneratedCandidateExpansions             = 25_000
	MaxGeneratedCandidateBytes                  = 16 << 20
	MaxDeclarationRecords                       = 25_000
	MaxDeclarationPathBytes                     = 16 << 20
	MaxGeneratedSymbolDescriptors               = 100_000
	MaxGeneratedSymbolIdentityBytes             = 32 << 20
	MaxGeneratedSymbolSources                   = 25_000
	MaxGeneratedSymbolSourceBytes         int64 = gocaller.MaxDirectGeneratedSourceBytes
	MaterializationTimeout                      = 5 * time.Minute
)

type Protocol string

const (
	ProtocolGRPC   Protocol = "grpc"
	ProtocolThrift Protocol = "thrift"
)

type adapter struct {
	protocol          Protocol
	pack              resolvercatalog.ResolverPack
	callerDomain      string
	callerVersion     string
	declarationDomain string
}

// Registry is the one ordered adapter generation shared by startup
// reconciliation, the catalog worker, and the candidate provider request.
// It is compiled from the already validated extraction registry rather than
// reconstructing feature flags independently.
type Registry struct {
	adapters         []adapter
	packs            []resolvercatalog.ResolverPack
	candidateDomains []extract.CandidateManifestDomain
}

func NewRegistry(extractors []extract.Extractor) (*Registry, error) {
	domains := make([]extract.CandidateManifestDomain, 0, len(extractors))
	versions := make(map[string]string, len(extractors))
	for index, extractor := range extractors {
		if extractor == nil {
			return nil, fmt.Errorf("resolver registry extractor %d is nil", index)
		}
		domain, version := extractor.Domain(), extractor.Version()
		if strings.TrimSpace(domain) != domain || domain == "" ||
			strings.TrimSpace(version) != version || version == "" {
			return nil, fmt.Errorf("resolver registry extractor %d has invalid identity", index)
		}
		if prior, duplicate := versions[domain]; duplicate {
			return nil, fmt.Errorf(
				"resolver registry domain %q is duplicated at %q and %q",
				domain, prior, version,
			)
		}
		versions[domain] = version
		domains = append(domains, extract.CandidateManifestDomain{
			Domain: domain, Version: version,
		})
	}
	sort.Slice(domains, func(i, j int) bool {
		if domains[i].Domain != domains[j].Domain {
			return domains[i].Domain < domains[j].Domain
		}
		return domains[i].Version < domains[j].Version
	})

	registry := &Registry{candidateDomains: domains}
	for _, item := range []struct {
		caller, declaration, pack string
		protocol                  Protocol
	}{
		{"grpc-caller", "proto-contract", GRPCGeneratedPackName, ProtocolGRPC},
		{"thrift-caller", "thrift-contract", ThriftGeneratedPackName, ProtocolThrift},
	} {
		callerVersion, enabled := versions[item.caller]
		if !enabled {
			continue
		}
		if _, declarationEnabled := versions[item.declaration]; !declarationEnabled {
			return nil, fmt.Errorf(
				"resolver caller %q requires declaration domain %q",
				item.caller, item.declaration,
			)
		}
		registry.adapters = append(registry.adapters, adapter{
			protocol: item.protocol,
			pack: resolvercatalog.ResolverPack{
				Name: item.pack, Version: PackVersion,
			},
			callerDomain:      callerVersionDomain(item.caller),
			callerVersion:     callerVersion,
			declarationDomain: item.declaration,
		})
	}
	if len(registry.adapters) == 0 {
		return registry, nil
	}
	registry.packs = append(registry.packs,
		resolvercatalog.ResolverPack{Name: GoModulePackName, Version: PackVersion},
	)
	for _, current := range registry.adapters {
		registry.packs = append(registry.packs, current.pack)
	}
	sort.Slice(registry.packs, func(i, j int) bool {
		return registry.packs[i].Name < registry.packs[j].Name
	})
	for index := 1; index < len(registry.packs); index++ {
		if registry.packs[index-1].Name == registry.packs[index].Name {
			return nil, errors.New("resolver registry produced duplicate packs")
		}
	}
	return registry, nil
}

func callerVersionDomain(domain string) string { return domain }

func (registry *Registry) Enabled() bool {
	return registry != nil && len(registry.adapters) > 0
}

func (registry *Registry) Packs() []resolvercatalog.ResolverPack {
	if registry == nil {
		return nil
	}
	return slices.Clone(registry.packs)
}

func (registry *Registry) CandidateDomains() []extract.CandidateManifestDomain {
	if registry == nil {
		return nil
	}
	return slices.Clone(registry.candidateDomains)
}

func (registry *Registry) validate() error {
	if registry == nil || len(registry.adapters) == 0 ||
		len(registry.packs) != len(registry.adapters)+1 ||
		len(registry.candidateDomains) == 0 {
		return errors.New("resolver registry is empty or incomplete")
	}
	for index, pack := range registry.packs {
		if pack.Name == "" || pack.Version != PackVersion ||
			index > 0 && registry.packs[index-1].Name >= pack.Name {
			return errors.New("resolver registry pack set is invalid or unordered")
		}
	}
	for index, current := range registry.adapters {
		if current.callerDomain == "" || current.callerVersion == "" ||
			current.declarationDomain == "" ||
			current.pack.Version != PackVersion ||
			current.protocol != ProtocolGRPC && current.protocol != ProtocolThrift ||
			index > 0 && registry.adapters[index-1].protocol >= current.protocol {
			return errors.New("resolver registry adapters are invalid or unordered")
		}
	}
	return nil
}

type MaterializationPolicy struct {
	Name                                  string `json:"name"`
	ModuleRecordSchema                    string `json:"module_record_schema"`
	GeneratedRecordSchema                 string `json:"generated_record_schema"`
	GeneratedSymbolRecordSchema           string `json:"generated_symbol_record_schema"`
	DeclarationRecordSchema               string `json:"declaration_record_schema"`
	ProtocolMemberSchema                  string `json:"protocol_member_schema"`
	StateVocabulary                       string `json:"state_vocabulary"`
	ModuleParsing                         string `json:"module_parsing"`
	SnapshotDecoding                      string `json:"snapshot_decoding"`
	ModuleOrdering                        string `json:"module_ordering"`
	GeneratedOrdering                     string `json:"generated_ordering"`
	GeneratedSymbolIdentityAccounting     string `json:"generated_symbol_identity_accounting"`
	MaxGoModuleBytes                      int    `json:"max_go_module_bytes"`
	MaxSnapshotBlobBytes                  int64  `json:"max_snapshot_blob_bytes"`
	MaxInputBlobReads                     int    `json:"max_input_blob_reads"`
	MaxInputBlobBytes                     int64  `json:"max_input_blob_bytes"`
	MaxLayoutRoots                        int    `json:"max_layout_roots"`
	MaxGeneratedMappings                  int    `json:"max_generated_mappings"`
	MaxGeneratorInvocations               int    `json:"max_generator_invocations"`
	MaxGeneratedCandidatesPerSelector     int    `json:"max_generated_candidates_per_selector"`
	MaxGeneratedCandidateBytesPerSelector int    `json:"max_generated_candidate_bytes_per_selector"`
	MaxGeneratedCandidateExpansions       int    `json:"max_generated_candidate_expansions"`
	MaxGeneratedCandidateBytes            int    `json:"max_generated_candidate_bytes"`
	MaxDeclarationRecords                 int    `json:"max_declaration_records"`
	MaxDeclarationPathBytes               int    `json:"max_declaration_path_bytes"`
	MaxGeneratedSymbolDescriptors         int    `json:"max_generated_symbol_descriptors"`
	MaxGeneratedSymbolIdentityBytes       int    `json:"max_generated_symbol_identity_bytes"`
	MaxGeneratedSymbolSources             int    `json:"max_generated_symbol_sources"`
	MaxGeneratedSymbolSourceBytes         int64  `json:"max_generated_symbol_source_bytes"`
	TimeoutMilliseconds                   int64  `json:"timeout_milliseconds"`
}

func FrozenMaterializationPolicy() MaterializationPolicy {
	return MaterializationPolicy{
		Name:                                  MaterializationPolicyName,
		ModuleRecordSchema:                    GoModuleRecordSchema,
		GeneratedRecordSchema:                 GeneratedRecordSchema,
		GeneratedSymbolRecordSchema:           GeneratedSymbolRecordSchema,
		DeclarationRecordSchema:               DeclarationRecordSchema,
		ProtocolMemberSchema:                  ProtocolMemberRecordSchema,
		StateVocabulary:                       "resolved-unavailable-ambiguous-unsupported-v1",
		ModuleParsing:                         "single-line-strict-token-factored-unsupported-v1",
		SnapshotDecoding:                      "exact-unique-keys-bounded-stream-v1",
		ModuleOrdering:                        "candidate-caller-order-v1",
		GeneratedOrdering:                     "declaration-input-mapping-symbol-sorted-candidates-invocation-v2",
		GeneratedSymbolIdentityAccounting:     "catalog-wide-unique-source-plus-descriptor-v1",
		MaxGoModuleBytes:                      resolverinput.MaxGoModuleBytes,
		MaxSnapshotBlobBytes:                  extract.MaxBlobBytes,
		MaxInputBlobReads:                     MaxInputBlobReads,
		MaxInputBlobBytes:                     MaxInputBlobBytes,
		MaxLayoutRoots:                        MaxLayoutRoots,
		MaxGeneratedMappings:                  MaxGeneratedMappings,
		MaxGeneratorInvocations:               MaxGeneratorInvocations,
		MaxGeneratedCandidatesPerSelector:     MaxGeneratedCandidatesPerSelector,
		MaxGeneratedCandidateBytesPerSelector: MaxGeneratedCandidateBytesPerSelector,
		MaxGeneratedCandidateExpansions:       MaxGeneratedCandidateExpansions,
		MaxGeneratedCandidateBytes:            MaxGeneratedCandidateBytes,
		MaxDeclarationRecords:                 MaxDeclarationRecords,
		MaxDeclarationPathBytes:               MaxDeclarationPathBytes,
		MaxGeneratedSymbolDescriptors:         MaxGeneratedSymbolDescriptors,
		MaxGeneratedSymbolIdentityBytes:       MaxGeneratedSymbolIdentityBytes,
		MaxGeneratedSymbolSources:             MaxGeneratedSymbolSources,
		MaxGeneratedSymbolSourceBytes:         MaxGeneratedSymbolSourceBytes,
		TimeoutMilliseconds:                   MaterializationTimeout.Milliseconds(),
	}
}

type memberMetadata struct {
	Schema       string                `json:"schema"`
	Pack         string                `json:"pack"`
	PackVersion  string                `json:"pack_version"`
	Protocol     Protocol              `json:"protocol,omitempty"`
	RecordSchema string                `json:"record_schema"`
	Policy       MaterializationPolicy `json:"policy"`
}

type moduleRecord struct {
	Schema        string `json:"schema"`
	RecordSchema  string `json:"record_schema"`
	Pack          string `json:"pack"`
	PackVersion   string `json:"pack_version"`
	Kind          string `json:"kind"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	Path          string `json:"path,omitempty"`
	ObjectID      string `json:"object_id,omitempty"`
	ContentDigest string `json:"content_digest,omitempty"`
	ModuleRoot    string `json:"module_root,omitempty"`
	ModulePath    string `json:"module_path,omitempty"`
}

type declarationRecord struct {
	Schema       string   `json:"schema"`
	RecordSchema string   `json:"record_schema"`
	Pack         string   `json:"pack"`
	PackVersion  string   `json:"pack_version"`
	Kind         string   `json:"kind"`
	State        string   `json:"state"`
	Protocol     Protocol `json:"protocol"`
	Path         string   `json:"path"`
	Lineage      string   `json:"lineage"`
	Operation    string   `json:"operation"`
}

type generatedCandidate struct {
	DeclarationPath    string `json:"declaration_path"`
	DeclarationLineage string `json:"declaration_lineage"`
}

type generatedRecord struct {
	Schema                  string               `json:"schema"`
	RecordSchema            string               `json:"record_schema"`
	Pack                    string               `json:"pack"`
	PackVersion             string               `json:"pack_version"`
	Kind                    string               `json:"kind"`
	State                   string               `json:"state"`
	Reason                  string               `json:"reason,omitempty"`
	Protocol                Protocol             `json:"protocol"`
	InputPath               string               `json:"input_path,omitempty"`
	InputObjectID           string               `json:"input_object_id,omitempty"`
	InputDigest             string               `json:"input_digest,omitempty"`
	GeneratedPath           string               `json:"generated_path,omitempty"`
	GeneratorRelativePath   string               `json:"generator_relative_path,omitempty"`
	GeneratedRoot           string               `json:"generated_root,omitempty"`
	GeneratorInvocationRoot string               `json:"generator_invocation_root,omitempty"`
	Candidates              []generatedCandidate `json:"candidates,omitempty"`
}

type generatedSymbolRecord struct {
	Schema                string   `json:"schema"`
	RecordSchema          string   `json:"record_schema"`
	Pack                  string   `json:"pack"`
	PackVersion           string   `json:"pack_version"`
	Kind                  string   `json:"kind"`
	State                 string   `json:"state"`
	Reason                string   `json:"reason,omitempty"`
	Protocol              Protocol `json:"protocol"`
	ImportPath            string   `json:"import_path,omitempty"`
	Package               string   `json:"package,omitempty"`
	ClientType            string   `json:"client_type,omitempty"`
	Method                string   `json:"method,omitempty"`
	Operation             string   `json:"operation,omitempty"`
	Constructors          []string `json:"constructors,omitempty"`
	GeneratedPath         string   `json:"generated_path"`
	GeneratedObjectID     string   `json:"generated_object_id"`
	GeneratedDigest       string   `json:"generated_digest"`
	GeneratorRelativePath string   `json:"generator_relative_path,omitempty"`
	DeclarationPath       string   `json:"declaration_path,omitempty"`
	DeclarationLineage    string   `json:"declaration_lineage,omitempty"`
}
