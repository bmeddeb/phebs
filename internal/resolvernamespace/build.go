package resolvernamespace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
)

type BuildRequest struct {
	Root                     string
	Repository               string
	Commit                   string
	ResolverGenerationDigest string
	ResolverManifestDigest   string
	Descriptors              []gocaller.DirectDescriptor
	Prior                    *Publication
}

// Prepared is a completely validated but invisible generation stage.
type Prepared struct {
	root       string
	repository string
	directory  string
	rootValue  Root
	closed     bool
}

func Build(ctx context.Context, request BuildRequest) (*Prepared, error) {
	if request.Root == "" {
		return nil, errors.New("resolver namespace root is required")
	}
	authority, err := newAuthority(
		request.Repository, request.Commit, request.ResolverGenerationDigest,
		request.ResolverManifestDigest,
	)
	if err != nil {
		return nil, err
	}
	records, err := normalizeDescriptors(request.Descriptors)
	if err != nil {
		return nil, err
	}
	if len(records) > MaxRecords {
		return nil, ErrLimit
	}
	byNamespace := make(map[string][]Record)
	for _, record := range records {
		key := namespaceKey(record.Language, record.Protocol, record.Namespace)
		if _, present := byNamespace[key]; !present && len(byNamespace) >= MaxNamespaces {
			return nil, ErrLimit
		}
		byNamespace[key] = append(byNamespace[key], record)
	}
	keys := make([]string, 0, len(byNamespace))
	for key := range byNamespace {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if err := ensureRepositoryDirectory(request.Root, request.Repository); err != nil {
		return nil, fmt.Errorf("create resolver namespace repository root: %w", err)
	}
	stage, err := os.MkdirTemp(repositoryRoot(request.Root, request.Repository), ".stage-")
	if err != nil {
		return nil, fmt.Errorf("create resolver namespace stage: %w", err)
	}
	prepared := &Prepared{root: request.Root, repository: request.Repository, directory: stage}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(stage)
		}
	}()

	prior := priorReceipts(request.Prior)
	rootValue := Root{
		Schema: RootSchema, Authority: authority, Policy: FrozenPolicy(),
		Namespaces: []NamespaceReceipt{},
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		values := byNamespace[key]
		slices.SortFunc(values, func(left, right Record) int {
			return strings.Compare(recordKey(left), recordKey(right))
		})
		if len(values) > MaxRecordsPerNamespace {
			return nil, ErrLimit
		}
		parts := strings.SplitN(key, "\x00", 3)
		language, protocol, namespace := parts[0], parts[1], parts[2]
		member := Member{
			Schema: MemberSchema, Language: language,
			Protocol: protocol, Namespace: namespace,
			Records: cloneRecords(values),
		}
		member.Digest, err = digestValue(Member{
			Schema: member.Schema, Language: member.Language, Protocol: member.Protocol,
			Namespace: member.Namespace, Records: member.Records,
		})
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(member)
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxMemberBytes {
			return nil, ErrLimit
		}
		name := memberName(language, protocol, namespace)
		destination := filepath.Join(stage, name)
		if receipt, reusable := prior[key]; reusable &&
			receipt.ContentDigest == member.Digest && receipt.ContentBytes == int64(len(raw)) {
			if err := reuseMember(ctx, request.Prior, receipt, destination, raw); err != nil {
				return nil, err
			}
		} else if err := writeExclusive(destination, raw); err != nil {
			return nil, err
		}
		rootValue.Namespaces = append(rootValue.Namespaces, NamespaceReceipt{
			Language: language, Protocol: protocol, Namespace: namespace, Member: name,
			RecordCount: len(values), ContentBytes: int64(len(raw)),
			ContentDigest: member.Digest,
		})
		rootValue.RecordCount += len(values)
		if int64(len(raw)) > MaxGenerationBytes-rootValue.ContentBytes {
			return nil, ErrLimit
		}
		rootValue.ContentBytes += int64(len(raw))
	}
	rootValue.NamespaceCount = len(rootValue.Namespaces)
	if err := setRootDigests(&rootValue); err != nil {
		return nil, err
	}
	rootRaw, err := json.Marshal(rootValue)
	if err != nil {
		return nil, err
	}
	if len(rootRaw) > MaxRootBytes {
		return nil, ErrLimit
	}
	if err := writeExclusive(filepath.Join(stage, "root.json"), rootRaw); err != nil {
		return nil, err
	}
	if err := syncDirectory(stage); err != nil {
		return nil, err
	}
	prepared.rootValue = rootValue
	if _, err := openGeneration(ctx, stage, rootValue, true); err != nil {
		return nil, fmt.Errorf("validate resolver namespace stage: %w", err)
	}
	return prepared, nil
}

