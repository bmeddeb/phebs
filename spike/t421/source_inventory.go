package t421

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/grpcgo"
	"github.com/bmeddeb/phebs/internal/extract/extractors/kafkago"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/scipfield"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftdecl"
	"github.com/bmeddeb/phebs/internal/extract/extractors/thriftgo"
	"github.com/bmeddeb/phebs/spike/t401"
)

type sourceTreeIdentity struct {
	Inventory                 SetIdentity
	ObservationInputInventory SetIdentity
	CandidateInventories      []CandidateInventory
	TreeOID                   string
}

type sourceTreeRecord struct {
	Path    string
	Bytes   uint64
	BlobOID string
}

func frozenReaderProbe() (ReaderProbeProfile, error) {
	profile, err := frozenStructuralProfile()
	if err != nil {
		return ReaderProbeProfile{}, err
	}
	const targetPath = "structural/cells/b000/c00000/f000.go"
	readTarget := func(revision string) (t401.FrozenTreeRecord, error) {
		stop := errors.New("reader probe record observed")
		var result t401.FrozenTreeRecord
		err := t401.WalkFrozenTreeRecords(profile, revision, func(record t401.FrozenTreeRecord) error {
			if record.Path != targetPath {
				return nil
			}
			result = record
			return stop
		})
		if !errors.Is(err, stop) || result.Path == "" || result.BlobOID == "" {
			return t401.FrozenTreeRecord{}, errors.New("T42.1 reader probe source identity is absent")
		}
		return result, nil
	}
	a, err := readTarget("a")
	if err != nil {
		return ReaderProbeProfile{}, err
	}
	b, err := readTarget("b")
	if err != nil || a.Path != b.Path || a.BlobOID == b.BlobOID {
		return ReaderProbeProfile{}, errors.New("T42.1 reader probe does not distinguish A from B")
	}
	pathSHA256 := SHA256([]byte(a.Path))
	querySHA256 := recipeDigest("t422-reader-query-v1", pathSHA256, a.BlobOID, b.BlobOID)
	return ReaderProbeProfile{
		Schema: "t422-revision-reader-probe-v1", Reader: "search-generation-exact-content-probe-v1",
		QuerySHA256: querySHA256, PathSHA256: pathSHA256,
		OldProjectionSHA256: recipeDigest("t422-reader-projection-v1", querySHA256, a.BlobOID),
		NewProjectionSHA256: recipeDigest("t422-reader-projection-v1", querySHA256, b.BlobOID),
		ExpectedRecords:     1, PostDeleteOutcome: "not_found",
	}, nil
}

func expectedCombinedSourceIdentities(profile CombinedProfile) (map[string]sourceTreeIdentity, error) {
	structural, err := frozenStructuralProfile()
	if err != nil {
		return nil, err
	}
	additions := make([]sourceTreeRecord, 0, profile.Overlay.RegularFiles+profile.GeneratedMapping.RegularFiles+profile.TypedIndex.RegularFiles)
	if err := WalkCombinedAdditions(func(path string, content []byte) error {
		additions = append(additions, sourceTreeRecord{
			Path: path, Bytes: uint64(len(content)), BlobOID: gitSHA1ObjectID("blob", content),
		})
		return nil
	}); err != nil {
		return nil, err
	}
	if uint64(len(additions)) != profile.Overlay.RegularFiles+profile.GeneratedMapping.RegularFiles+profile.TypedIndex.RegularFiles ||
		!slices.IsSortedFunc(additions, func(left, right sourceTreeRecord) int {
			return strings.Compare(left.Path, right.Path)
		}) {
		return nil, errors.New("T42.1 combined additions are not the exact sorted inventory")
	}
	baseTrees := map[string]string{
		"a": "96b33ec020abad515767d23b0ab0a3c12933ae22",
		"b": "f58ccffd268a5cf4bc40dcd9d2c5a64476589aec",
	}
	result := make(map[string]sourceTreeIdentity, 3)
	for _, revision := range []string{"a", "b"} {
		identity, err := expectedCombinedSourceIdentity(structural, revision, baseTrees[revision], additions)
		if err != nil {
			return nil, err
		}
		result[revision] = identity
	}
	result["a-return"] = result["a"]
	return result, nil
}

