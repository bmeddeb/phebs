package candidate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
)

const (
	maxTreeRecordBytes    = 1 << 20
	maxCandidatePathBytes = repopath.MaxBytes
	maxSymlinkBytes       = 4096
	maxSymlinkDepth       = 16
)

type Request struct {
	RepoDir    string
	OutputDir  string
	Repository string
	Commit     string
	Unit       *analysisunit.State
	Policies   []Policy
}

type treeRecord struct {
	mode    string
	kind    string
	oid     string
	size    int64
	path    string
	rawPath []byte
}

type boundPolicy struct {
	identity      PolicyIdentity
	enumerate     func(string) bool
	required      func(string) bool
	rejectSymlink func(string) bool
}

type domainAccumulator struct {
	summary        DomainSummary
	repositoryHash hash.Hash
	unitHash       hash.Hash
}

type spool struct {
	path          string
	prefix        string
	bits          int
	count         int
	declaredBytes int64
}

// candidatePathHash is a package-private test seam. Production always uses
// the domain-separated SHA-256 implementation below.
var candidatePathHash = productionCallerPathHash

func productionCallerPathHash(value string) [sha256.Size]byte {
	return sha256.Sum256(append([]byte(CallerHashPolicy+"\x00"), []byte(value)...))
}

func Build(ctx context.Context, request Request) (Manifest, error) {
	if !safeRepository(request.Repository) || !gitobj.IsObjectID(request.Commit) {
		return Manifest{}, errors.New("candidate build identity is invalid")
	}
	if !filepath.IsAbs(request.RepoDir) || !filepath.IsAbs(request.OutputDir) {
		return Manifest{}, errors.New("candidate build directories must be absolute")
	}
	if err := gitobj.RejectAlternates(request.RepoDir); err != nil {
		return Manifest{}, fmt.Errorf("candidate repository object store: %w", err)
	}
	if err := gitobj.EnsureCommit(
		ctx, request.RepoDir, request.Commit,
	); err != nil {
		return Manifest{}, fmt.Errorf("candidate commit: %w", err)
	}
	if err := requireEmptyDirectory(request.OutputDir); err != nil {
		return Manifest{}, err
	}
	unitDigest, err := validateUnit(request.Repository, request.Unit)
	if err != nil {
		return Manifest{}, err
	}
	policies, identities, err := bindPolicies(request.Policies)
	if err != nil {
		return Manifest{}, err
	}
	policyDigest, err := PolicyDigest(identities)
	if err != nil {
		return Manifest{}, err
	}
	typedIndex, err := typedIndexSelection(request.Unit, identities)
	if err != nil {
		return Manifest{}, err
	}
	generationDigest, err := GenerationDigest(
		request.Repository, request.Commit, request.Unit, identities,
	)
	if err != nil {
		return Manifest{}, err
	}

	spoolDir, err := os.MkdirTemp(request.OutputDir, ".candidate-spool-")
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = os.RemoveAll(spoolDir) }()

	repositoryWriter := newArtifactPacker(
		request.OutputDir, request.Repository, generationDigest, "repository",
	)
	defer func() { _ = repositoryWriter.abort() }()
	localProjectionBudget := &artifactBudget{
		maxMembers: MaxLocalProjectionArtifacts,
		maxBytes:   MaxLocalProjectionContentBytes,
	}
	localProjectionWriters := make(map[string]*artifactPacker)
	localProjections := make([]LocalProjection, 0)
	if request.Unit != nil {
		for policyOrdinal, identity := range identities {
			if identity.Plane != PlaneLocal {
				continue
			}
			writer := newArtifactPacker(
				request.OutputDir, request.Repository, generationDigest,
				fmt.Sprintf("local-%03d", policyOrdinal),
			)
			writer.budget = localProjectionBudget
			localProjectionWriters[identity.Domain] = writer
			localProjections = append(localProjections, LocalProjection{
				Domain: identity.Domain, Version: identity.Version,
				PolicyOrdinal: policyOrdinal, Members: []Artifact{},
			})
		}
	}
	defer func() {
		for _, writer := range localProjectionWriters {
			_ = writer.abort()
		}
	}()
	callerSpools := make(map[string]*spool)
	corpus := newCorpusAccumulator()
	unitCorpus := newCorpusAccumulator()
	typedInputs := make([]TypedInput, 0, 1)
	typedInputIndex := -1
	typedIndexPresent := false
	if typedIndex != nil {
		domains := typedInputDomains(identities, typedIndex.Kind)
		if len(domains) > 0 {
			typedInputs = append(typedInputs, TypedInput{
				Kind: typedIndex.Kind, Path: typedIndex.Path,
				Domains: domains,
				InUnit:  request.Unit == nil,
			})
			typedInputIndex = 0
		}
	}
	selected := selectedPaths(request.Unit)
	var previousTreePath []byte
	var primaryPaths, supportingPaths []string
	if request.Unit != nil {
		primaryPaths = request.Unit.PrimaryPaths
		supportingPaths = request.Unit.SupportingPaths
	}

	err = walkTree(ctx, request.RepoDir, request.Commit, func(current treeRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(previousTreePath) > 0 &&
			bytesCompare(previousTreePath, current.rawPath) >= 0 {
			return errors.New("git tree paths are not sorted and unique")
		}
		previousTreePath = append(previousTreePath[:0], current.rawPath...)
		if err := corpus.add(current); err != nil {
			return err
		}
		inUnit, shared, selectedRoots := classifyUnitPath(
			current.path, primaryPaths, supportingPaths,
		)
		for _, root := range selectedRoots {
			selected[root] = true
		}
		canonicalPath := safePath(current.path) &&
			len(current.path) <= maxCandidatePathBytes
		if inUnit && !canonicalPath {
			return fmt.Errorf(
				"%w: scoped path %q is not canonical and bounded",
				ErrSelectedPath, current.path,
			)
		}
		if inUnit && !isRegular(current) {
			return fmt.Errorf(
				"%w: %q has unsupported mode %s",
				ErrSelectedPath, current.path, current.mode,
			)
		}
		if inUnit {
			if err := unitCorpus.add(current); err != nil {
				return err
			}
		}
		if typedIndex != nil && current.path == typedIndex.Path {
			if !isRegular(current) {
				return fmt.Errorf(
					"%w: typed index %q has unsupported mode %s",
					ErrSelectedPath, current.path, current.mode,
				)
			}
			if current.size > MaxDeclaredBytesPerArtifact {
				return fmt.Errorf(
					"%w: typed index %q declares %d bytes",
					ErrCandidateTooLarge, current.path, current.size,
				)
			}
			typedIndexPresent = true
			if typedInputIndex >= 0 {
				typedInputs[typedInputIndex].OID = current.oid
				typedInputs[typedInputIndex].DeclaredBytes = current.size
				typedInputs[typedInputIndex].Present = true
				typedInputs[typedInputIndex].InUnit = request.Unit == nil || inUnit
				typedInputs[typedInputIndex].Shared = request.Unit != nil && shared
			}
		}
		// A valid gitlink is a repository boundary regardless of how its path
		// happens to match an extractor policy. Its census commitment above is
		// authoritative; it is never a retained/readable candidate.
		if current.mode == "160000" {
			return nil
		}
		repositoryDomains, repositoryRequired, callerDomains, callerRequired, err :=
			evaluatePolicies(policies, current.path)
		if err != nil {
			return err
		}
		selectedCandidate := len(repositoryDomains)+len(callerDomains) > 0
		if current.mode == "120000" {
			if err := rejectCandidateSymlink(
				policies, current.path,
			); err != nil {
				return err
			}
			if len(repositoryRequired)+len(callerRequired) > 0 {
				if !canonicalPath {
					return fmt.Errorf(
						"candidate Git path is not canonical and bounded: %q",
						current.path,
					)
				}
				if err := validateSymlinkAlias(
					ctx, request.RepoDir, request.Commit, current,
					policies, repositoryRequired, callerRequired,
				); err != nil {
					return err
				}
			}
			return nil
		}
		if !canonicalPath {
			if selectedCandidate {
				return fmt.Errorf(
					"candidate Git path is not canonical and bounded: %q", current.path,
				)
			}
			return nil
		}
		if !isRegular(current) {
			if len(repositoryDomains)+len(callerDomains) > 0 {
				return fmt.Errorf("candidate %q has unsupported Git mode %s", current.path, current.mode)
			}
			return nil
		}
		if current.size > MaxDeclaredBytesPerArtifact &&
			len(repositoryDomains)+len(callerDomains) > 0 {
			return fmt.Errorf("%w: %q declares %d bytes", ErrCandidateTooLarge, current.path, current.size)
		}

		if len(repositoryDomains) > 0 {
			record := newRecord(
				current, repositoryDomains, repositoryRequired, inUnit, shared, "",
			)
			if err := repositoryWriter.add(record); err != nil {
				return err
			}
			if record.InUnit {
				for _, domain := range record.Domains {
					writer := localProjectionWriters[domain]
					if writer == nil {
						continue
					}
					if err := writer.add(record); err != nil {
						return fmt.Errorf(
							"pack local projection %q: %w", domain, err,
						)
					}
				}
			}
		}
		if len(callerDomains) > 0 {
			sum := candidatePathHash(current.path)
			hashText := hex.EncodeToString(sum[:])
			record := newRecord(
				current, callerDomains, callerRequired, inUnit, shared, hashText,
			)
			prefix := hashPrefix(sum, InitialCallerPrefixBits)
			currentSpool := callerSpools[prefix]
			if currentSpool == nil {
				currentSpool = &spool{
					path:   filepath.Join(spoolDir, "root-"+prefix+".ndjson"),
					prefix: prefix, bits: InitialCallerPrefixBits,
				}
				callerSpools[prefix] = currentSpool
			}
			if err := appendSpoolRecord(currentSpool, record); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	for root, found := range selected {
		if !found {
			return Manifest{}, fmt.Errorf("%w: %q", ErrSelectedPath, root)
		}
	}
	if typedIndex != nil && request.Unit != nil && !typedIndexPresent {
		return Manifest{}, fmt.Errorf(
			"%w: typed index %q is not a regular file",
			ErrSelectedPath, typedIndex.Path,
		)
	}
	repositoryMembers, err := repositoryWriter.finish()
	if err != nil {
		return Manifest{}, err
	}
	for index := range localProjections {
		projection := &localProjections[index]
		members, finishErr :=
			localProjectionWriters[projection.Domain].finish()
		if finishErr != nil {
			return Manifest{}, fmt.Errorf(
				"finish local projection %q: %w",
				projection.Domain, finishErr,
			)
		}
		projection.Members = members
	}
	callerLeaves, err := planCallerLeaves(
		ctx, spoolDir, request.OutputDir, request.Repository, generationDigest, callerSpools,
	)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.RemoveAll(spoolDir); err != nil {
		return Manifest{}, err
	}
	accumulators, err := summarizeCanonicalArtifacts(
		ctx, request.OutputDir, identities, repositoryMembers, callerLeaves,
	)
	if err != nil {
		return Manifest{}, err
	}

	corpusSummary := corpus.summary()
	unitCorpusSummary := unitCorpus.summary()
	if request.Unit == nil {
		unitCorpusSummary = corpusSummary
	}
	manifest := Manifest{
		Schema: ManifestSchema, Repository: request.Repository, Commit: request.Commit,
		UnitDigest: unitDigest, PolicyDigest: policyDigest,
		GenerationDigest: generationDigest,
		TypedIndex:       cloneTypedIndexSelection(typedIndex),
		PartitionPolicy:  frozenPartitionPolicy(),
		Policies:         identities, Corpus: corpusSummary, UnitCorpus: unitCorpusSummary,
		Domains:           finishDomainAccumulators(accumulators),
		TypedInputs:       typedInputs,
		RepositoryMembers: repositoryMembers,
		LocalProjections:  localProjections,
		CallerLeaves:      callerLeaves,
	}
	manifest.Digest, err = ManifestDigest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := writeJSONFile(
		filepath.Join(request.OutputDir, ManifestName(request.Repository)), manifest,
	); err != nil {
		return Manifest{}, err
	}
	if err := syncDirectory(request.OutputDir); err != nil {
		return Manifest{}, err
	}
	if _, err := ValidateStageContext(ctx, request.OutputDir, Expected{
		Repository: request.Repository, Commit: request.Commit, Unit: request.Unit,
		Policies: identities, PolicyDigest: policyDigest,
		GenerationDigest: generationDigest, ManifestDigest: manifest.Digest,
	}); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func typedInputDomains(identities []PolicyIdentity, kind string) []string {
	domains := make([]string, 0)
	for _, identity := range identities {
		if slices.Contains(identity.TypedInputs, kind) {
			domains = append(domains, identity.Domain)
		}
	}
	return domains
}

func summarizeCanonicalArtifacts(
	ctx context.Context,
	directory string,
	identities []PolicyIdentity,
	repositoryMembers []Artifact,
	callerLeaves []CallerLeaf,
) (map[string]*domainAccumulator, error) {
	accumulators := makeDomainAccumulators(identities)
	members := slices.Clone(repositoryMembers)
	for _, leaf := range callerLeaves {
		members = append(members, leaf.Artifact)
	}
	for _, member := range members {
		if err := forEachCanonicalRecordContext(
			ctx,
			filepath.Join(directory, member.Name),
			func(record Record) error {
				return addDomainRecords(
					accumulators, record, record.Domains, record.RequiredDomains,
				)
			},
		); err != nil {
			return nil, fmt.Errorf(
				"summarize candidate member %q: %w", member.Name, err,
			)
		}
	}
	return accumulators, nil
}

func bindPolicies(input []Policy) ([]boundPolicy, []PolicyIdentity, error) {
	identities, err := PolicyIdentities(input)
	if err != nil {
		return nil, nil, err
	}
	byKey := make(map[string]Policy, len(input))
	for _, policy := range input {
		byKey[policy.Domain+"\x00"+policy.Version] = policy
	}
	bound := make([]boundPolicy, 0, len(identities))
	for _, identity := range identities {
		policy := byKey[identity.Domain+"\x00"+identity.Version]
		bound = append(bound, boundPolicy{
			identity: identity, enumerate: policy.Enumerate,
			required: policy.Required, rejectSymlink: policy.RejectSymlink,
		})
	}
	return bound, identities, nil
}

func evaluatePolicies(
	policies []boundPolicy, candidatePath string,
) (repositoryDomains, repositoryRequired, callerDomains, callerRequired []string, err error) {
	for _, policy := range policies {
		enumerated, evalErr := callPredicate(policy.enumerate, candidatePath)
		if evalErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("%s enumerate %q: %w", policy.identity.Domain, candidatePath, evalErr)
		}
		required := false
		if policy.required != nil {
			required, evalErr = callPredicate(policy.required, candidatePath)
			if evalErr != nil {
				return nil, nil, nil, nil, fmt.Errorf("%s required %q: %w", policy.identity.Domain, candidatePath, evalErr)
			}
		}
		if !enumerated && !required {
			continue
		}
		if required && !enumerated {
			return nil, nil, nil, nil, fmt.Errorf(
				"%w: %s required path %q is outside its enumeration policy",
				ErrInvalidPolicy, policy.identity.Domain, candidatePath,
			)
		}
		if policy.identity.Plane == PlaneCaller {
			callerDomains = append(callerDomains, policy.identity.Domain)
			if required {
				callerRequired = append(callerRequired, policy.identity.Domain)
			}
		} else {
			repositoryDomains = append(repositoryDomains, policy.identity.Domain)
			if required {
				repositoryRequired = append(repositoryRequired, policy.identity.Domain)
			}
		}
	}
	return
}

func callPredicate(predicate func(string) bool, value string) (result bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("predicate panic: %v", recovered)
		}
	}()
	return predicate(value), nil
}

