package rpccallerposting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/resolvernamespace"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

type observationSource interface {
	Manifest() observationpublication.Manifest
	WalkObserved(context.Context, func(observationpublication.Record, sourceobservation.Observation) error) error
}

type resolverSource interface {
	Root() resolvernamespace.Root
	LookupNamespace(context.Context, string, string, string) ([]resolvernamespace.Record, error)
}

type BuildRequest struct {
	Root         string
	Observations *observationpublication.Publication
	Resolver     *resolvernamespace.Publication
	// ResidentLimitBytes is an operational pre-growth charge fence used by a
	// registered worker. Zero preserves the contract's frozen identity bound.
	ResidentLimitBytes int64
}

type BuildRequestV2 struct {
	Root               string
	Observations       observationpublication.DownstreamSource
	Resolver           *resolvernamespace.Publication
	Upstream           downstreamauthority.Authority
	Prior              *Publication
	ResidentLimitBytes int64
}

type Prepared struct {
	root       string
	repository string
	directory  string
	rootValue  Root
	closed     bool
}

type namespaceCache struct {
	resolver   resolverSource
	namespaces []resolvernamespace.NamespaceReceipt
	values     map[string][]resolvernamespace.Record
	reads      int
}

type callProjection struct {
	class              string
	reason             string
	lookup             string
	operation          string
	candidates         []string
	declarationPath    string
	declarationLineage string
	digests            []string
	generated          map[string]struct{}
}

func Build(ctx context.Context, request BuildRequest) (*Prepared, error) {
	return buildSourcesBounded(
		ctx, request.Root, request.Observations, request.Resolver,
		request.ResidentLimitBytes,
	)
}

// BuildV2 binds the complete T40.4 source/inventory authority into the
// component root. It shares the same cold posting walk as V1; only authority
// construction and root schema differ.
func BuildV2(ctx context.Context, request BuildRequestV2) (*Prepared, error) {
	if request.Observations == nil || request.Resolver == nil || request.Root == "" {
		return nil, errors.New("RPC caller posting v2 observations are incomplete")
	}
	observation := request.Observations.DownstreamAuthority()
	if downstreamauthority.RequireUsable(request.Upstream) != nil ||
		request.Upstream.Observation != observation {
		return nil, fmt.Errorf("%w: RPC caller posting v2 upstream", ErrInvalid)
	}
	resolverRoot := request.Resolver.Root()
	policyDigest, err := digestValue(FrozenPolicy())
	if err != nil {
		return nil, err
	}
	authority := Authority{
		Repository:                  observation.Repository,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ObservationManifestDigest:   observation.ObservationRootDigest,
		ObservationSourceDigest:     observation.SourceGenerationDigest,
		ResolverCommit:              resolverRoot.Authority.Commit,
		ResolverGenerationDigest:    resolverRoot.GenerationDigest,
		ResolverRootDigest:          resolverRoot.Digest, PolicyDigest: policyDigest,
		ObservationV2: &observation, Upstream: &request.Upstream,
	}
	if validateAuthority(RootSchemaV2, authority) != nil ||
		resolvernamespace.ValidateRoot(resolverRoot) != nil || observation.Repository != resolverRoot.Authority.Repository {
		return nil, fmt.Errorf("%w: RPC caller posting v2 authority", ErrInvalid)
	}
	return buildObservedBounded(ctx, request.Root, request.Observations, request.Resolver, resolverRoot.Namespaces, authority,
		observation.ObservedCount, RootSchemaV2, request.Prior, request.ResidentLimitBytes)
}

func buildSources(
	ctx context.Context,
	root string,
	observations observationSource,
	resolver resolverSource,
) (*Prepared, error) {
	return buildSourcesBounded(ctx, root, observations, resolver, 0)
}