func expectedCombinedSourceIdentity(
	structural t401.Profile,
	revision, expectedBaseTree string,
	additions []sourceTreeRecord,
) (sourceTreeIdentity, error) {
	policies, err := combinedCandidatePolicies()
	if err != nil {
		return sourceTreeIdentity{}, err
	}
	combined := newTreeInventoryAccumulator(policies)
	baseTree := newGitTreeHasher()
	addition := 0
	structuralGoOrdinal := uint64(0)
	err = t401.WalkFrozenTreeRecords(structural, revision, func(record t401.FrozenTreeRecord) error {
		base := sourceTreeRecord{Path: record.Path, Bytes: record.Bytes, BlobOID: record.BlobOID}
		if err := baseTree.add(base); err != nil {
			return err
		}
		if strings.HasSuffix(base.Path, ".go") {
			if structuralGoOrdinal >= structural.Aggregate.EligibleGoFiles-structuralNonCandidateFiles {
				base.Path = strings.TrimSuffix(base.Path, ".go") + ".txt"
			}
			structuralGoOrdinal++
		}
		for addition < len(additions) && additions[addition].Path < base.Path {
			if err := combined.add(additions[addition]); err != nil {
				return err
			}
			addition++
		}
		if addition < len(additions) && additions[addition].Path == base.Path {
			return fmt.Errorf("T42.1 addition collides with base path %q", base.Path)
		}
		return combined.add(base)
	})
	if err != nil {
		return sourceTreeIdentity{}, err
	}
	if structuralGoOrdinal != structural.Aggregate.EligibleGoFiles {
		return sourceTreeIdentity{}, errors.New("T42.1 structural Go inventory is incomplete")
	}
	for ; addition < len(additions); addition++ {
		if err := combined.add(additions[addition]); err != nil {
			return sourceTreeIdentity{}, err
		}
	}
	baseOID, err := baseTree.finish()
	if err != nil || baseOID != expectedBaseTree {
		return sourceTreeIdentity{}, errors.New("T42.1 streamed structural tree differs from its frozen Git identity")
	}
	return combined.finish()
}

type treeInventoryAccumulator struct {
	identity         *identityBuilder
	observationInput *identityBuilder
	tree             *gitTreeHasher
	candidates       []candidateInventoryAccumulator
	lastPath         string
}

type candidateInventoryAccumulator struct {
	policy   candidate.Policy
	identity *identityBuilder
}

func newTreeInventoryAccumulator(policies []candidate.Policy) *treeInventoryAccumulator {
	candidates := make([]candidateInventoryAccumulator, len(policies))
	for index, policy := range policies {
		candidates[index] = candidateInventoryAccumulator{
			policy: policy, identity: newIdentityBuilder("t421-expected-candidate-inventory-v1/" + policy.Domain),
		}
	}
	return &treeInventoryAccumulator{
		identity:         newIdentityBuilder("t421-expected-combined-tree-inventory-v1"),
		observationInput: newIdentityBuilder("t421-expected-observation-input-inventory-v1"),
		tree:             newGitTreeHasher(),
		candidates:       candidates,
	}
}

func (value *treeInventoryAccumulator) add(record sourceTreeRecord) error {
	if record.Path == "" || value.lastPath != "" && record.Path <= value.lastPath ||
		!validGitObjectID(record.BlobOID, "sha1") {
		return errors.New("T42.1 expected source-tree record is invalid or unordered")
	}
	value.lastPath = record.Path
	fields := []string{record.Path, regularFileMode, strconv.FormatUint(record.Bytes, 10), record.BlobOID}
	for _, field := range fields {
		value.identity.writeFrame([]byte(field))
	}
	value.identity.records++
	if supportedObservationPath(record.Path) {
		for _, field := range fields {
			value.observationInput.writeFrame([]byte(field))
		}
		value.observationInput.records++
	}
	for index := range value.candidates {
		current := &value.candidates[index]
		if current.policy.Enumerate(record.Path) {
			for _, field := range fields {
				current.identity.writeFrame([]byte(field))
			}
			current.identity.records++
		}
	}
	return value.tree.add(record)
}

func supportedObservationPath(path string) bool {
	return strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".proto") || strings.HasSuffix(path, ".thrift")
}

func (value *treeInventoryAccumulator) finish() (sourceTreeIdentity, error) {
	treeOID, err := value.tree.finish()
	if err != nil {
		return sourceTreeIdentity{}, err
	}
	candidates := make([]CandidateInventory, len(value.candidates))
	for index, current := range value.candidates {
		candidates[index] = CandidateInventory{Domain: current.policy.Domain, Candidates: current.identity.finish()}
	}
	return sourceTreeIdentity{
		Inventory: value.identity.finish(), ObservationInputInventory: value.observationInput.finish(),
		CandidateInventories: candidates, TreeOID: treeOID,
	}, nil
}

