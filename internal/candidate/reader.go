package candidate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/gitobj"
)

// Publication is an already strictly validated, immutable candidate
// generation. Callers may replay a domain without retaining the complete Git
// tree or all candidate records in memory.
type Publication struct {
	root     string
	manifest Manifest
	state    State
}

// DomainView is one exact domain/version projection of a publication.
type DomainView struct {
	publication *Publication
	identity    PolicyIdentity
}

type analysisUnitView struct {
	primary    []string
	supporting []string
	known      bool
}

func (publication *Publication) Manifest() Manifest {
	return cloneManifest(publication.manifest)
}

func (publication *Publication) State() State {
	return publication.state
}

func (publication *Publication) Corpus() CorpusSummary {
	return publication.manifest.Corpus
}

func (publication *Publication) Domain(domain, version string) (*DomainView, error) {
	for _, identity := range publication.manifest.Policies {
		if identity.Domain == domain && identity.Version == version {
			return &DomainView{publication: publication, identity: identity}, nil
		}
	}
	return nil, fmt.Errorf("candidate domain %s/%s: %w", domain, version, os.ErrNotExist)
}

// ForEachRepositoryRecord replays the repository view in its canonical
// artifact order. T30.4 deliberately does not filter on InUnit.
func (view *DomainView) ForEachRepositoryRecord(
	ctx context.Context,
	visit func(Record) error,
) error {
	if view == nil || view.publication == nil || visit == nil {
		return errors.New("candidate domain replay is invalid")
	}
	if IsPublishing(
		view.publication.root, view.publication.manifest.Repository,
	) {
		return ErrPublishing
	}
	members := view.publication.manifest.RepositoryMembers
	if view.identity.Plane == PlaneCaller {
		members = make([]Artifact, 0, len(view.publication.manifest.CallerLeaves))
		for _, leaf := range view.publication.manifest.CallerLeaves {
			members = append(members, leaf.Artifact)
		}
	}
	for _, member := range members {
		err := validateArtifactFile(
			ctx, filepath.Join(view.publication.root, member.Name), member,
			func(record Record) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !slices.Contains(record.Domains, view.identity.Domain) {
					return nil
				}
				record.Required = slices.Contains(
					record.RequiredDomains, view.identity.Domain,
				)
				return visit(record)
			},
		)
		if err != nil {
			return fmt.Errorf("replay candidate member %q: %w", member.Name, err)
		}
	}
	if IsPublishing(
		view.publication.root, view.publication.manifest.Repository,
	) {
		return ErrPublishing
	}
	return nil
}