func normalizeDescriptors(values []gocaller.DirectDescriptor) ([]Record, error) {
	if len(values) > MaxRecords {
		return nil, ErrLimit
	}
	records := make([]Record, 0, len(values))
	seen := make(map[string]string, len(values))
	var identityBytes int64
	for _, descriptor := range values {
		if err := validateDescriptorInput(descriptor); err != nil {
			return nil, err
		}
		record := Record{
			Schema: RecordSchema, Kind: "symbol", State: descriptor.State,
			Reason: descriptor.Reason, Language: LanguageGo, Protocol: descriptor.Protocol,
			Namespace: descriptor.ImportPath, Package: descriptor.Package,
			ClientType: descriptor.ClientType, Method: descriptor.Method,
			Operation: descriptor.Operation, Constructors: slices.Clone(descriptor.Constructors),
			GeneratedPath:         descriptor.GeneratedPath,
			GeneratedObjectID:     descriptor.GeneratedObjectID,
			GeneratedDigest:       descriptor.GeneratedDigest,
			GeneratorRelativePath: descriptor.GeneratorRelativePath,
			DeclarationPath:       descriptor.DeclarationPath,
			DeclarationLineage:    descriptor.DeclarationLineage,
			Candidates:            []Candidate{},
		}
		if err := setRecordDigest(&record); err != nil {
			return nil, err
		}
		if err := validateRecord(record); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxRecordBytes || int64(len(raw)) > MaxRecordIdentityBytes-identityBytes {
			return nil, ErrLimit
		}
		identity := recordKey(record)
		if prior, duplicate := seen[identity]; duplicate {
			if prior != string(raw) {
				return nil, fmt.Errorf("%w: descriptor identity conflict", ErrInvalid)
			}
			continue
		}
		seen[identity] = string(raw)
		records = append(records, record)
		identityBytes += int64(len(raw))
	}
	conflicts, err := conflictRecords(records)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > MaxRecords-len(records) {
		return nil, ErrLimit
	}
	for _, conflict := range conflicts {
		raw, err := json.Marshal(conflict)
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxRecordBytes || int64(len(raw)) > MaxRecordIdentityBytes-identityBytes {
			return nil, ErrLimit
		}
		identityBytes += int64(len(raw))
	}
	records = append(records, conflicts...)
	return records, nil
}

func validateDescriptorInput(value gocaller.DirectDescriptor) error {
	if len(value.Constructors) > MaxConstructors || !validText(value.Protocol) ||
		!validText(value.ImportPath) || !validText(value.Package) ||
		!validText(value.ClientType) || !validText(value.Method) ||
		!validText(value.Operation) || !validText(value.GeneratedPath) ||
		!validText(value.GeneratedObjectID) || !validText(value.GeneratedDigest) ||
		len(value.Reason) > MaxTextBytes || len(value.GeneratorRelativePath) > MaxTextBytes ||
		len(value.DeclarationPath) > MaxTextBytes || len(value.DeclarationLineage) > MaxTextBytes {
		return fmt.Errorf("%w: descriptor input", ErrInvalid)
	}
	for index, constructor := range value.Constructors {
		if !validText(constructor) || index > 0 && value.Constructors[index-1] >= constructor {
			return fmt.Errorf("%w: descriptor constructors", ErrInvalid)
		}
	}
	return nil
}

func conflictRecords(records []Record) ([]Record, error) {
	groups := make(map[string]map[string]Candidate)
	packages := make(map[string]string)
	base := make(map[string]Record)
	for _, record := range records {
		if record.State != "resolved" {
			continue
		}
		key := recordLookupKey(record)
		candidate := Candidate{
			Package: record.Package, Operation: record.Operation,
			DeclarationPath:    record.DeclarationPath,
			DeclarationLineage: record.DeclarationLineage,
		}
		if groups[key] == nil {
			groups[key] = make(map[string]Candidate)
		}
		candidateIdentity := candidateKey(candidate)
		if _, present := groups[key][candidateIdentity]; !present {
			if len(groups[key]) >= MaxConflictCandidates {
				return nil, ErrLimit
			}
			groups[key][candidateIdentity] = candidate
		}
		if prior, present := packages[key]; present && prior != record.Package {
			packages[key] = "<conflict>"
		} else if !present {
			packages[key] = record.Package
		}
		base[key] = record
	}
	result := []Record{}
	for key, candidateSet := range groups {
		candidates := make([]Candidate, 0, len(candidateSet))
		for _, candidate := range candidateSet {
			candidates = append(candidates, candidate)
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidateKey(candidates[i]) < candidateKey(candidates[j])
		})
		if len(candidates) < 2 {
			continue
		}
		seed := base[key]
		record := Record{
			Schema: RecordSchema, Kind: "conflict", State: "conflict",
			Reason: "multiple_resolver_targets", Language: seed.Language, Protocol: seed.Protocol,
			Namespace: seed.Namespace, Package: packages[key],
			ClientType: seed.ClientType, Method: seed.Method,
			Constructors: []string{}, Candidates: slices.Clone(candidates),
		}
		if err := setRecordDigest(&record); err != nil {
			return nil, err
		}
		if err := validateRecord(record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

func priorReceipts(publication *Publication) map[string]NamespaceReceipt {
	result := map[string]NamespaceReceipt{}
	if publication == nil {
		return result
	}
	for _, receipt := range publication.rootValue.Namespaces {
		result[namespaceKey(receipt.Language, receipt.Protocol, receipt.Namespace)] = receipt
	}
	return result
}

func reuseMember(
	ctx context.Context,
	prior *Publication,
	receipt NamespaceReceipt,
	destination string,
	expected []byte,
) error {
	if prior == nil {
		return errors.New("resolver namespace reuse lacks prior publication")
	}
	member, raw, err := prior.openMember(ctx, receipt)
	if err != nil {
		return err
	}
	if member.Digest != receipt.ContentDigest || !slices.Equal(raw, expected) {
		return fmt.Errorf("%w: prior namespace bytes changed", ErrInvalid)
	}
	if err := os.Link(filepath.Join(prior.directory, receipt.Member), destination); err != nil {
		return fmt.Errorf("hard-link unchanged resolver namespace: %w", err)
	}
	return nil
}

func writeExclusive(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	failed = false
	return nil
}
