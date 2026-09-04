// Package t421sourceprojection derives the exact source-set and Git-tree
// identities shared by T42.1 plan authoring and final exact inspection.
package t421sourceprojection

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // Git SHA-1 object identity is a compatibility format, not a security boundary.
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
)

const (
	treeInventoryDomain      = "t421-expected-combined-tree-inventory-v1"
	observationInputDomain   = "t421-expected-observation-input-inventory-v1"
	candidateInventoryPrefix = "t421-expected-candidate-inventory-v1/"
	candidateProofPrefix     = "t421-candidate-content-proof-v1/"
)

// ErrInvalid identifies malformed, unordered, mixed-object-format, or
// otherwise incomplete projection input.
var ErrInvalid = errors.New("invalid T42.1 source projection")

// SetIdentity is the scalar identity of one domain-framed source set.
type SetIdentity struct {
	Records     uint64 `json:"records"`
	FramedBytes uint64 `json:"framed_bytes"`
	SHA256      string `json:"sha256"`
}

// CandidateInventory is one policy-domain candidate set.
type CandidateInventory struct {
	Domain     string      `json:"domain"`
	Candidates SetIdentity `json:"candidates"`
	// Proof is an order-independent private cross-check against the immutable
	// candidate records. It is not part of the source-free receipt shape.
	Proof SetIdentity `json:"-"`
}

// Projection contains the exact source-free identities and Git tree object
// identity for one ordered source inventory.
type Projection struct {
	TreeInventory             SetIdentity          `json:"tree_inventory"`
	ObservationInputInventory SetIdentity          `json:"observation_input_inventory"`
	CandidateInventories      []CandidateInventory `json:"candidate_inventories"`
	TreeOID                   string               `json:"tree_oid"`
}

// Accumulator consumes one already-validated source inventory without
// retaining its records. It is single-use and not safe for concurrent calls.
type Accumulator struct {
	ctx              context.Context
	identity         *identityBuilder
	observationInput *identityBuilder
	candidates       []candidateAccumulator
	selected         []bool
	tree             *treeHasher
	lastPath         string
	goOnly           bool
	finished         bool
	err              error
}

type candidateAccumulator struct {
	domain    string
	enumerate func(string) bool
	required  func(string) bool
	identity  *identityBuilder
	proof     *CandidateProofAccumulator
}

// New starts a streaming T42.1 source projection. Candidate output is ordered
// by the canonical policy identities, independent of caller slice order.
func New(
	ctx context.Context,
	policies []candidate.Policy,
	goOnly bool,
) (*Accumulator, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		return nil, fmt.Errorf("%w: candidate policies: %w", ErrInvalid, err)
	}
	type policyKey struct {
		domain  string
		version string
	}
	byKey := make(map[policyKey]candidate.Policy, len(policies))
	for _, policy := range policies {
		byKey[policyKey{domain: policy.Domain, version: policy.Version}] = policy
	}
	candidates := make([]candidateAccumulator, 0, len(identities))
	for _, identity := range identities {
		policy, ok := byKey[policyKey{domain: identity.Domain, version: identity.Version}]
		if !ok || policy.Enumerate == nil {
			return nil, fmt.Errorf("%w: candidate policy binding", ErrInvalid)
		}
		candidates = append(candidates, candidateAccumulator{
			domain: identity.Domain, enumerate: policy.Enumerate, required: policy.Required,
			identity: newIdentityBuilder(candidateInventoryPrefix + identity.Domain),
			proof:    NewCandidateProof(identity.Domain),
		})
	}
	return &Accumulator{
		ctx: ctx, identity: newIdentityBuilder(treeInventoryDomain),
		observationInput: newIdentityBuilder(observationInputDomain),
		candidates:       candidates, selected: make([]bool, len(candidates)),
		tree: newTreeHasher(), goOnly: goOnly,
	}, nil
}