func validateArtifactSet(
	ctx context.Context,
	directory string,
	manifest Manifest,
	exactDirectory bool,
	unit *analysisUnitView,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if unit == nil {
		unit = &analysisUnitView{}
	}
	policyByDomain := make(map[string]PolicyIdentity, len(manifest.Policies))
	for _, identity := range manifest.Policies {
		policyByDomain[identity.Domain] = identity
	}
	allowed := map[string]bool{ManifestName(manifest.Repository): true}
	accumulators := makeDomainAccumulators(manifest.Policies)
	projectionDirectory, err := os.MkdirTemp(
		directory, validationDirectoryPrefix,
	)
	if err != nil {
		return fmt.Errorf("create candidate validation spool: %w", err)
	}
	defer func() {
		if projectionDirectory != "" {
			_ = os.RemoveAll(projectionDirectory)
		}
	}()
	projections := newProjectionSorter(ctx, projectionDirectory)

	var previousRepository *Record
	repositoryFirst := make([]Record, 0, len(manifest.RepositoryMembers))
	for ordinal, member := range manifest.RepositoryMembers {
		expectedName := ArtifactPrefix(
			manifest.Repository, manifest.GenerationDigest,
		) + fmt.Sprintf("repository-%06d.ndjson", ordinal)
		if err := validateArtifactEnvelope(
			member, ordinal, expectedName,
		); err != nil {
			return err
		}
		allowed[member.Name] = true
		var previousInMember *Record
		err := validateArtifactFile(
			ctx, filepath.Join(directory, member.Name), member,
			func(record Record) error {
				if err := validateRecord(
					record, PlaneRepository, policyByDomain, unit,
				); err != nil {
					return err
				}
				if previousRepository != nil {
					if previousRepository.Path == record.Path {
						return fmt.Errorf(
							"%w: duplicate repository path %q",
							ErrInvalidManifest, record.Path,
						)
					}
					if compareRepositoryRecords(*previousRepository, record) >= 0 {
						return fmt.Errorf(
							"%w: repository records are not canonical",
							ErrInvalidManifest,
						)
					}
				}
				if previousInMember != nil &&
					compareRepositoryRecords(*previousInMember, record) >= 0 {
					return fmt.Errorf("%w: member records are not canonical", ErrInvalidManifest)
				}
				copied := record
				if previousInMember == nil {
					repositoryFirst = append(repositoryFirst, copied)
				}
				previousRepository = &copied
				previousInMember = &copied
				if err := projections.add(
					projectionFromRecord(record, PlaneRepository),
				); err != nil {
					return err
				}
				return addDomainRecords(
					accumulators, record, record.Domains, record.RequiredDomains,
				)
			},
		)
		if err != nil {
			return fmt.Errorf("candidate member %q: %w", member.Name, err)
		}
	}
	for index := 0; index+1 < len(manifest.RepositoryMembers); index++ {
		current := manifest.RepositoryMembers[index]
		next := repositoryFirst[index+1]
		if current.RecordCount < MaxRecordsPerArtifact &&
			next.DeclaredBytes <=
				MaxDeclaredBytesPerArtifact-current.DeclaredBytes {
			return fmt.Errorf(
				"%w: repository member %d has a non-greedy boundary",
				ErrInvalidManifest, index,
			)
		}
	}

	var previousLeaf string
	var previousCaller *Record
	for ordinal, leaf := range manifest.CallerLeaves {
		expectedName := ArtifactPrefix(
			manifest.Repository, manifest.GenerationDigest,
		) + fmt.Sprintf("caller-%06d.ndjson", ordinal)
		if err := validateArtifactEnvelope(
			leaf.Artifact, ordinal, expectedName,
		); err != nil {
			return err
		}
		if err := validateCallerLeaf(leaf, previousLeaf); err != nil {
			return err
		}
		if previousLeaf != "" && strings.HasPrefix(leaf.Prefix, previousLeaf) {
			return fmt.Errorf("%w: caller leaves overlap", ErrInvalidManifest)
		}
		previousLeaf = leaf.Prefix
		allowed[leaf.Name] = true
		err := validateArtifactFile(
			ctx, filepath.Join(directory, leaf.Name), leaf.Artifact,
			func(record Record) error {
				if err := validateRecord(
					record, PlaneCaller, policyByDomain, unit,
				); err != nil {
					return err
				}
				if !hashHasPrefix(record.Hash, leaf.Prefix) {
					return fmt.Errorf("%w: caller record is outside its leaf", ErrInvalidManifest)
				}
				if previousCaller != nil {
					if previousCaller.Path == record.Path {
						return fmt.Errorf(
							"%w: duplicate caller path %q",
							ErrInvalidManifest, record.Path,
						)
					}
					if compareCallerRecords(*previousCaller, record) >= 0 {
						return fmt.Errorf(
							"%w: caller records are not canonical",
							ErrInvalidManifest,
						)
					}
				}
				copied := record
				previousCaller = &copied
				if err := projections.add(
					projectionFromRecord(record, PlaneCaller),
				); err != nil {
					return err
				}
				return addDomainRecords(
					accumulators, record, record.Domains, record.RequiredDomains,
				)
			},
		)
		if err != nil {
			return fmt.Errorf("caller leaf %q: %w", leaf.Name, err)
		}
	}
	if err := validateMinimalCallerSplits(
		ctx, manifest.CallerLeaves,
	); err != nil {
		return err
	}
	projectionPath, err := projections.finish()
	if err != nil {
		return fmt.Errorf("validate candidate projection: %w", err)
	}
	if err := validateCorpusSummary(
		ctx, manifest.Corpus, projectionPath,
	); err != nil {
		return err
	}
	if err := os.RemoveAll(projectionDirectory); err != nil {
		return fmt.Errorf("remove candidate validation spool: %w", err)
	}
	projectionDirectory = ""
	recomputed := finishDomainAccumulators(accumulators)
	if !slices.Equal(recomputed, manifest.Domains) {
		return fmt.Errorf(
			"%w: domain summaries do not match members: actual=%+v expected=%+v",
			ErrInvalidManifest, recomputed, manifest.Domains,
		)
	}
	return validateDirectoryMembership(
		ctx, directory, manifest.Repository, manifest.GenerationDigest,
		allowed, exactDirectory,
	)
}

type prefixBound struct {
	count         int
	declaredBytes int64
}