func buildSourcesBounded(
	ctx context.Context,
	root string,
	observations observationSource,
	resolver resolverSource,
	residentLimit int64,
) (*Prepared, error) {
	if root == "" || observations == nil || resolver == nil {
		return nil, errors.New("RPC caller posting inputs are incomplete")
	}
	observationManifest := observations.Manifest()
	resolverRoot := resolver.Root()
	if err := observationpublication.ValidateManifest(observationManifest); err != nil {
		return nil, fmt.Errorf("validate observation authority: %w", err)
	}
	if err := resolvernamespace.ValidateRoot(resolverRoot); err != nil {
		return nil, fmt.Errorf("validate resolver authority: %w", err)
	}
	if observationManifest.Repository != resolverRoot.Authority.Repository {
		return nil, errors.New("observation and resolver repositories differ")
	}
	policyDigest, err := digestValue(FrozenPolicy())
	if err != nil {
		return nil, err
	}
	authority := Authority{
		Repository:                  observationManifest.Repository,
		ObservationGenerationDigest: observationManifest.GenerationDigest,
		ObservationManifestDigest:   observationManifest.Digest,
		ObservationSourceDigest:     observationManifest.SourceGenerationDigest,
		ResolverCommit:              resolverRoot.Authority.Commit,
		ResolverGenerationDigest:    resolverRoot.GenerationDigest,
		ResolverRootDigest:          resolverRoot.Digest, PolicyDigest: policyDigest,
	}
	if err := validateAuthority(RootSchema, authority); err != nil {
		return nil, err
	}
	return buildObservedBounded(ctx, root, observations, resolver, resolverRoot.Namespaces, authority,
		observationManifest.ObservedCount, RootSchema, nil, residentLimit)
}

type observedSource interface {
	WalkObserved(context.Context, func(observationpublication.Record, sourceobservation.Observation) error) error
}