// Add consumes the next strict ASCII-path-ordered source record.
func (value *Accumulator) Add(record repositoryindex.SourceRecord) error {
	if value == nil {
		return fmt.Errorf("%w: accumulator is nil", ErrInvalid)
	}
	if value.finished {
		return value.fail(fmt.Errorf("%w: accumulator is finished", ErrInvalid))
	}
	if value.err != nil {
		return value.err
	}
	if err := value.ctx.Err(); err != nil {
		return value.fail(err)
	}
	if err := validateRecord(record); err != nil {
		return value.fail(err)
	}
	if value.lastPath != "" &&
		(record.Path <= value.lastPath || strings.HasPrefix(record.Path, value.lastPath+"/")) {
		return value.fail(fmt.Errorf("%w: records are unordered or path-conflicting", ErrInvalid))
	}
	if value.identity.records >= repositoryindex.MaxOwners {
		return value.fail(fmt.Errorf("%w: source owner bound exceeded", ErrInvalid))
	}

	for index := range value.candidates {
		if err := value.ctx.Err(); err != nil {
			return value.fail(err)
		}
		match, err := callPredicate(value.candidates[index].enumerate, record.Path)
		if err != nil {
			return value.fail(err)
		}
		required := false
		if value.candidates[index].required != nil {
			required, err = callPredicate(value.candidates[index].required, record.Path)
			if err != nil {
				return value.fail(err)
			}
		}
		if required && !match {
			return value.fail(fmt.Errorf("%w: required candidate is not enumerated", ErrInvalid))
		}
		value.selected[index] = match && record.Kind == "regular"
		if value.selected[index] {
			if err := value.candidates[index].proof.Add(
				record.Path, record.ObjectID, record.DeclaredBytes, required,
			); err != nil {
				return value.fail(err)
			}
		}
	}
	if err := value.tree.add(record.Path, record.Mode, record.ObjectID); err != nil {
		return value.fail(err)
	}

	fields := [...]string{
		record.Path,
		record.Mode,
		strconv.FormatInt(record.DeclaredBytes, 10),
		record.ObjectID,
	}
	value.identity.addRecord(fields)
	if supportedObservationPath(record.Path) &&
		(!value.goOnly || strings.HasSuffix(record.Path, ".go")) {
		value.observationInput.addRecord(fields)
	}
	for index, match := range value.selected {
		if match {
			value.candidates[index].identity.addRecord(fields)
		}
	}
	value.lastPath = record.Path
	return nil
}

// Finish closes the tree and returns the exact identities. An accumulator can
// be finished only once, including after a failed Add.
func (value *Accumulator) Finish() (Projection, error) {
	if value == nil {
		return Projection{}, fmt.Errorf("%w: accumulator is nil", ErrInvalid)
	}
	if value.finished {
		return Projection{}, fmt.Errorf("%w: accumulator is finished", ErrInvalid)
	}
	value.finished = true
	if value.err != nil {
		return Projection{}, value.err
	}
	if err := value.ctx.Err(); err != nil {
		value.err = err
		return Projection{}, err
	}
	treeOID, err := value.tree.finish(value.ctx)
	if err != nil {
		value.err = err
		return Projection{}, err
	}
	candidates := make([]CandidateInventory, len(value.candidates))
	for index, current := range value.candidates {
		proof, err := current.proof.Finish()
		if err != nil {
			value.err = err
			return Projection{}, err
		}
		candidates[index] = CandidateInventory{
			Domain: current.domain, Candidates: current.identity.finish(), Proof: proof,
		}
	}
	return Projection{
		TreeInventory:             value.identity.finish(),
		ObservationInputInventory: value.observationInput.finish(),
		CandidateInventories:      candidates,
		TreeOID:                   treeOID,
	}, nil
}

func (value *Accumulator) fail(err error) error {
	if value.err == nil {
		value.err = err
	}
	return value.err
}