func validateMinimalCallerSplits(
	ctx context.Context,
	leaves []CallerLeaf,
) error {
	bounds := make(map[string]prefixBound)
	for _, leaf := range leaves {
		if err := ctx.Err(); err != nil {
			return err
		}
		for bits := InitialCallerPrefixBits; bits <= leaf.PrefixBits; bits++ {
			prefix := leaf.Prefix[:bits]
			current := bounds[prefix]
			if leaf.RecordCount > math.MaxInt-current.count ||
				leaf.DeclaredBytes > math.MaxInt64-current.declaredBytes {
				return fmt.Errorf("%w: caller prefix summary overflows", ErrInvalidManifest)
			}
			current.count += leaf.RecordCount
			current.declaredBytes += leaf.DeclaredBytes
			bounds[prefix] = current
		}
	}
	for _, leaf := range leaves {
		if err := ctx.Err(); err != nil {
			return err
		}
		for bits := InitialCallerPrefixBits + 1; bits <= leaf.PrefixBits; bits++ {
			parent := bounds[leaf.Prefix[:bits-1]]
			if parent.count <= MaxRecordsPerArtifact &&
				parent.declaredBytes <= MaxDeclaredBytesPerArtifact {
				return fmt.Errorf(
					"%w: caller prefix %q was split below its bounds",
					ErrInvalidManifest, leaf.Prefix[:bits-1],
				)
			}
		}
	}
	return nil
}

func hashHasPrefix(hashText, prefix string) bool {
	decoded, err := hex.DecodeString(hashText)
	if err != nil || len(decoded)*8 < len(prefix) {
		return false
	}
	for bit, expected := range []byte(prefix) {
		if hashBit(decoded, bit) != expected-'0' {
			return false
		}
	}
	return true
}

func validateArtifactEnvelope(member Artifact, ordinal int, expectedName string) error {
	if member.Ordinal != ordinal || member.Name != expectedName ||
		filepath.Base(member.Name) != member.Name ||
		member.RecordCount <= 0 || member.RecordCount > MaxRecordsPerArtifact ||
		member.DeclaredBytes < 0 ||
		member.DeclaredBytes > MaxDeclaredBytesPerArtifact ||
		member.ContentBytes <= 0 || member.ContentBytes > maxArtifactBytes ||
		!validDigest(member.ContentDigest) {
		return fmt.Errorf("%w: malformed artifact envelope", ErrInvalidManifest)
	}
	return nil
}

func validateCallerLeaf(leaf CallerLeaf, previous string) error {
	if leaf.PrefixBits < InitialCallerPrefixBits ||
		leaf.PrefixBits > sha256.Size*8 ||
		len(leaf.Prefix) != leaf.PrefixBits ||
		(previous != "" && previous >= leaf.Prefix) {
		return fmt.Errorf("%w: caller leaf is not canonical", ErrInvalidManifest)
	}
	for _, current := range leaf.Prefix {
		if current != '0' && current != '1' {
			return fmt.Errorf("%w: caller leaf has a non-binary prefix", ErrInvalidManifest)
		}
	}
	return nil
}

func validateArtifactFile(
	ctx context.Context,
	filePath string,
	member Artifact,
	visit func(Record) error,
) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() != member.ContentBytes ||
		info.Size() > maxArtifactBytes {
		return errors.New("artifact is special, oversized, or has the wrong size")
	}
	source, err := openStableRegularFile(filePath, info)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := source.file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("phebs-candidate-artifact-v1\x00"))
	counted := &byteCountingReader{reader: source.file}
	reader := bufio.NewReaderSize(io.TeeReader(counted, hasher), 64<<10)
	count := 0
	var declared int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, readErr := readCanonicalLine(reader, maxTreeRecordBytes)
		if len(line) > 0 {
			var record Record
			if err := strictCanonicalJSONLine(line, &record); err != nil {
				return err
			}
			if record.DeclaredBytes > MaxDeclaredBytesPerArtifact-declared {
				return ErrCandidateTooLarge
			}
			declared += record.DeclaredBytes
			count++
			if count > MaxRecordsPerArtifact {
				return errors.New("artifact record count exceeds bound")
			}
			if err := visit(record); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := source.verifyAfterRead(counted.count); err != nil {
		return err
	}
	actualDigest := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if count != member.RecordCount || declared != member.DeclaredBytes ||
		actualDigest != member.ContentDigest {
		return errors.New("artifact count, declared bytes, or digest mismatch")
	}
	return nil
}

func readCanonicalLine(reader *bufio.Reader, limit int) ([]byte, error) {
	if reader == nil || limit <= 0 {
		return nil, errors.New("candidate record has an invalid byte limit")
	}
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > limit-len(line) {
			return nil, errors.New("candidate record exceeds its byte limit")
		}
		if line == nil && err == nil {
			return fragment[:len(fragment):len(fragment)], nil
		}
		line = appendLineFragment(line, fragment, limit)
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return nil, io.EOF
			}
			return nil, errors.New(
				"candidate artifact has a partial trailing record",
			)
		default:
			return nil, err
		}
	}
}