func walkTree(
	ctx context.Context, repoDir, commit string, visit func(treeRecord) error,
) error {
	args := []string{"ls-tree", "-r", "-l", "-z", "--full-tree", commit}
	command := gitobj.Command(ctx, repoDir, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr gitobj.StderrBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(stdout, 64<<10)
	var walkErr error
	for {
		raw, readErr := readNULRecord(reader, maxTreeRecordBytes)
		if len(raw) > 0 {
			current, parseErr := parseTreeRecord(raw)
			if parseErr != nil {
				walkErr = parseErr
				break
			}
			if visitErr := visit(current); visitErr != nil {
				walkErr = visitErr
				break
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			walkErr = readErr
			break
		}
	}
	if walkErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return walkErr
	}
	if err := command.Wait(); err != nil {
		return gitobj.WrapError(ctx, args, err, stderr.String())
	}
	return nil
}

func readNULRecord(reader *bufio.Reader, limit int) ([]byte, error) {
	var result []byte
	for {
		part, err := reader.ReadSlice(0)
		if len(result)+len(part) > limit+1 {
			return nil, fmt.Errorf("git tree record exceeds %d-byte limit", limit)
		}
		result = append(result, part...)
		if err == nil {
			return result[:len(result)-1], nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if errors.Is(err, io.EOF) && len(result) > 0 {
				return nil, errors.New("git tree output has an unterminated record")
			}
			return result, err
		}
	}
}

func parseTreeRecord(raw []byte) (treeRecord, error) {
	header, pathBytes, found := bytesCut(raw, '\t')
	if !found || len(pathBytes) == 0 {
		return treeRecord{}, errors.New("git tree returned a malformed path")
	}
	fields := strings.Fields(string(header))
	if len(fields) != 4 || !gitobj.IsObjectID(fields[2]) {
		return treeRecord{}, errors.New("git tree returned malformed metadata")
	}
	size := int64(0)
	if fields[3] != "-" {
		parsed, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || parsed < 0 {
			return treeRecord{}, errors.New("git tree returned an invalid declared size")
		}
		size = parsed
	}
	return treeRecord{
		mode: fields[0], kind: fields[1], oid: fields[2], size: size,
		path: string(pathBytes), rawPath: slices.Clone(pathBytes),
	}, nil
}

func bytesCompare(left, right []byte) int {
	return strings.Compare(string(left), string(right))
}

func bytesCut(input []byte, separator byte) ([]byte, []byte, bool) {
	index := slices.Index(input, separator)
	if index < 0 {
		return input, nil, false
	}
	return input[:index], input[index+1:], true
}

func isRegular(record treeRecord) bool {
	return record.kind == "blob" && (record.mode == "100644" || record.mode == "100755")
}

func selectedPaths(unit *analysisunit.State) map[string]bool {
	if unit == nil {
		return map[string]bool{}
	}
	result := make(map[string]bool, len(unit.PrimaryPaths)+len(unit.SupportingPaths))
	for _, current := range unit.PrimaryPaths {
		result[current] = false
	}
	for _, current := range unit.SupportingPaths {
		result[current] = false
	}
	return result
}

func classifyUnitPath(
	value string, primary, supporting []string,
) (inUnit, shared bool, roots []string) {
	for _, root := range primary {
		if value == root || strings.HasPrefix(value, root+"/") {
			inUnit = true
			roots = append(roots, root)
		}
	}
	for _, root := range supporting {
		if value == root || strings.HasPrefix(value, root+"/") {
			inUnit = true
			shared = true
			roots = append(roots, root)
		}
	}
	return
}

func newRecord(
	current treeRecord, domains, required []string, inUnit, shared bool, hashText string,
) Record {
	return Record{
		Schema: RecordSchema, Path: current.path, OID: current.oid,
		DeclaredBytes: current.size, Domains: append([]string{}, domains...),
		RequiredDomains: append([]string{}, required...), InUnit: inUnit,
		Shared: shared, Hash: hashText,
	}
}

type corpusAccumulator struct {
	regularHash, gitlinkHash, symlinkHash    hash.Hash
	regularCount, gitlinkCount, symlinkCount int
	regularBytes, gitlinkBytes, symlinkBytes int64
}

func newCorpusAccumulator() *corpusAccumulator {
	result := &corpusAccumulator{
		regularHash: sha256.New(), gitlinkHash: sha256.New(), symlinkHash: sha256.New(),
	}
	_, _ = result.regularHash.Write([]byte("phebs-candidate-corpus-regular-v1\x00"))
	_, _ = result.gitlinkHash.Write([]byte("phebs-candidate-corpus-gitlink-v1\x00"))
	_, _ = result.symlinkHash.Write([]byte("phebs-candidate-corpus-symlink-v1\x00"))
	return result
}

func (corpus *corpusAccumulator) add(current treeRecord) error {
	switch {
	case isRegular(current):
		if corpus.regularCount >= MaxCorpusEntries {
			return fmt.Errorf(
				"%w: regular entries exceed %d",
				ErrCorpusTooLarge, MaxCorpusEntries,
			)
		}
		if current.size > math.MaxInt64-corpus.regularBytes {
			return errors.New("regular corpus summary overflows")
		}
		corpus.regularCount++
		corpus.regularBytes += current.size
		return hashTreeRecord(corpus.regularHash, current)
	case current.mode == "160000" && current.kind == "commit":
		if corpus.gitlinkCount >= MaxCorpusEntries {
			return fmt.Errorf(
				"%w: gitlink entries exceed %d",
				ErrCorpusTooLarge, MaxCorpusEntries,
			)
		}
		if current.size > math.MaxInt64-corpus.gitlinkBytes {
			return errors.New("gitlink corpus summary overflows")
		}
		corpus.gitlinkCount++
		corpus.gitlinkBytes += current.size
		return hashTreeRecord(corpus.gitlinkHash, current)
	case current.mode == "120000" && current.kind == "blob":
		if corpus.symlinkCount >= MaxCorpusEntries {
			return fmt.Errorf(
				"%w: symlink entries exceed %d",
				ErrCorpusTooLarge, MaxCorpusEntries,
			)
		}
		if current.size > math.MaxInt64-corpus.symlinkBytes {
			return errors.New("symlink corpus summary overflows")
		}
		corpus.symlinkCount++
		corpus.symlinkBytes += current.size
		return hashTreeRecord(corpus.symlinkHash, current)
	default:
		return fmt.Errorf(
			"unsupported Git tree entry %q with mode %s and type %s",
			current.path, current.mode, current.kind,
		)
	}
}

func hashTreeRecord(destination hash.Hash, current treeRecord) error {
	for _, value := range [][]byte{
		current.rawPath,
		[]byte(current.mode),
		[]byte(current.kind),
		[]byte(current.oid),
		[]byte(strconv.FormatInt(current.size, 10)),
	} {
		_, _ = destination.Write([]byte(strconv.Itoa(len(value))))
		_, _ = destination.Write([]byte{':'})
		_, _ = destination.Write(value)
	}
	_, _ = destination.Write([]byte{0})
	return nil
}

func (corpus *corpusAccumulator) summary() CorpusSummary {
	return CorpusSummary{
		RegularCount: corpus.regularCount, RegularDeclaredBytes: corpus.regularBytes,
		RegularDigest: "sha256:" + hex.EncodeToString(corpus.regularHash.Sum(nil)),
		GitlinkCount:  corpus.gitlinkCount, GitlinkDeclaredBytes: corpus.gitlinkBytes,
		GitlinkDigest: "sha256:" + hex.EncodeToString(corpus.gitlinkHash.Sum(nil)),
		SymlinkCount:  corpus.symlinkCount, SymlinkDeclaredBytes: corpus.symlinkBytes,
		SymlinkDigest: "sha256:" + hex.EncodeToString(corpus.symlinkHash.Sum(nil)),
	}
}

func makeDomainAccumulators(identities []PolicyIdentity) map[string]*domainAccumulator {
	result := make(map[string]*domainAccumulator, len(identities))
	for _, identity := range identities {
		current := &domainAccumulator{
			summary: DomainSummary{
				Domain: identity.Domain, Version: identity.Version,
				EnumerationPolicy: identity.EnumerationPolicy,
				SymlinkPolicy:     identity.SymlinkPolicy, Plane: identity.Plane,
			},
			repositoryHash: sha256.New(),
			unitHash:       sha256.New(),
		}
		_, _ = current.repositoryHash.Write([]byte("phebs-candidate-domain-repository-v1\x00"))
		_, _ = current.unitHash.Write([]byte("phebs-candidate-domain-unit-v1\x00"))
		result[identity.Domain] = current
	}
	return result
}

func addDomainRecords(
	accumulators map[string]*domainAccumulator,
	record Record,
	domains, required []string,
) error {
	requiredSet := make(map[string]bool, len(required))
	for _, domain := range required {
		requiredSet[domain] = true
	}
	for _, domain := range domains {
		current := accumulators[domain]
		if current == nil ||
			current.summary.RepositoryCandidateCount == math.MaxInt ||
			record.DeclaredBytes >
				math.MaxInt64-current.summary.RepositoryDeclaredBytes {
			return errors.New("candidate domain repository summary overflows")
		}
		current.summary.RepositoryCandidateCount++
		current.summary.RepositoryDeclaredBytes += record.DeclaredBytes
		if requiredSet[domain] {
			if current.summary.RepositoryRequiredCount == math.MaxInt {
				return errors.New("candidate domain required summary overflows")
			}
			current.summary.RepositoryRequiredCount++
		}
		payload, _ := json.Marshal(struct {
			Path     string `json:"path"`
			OID      string `json:"oid"`
			Size     int64  `json:"declared_bytes"`
			InUnit   bool   `json:"in_unit"`
			Shared   bool   `json:"shared"`
			Required bool   `json:"required"`
		}{record.Path, record.OID, record.DeclaredBytes, record.InUnit, record.Shared, requiredSet[domain]})
		_, _ = current.repositoryHash.Write(payload)
		_, _ = current.repositoryHash.Write([]byte{'\n'})
		if record.InUnit {
			if current.summary.UnitCandidateCount == math.MaxInt ||
				record.DeclaredBytes >
					math.MaxInt64-current.summary.UnitDeclaredBytes {
				return errors.New("candidate domain unit summary overflows")
			}
			current.summary.UnitCandidateCount++
			current.summary.UnitDeclaredBytes += record.DeclaredBytes
			if requiredSet[domain] {
				if current.summary.UnitRequiredCount == math.MaxInt {
					return errors.New("candidate unit required summary overflows")
				}
				current.summary.UnitRequiredCount++
			}
			_, _ = current.unitHash.Write(payload)
			_, _ = current.unitHash.Write([]byte{'\n'})
		}
	}
	return nil
}

func finishDomainAccumulators(input map[string]*domainAccumulator) []DomainSummary {
	result := make([]DomainSummary, 0, len(input))
	for _, current := range input {
		current.summary.RepositoryDigest = "sha256:" + hex.EncodeToString(current.repositoryHash.Sum(nil))
		current.summary.UnitDigest = "sha256:" + hex.EncodeToString(current.unitHash.Sum(nil))
		result = append(result, current.summary)
	}
	slices.SortFunc(result, func(a, b DomainSummary) int {
		return strings.Compare(a.Domain, b.Domain)
	})
	return result
}

type artifactPacker struct {
	outputDir, repository, generation, plane string
	members                                  []Artifact
	nextOrdinal                              int
	file                                     *os.File
	hash                                     hash.Hash
	current                                  Artifact
	budget                                   *artifactBudget
}

type artifactBudget struct {
	maxMembers, members int
	maxBytes, bytes     int64
}

func (budget *artifactBudget) reserveMember() error {
	if budget == nil {
		return nil
	}
	if budget.members >= budget.maxMembers {
		return fmt.Errorf(
			"%w: local projections exceed %d artifacts",
			ErrCandidateTooLarge, budget.maxMembers,
		)
	}
	budget.members++
	return nil
}

func (budget *artifactBudget) reserveBytes(count int64) error {
	if budget == nil {
		return nil
	}
	if count < 0 || count > budget.maxBytes-budget.bytes {
		return fmt.Errorf(
			"%w: local projections exceed %d content bytes",
			ErrCandidateTooLarge, budget.maxBytes,
		)
	}
	budget.bytes += count
	return nil
}

func newArtifactPacker(outputDir, repository, generation, plane string) *artifactPacker {
	return &artifactPacker{
		outputDir: outputDir, repository: repository, generation: generation, plane: plane,
	}
}

func (packer *artifactPacker) add(record Record) error {
	if record.DeclaredBytes > MaxDeclaredBytesPerArtifact {
		return fmt.Errorf("%w: %q", ErrCandidateTooLarge, record.Path)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if len(payload) > maxTreeRecordBytes {
		return errors.New("candidate record exceeds its canonical byte limit")
	}
	if packer.file != nil &&
		(packer.current.RecordCount >= MaxRecordsPerArtifact ||
			record.DeclaredBytes >
				MaxDeclaredBytesPerArtifact-packer.current.DeclaredBytes) {
		if err := packer.flush(); err != nil {
			return err
		}
	}
	if packer.file == nil {
		if err := packer.start(); err != nil {
			return err
		}
	}
	if err := packer.budget.reserveBytes(int64(len(payload))); err != nil {
		return err
	}
	written, err := packer.file.Write(payload)
	if err != nil {
		return err
	}
	_, _ = packer.hash.Write(payload)
	packer.current.RecordCount++
	packer.current.DeclaredBytes += record.DeclaredBytes
	packer.current.ContentBytes += int64(written)
	return nil
}

func (packer *artifactPacker) start() error {
	if err := packer.budget.reserveMember(); err != nil {
		return err
	}
	ordinal := packer.nextOrdinal
	name := ArtifactPrefix(packer.repository, packer.generation) +
		fmt.Sprintf("%s-%06d.ndjson", packer.plane, ordinal)
	file, err := os.OpenFile(
		filepath.Join(packer.outputDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
	)
	if err != nil {
		return err
	}
	packer.file = file
	packer.hash = sha256.New()
	_, _ = packer.hash.Write([]byte("phebs-candidate-artifact-v1\x00"))
	packer.current = Artifact{Name: name, Ordinal: ordinal}
	return nil
}

func (packer *artifactPacker) flush() error {
	if packer.file == nil {
		return nil
	}
	if err := packer.file.Sync(); err != nil {
		closeErr := packer.file.Close()
		packer.file = nil
		return errors.Join(err, closeErr)
	}
	if err := packer.file.Close(); err != nil {
		packer.file = nil
		return err
	}
	packer.current.ContentDigest = "sha256:" + hex.EncodeToString(packer.hash.Sum(nil))
	packer.members = append(packer.members, packer.current)
	packer.nextOrdinal++
	packer.file = nil
	return nil
}

// abort closes an unfinished member without making it part of the canonical
// member inventory. It is idempotent so every packer owner can defer it across
// predicate, walk, encoding, and publication failures.
func (packer *artifactPacker) abort() error {
	if packer == nil || packer.file == nil {
		return nil
	}
	file := packer.file
	packer.file = nil
	packer.hash = nil
	packer.current = Artifact{}
	return file.Close()
}

func (packer *artifactPacker) finish() ([]Artifact, error) {
	if err := packer.flush(); err != nil {
		return nil, err
	}
	return append([]Artifact{}, packer.members...), nil
}

func appendSpoolRecord(destination *spool, record Record) error {
	if destination.count == math.MaxInt ||
		record.DeclaredBytes > math.MaxInt64-destination.declaredBytes {
		return errors.New("caller spool summary overflows")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(payload)+1 > maxTreeRecordBytes {
		return errors.New("candidate record exceeds its canonical byte limit")
	}
	file, err := os.OpenFile(destination.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	destination.count++
	destination.declaredBytes += record.DeclaredBytes
	return nil
}

func planCallerLeaves(
	ctx context.Context,
	spoolDir, outputDir, repository, generation string,
	roots map[string]*spool,
) ([]CallerLeaf, error) {
	prefixes := make([]string, 0, len(roots))
	for prefix := range roots {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	var planned []*spool
	for _, prefix := range prefixes {
		if err := splitSpool(ctx, spoolDir, roots[prefix], &planned); err != nil {
			return nil, err
		}
	}
	leaves := make([]CallerLeaf, 0, len(planned))
	for ordinal, current := range planned {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		records, err := readSpoolRecords(
			ctx, current.path, current.count,
		)
		if err != nil {
			return nil, err
		}
		slices.SortFunc(records, compareCallerRecords)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		packer := newArtifactPacker(outputDir, repository, generation, "caller")
		packer.nextOrdinal = ordinal
		members, err := packArtifactRecords(ctx, packer, records)
		if err != nil {
			return nil, err
		}
		if len(members) != 1 {
			return nil, errors.New("bounded caller leaf produced multiple artifacts")
		}
		member := members[0]
		leaves = append(leaves, CallerLeaf{
			Artifact: member, Prefix: current.prefix, PrefixBits: current.bits,
		})
		if err := os.Remove(current.path); err != nil {
			return nil, err
		}
	}
	return leaves, nil
}

func packArtifactRecords(
	ctx context.Context,
	packer *artifactPacker,
	records []Record,
) ([]Artifact, error) {
	defer func() { _ = packer.abort() }()
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := packer.add(record); err != nil {
			return nil, err
		}
	}
	return packer.finish()
}

func splitSpool(ctx context.Context, spoolDir string, current *spool, leaves *[]*spool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if current.count <= MaxRecordsPerArtifact &&
		current.declaredBytes <= MaxDeclaredBytesPerArtifact {
		*leaves = append(*leaves, current)
		return nil
	}
	if current.bits >= sha256.Size*8 {
		return fmt.Errorf("%w: prefix %s has %d records", ErrHashCollision, current.prefix, current.count)
	}
	firstHash := ""
	allSameHash := true
	if err := forEachSpoolRecord(ctx, current.path, func(record Record) error {
		if firstHash == "" {
			firstHash = record.Hash
		} else if record.Hash != firstHash {
			allSameHash = false
		}
		return nil
	}); err != nil {
		return err
	}
	if allSameHash {
		return fmt.Errorf(
			"%w: %d records share hash %s",
			ErrHashCollision, current.count, firstHash,
		)
	}
	left := &spool{
		path:   filepath.Join(spoolDir, "split-"+current.prefix+"0.ndjson"),
		prefix: current.prefix + "0", bits: current.bits + 1,
	}
	right := &spool{
		path:   filepath.Join(spoolDir, "split-"+current.prefix+"1.ndjson"),
		prefix: current.prefix + "1", bits: current.bits + 1,
	}
	if err := forEachSpoolRecord(ctx, current.path, func(record Record) error {
		hashBytes, err := hex.DecodeString(record.Hash)
		if err != nil || len(hashBytes) != sha256.Size {
			return errors.New("spooled caller hash is invalid")
		}
		destination := left
		if hashBit(hashBytes, current.bits) == 1 {
			destination = right
		}
		return appendSpoolRecord(destination, record)
	}); err != nil {
		return err
	}
	if err := os.Remove(current.path); err != nil {
		return err
	}
	for _, child := range []*spool{left, right} {
		if child.count == 0 {
			continue
		}
		if err := splitSpool(ctx, spoolDir, child, leaves); err != nil {
			return err
		}
	}
	return nil
}

func readSpoolRecords(
	ctx context.Context,
	filePath string,
	expected int,
) ([]Record, error) {
	result := make([]Record, 0, expected)
	err := forEachSpoolRecord(ctx, filePath, func(record Record) error {
		result = append(result, record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(result) != expected {
		return nil, errors.New("caller spool record count mismatch")
	}
	return result, nil
}

func forEachSpoolRecord(
	ctx context.Context,
	filePath string,
	visit func(Record) error,
) error {
	return forEachCanonicalRecordContext(ctx, filePath, visit)
}

func compareCallerRecords(left, right Record) int {
	if value := strings.Compare(left.Hash, right.Hash); value != 0 {
		return value
	}
	if value := strings.Compare(left.Path, right.Path); value != 0 {
		return value
	}
	return strings.Compare(left.OID, right.OID)
}

func hashPrefix(value [sha256.Size]byte, bits int) string {
	var builder strings.Builder
	builder.Grow(bits)
	for index := 0; index < bits; index++ {
		builder.WriteByte('0' + hashBit(value[:], index))
	}
	return builder.String()
}

func hashBit(value []byte, bit int) byte {
	return (value[bit/8] >> (7 - uint(bit%8))) & 1
}

func validateSymlinkAlias(
	ctx context.Context,
	repoDir, commit string,
	alias treeRecord,
	policies []boundPolicy,
	repositoryRequired, callerRequired []string,
) error {
	expected := make(
		map[string]bool,
		len(repositoryRequired)+len(callerRequired),
	)
	for _, domain := range repositoryRequired {
		expected[domain] = true
	}
	for _, domain := range callerRequired {
		expected[domain] = true
	}
	current := alias
	seen := make(map[string]bool)
	for links := 0; ; links++ {
		if seen[current.path] {
			return fmt.Errorf("candidate symlink %q has a cycle", alias.path)
		}
		seen[current.path] = true
		if current.mode != "120000" {
			if !isRegular(current) {
				return fmt.Errorf("candidate symlink %q targets unsupported mode %s", alias.path, current.mode)
			}
			for _, policy := range policies {
				if !expected[policy.identity.Domain] {
					continue
				}
				ok, err := callPredicate(policy.enumerate, current.path)
				if err != nil || !ok {
					return fmt.Errorf("candidate symlink %q target %q is not selected by %s", alias.path, current.path, policy.identity.Domain)
				}
				required := false
				if policy.required != nil {
					required, err = callPredicate(
						policy.required, current.path,
					)
				}
				if err != nil || !required {
					return fmt.Errorf(
						"candidate symlink %q target %q does not preserve required domain %s",
						alias.path, current.path, policy.identity.Domain,
					)
				}
			}
			return nil
		}
		if links >= maxSymlinkDepth {
			return fmt.Errorf(
				"candidate symlink %q exceeds %d-link depth",
				alias.path, maxSymlinkDepth,
			)
		}
		target, err := gitobj.ReadBlob(ctx, repoDir, current.oid, maxSymlinkBytes)
		if err != nil {
			return fmt.Errorf("read candidate symlink %q: %w", current.path, err)
		}
		targetPath, err := resolveSymlinkTarget(current.path, string(target))
		if err != nil {
			return err
		}
		current, err = lookupTreeRecord(ctx, repoDir, commit, targetPath)
		if err != nil {
			return fmt.Errorf("candidate symlink %q target: %w", alias.path, err)
		}
	}
}

func rejectCandidateSymlink(
	policies []boundPolicy,
	candidatePath string,
) error {
	for _, policy := range policies {
		if policy.rejectSymlink == nil {
			continue
		}
		rejected, err := callPredicate(
			policy.rejectSymlink, candidatePath,
		)
		if err != nil {
			return fmt.Errorf(
				"%s symlink policy %q: %w",
				policy.identity.Domain, candidatePath, err,
			)
		}
		if rejected {
			return fmt.Errorf(
				"candidate symlink %q is forbidden by %s policy %s",
				candidatePath, policy.identity.Domain,
				policy.identity.SymlinkPolicy,
			)
		}
	}
	return nil
}

func resolveSymlinkTarget(linkPath, target string) (string, error) {
	if target == "" || path.IsAbs(target) || strings.ContainsRune(target, 0) ||
		!utf8.ValidString(target) {
		return "", fmt.Errorf("candidate symlink %q has an unsafe target", linkPath)
	}
	resolved := path.Clean(path.Join(path.Dir(linkPath), target))
	if !safePath(resolved) {
		return "", fmt.Errorf("candidate symlink %q escapes the repository", linkPath)
	}
	return resolved, nil
}

func lookupTreeRecord(
	ctx context.Context, repoDir, commit, candidatePath string,
) (treeRecord, error) {
	raw, err := gitobj.Output(
		ctx, repoDir, maxTreeRecordBytes,
		"ls-tree", "-l", "-z", "--full-tree", commit, "--",
		":(literal)"+candidatePath,
	)
	if err != nil {
		return treeRecord{}, err
	}
	if len(raw) == 0 || raw[len(raw)-1] != 0 {
		return treeRecord{}, errors.New("symlink target is missing")
	}
	current, err := parseTreeRecord(raw[:len(raw)-1])
	if err != nil {
		return treeRecord{}, err
	}
	if current.path != candidatePath {
		return treeRecord{}, errors.New("symlink target did not resolve exactly")
	}
	return current, nil
}

func requireEmptyDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("candidate output is not a real directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("candidate output directory is not empty")
	}
	return nil
}

func writeJSONFile(filePath string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