func validateRecord(record repositoryindex.SourceRecord) error {
	if record.Schema != repositoryindex.SourceMemberSchema ||
		repopath.Validate(record.Path) != nil ||
		!gitobj.IsObjectID(record.ObjectID) || record.DeclaredBytes < 0 ||
		len(record.Revisions) == 0 {
		return fmt.Errorf("%w: source record identity", ErrInvalid)
	}
	switch record.Kind {
	case "regular":
		if record.Mode != "100644" && record.Mode != "100755" {
			return fmt.Errorf("%w: regular source mode", ErrInvalid)
		}
	case "symlink":
		if record.Mode != "120000" {
			return fmt.Errorf("%w: symlink source mode", ErrInvalid)
		}
	case "gitlink":
		if record.Mode != "160000" || record.DeclaredBytes != 0 {
			return fmt.Errorf("%w: gitlink source identity", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: source kind", ErrInvalid)
	}
	for index, revision := range record.Revisions {
		if revision < 0 || index > 0 && record.Revisions[index-1] >= revision {
			return fmt.Errorf("%w: source revisions", ErrInvalid)
		}
	}
	return nil
}

func supportedObservationPath(path string) bool {
	return strings.HasSuffix(path, ".go") ||
		strings.HasSuffix(path, ".proto") ||
		strings.HasSuffix(path, ".thrift")
}

func callPredicate(predicate func(string) bool, path string) (result bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = false
			err = fmt.Errorf("%w: candidate enumeration predicate panicked", ErrInvalid)
		}
	}()
	return predicate(path), nil
}

// CandidateProofAccumulator creates a constant-memory, order-independent
// commitment to the fields an extraction domain actually consumes. Source
// and candidate records arrive in different canonical orders for caller
// domains, so an ordered stream digest cannot compare the two planes.
type CandidateProofAccumulator struct {
	domain   string
	xor      [sha256.Size]byte
	bytes    uint64
	records  uint64
	finished bool
	err      error
}

func NewCandidateProof(domain string) *CandidateProofAccumulator {
	return &CandidateProofAccumulator{domain: domain}
}

func (value *CandidateProofAccumulator) Add(
	path, objectID string,
	declaredBytes int64,
	required bool,
) error {
	if value == nil || value.finished || value.domain == "" ||
		repopath.Validate(path) != nil || !gitobj.IsObjectID(objectID) || declaredBytes < 0 {
		if value != nil && value.err == nil {
			value.err = fmt.Errorf("%w: candidate proof record", ErrInvalid)
		}
		if value != nil {
			return value.err
		}
		return fmt.Errorf("%w: candidate proof accumulator", ErrInvalid)
	}
	if value.err != nil {
		return value.err
	}
	builder := newIdentityBuilder(candidateProofPrefix + value.domain)
	builder.addRecord([4]string{
		path, objectID, strconv.FormatInt(declaredBytes, 10), strconv.FormatBool(required),
	})
	if value.records == ^uint64(0) || builder.bytes > ^uint64(0)-value.bytes {
		value.err = fmt.Errorf("%w: candidate proof overflow", ErrInvalid)
		return value.err
	}
	digest := builder.hash.Sum(nil)
	for index := range value.xor {
		value.xor[index] ^= digest[index]
	}
	value.bytes += builder.bytes
	value.records++
	return nil
}

func (value *CandidateProofAccumulator) Finish() (SetIdentity, error) {
	if value == nil || value.finished || value.domain == "" {
		return SetIdentity{}, fmt.Errorf("%w: candidate proof accumulator", ErrInvalid)
	}
	value.finished = true
	if value.err != nil {
		return SetIdentity{}, value.err
	}
	builder := newIdentityBuilder(candidateProofPrefix + value.domain + "/set")
	builder.addRecord([4]string{
		strconv.FormatUint(value.records, 10),
		strconv.FormatUint(value.bytes, 10),
		hex.EncodeToString(value.xor[:]),
		"xor-sha256-v1",
	})
	result := builder.finish()
	if value.bytes > ^uint64(0)-result.FramedBytes {
		return SetIdentity{}, fmt.Errorf("%w: candidate proof overflow", ErrInvalid)
	}
	result.Records = value.records
	result.FramedBytes += value.bytes
	return result, nil
}

type identityBuilder struct {
	hash    hash.Hash
	bytes   uint64
	records uint64
}

func newIdentityBuilder(domain string) *identityBuilder {
	builder := &identityBuilder{hash: sha256.New()}
	builder.writeFrame(domain)
	return builder
}

