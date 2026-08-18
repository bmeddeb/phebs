package kafkatopicposting

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
	"strings"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
)

type observationSource interface {
	Manifest() observationpublication.Manifest
	WalkObserved(context.Context, func(observationpublication.Record, sourceobservation.Observation) error) error
}

type BuildRequest struct {
	Root         string
	Observations *observationpublication.Publication
	// ResidentLimitBytes is an operational pre-growth charge fence used by a
	// registered worker. Zero preserves the contract's frozen identity bound.
	ResidentLimitBytes int64
}

type BuildRequestV2 struct {
	Root               string
	Observations       observationpublication.DownstreamSource
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

func Build(ctx context.Context, request BuildRequest) (*Prepared, error) {
	return buildSourceBounded(
		ctx, request.Root, request.Observations, request.ResidentLimitBytes,
	)
}

func BuildV2(ctx context.Context, request BuildRequestV2) (*Prepared, error) {
	if request.Root == "" || request.Observations == nil {
		return nil, errors.New("kafka topic posting v2 inputs are incomplete")
	}
	observation := request.Observations.DownstreamAuthority()
	if downstreamauthority.RequireUsable(request.Upstream) != nil ||
		request.Upstream.Observation != observation {
		return nil, fmt.Errorf("%w: kafka topic posting v2 upstream", ErrInvalid)
	}
	policyDigest, err := digestValue(FrozenPolicy())
	if err != nil {
		return nil, err
	}
	authority := Authority{
		Repository:                  observation.Repository,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ObservationManifestDigest:   observation.ObservationRootDigest,
		ObservationSourceDigest:     observation.SourceGenerationDigest,
		PolicyDigest:                policyDigest, ObservationV2: &observation,
		Upstream: &request.Upstream,
	}
	if validateAuthority(RootSchemaV2, authority) != nil {
		return nil, fmt.Errorf("%w: kafka topic posting v2 authority", ErrInvalid)
	}
	return buildObservedBounded(ctx, request.Root, request.Observations, authority,
		observation.ObservedCount, RootSchemaV2, request.Prior, request.ResidentLimitBytes)
}

func buildSource(ctx context.Context, root string, observations observationSource) (*Prepared, error) {
	return buildSourceBounded(ctx, root, observations, 0)
}

func buildSourceBounded(
	ctx context.Context, root string, observations observationSource, residentLimit int64,
) (*Prepared, error) {
	if root == "" || observations == nil {
		return nil, errors.New("kafka topic posting inputs are incomplete")
	}
	manifest := observations.Manifest()
	if err := observationpublication.ValidateManifest(manifest); err != nil {
		return nil, fmt.Errorf("validate observation authority: %w", err)
	}
	policyDigest, err := digestValue(FrozenPolicy())
	if err != nil {
		return nil, err
	}
	authority := Authority{
		Repository:                  manifest.Repository,
		ObservationGenerationDigest: manifest.GenerationDigest,
		ObservationManifestDigest:   manifest.Digest,
		ObservationSourceDigest:     manifest.SourceGenerationDigest,
		PolicyDigest:                policyDigest,
	}
	if err := validateAuthority(RootSchema, authority); err != nil {
		return nil, err
	}
	return buildObservedBounded(ctx, root, observations, authority,
		manifest.ObservedCount, RootSchema, nil, residentLimit)
}

type observedSource interface {
	WalkObserved(context.Context, func(observationpublication.Record, sourceobservation.Observation) error) error
}

func buildObservedBounded(
	ctx context.Context, root string, observations observedSource, authority Authority,
	expectedObserved int, rootSchema string, prior *Publication, residentLimit int64,
) (*Prepared, error) {

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
	err := observations.WalkObserved(ctx, func(
		record observationpublication.Record,
		observation sourceobservation.Observation,
	) error {
		walkedRecords++
		if walkedRecords > expectedObserved {
			return fmt.Errorf("%w: observation walk exceeds manifest", ErrInvalid)
		}
		if sourceobservation.Validate(observation) != nil || observation.ContentDigest != record.ContentDigest {
			return fmt.Errorf("%w: observation input", ErrInvalid)
		}
		for _, site := range observation.TopicSites {
			for _, placement := range record.Placements {
				posting := postingFromSite(record, placement.Path, placement.Mode, placement.Revisions, site)
				if err := setPostingDigest(&posting); err != nil {
					return err
				}
				if err := validatePosting(posting); err != nil {
					return err
				}
				if err := addPosting(
					byMember, seen, &postingCount, &identityBytes,
					&residentCharge, residentLimit, posting,
				); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if walkedRecords != expectedObserved {
		return nil, fmt.Errorf("%w: observation walk total", ErrInvalid)
	}
	return writeStage(ctx, root, authority, rootSchema, prior, byMember)
}

func addPosting(
	byMember map[string][]Posting,
	seen map[string]struct{},
	postingCount *int,
	identityBytes *int64,
	residentCharge *int64,
	residentLimit int64,
	posting Posting,
) error {
	if _, duplicate := seen[posting.Digest]; duplicate {
		return nil
	}
	if *postingCount >= MaxPostings {
		return ErrLimit
	}
	bucket := partitionBucket(partitionIdentity(posting))
	key := memberKey(posting.Plane, bucket)
	if len(byMember[key]) >= MaxPostingsPerMember {
		return ErrLimit
	}
	raw, err := json.Marshal(posting)
	if err != nil {
		return err
	}
	if len(raw) > MaxPostingBytes || int64(len(raw)) > MaxPostingIdentityBytes-*identityBytes {
		return ErrLimit
	}
	const postingOverhead = 512
	charge := int64(len(raw) + postingOverhead)
	if charge > residentLimit-*residentCharge {
		return &ResidentLimitError{
			ObservedBytes: *residentCharge + charge,
			LimitBytes:    residentLimit,
		}
	}
	seen[posting.Digest] = struct{}{}
	(*postingCount)++
	*identityBytes += int64(len(raw))
	*residentCharge += charge
	byMember[key] = append(byMember[key], posting)
	return nil
}

func postingFromSite(
	record observationpublication.Record,
	path, mode string,
	revisions []int,
	site sourceobservation.TopicSite,
) Posting {
	value := Posting{
		Schema: PostingSchema, Plane: site.Plane,
		GroupIDSpelling: site.GroupID, Library: site.Library,
		ImportPath: site.ImportPath, Shape: site.Shape,
		ObjectID: record.ObjectID, ContentDigest: record.ContentDigest,
		Path: path, Mode: mode, Revisions: slices.Clone(revisions), Span: site.Span,
		SourceRole: sourceRole(path),
	}
	if site.State == "resolved" {
		value.Class = "literal"
		value.TopicSpelling = site.Topic
		value.Binding = site.Binding
	} else {
		value.Class = "unresolved"
		value.SourceReason = site.Reason
		if site.State == "dynamic" {
			value.Reason = "dynamic_topic"
		} else {
			value.Reason = "unsupported_topic"
		}
	}
	return value
}

func sourceRole(path string) string {
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
) (*Prepared, error) {
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
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(directory)
		}
	}()
	keys := make([]string, 0, len(byMember))
	for key := range byMember {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	rootValue := Root{Schema: rootSchema, Authority: authority, Policy: FrozenPolicy(), Members: []MemberReceipt{}}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		values := byMember[key]
		slices.SortFunc(values, func(left, right Posting) int {
			return strings.Compare(postingKey(left), postingKey(right))
		})
		separator := strings.IndexByte(key, 0)
		plane := key[:separator]
		var bucket int
		if _, err := fmt.Sscanf(key[separator+1:], "%03d", &bucket); err != nil {
			return nil, err
		}
		member := Member{Schema: MemberSchema, Plane: plane, Bucket: bucket, Postings: clonePostings(values)}
		member.Digest, err = digestValue(Member{
			Schema: member.Schema, Plane: member.Plane, Bucket: member.Bucket, Postings: member.Postings,
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
		name := memberName(plane, bucket)
		if !reusePriorMember(prior, name, member.Digest, raw, filepath.Join(directory, name)) {
			if err := writeExclusive(filepath.Join(directory, name), raw); err != nil {
				return nil, err
			}
		}
		rootValue.Members = append(rootValue.Members, MemberReceipt{
			Plane: plane, Bucket: bucket, Name: name, PostingCount: len(values),
			ContentBytes: int64(len(raw)), ContentDigest: member.Digest,
		})
		rootValue.PostingCount += len(values)
		rootValue.EncodedMemberBytes += int64(len(raw))
		for _, posting := range values {
			if posting.Plane == "producer" {
				rootValue.ProducerCount++
			} else {
				rootValue.ConsumerCount++
			}
			if posting.Class == "literal" {
				rootValue.LiteralCount++
			} else {
				rootValue.UnresolvedCount++
			}
		}
	}
	if err := setRootDigests(&rootValue); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(rootValue)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxRootBytes {
		return nil, ErrLimit
	}
	if err := writeExclusive(filepath.Join(directory, "root.json"), raw); err != nil {
		return nil, err
	}
	if err := syncDirectory(directory); err != nil {
		return nil, err
	}
	prepared := &Prepared{root: root, repository: authority.Repository, directory: directory, rootValue: rootValue}
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