func combinedCandidatePolicies() ([]candidate.Policy, error) {
	return extract.CandidatePolicies([]extract.Extractor{
		gocaller.NewGRPC(), grpcgo.New(), kafkago.NewConsumer(), kafkago.NewProducer(), protodecl.New(),
		scipfield.New(), gocaller.NewThrift(), thriftgo.New(), thriftdecl.New(),
	})
}

type gitTreeFrame struct {
	name        string
	entries     bytes.Buffer
	lastSortKey string
}

type gitTreeHasher struct {
	stack    []gitTreeFrame
	lastPath string
}

func newGitTreeHasher() *gitTreeHasher {
	return &gitTreeHasher{stack: []gitTreeFrame{{}}}
}

func (value *gitTreeHasher) add(record sourceTreeRecord) error {
	if record.Path == "" || strings.HasPrefix(record.Path, "/") || strings.HasSuffix(record.Path, "/") ||
		value.lastPath != "" && record.Path <= value.lastPath {
		return errors.New("T42.1 Git tree record path is invalid or unordered")
	}
	parts := strings.Split(record.Path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, 0) {
			return errors.New("T42.1 Git tree record path component is invalid")
		}
	}
	directories := parts[:len(parts)-1]
	common := 0
	for common < len(directories) && common+1 < len(value.stack) &&
		value.stack[common+1].name == directories[common] {
		common++
	}
	for len(value.stack)-1 > common {
		if err := value.closeTop(); err != nil {
			return err
		}
	}
	for _, directory := range directories[common:] {
		value.stack = append(value.stack, gitTreeFrame{name: directory})
	}
	value.lastPath = record.Path
	return appendGitTreeEntry(&value.stack[len(value.stack)-1], regularFileMode, parts[len(parts)-1], record.BlobOID, false)
}

func (value *gitTreeHasher) finish() (string, error) {
	for len(value.stack) > 1 {
		if err := value.closeTop(); err != nil {
			return "", err
		}
	}
	if value.lastPath == "" {
		return "", errors.New("T42.1 Git tree inventory is empty")
	}
	return gitSHA1ObjectID("tree", value.stack[0].entries.Bytes()), nil
}

func (value *gitTreeHasher) closeTop() error {
	index := len(value.stack) - 1
	child := value.stack[index]
	value.stack = value.stack[:index]
	oid := gitSHA1ObjectID("tree", child.entries.Bytes())
	return appendGitTreeEntry(&value.stack[index-1], "40000", child.name, oid, true)
}

func appendGitTreeEntry(frame *gitTreeFrame, mode, name, oid string, directory bool) error {
	sortKey := name
	if directory {
		sortKey += "/"
	}
	if frame.lastSortKey != "" && sortKey <= frame.lastSortKey {
		return errors.New("T42.1 Git tree entries are not in canonical order")
	}
	rawOID, err := hex.DecodeString(oid)
	if err != nil || len(rawOID) != 20 {
		return errors.New("T42.1 Git tree entry object identity is invalid")
	}
	frame.lastSortKey = sortKey
	frame.entries.WriteString(mode)
	frame.entries.WriteByte(' ')
	frame.entries.WriteString(name)
	frame.entries.WriteByte(0)
	_, _ = frame.entries.Write(rawOID)
	return nil
}

func canonicalGitCommitBytesFor(tree, parent, message string, recipe SourceRecipe) ([]byte, error) {
	if recipe.GitObjectFormat != "sha1" || !validGitObjectID(tree, "sha1") ||
		parent != "" && !validGitObjectID(parent, "sha1") || message == "" {
		return nil, errors.New("T42.1 canonical Git commit inputs are invalid")
	}
	var result bytes.Buffer
	fmt.Fprintf(&result, "tree %s\n", tree)
	if parent != "" {
		fmt.Fprintf(&result, "parent %s\n", parent)
	}
	author := fmt.Sprintf("%s <%s> %d %s", recipe.AuthorName, recipe.AuthorEmail, recipe.Timestamp, recipe.Timezone)
	committer := fmt.Sprintf("%s <%s> %d %s", recipe.CommitterName, recipe.CommitterEmail, recipe.Timestamp, recipe.Timezone)
	fmt.Fprintf(&result, "author %s\ncommitter %s\n\n%s\n", author, committer, message)
	return result.Bytes(), nil
}