func (builder *identityBuilder) addRecord(fields [4]string) {
	for _, field := range fields {
		builder.writeFrame(field)
	}
	builder.records++
}

func (builder *identityBuilder) writeFrame(raw string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
	_, _ = builder.hash.Write(length[:])
	_, _ = builder.hash.Write([]byte(raw))
	builder.bytes += uint64(len(length) + len(raw))
}

func (builder *identityBuilder) finish() SetIdentity {
	return SetIdentity{
		Records: builder.records, FramedBytes: builder.bytes,
		SHA256: "sha256:" + hex.EncodeToString(builder.hash.Sum(nil)),
	}
}

type treeFrame struct {
	name        string
	entries     bytes.Buffer
	lastSortKey string
}

type treeHasher struct {
	stack    []treeFrame
	oidBytes int
}

func newTreeHasher() *treeHasher {
	return &treeHasher{stack: []treeFrame{{}}}
}

func (value *treeHasher) add(path, mode, oid string) error {
	rawOID, err := hex.DecodeString(oid)
	if err != nil || len(rawOID) != sha1.Size && len(rawOID) != sha256.Size {
		return fmt.Errorf("%w: Git object identity", ErrInvalid)
	}
	if value.oidBytes == 0 {
		value.oidBytes = len(rawOID)
	} else if len(rawOID) != value.oidBytes {
		return fmt.Errorf("%w: mixed Git object formats", ErrInvalid)
	}
	parts := strings.Split(path, "/")
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
		value.stack = append(value.stack, treeFrame{name: directory})
	}
	return value.appendEntry(
		&value.stack[len(value.stack)-1], mode, parts[len(parts)-1], rawOID, false,
	)
}

func (value *treeHasher) finish(ctx context.Context) (string, error) {
	if value.oidBytes == 0 {
		return "", fmt.Errorf("%w: source inventory is empty", ErrInvalid)
	}
	for len(value.stack) > 1 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := value.closeTop(); err != nil {
			return "", err
		}
	}
	return value.objectID("tree", value.stack[0].entries.Bytes())
}

func (value *treeHasher) closeTop() error {
	index := len(value.stack) - 1
	child := value.stack[index]
	value.stack = value.stack[:index]
	oid, err := value.objectID("tree", child.entries.Bytes())
	if err != nil {
		return err
	}
	rawOID, err := hex.DecodeString(oid)
	if err != nil {
		return fmt.Errorf("%w: derived Git tree identity", ErrInvalid)
	}
	return value.appendEntry(
		&value.stack[index-1], "40000", child.name, rawOID, true,
	)
}

func (value *treeHasher) appendEntry(
	frame *treeFrame,
	mode, name string,
	rawOID []byte,
	directory bool,
) error {
	sortKey := name
	if directory {
		sortKey += "/"
	}
	if frame.lastSortKey != "" && sortKey <= frame.lastSortKey {
		return fmt.Errorf("%w: Git tree entries are unordered", ErrInvalid)
	}
	if len(rawOID) != value.oidBytes {
		return fmt.Errorf("%w: Git tree object format", ErrInvalid)
	}
	frame.lastSortKey = sortKey
	frame.entries.WriteString(mode)
	frame.entries.WriteByte(' ')
	frame.entries.WriteString(name)
	frame.entries.WriteByte(0)
	_, _ = frame.entries.Write(rawOID)
	return nil
}

func (value *treeHasher) objectID(kind string, content []byte) (string, error) {
	var digest hash.Hash
	switch value.oidBytes {
	case sha1.Size:
		digest = sha1.New() //nolint:gosec // Git SHA-1 object identity is compatibility hashing.
	case sha256.Size:
		digest = sha256.New()
	default:
		return "", fmt.Errorf("%w: Git object format", ErrInvalid)
	}
	_, _ = fmt.Fprintf(digest, "%s %d%c", kind, len(content), byte(0))
	_, _ = digest.Write(content)
	return hex.EncodeToString(digest.Sum(nil)), nil
}