func appendLineFragment(line, fragment []byte, limit int) []byte {
	required := len(line) + len(fragment)
	if cap(line) < required {
		nextCapacity := cap(line) * 2
		if nextCapacity < required {
			nextCapacity = required
		}
		if nextCapacity > limit {
			nextCapacity = limit
		}
		grown := make([]byte, len(line), nextCapacity)
		copy(grown, line)
		line = grown
	}
	return append(line, fragment...)
}

type byteCountingReader struct {
	reader io.Reader
	count  int64
}

func (reader *byteCountingReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	reader.count += int64(count)
	return count, err
}

func strictCanonicalJSONLine(line []byte, destination any) error {
	if len(line) == 0 || line[len(line)-1] != '\n' {
		return errors.New("candidate JSON line is partial")
	}
	if err := strictDecode(
		bytes.NewReader(line[:len(line)-1]), int64(len(line)), destination,
	); err != nil {
		return err
	}
	canonical, err := json.Marshal(destination)
	if err != nil {
		return err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(line, canonical) {
		return errors.New("candidate JSON line is not canonical")
	}
	return nil
}

func validateRecord(
	record Record,
	artifactPlane Plane,
	policies map[string]PolicyIdentity,
	unit *analysisUnitView,
) error {
	if record.Schema != RecordSchema || !safePath(record.Path) ||
		len(record.Path) > maxCandidatePathBytes ||
		!gitobj.IsObjectID(record.OID) ||
		record.DeclaredBytes < 0 ||
		record.DeclaredBytes > MaxDeclaredBytesPerArtifact ||
		len(record.Domains) == 0 ||
		!sortedUnique(record.Domains) ||
		!sortedUnique(record.RequiredDomains) ||
		record.RequiredDomains == nil ||
		record.Shared && !record.InUnit {
		return fmt.Errorf("%w: malformed candidate record", ErrInvalidManifest)
	}
	for _, domain := range record.Domains {
		identity, present := policies[domain]
		if !present {
			return fmt.Errorf("%w: record names unknown domain %q", ErrInvalidManifest, domain)
		}
		if artifactPlane == PlaneCaller && identity.Plane != PlaneCaller ||
			artifactPlane == PlaneRepository && identity.Plane == PlaneCaller {
			return fmt.Errorf("%w: record domain is on the wrong plane", ErrInvalidManifest)
		}
	}
	for _, domain := range record.RequiredDomains {
		if !slices.Contains(record.Domains, domain) {
			return fmt.Errorf("%w: required domain is not enumerated", ErrInvalidManifest)
		}
	}
	if artifactPlane == PlaneCaller {
		decoded, err := hex.DecodeString(record.Hash)
		expected := productionCallerPathHash(record.Path)
		if err != nil || len(decoded) != sha256.Size ||
			hex.EncodeToString(decoded) != record.Hash ||
			record.Hash != hex.EncodeToString(expected[:]) {
			return fmt.Errorf("%w: caller path hash mismatch", ErrInvalidManifest)
		}
	} else if record.Hash != "" {
		return fmt.Errorf("%w: repository record carries caller hash", ErrInvalidManifest)
	}
	if unit.known {
		inUnit, shared, _ := classifyUnitPath(
			record.Path, unit.primary, unit.supporting,
		)
		if record.InUnit != inUnit || record.Shared != shared {
			return fmt.Errorf("%w: candidate unit flags mismatch", ErrInvalidManifest)
		}
	}
	return nil
}

func compareRepositoryRecords(left, right Record) int {
	if value := strings.Compare(left.Path, right.Path); value != 0 {
		return value
	}
	return strings.Compare(left.OID, right.OID)
}

func sortedUnique(values []string) bool {
	for index, value := range values {
		if !validToken(value, 128) ||
			index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func validateCorpusSummary(
	ctx context.Context,
	summary CorpusSummary,
	projectionPath string,
) (resultErr error) {
	if summary.RegularCount < 0 || summary.RegularDeclaredBytes < 0 ||
		summary.GitlinkCount < 0 || summary.GitlinkDeclaredBytes < 0 ||
		summary.SymlinkCount < 0 || summary.SymlinkDeclaredBytes < 0 ||
		summary.RegularCount > MaxCorpusEntries ||
		summary.GitlinkCount > MaxCorpusEntries ||
		summary.SymlinkCount > MaxCorpusEntries ||
		!validDigest(summary.RegularDigest) ||
		!validDigest(summary.GitlinkDigest) ||
		!validDigest(summary.SymlinkDigest) {
		return fmt.Errorf("%w: malformed corpus summary", ErrInvalidManifest)
	}
	empty := newCorpusAccumulator().summary()
	if summary.RegularCount == 0 &&
		(summary.RegularDeclaredBytes != 0 ||
			summary.RegularDigest != empty.RegularDigest) ||
		summary.GitlinkCount == 0 &&
			(summary.GitlinkDeclaredBytes != 0 ||
				summary.GitlinkDigest != empty.GitlinkDigest) ||
		summary.SymlinkCount == 0 &&
			(summary.SymlinkDeclaredBytes != 0 ||
				summary.SymlinkDigest != empty.SymlinkDigest) ||
		summary.GitlinkDeclaredBytes != 0 {
		return fmt.Errorf("%w: empty or gitlink corpus summary mismatch", ErrInvalidManifest)
	}
	if projectionPath == "" {
		return nil
	}
	run, err := openProjectionRun(projectionPath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := run.close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()

	candidateCount := 0
	var candidateBytes int64
	for run.current != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := *run.current
		if err := run.advance(); err != nil {
			return err
		}
		if run.current != nil && run.current.Path == current.Path {
			if run.current.Plane == current.Plane ||
				!sameProjectionIdentity(current, *run.current) {
				return fmt.Errorf(
					"%w: cross-plane candidate mismatch for %q",
					ErrInvalidManifest, current.Path,
				)
			}
			if err := run.advance(); err != nil {
				return err
			}
		}
		if candidateCount == math.MaxInt {
			return fmt.Errorf("%w: candidate count overflows", ErrInvalidManifest)
		}
		candidateCount++
		if current.DeclaredBytes > math.MaxInt64-candidateBytes {
			return fmt.Errorf("%w: candidate bytes overflow", ErrInvalidManifest)
		}
		candidateBytes += current.DeclaredBytes
		if candidateBytes > summary.RegularDeclaredBytes {
			return fmt.Errorf("%w: candidates exceed corpus bytes", ErrInvalidManifest)
		}
	}
	if candidateCount > summary.RegularCount {
		return fmt.Errorf("%w: candidates exceed corpus count", ErrInvalidManifest)
	}
	return nil
}

func validateDirectoryMembership(
	ctx context.Context,
	directory, repository, generation string,
	allowed map[string]bool,
	exactDirectory bool,
) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	base := ArtifactBase(repository)
	generationPrefix := ArtifactPrefix(repository, generation)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		relevant := exactDirectory ||
			name == ManifestName(repository) ||
			name == PublishingName(repository) ||
			strings.HasPrefix(name, generationPrefix) ||
			strings.HasPrefix(name, base+"-")
		if !relevant {
			continue
		}
		if name == PublishingName(repository) && !exactDirectory {
			continue
		}
		if !allowed[name] {
			return fmt.Errorf("%w: undeclared candidate artifact %q", ErrInvalidManifest, name)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("%w: special candidate artifact %q", ErrInvalidManifest, name)
		}
	}
	return nil
}

func unitView(expected Expected) (*analysisUnitView, error) {
	if expected.Unit == nil {
		return &analysisUnitView{known: true}, nil
	}
	if _, err := validateUnit(expected.Repository, expected.Unit); err != nil {
		return nil, err
	}
	return &analysisUnitView{
		primary:    slices.Clone(expected.Unit.PrimaryPaths),
		supporting: slices.Clone(expected.Unit.SupportingPaths),
		known:      true,
	}, nil
}

func forEachCanonicalRecord(
	filePath string,
	visit func(Record) error,
) error {
	return forEachCanonicalRecordContext(
		context.Background(), filePath, visit,
	)
}

func forEachCanonicalRecordContext(
	ctx context.Context,
	filePath string,
	visit func(Record) error,
) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	reader := bufio.NewReaderSize(file, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, readErr := readCanonicalLine(reader, maxTreeRecordBytes)
		if len(line) > 0 {
			var record Record
			if err := strictCanonicalJSONLine(line, &record); err != nil {
				return err
			}
			if err := visit(record); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func cloneManifest(input Manifest) Manifest {
	result := input
	result.Policies = slices.Clone(input.Policies)
	result.Domains = slices.Clone(input.Domains)
	result.RepositoryMembers = slices.Clone(input.RepositoryMembers)
	result.CallerLeaves = slices.Clone(input.CallerLeaves)
	return result
}