func buildObservedBounded(
	ctx context.Context, root string, observations observedSource, resolver resolverSource,
	namespaces []resolvernamespace.NamespaceReceipt, authority Authority,
	expectedObserved int, rootSchema string, prior *Publication, residentLimit int64,
) (*Prepared, error) {

	// Both entry paths validated this immutable Go namespace inventory already.
	// Retain their single root snapshot, not a second copy or a negative cache.
	cache := &namespaceCache{resolver: resolver, namespaces: namespaces, values: make(map[string][]resolvernamespace.Record)}
	byMember := make(map[string][]Posting)
	seen := make(map[string]struct{})
	var identityBytes int64
	var residentCharge int64
	if residentLimit == 0 {
		residentLimit = math.MaxInt64
	}
	if residentLimit < 1 {
		return nil, ErrLimit
	}
	postingCount := 0
	walkedRecords := 0
	err := observations.WalkObserved(
		ctx,
		func(record observationpublication.Record, observation sourceobservation.Observation) error {
			walkedRecords++
			if walkedRecords > expectedObserved {
				return fmt.Errorf("%w: observation walk exceeds manifest", ErrInvalid)
			}
			if sourceobservation.Validate(observation) != nil ||
				observation.ContentDigest != record.ContentDigest {
				return fmt.Errorf("%w: observation input", ErrInvalid)
			}
			for _, function := range observation.Functions {
				for _, call := range function.Calls {
					for _, protocol := range []string{"grpc", "thrift"} {
						projection, present, err := projectCall(ctx, cache, observation, call, protocol)
						if err != nil {
							return err
						}
						if !present {
							continue
						}
						for _, placement := range record.Placements {
							posting := Posting{
								Schema: PostingSchema, Protocol: protocol,
								Class: projection.class, Reason: projection.reason,
								LookupOperation: projection.lookup, Operation: projection.operation,
								CandidateOperations: slices.Clone(projection.candidates),
								ObjectID:            record.ObjectID, ContentDigest: record.ContentDigest,
								Path: placement.Path, Mode: placement.Mode,
								Revisions: slices.Clone(placement.Revisions), Span: call.Span,
								SourceRole:            sourceRole(placement.Path, projection.generated),
								DeclarationPath:       projection.declarationPath,
								DeclarationLineage:    projection.declarationLineage,
								ResolverRecordDigests: slices.Clone(projection.digests),
							}
							if posting.SourceRole == "generated" {
								posting.Class, posting.Reason = "unresolved", "resolver_generated_input"
								posting.LookupOperation, posting.Operation = "", ""
								posting.DeclarationPath, posting.DeclarationLineage = "", ""
								if projection.operation != "" {
									posting.CandidateOperations = []string{projection.operation}
								}
							}
							if err := setPostingDigest(&posting); err != nil {
								return err
							}
							if err := validatePosting(posting); err != nil {
								return err
							}
							if _, duplicate := seen[posting.Digest]; duplicate {
								continue
							}
							raw, err := json.Marshal(posting)
							if err != nil {
								return err
							}
							if len(raw) > MaxPostingBytes ||
								int64(len(raw)) > MaxPostingIdentityBytes-identityBytes {
								return ErrLimit
							}
							const postingOverhead = 512
							charge := int64(len(raw) + postingOverhead)
							if charge > residentLimit-residentCharge {
								return pipelinerefusal.Measure(
									ErrLimit, pipelinerefusal.DimensionResidentBytes,
									residentCharge+charge, residentLimit,
								)
							}
							if postingCount >= MaxPostings {
								return ErrLimit
							}
							seen[posting.Digest] = struct{}{}
							postingCount++
							identityBytes += int64(len(raw))
							residentCharge += charge
							bucket := partitionBucket(partitionOperation(posting))
							key := memberKey(protocol, bucket)
							if len(byMember[key]) >= MaxPostingsPerMember {
								return ErrLimit
							}
							byMember[key] = append(byMember[key], posting)
						}
					}
				}
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	if walkedRecords != expectedObserved {
		return nil, fmt.Errorf("%w: observation walk total", ErrInvalid)
	}
	return writeStage(ctx, root, authority, rootSchema, prior, byMember)
}

func projectCall(
	ctx context.Context,
	cache *namespaceCache,
	observation sourceobservation.Observation,
	call sourceobservation.Call,
	protocol string,
) (callProjection, bool, error) {
	namespaces := make(map[string]struct{})
	for _, imported := range observation.Imports {
		if imported.Kind != "blank" {
			namespaces[imported.Path] = struct{}{}
		}
	}
	for _, binding := range call.Bindings {
		if binding.ImportPath != "" {
			namespaces[binding.ImportPath] = struct{}{}
		}
	}
	paths := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		paths = append(paths, namespace)
	}
	slices.Sort(paths)
	all := []resolvernamespace.Record{}
	for _, namespace := range paths {
		records, err := cache.namespace(ctx, protocol, namespace)
		if err != nil {
			return callProjection{}, false, err
		}
		for _, record := range records {
			if record.Method == call.Selector {
				all = append(all, record)
			}
		}
	}
	if len(all) == 0 {
		return callProjection{}, false, nil
	}
	exact := exactRecords(all, call.Bindings)
	selected := exact
	if len(selected) == 0 {
		selected = all
	}
	projection, err := projectionFromRecords(selected)
	if err != nil {
		return callProjection{}, false, err
	}
	if !projection.present() {
		return callProjection{}, false, nil
	}
	if call.Receiver != "resolved" {
		projection.class = "unresolved"
		projection.lookup, projection.operation = "", ""
		projection.declarationPath, projection.declarationLineage = "", ""
		if call.Receiver == "dynamic" {
			projection.reason = "dynamic_receiver"
		} else {
			projection.reason = "unsupported_receiver"
		}
		return projection, true, nil
	}
	if len(exact) == 0 && projection.class == "resolved" {
		projection.class, projection.reason = "name_match", "name_match_only"
		projection.candidates = []string{projection.operation}
		projection.lookup = projection.operation
		projection.operation = ""
		projection.declarationPath, projection.declarationLineage = "", ""
	}
	return projection, true, nil
}

func (cache *namespaceCache) namespace(
	ctx context.Context,
	protocol, namespace string,
) ([]resolvernamespace.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if (protocol != "grpc" && protocol != "thrift") || !validText(namespace) {
		return nil, fmt.Errorf("%w: namespace lookup", resolvernamespace.ErrInvalid)
	}
	key := protocol + "\x00" + namespace
	if values, present := cache.values[key]; present {
		return values, nil
	}
	_, present := slices.BinarySearchFunc(cache.namespaces, namespace, func(receipt resolvernamespace.NamespaceReceipt, target string) int {
		if compared := strings.Compare(receipt.Protocol, protocol); compared != 0 {
			return compared
		}
		return strings.Compare(receipt.Namespace, target)
	})
	if !present {
		return []resolvernamespace.Record{}, nil
	}
	if cache.reads >= MaxNamespaceReads {
		return nil, ErrLimit
	}
	values, err := cache.resolver.LookupNamespace(
		ctx, resolvernamespace.LanguageGo, protocol, namespace,
	)
	if err != nil {
		return nil, err
	}
	cache.reads++
	cache.values[key] = values
	return values, nil
}

func exactRecords(
	values []resolvernamespace.Record,
	bindings []sourceobservation.Binding,
) []resolvernamespace.Record {
	clientTypes := make(map[string]struct{})
	result := []resolvernamespace.Record{}
	seen := make(map[string]struct{})
	for _, value := range values {
		if value.Kind != "symbol" {
			continue
		}
		for _, binding := range bindings {
			if binding.ImportPath != value.Namespace ||
				(binding.TypeName != value.ClientType && !slices.Contains(value.Constructors, binding.Constructor)) {
				continue
			}
			if _, duplicate := seen[value.Digest]; !duplicate {
				seen[value.Digest] = struct{}{}
				result = append(result, value)
				clientTypes[value.ClientType] = struct{}{}
			}
		}
	}
	for _, value := range values {
		if value.Kind != "conflict" {
			continue
		}
		if _, relevant := clientTypes[value.ClientType]; relevant {
			result = append(result, value)
		}
	}
	return result
}

func projectionFromRecords(values []resolvernamespace.Record) (callProjection, error) {
	projection := callProjection{generated: make(map[string]struct{})}
	resolved := []resolvernamespace.Record{}
	states := make(map[string]struct{})
	digestSet := make(map[string]struct{})
	candidateSet := make(map[string]struct{})
	for _, value := range values {
		if _, present := digestSet[value.Digest]; !present {
			if len(digestSet) >= MaxResolverRecordDigests {
				return callProjection{}, ErrLimit
			}
			digestSet[value.Digest] = struct{}{}
			projection.digests = append(projection.digests, value.Digest)
		}
		if value.GeneratedPath != "" {
			projection.generated[value.GeneratedPath] = struct{}{}
		}
		if value.Kind == "conflict" {
			states["conflict"] = struct{}{}
			for _, candidate := range value.Candidates {
				if _, present := candidateSet[candidate.Operation]; !present {
					if len(candidateSet) >= MaxCandidateOperations {
						return callProjection{}, ErrLimit
					}
					candidateSet[candidate.Operation] = struct{}{}
					projection.candidates = append(projection.candidates, candidate.Operation)
				}
			}
			continue
		}
		if value.State == "resolved" {
			resolved = append(resolved, value)
		} else {
			states[value.State] = struct{}{}
		}
		if _, present := candidateSet[value.Operation]; !present {
			if len(candidateSet) >= MaxCandidateOperations {
				return callProjection{}, ErrLimit
			}
			candidateSet[value.Operation] = struct{}{}
			projection.candidates = append(projection.candidates, value.Operation)
		}
	}
	projection.digests = sortedUnique(projection.digests)
	projection.candidates = sortedUnique(projection.candidates)
	if _, conflict := states["conflict"]; conflict {
		projection.class, projection.reason = "unresolved", "resolver_conflict"
		return projection, nil
	}
	if len(states) != 0 {
		projection.class, projection.reason = "unresolved", resolverStateReason(states)
		return projection, nil
	}
	if len(resolved) != 1 {
		projection.class = "unresolved"
		if len(projection.candidates) > 1 {
			projection.reason = "ambiguous_method_candidates"
		} else {
			projection.reason = "ambiguous_receiver_provenance"
		}
		return projection, nil
	}
	value := resolved[0]
	projection.class, projection.lookup, projection.operation = "resolved", value.Operation, value.Operation
	projection.candidates = []string{}
	projection.declarationPath = value.DeclarationPath
	projection.declarationLineage = value.DeclarationLineage
	return projection, nil
}

func (projection callProjection) present() bool {
	return projection.class != "" &&
		(projection.operation != "" || len(projection.candidates) != 0 || len(projection.digests) != 0)
}

func resolverStateReason(states map[string]struct{}) string {
	for _, state := range []string{"ambiguous", "unsupported", "unavailable"} {
		if _, present := states[state]; present {
			return "resolver_" + state
		}
	}
	return "ambiguous_receiver_provenance"
}

func sortedUnique(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if value != "" && (len(result) == 0 || result[len(result)-1] != value) {
			result = append(result, value)
		}
	}
	return result
}

func sourceRole(path string, generated map[string]struct{}) string {
	if _, present := generated[path]; present {
		return "generated"
	}
	for _, component := range strings.Split(path, "/") {
		if component == "vendor" {
			return "vendor"
		}
	}
	if strings.HasSuffix(path, "_test.go") {
		return "test"
	}
	for _, component := range strings.Split(path, "/") {
		if component == "test" || component == "tests" || component == "testdata" {
			return "test"
		}
	}
	return "production"
}

func writeStage(
	ctx context.Context,
	root string,
	authority Authority,
	rootSchema string,
	prior *Publication,
	byMember map[string][]Posting,
) (_ *Prepared, retErr error) {
	if err := ensureRoot(root); err != nil {
		return nil, err
	}
	repositoryDirectory := publicationRepository(root, authority.Repository)
	if err := os.Mkdir(repositoryDirectory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := validateDirectory(repositoryDirectory); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(repositoryDirectory, ".stage-")
	if err != nil {
		return nil, err
	}
	prepared := &Prepared{
		root: root, repository: authority.Repository, directory: directory,
	}
	failed := true
	defer func() {
		if !failed {
			return
		}
		if cleanupErr := prepared.Discard(); cleanupErr != nil {
			cleanupErr = fmt.Errorf("clean failed RPC caller posting stage: %w", cleanupErr)
			if errors.Is(retErr, ErrLimit) {
				retErr = fmt.Errorf("%v; %w", retErr, cleanupErr)
			} else {
				retErr = errors.Join(retErr, cleanupErr)
			}
		}
	}()
	keys := make([]string, 0, len(byMember))
	for key := range byMember {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	rootValue := Root{
		Schema: rootSchema, Authority: authority, Policy: FrozenPolicy(),
		Members: []MemberReceipt{},
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		values := byMember[key]
		slices.SortFunc(values, func(left, right Posting) int {
			return strings.Compare(postingKey(left), postingKey(right))
		})
		separator := strings.IndexByte(key, 0)
		protocol := key[:separator]
		var bucket int
		if _, err := fmt.Sscanf(key[separator+1:], "%03d", &bucket); err != nil {
			return nil, err
		}
		member := Member{
			Schema: MemberSchema, Protocol: protocol, Bucket: bucket,
			Postings: clonePostings(values),
		}
		member.Digest, err = digestValue(Member{
			Schema: member.Schema, Protocol: member.Protocol,
			Bucket: member.Bucket, Postings: member.Postings,
		})
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(member)
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxMemberBytes || int64(len(raw)) > MaxGenerationBytes-rootValue.EncodedMemberBytes {
			return nil, ErrLimit
		}
		name := memberName(protocol, bucket)
		if !reusePriorMember(prior, name, member.Digest, raw, filepath.Join(directory, name)) {
			if err := writeExclusive(filepath.Join(directory, name), raw); err != nil {
				return nil, err
			}
		}
		rootValue.Members = append(rootValue.Members, MemberReceipt{
			Protocol: protocol, Bucket: bucket, Name: name,
			PostingCount: len(values), ContentBytes: int64(len(raw)),
			ContentDigest: member.Digest,
		})
		rootValue.PostingCount += len(values)
		rootValue.EncodedMemberBytes += int64(len(raw))
		for _, posting := range values {
			switch posting.Class {
			case "resolved":
				rootValue.ResolvedCount++
			case "name_match":
				rootValue.NameMatchCount++
			case "unresolved":
				rootValue.UnresolvedCount++
			}
		}
	}
	if err := setRootDigests(&rootValue); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(rootValue)
	if err != nil || len(raw) > MaxRootBytes {
		return nil, ErrLimit
	}
	if err := writeExclusive(filepath.Join(directory, "root.json"), raw); err != nil {
		return nil, err
	}
	if err := syncDirectory(directory); err != nil {
		return nil, err
	}
	prepared.rootValue = rootValue
	if _, err := openDirectory(ctx, directory, rootValue, true); err != nil {
		return nil, err
	}
	failed = false
	return prepared, nil
}

func reusePriorMember(prior *Publication, name, digest string, raw []byte, destination string) bool {
	if prior == nil {
		return false
	}
	_, found := slices.BinarySearchFunc(prior.rootValue.Members, name, func(value MemberReceipt, target string) int {
		return strings.Compare(value.Name, target)
	})
	if !found {
		return false
	}
	priorRaw, err := readRegular(filepath.Join(prior.directory, name), MaxMemberBytes)
	if err != nil || !bytes.Equal(priorRaw, raw) {
		return false
	}
	var member Member
	if decodeExact(priorRaw, MaxMemberBytes, &member) != nil || validateMember(member) != nil || member.Digest != digest {
		return false
	}
	return os.Link(filepath.Join(prior.directory, name), destination) == nil
}
