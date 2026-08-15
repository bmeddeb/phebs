package callerleaf

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/reponame"
)

const stageArtifactName = "artifact.ndjson"

var errCallerLeafDirectoryTransition = errors.New(
	"caller leaf directory authority transitioned",
)

type Stage struct {
	root       string
	repository string
	directory  string
	file       *os.File
	hash       hashWriter
	generation GenerationIdentity
	pair       PairIdentity
	receipt    Receipt
	sealed     bool
	authority  *directoryAuthority
	stageName  string
	stageInfo  os.FileInfo
	closeRoot  bool
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

type Prepared struct {
	root       string
	repository string
	directory  string
	temporary  string
	final      string
	generation GenerationIdentity
	pair       PairIdentity
	receipt    Receipt
	installed  bool
	authority  *directoryAuthority
	stageName  string
	stageInfo  os.FileInfo
	finalName  string
	closeRoot  bool
}

type Publication struct {
	root          string
	path          string
	receipt       Receipt
	info          os.FileInfo
	directoryInfo os.FileInfo
}

// RecordReference is one bounded position produced by ScanRecords. Digest
// binds the exact canonical NDJSON line at that position, allowing a paged
// reader to reread only selected records without rehashing the complete leaf.
// References are meaningful only with the Publication that produced them.
type RecordReference struct {
	Offset int64
	Length int
	Digest string
}

type directoryAuthority struct {
	root *os.Root
	info os.FileInfo
	once sync.Once
}

func (authority *directoryAuthority) close() {
	if authority == nil {
		return
	}
	authority.once.Do(func() { _ = authority.root.Close() })
}

// ArtifactInventory is one bounded, package-owned repository-directory pass.
// A worker reuses it across every pair installed in the same bounded turn so
// same-pair divergence checks remain O(P), not one O(P) scan per pair.
type ArtifactInventory struct {
	root       string
	repository string
	directory  string
	byPair     map[string][]string
	entryCount int
	authority  *directoryAuthority
}

// Close releases the pinned repository directory descriptor.
func (inventory *ArtifactInventory) Close() error {
	if inventory == nil || inventory.authority == nil {
		return nil
	}
	inventory.authority.close()
	inventory.authority = nil
	return nil
}

// HasCapacity reports whether one more pair can stage and install a new
// immutable artifact without crossing the digest-bound repository entry cap,
// including the rename-before-stage-removal crash slot.
func (inventory *ArtifactInventory) HasCapacity() bool {
	return inventory != nil &&
		inventory.entryCount < MaxRepositoryDirectoryEntries-1
}

// CanPrepare allows a near-cap exact orphan/divergent sibling to be examined
// when one stage slot remains, but never permits a new immutable artifact into
// the reserved rename crash slot.
func (inventory *ArtifactInventory) CanPrepare(pairDigest string) bool {
	if inventory == nil || inventory.entryCount >= MaxRepositoryDirectoryEntries {
		return false
	}
	return inventory.HasCapacity() || len(inventory.byPair[pairDigest]) > 0
}

func NewStage(
	root string,
	generation GenerationIdentity,
	pair PairIdentity,
) (*Stage, error) {
	if err := ValidatePairIdentity(generation, pair); err != nil {
		return nil, err
	}
	directory, authority, err := openRepositoryAuthority(
		root, generation.Repository, true,
	)
	if err != nil {
		return nil, err
	}
	stage, err := newStageAt(
		root, directory, generation, pair, authority, true,
	)
	if err != nil {
		authority.close()
	}
	return stage, err
}

// NewStageWithInventory creates a stage beneath the descriptor-pinned
// inventory used for the complete repository turn.
func NewStageWithInventory(
	root string,
	generation GenerationIdentity,
	pair PairIdentity,
	inventory *ArtifactInventory,
) (*Stage, error) {
	if err := ValidatePairIdentity(generation, pair); err != nil {
		return nil, err
	}
	if inventory == nil || inventory.authority == nil || inventory.root != root ||
		inventory.repository != generation.Repository {
		return nil, errors.New("caller leaf inventory does not own the stage")
	}
	return newStageAt(
		root, inventory.directory, generation, pair, inventory.authority, false,
	)
}

func newStageAt(
	root, directory string,
	generation GenerationIdentity,
	pair PairIdentity,
	authority *directoryAuthority,
	closeRoot bool,
) (*Stage, error) {
	stageName, stageInfo, err := createStageDirectory(authority)
	if err != nil {
		return nil, err
	}
	file, err := authority.root.OpenFile(
		stageName+"/"+stageArtifactName,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
	)
	if err != nil {
		_ = removeOwnedStageAt(authority, stageName, stageInfo)
		return nil, err
	}
	if err := syncRootDirectory(authority.root, "."); err != nil {
		_ = file.Close()
		_ = removeOwnedStageAt(authority, stageName, stageInfo)
		return nil, err
	}
	return &Stage{
		root: root, repository: generation.Repository,
		directory: filepath.Join(directory, stageName), file: file, hash: sha256.New(),
		generation: generation, pair: pair, authority: authority,
		stageName: stageName, stageInfo: stageInfo, closeRoot: closeRoot,
	}, nil
}

func (stage *Stage) Add(record Record) error {
	if stage == nil || stage.file == nil || stage.hash == nil || stage.sealed {
		return errors.New("caller leaf stage is not writable")
	}
	if err := ValidateRecord(record); err != nil {
		return err
	}
	switch record.Kind {
	case RecordResult:
		if stage.receipt.CoverageRecordCount != 0 {
			return errors.New("caller leaf coverage cannot mix with result records")
		}
		if stage.receipt.ResultCount >= MaxResultRecordsPerPair {
			return callerLimit(
				pipelinerefusal.DimensionCallerPairResults,
				int64(stage.receipt.ResultCount)+1, MaxResultRecordsPerPair,
			)
		}
		stage.receipt.ResultCount++
	case RecordAbstention:
		if stage.receipt.CoverageRecordCount != 0 {
			return errors.New("caller leaf coverage cannot mix with abstention records")
		}
		if stage.receipt.AbstentionCount >= MaxAbstentionRecordsPerPair {
			return callerLimit(
				pipelinerefusal.DimensionCallerPairAbstentions,
				int64(stage.receipt.AbstentionCount)+1, MaxAbstentionRecordsPerPair,
			)
		}
		stage.receipt.AbstentionCount++
	case RecordCoverage:
		if record.Coverage == nil || validateCoverageForPair(*record.Coverage, stage.pair) != nil ||
			stage.receipt.RecordCount != 0 || stage.receipt.ResultCount != 0 ||
			stage.receipt.AbstentionCount != 0 || stage.receipt.CoverageRecordCount != 0 ||
			stage.receipt.SourceBlobReads != 0 || stage.receipt.SourceBlobBytes != 0 ||
			stage.receipt.ExcludedGoTestRecords != 0 {
			return errors.New("caller leaf compact coverage is invalid or duplicated")
		}
		stage.receipt.CoverageRecordCount = 1
		stage.receipt.CoveredCandidateCount = record.Coverage.CoveredCandidateCount
		stage.receipt.ExcludedGoTestRecords = coverageGapCount(
			*record.Coverage, CoverageGapExcludedGoTest,
		)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(raw) > MaxRecordBytes {
		return callerLimit(
			pipelinerefusal.DimensionCallerRecordBytes,
			int64(len(raw)), MaxRecordBytes,
		)
	}
	raw = append(raw, '\n')
	if int64(len(raw)) > MaxCanonicalBytesPerPair-stage.receipt.ContentBytes {
		return callerByteLimit(
			pipelinerefusal.DimensionCallerPairCanonicalBytes,
			stage.receipt.ContentBytes+int64(len(raw)), MaxCanonicalBytesPerPair,
		)
	}
	if _, err := stage.file.Write(raw); err != nil {
		return err
	}
	_, _ = stage.hash.Write(raw)
	stage.receipt.RecordCount++
	stage.receipt.ContentBytes += int64(len(raw))
	return nil
}

// AddNoResolverCoverage emits one exact certificate over the complete pair
// member. It is available only to the V2 adapter and deliberately accepts
// scalar counts rather than paths or source bytes: the candidate reader has
// already descriptor-validated every applicable record, while the pair binds
// the immutable member and the certificate names every explicit gap.
func (stage *Stage) AddNoResolverCoverage(
	noDirect int,
	gaps []CoverageGap,
) error {
	if stage == nil {
		return errors.New("caller leaf stage is unavailable")
	}
	return stage.Add(Record{
		Schema: CoverageRecordSchema, Kind: RecordCoverage,
		Coverage: &Coverage{
			Schema: CoverageSchema, PairDigest: stage.pair.Digest,
			CandidateMemberName:    stage.pair.Leaf.Name,
			CandidateContentDigest: stage.pair.Leaf.ContentDigest,
			CandidateRecordCount:   stage.pair.Leaf.RecordCount,
			CoveredCandidateCount:  stage.pair.Leaf.RecordCount,
			NoDirectCandidateCount: noDirect,
			Gaps:                   slices.Clone(gaps),
			Reason:                 CoverageReasonNoResolverDescriptors,
		},
	})
}

func (stage *Stage) ObserveExcludedGoTest() {
	if stage != nil && !stage.sealed {
		stage.receipt.ExcludedGoTestRecords++
	}
}

func (stage *Stage) ObserveSourceBlob(bytes int64) error {
	if stage == nil || stage.sealed || bytes < 0 {
		return errors.New("caller leaf source observation is invalid")
	}
	if bytes > MaxSourceBlobBytesPerPair-stage.receipt.SourceBlobBytes {
		return callerByteLimit(
			pipelinerefusal.DimensionCallerPairSourceBytes,
			stage.receipt.SourceBlobBytes+bytes, MaxSourceBlobBytesPerPair,
		)
	}
	stage.receipt.SourceBlobReads++
	stage.receipt.SourceBlobBytes += bytes
	return nil
}

func (stage *Stage) Seal() (*Prepared, error) {
	if stage == nil || stage.file == nil || stage.hash == nil || stage.sealed {
		return nil, errors.New("caller leaf stage cannot be sealed")
	}
	stage.sealed = true
	if stage.receipt.ContentBytes > MaxStagingBytesPerPair {
		_ = stage.file.Close()
		return nil, callerByteLimit(
			pipelinerefusal.DimensionCallerPairStagingBytes,
			stage.receipt.ContentBytes, MaxStagingBytesPerPair,
		)
	}
	if err := stage.file.Sync(); err != nil {
		_ = stage.file.Close()
		return nil, err
	}
	if err := stage.file.Close(); err != nil {
		return nil, err
	}
	stage.file = nil
	stage.receipt.ContentDigest = "sha256:" + hex.EncodeToString(stage.hash.Sum(nil))
	_, metadataDigest, err := MetadataFor(stage.generation, stage.pair)
	if err != nil {
		return nil, err
	}
	stage.receipt.MetadataDigest = metadataDigest
	stage.receipt.Name = callerleafid.ArtifactName(
		stage.pair.Digest, stage.receipt.ContentDigest,
	)
	stage.receipt.StagingBytes = stage.receipt.ContentBytes
	if err := ValidateReceipt(stage.generation, stage.pair, stage.receipt); err != nil {
		return nil, err
	}
	if err := validateStageAuthority(stage.authority, stage.stageName, stage.stageInfo); err != nil {
		return nil, err
	}
	if err := syncRootDirectory(stage.authority.root, stage.stageName); err != nil {
		return nil, err
	}
	repositoryRoot := filepath.Dir(stage.directory)
	return &Prepared{
		root: stage.root, repository: stage.repository,
		directory:  stage.directory,
		temporary:  filepath.Join(stage.directory, stageArtifactName),
		final:      filepath.Join(repositoryRoot, stage.receipt.Name),
		generation: stage.generation, pair: stage.pair, receipt: stage.receipt,
		authority: stage.authority, stageName: stage.stageName,
		stageInfo: stage.stageInfo, finalName: stage.receipt.Name,
		closeRoot: stage.closeRoot,
	}, nil
}

func (stage *Stage) Discard() error {
	if stage == nil {
		return nil
	}
	if stage.file != nil {
		_ = stage.file.Close()
		stage.file = nil
	}
	if stage.authority == nil {
		return nil
	}
	err := removeOwnedStageAt(stage.authority, stage.stageName, stage.stageInfo)
	if stage.closeRoot {
		stage.authority.close()
	}
	return err
}

func (prepared *Prepared) Receipt() Receipt {
	if prepared == nil {
		return Receipt{}
	}
	return prepared.receipt
}

func (prepared *Prepared) Install(ctx context.Context) (*Publication, error) {
	if prepared == nil {
		return nil, errors.New("caller leaf prepared artifact is not installable")
	}
	inventory, err := OpenArtifactInventory(ctx, prepared.root, prepared.repository)
	if err != nil {
		return nil, err
	}
	defer func() { _ = inventory.Close() }()
	return prepared.InstallWithInventory(ctx, inventory)
}

// InstallWithInventory installs against one descriptor-validated inventory
// captured for this repository turn. The caller must not share it across
// repositories or concurrent repository workers.
func (prepared *Prepared) InstallWithInventory(
	ctx context.Context,
	inventory *ArtifactInventory,
) (*Publication, error) {
	if prepared == nil || prepared.installed {
		return nil, errors.New("caller leaf prepared artifact is not installable")
	}
	if inventory == nil || inventory.root != prepared.root ||
		inventory.repository != prepared.repository ||
		inventory.directory != filepath.Dir(prepared.final) ||
		inventory.authority == nil || prepared.authority == nil ||
		!os.SameFile(inventory.authority.info, prepared.authority.info) {
		return nil, errors.New("caller leaf artifact inventory does not own this pair")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, name := range inventory.byPair[prepared.pair.Digest] {
		if name != filepath.Base(prepared.final) {
			return nil, fmt.Errorf(
				"%w: pair already has a different content artifact %q",
				ErrNondeterministic, name,
			)
		}
	}
	if _, err := inventory.authority.root.Lstat(prepared.finalName); err == nil {
		publication, openErr := openArtifactAt(
			ctx, inventory.directory, inventory.authority,
			prepared.generation, prepared.pair, prepared.receipt, nil,
		)
		if openErr != nil {
			return nil, fmt.Errorf("%w: existing pair artifact: %v", ErrNondeterministic, openErr)
		}
		if err := prepared.Discard(); err != nil {
			return nil, err
		}
		prepared.installed = true
		if prepared.closeRoot {
			prepared.authority.close()
		}
		return publication, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !inventory.HasCapacity() {
		return nil, ErrCapacity
	}
	if err := validateStageAuthority(
		inventory.authority, prepared.stageName, prepared.stageInfo,
	); err != nil {
		return nil, err
	}
	if err := inventory.authority.root.Rename(
		prepared.stageName+"/"+stageArtifactName, prepared.finalName,
	); err != nil {
		return nil, err
	}
	if err := syncRootDirectory(inventory.authority.root, "."); err != nil {
		return nil, err
	}
	if err := removeEmptyStageAt(
		inventory.authority, prepared.stageName, prepared.stageInfo,
	); err != nil {
		return nil, err
	}
	prepared.installed = true
	if prepared.closeRoot {
		defer prepared.authority.close()
	}
	publication, err := openArtifactAt(
		ctx, inventory.directory, inventory.authority,
		prepared.generation, prepared.pair, prepared.receipt, nil,
	)
	if err != nil {
		return nil, err
	}
	inventory.byPair[prepared.pair.Digest] = append(
		inventory.byPair[prepared.pair.Digest], filepath.Base(prepared.final),
	)
	inventory.entryCount++
	return publication, nil
}

func (prepared *Prepared) Discard() error {
	if prepared == nil || prepared.installed {
		return nil
	}
	if prepared.authority == nil {
		return nil
	}
	err := removeOwnedStageAt(
		prepared.authority, prepared.stageName, prepared.stageInfo,
	)
	if prepared.closeRoot {
		prepared.authority.close()
	}
	return err
}

// Open performs one descriptor-stable, canonical content validation. The
// visitor must not publish side effects until Open returns successfully.
func Open(
	ctx context.Context,
	root string,
	generation GenerationIdentity,
	pair PairIdentity,
	receipt Receipt,
	visit func(Record) error,
) (_ *Publication, resultErr error) {
	directory, authority, err := openRepositoryAuthority(
		root, generation.Repository, false,
	)
	if err != nil {
		return nil, err
	}
	defer authority.close()
	return openArtifactAt(
		ctx, directory, authority, generation, pair, receipt, visit,
	)
}

// VerifyReader performs the canonical content, receipt, and aggregate checks
// used by Open against one already bounded artifact stream. The visitor must
// not publish side effects until VerifyReader returns successfully.
func VerifyReader(
	ctx context.Context,
	reader io.Reader,
	generation GenerationIdentity,
	pair PairIdentity,
	receipt Receipt,
	visit func(Record) error,
) error {
	if ctx == nil {
		return errors.New("caller leaf verification context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateReceipt(generation, pair, receipt); err != nil {
		return err
	}
	return verifyReader(ctx, reader, pair, receipt, visit)
}

func verifyReader(
	ctx context.Context,
	reader io.Reader,
	pair PairIdentity,
	receipt Receipt,
	visit func(Record) error,
) error {
	if visit == nil {
		return verifyReaderAt(ctx, reader, pair, receipt, nil)
	}
	return verifyReaderAt(
		ctx, reader, pair, receipt,
		func(_ RecordReference, record Record) error { return visit(record) },
	)
}

func verifyReaderAt(
	ctx context.Context,
	reader io.Reader,
	pair PairIdentity,
	receipt Receipt,
	visit func(RecordReference, Record) error,
) error {
	if ctx == nil {
		return errors.New("caller leaf verification context is required")
	}
	if reader == nil {
		return fmt.Errorf("%w: artifact reader is unavailable", ErrInvalidArtifact)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	hash := sha256.New()
	buffered := bufio.NewReaderSize(io.TeeReader(reader, hash), 64<<10)
	counts := Receipt{}
	var consumed int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, readErr := readLine(buffered, MaxRecordBytes+1)
		consumed += int64(len(line))
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if len(line) > 0 {
			var record Record
			if err := decodeCanonicalLine(line, &record); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidArtifact, err)
			}
			if err := ValidateRecord(record); err != nil {
				return err
			}
			counts.RecordCount++
			switch record.Kind {
			case RecordResult:
				counts.ResultCount++
			case RecordAbstention:
				counts.AbstentionCount++
			case RecordCoverage:
				if record.Coverage == nil ||
					validateCoverageForPair(*record.Coverage, pair) != nil {
					return fmt.Errorf("%w: coverage record differs from its pair", ErrInvalidArtifact)
				}
				counts.CoverageRecordCount++
				counts.CoveredCandidateCount += record.Coverage.CoveredCandidateCount
				counts.ExcludedGoTestRecords += coverageGapCount(
					*record.Coverage, CoverageGapExcludedGoTest,
				)
			}
			if visit != nil {
				reference := RecordReference{
					Offset: consumed - int64(len(line)), Length: len(line),
					Digest: recordReferenceDigest(line),
				}
				if err := visit(reference, record); err != nil {
					return err
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if consumed != receipt.ContentBytes || digest != receipt.ContentDigest ||
		counts.RecordCount != receipt.RecordCount ||
		counts.ResultCount != receipt.ResultCount ||
		counts.AbstentionCount != receipt.AbstentionCount ||
		counts.CoverageRecordCount != receipt.CoverageRecordCount ||
		counts.CoveredCandidateCount != receipt.CoveredCandidateCount ||
		receipt.CoverageRecordCount != 0 &&
			counts.ExcludedGoTestRecords != receipt.ExcludedGoTestRecords {
		return fmt.Errorf("%w: artifact receipt mismatch", ErrInvalidArtifact)
	}
	return nil
}

func recordReferenceDigest(line []byte) string {
	digest := sha256.Sum256(line)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func openArtifactAt(
	ctx context.Context,
	repositoryDirectory string,
	authority *directoryAuthority,
	generation GenerationIdentity,
	pair PairIdentity,
	receipt Receipt,
	visit func(Record) error,
) (_ *Publication, resultErr error) {
	if err := ValidateReceipt(generation, pair, receipt); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if authority == nil || authority.root == nil {
		return nil, errors.New("caller leaf repository authority is unavailable")
	}
	artifactPath := filepath.Join(repositoryDirectory, receipt.Name)
	before, err := authority.root.Lstat(receipt.Name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() != receipt.ContentBytes {
		return nil, fmt.Errorf("%w: artifact is special or has wrong size", ErrInvalidArtifact)
	}
	file, err := authority.root.Open(receipt.Name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !sameFile(before, opened) {
		return nil, fmt.Errorf("%w: artifact changed while opening", ErrInvalidArtifact)
	}
	if err := verifyReader(ctx, file, pair, receipt, visit); err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	current, err := authority.root.Lstat(receipt.Name)
	if err != nil {
		return nil, err
	}
	if !sameFile(before, after) ||
		!sameFile(after, current) {
		return nil, fmt.Errorf("%w: artifact changed while reading", ErrInvalidArtifact)
	}
	return &Publication{
		root: repositoryDirectory, path: artifactPath, receipt: receipt, info: current,
		directoryInfo: authority.info,
	}, nil
}

func (publication *Publication) Current() bool {
	if publication == nil || publication.info == nil {
		return false
	}
	authority, err := openStableDirectoryRoot(publication.root)
	if err != nil {
		return false
	}
	defer authority.close()
	if !os.SameFile(publication.directoryInfo, authority.info) {
		return false
	}
	current, err := authority.root.Lstat(filepath.Base(publication.path))
	return err == nil && sameFile(publication.info, current)
}

// ScanRecords descriptor-stably verifies the complete immutable artifact and
// reports each canonical record with an exact bounded reread reference. The
// visitor must not publish side effects until ScanRecords returns successfully:
// a later record or the final receipt/descriptor check may still fail.
func (publication *Publication) ScanRecords(
	ctx context.Context,
	generation GenerationIdentity,
	pair PairIdentity,
	visit func(RecordReference, Record) error,
) (resultErr error) {
	if ctx == nil || publication == nil || publication.info == nil || visit == nil {
		return errors.New("caller leaf record scan is invalid")
	}
	if err := ValidateReceipt(generation, pair, publication.receipt); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	authority, err := openStableDirectoryRoot(publication.root)
	if err != nil {
		return callerLeafReadAccessError("open caller leaf repository", err)
	}
	defer authority.close()
	if !sameDirectory(publication.directoryInfo, authority.info) {
		return fmt.Errorf("%w: caller leaf repository changed", ErrInvalidArtifact)
	}
	name := filepath.Base(publication.path)
	before, err := authority.root.Lstat(name)
	if err != nil {
		return callerLeafReadAccessError("stat caller leaf before scan", err)
	}
	if !sameFile(publication.info, before) {
		return fmt.Errorf("%w: caller leaf changed before scan", ErrInvalidArtifact)
	}
	file, err := authority.root.Open(name)
	if err != nil {
		return callerLeafReadAccessError("open caller leaf for scan", err)
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat caller leaf after open: %w", err)
	}
	if !sameFile(before, opened) {
		return fmt.Errorf("%w: caller leaf changed while opening", ErrInvalidArtifact)
	}
	if err := verifyReaderAt(ctx, file, pair, publication.receipt, visit); err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat caller leaf after scan: %w", err)
	}
	current, err := authority.root.Lstat(name)
	if err != nil {
		return callerLeafReadAccessError("stat caller leaf after scan", err)
	}
	if !sameFile(before, after) ||
		!sameFile(after, current) {
		return fmt.Errorf("%w: caller leaf changed while scanning", ErrInvalidArtifact)
	}
	return nil
}

// ReadRecord descriptor-stably reads and validates exactly one reference
// produced by ScanRecords. It never scans or hashes the rest of the artifact.
func (publication *Publication) ReadRecord(
	ctx context.Context,
	reference RecordReference,
) (result Record, resultErr error) {
	if ctx == nil || publication == nil || publication.info == nil ||
		reference.Offset < 0 || reference.Length < 1 ||
		reference.Length > MaxRecordBytes+1 || !validDigest(reference.Digest) ||
		int64(reference.Length) > publication.receipt.ContentBytes ||
		reference.Offset > publication.receipt.ContentBytes-int64(reference.Length) {
		return Record{}, fmt.Errorf("%w: caller leaf record reference is invalid", ErrInvalidArtifact)
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	authority, err := openStableDirectoryRoot(publication.root)
	if err != nil {
		return Record{}, callerLeafReadAccessError("open caller leaf repository", err)
	}
	defer authority.close()
	if !sameDirectory(publication.directoryInfo, authority.info) {
		return Record{}, fmt.Errorf("%w: caller leaf repository changed", ErrInvalidArtifact)
	}
	name := filepath.Base(publication.path)
	before, err := authority.root.Lstat(name)
	if err != nil {
		return Record{}, callerLeafReadAccessError("stat caller leaf before read", err)
	}
	if !sameFile(publication.info, before) {
		return Record{}, fmt.Errorf("%w: caller leaf changed before read", ErrInvalidArtifact)
	}
	file, err := authority.root.Open(name)
	if err != nil {
		return Record{}, callerLeafReadAccessError("open caller leaf for read", err)
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return Record{}, fmt.Errorf("stat caller leaf after open: %w", err)
	}
	if !sameFile(before, opened) {
		return Record{}, fmt.Errorf("%w: caller leaf changed while opening", ErrInvalidArtifact)
	}
	raw := make([]byte, reference.Length)
	if _, err := io.ReadFull(
		io.NewSectionReader(file, reference.Offset, int64(reference.Length)), raw,
	); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Record{}, fmt.Errorf(
				"%w: referenced caller leaf record was truncated",
				ErrInvalidArtifact,
			)
		}
		return Record{}, fmt.Errorf("read referenced caller leaf record: %w", err)
	}
	if recordReferenceDigest(raw) != reference.Digest ||
		decodeCanonicalLine(raw, &result) != nil || ValidateRecord(result) != nil {
		return Record{}, fmt.Errorf("%w: referenced caller leaf record differs", ErrInvalidArtifact)
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	after, err := file.Stat()
	if err != nil {
		return Record{}, fmt.Errorf("stat caller leaf after record read: %w", err)
	}
	current, err := authority.root.Lstat(name)
	if err != nil {
		return Record{}, callerLeafReadAccessError(
			"stat caller leaf after record read", err,
		)
	}
	if !sameFile(before, after) ||
		!sameFile(after, current) {
		return Record{}, fmt.Errorf("%w: caller leaf changed while reading", ErrInvalidArtifact)
	}
	return result, nil
}

func callerLeafReadAccessError(action string, err error) error {
	if errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, errCallerLeafDirectoryTransition) {
		return fmt.Errorf("%w: %s: %v", ErrInvalidArtifact, action, err)
	}
	return fmt.Errorf("%s: %w", action, err)
}

// CurrentAtRepository verifies this cold admission against identities read
// through a caller-owned descriptor for the exact shared repository. It does
// no filesystem I/O, allowing a complete-generation publication to check all
// of its leaves after opening the repository only once.
func (publication *Publication) CurrentAtRepository(
	directory string,
	receipt Receipt,
	directoryInfo, artifactInfo os.FileInfo,
) bool {
	if publication == nil || publication.info == nil ||
		publication.receipt != receipt || !validArtifactFileName(receipt.Name) {
		return false
	}
	return publication.root == directory &&
		publication.path == filepath.Join(directory, receipt.Name) &&
		sameDirectory(publication.directoryInfo, directoryInfo) &&
		sameFile(publication.info, artifactInfo)
}

func (publication *Publication) Receipt() Receipt {
	if publication == nil {
		return Receipt{}
	}
	return publication.receipt
}

// Matches proves that a descriptor-stable publication was opened for the
// exact caller-owned root, repository, and derived artifact name. Complete
// generation admission uses it to reuse a caller worker's cold validation
// without trusting a publication from another repository namespace.
func (publication *Publication) Matches(
	root, repository string,
	receipt Receipt,
) bool {
	if publication == nil || publication.receipt != receipt ||
		!validArtifactFileName(receipt.Name) {
		return false
	}
	wantedRoot, err := repositoryRoot(root, repository)
	if err != nil {
		return false
	}
	return publication.root == wantedRoot &&
		publication.path == filepath.Join(wantedRoot, receipt.Name)
}

func ArtifactPath(root, repository string, receipt Receipt) (string, error) {
	repositoryRoot, err := repositoryRoot(root, repository)
	if err != nil {
		return "", err
	}
	if !validArtifactFileName(receipt.Name) {
		return "", fmt.Errorf("%w: invalid artifact name", ErrInvalidArtifact)
	}
	return filepath.Join(repositoryRoot, receipt.Name), nil
}

func RemoveArtifact(root, repository string, receipt Receipt) error {
	if !validArtifactFileName(receipt.Name) {
		return fmt.Errorf("%w: invalid artifact name", ErrInvalidArtifact)
	}
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer authority.close()
	if err := authority.root.Remove(receipt.Name); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

// ArtifactNames returns only package-owned immutable artifact names for one
// trusted repository directory. Stages are excluded and foreign entries fail
// closed rather than becoming cleanup targets.
func ArtifactNames(root, repository string) ([]string, error) {
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer authority.close()
	entries, err := readRootDirectory(authority.root, MaxRepositoryDirectoryEntries)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if validStageDirectoryName(name) {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf(
					"%w: invalid stage entry %q", ErrInvalidArtifact, name,
				)
			}
			continue
		}
		if reservedCallerPublicationEntry(entry) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() ||
			!validArtifactFileName(name) {
			return nil, fmt.Errorf("%w: unexpected repository artifact %q", ErrInvalidArtifact, name)
		}
		result = append(result, name)
	}
	slices.Sort(result)
	return result, nil
}

// OpenArtifactInventory validates one caller-owned repository directory and
// indexes only canonical artifact names by their exact pair digest. Stage
// directories are ignored because startup/worker stage lifecycle owns them.
func OpenArtifactInventory(
	ctx context.Context,
	root, repository string,
) (*ArtifactInventory, error) {
	directory, authority, err := openRepositoryAuthority(root, repository, true)
	if err != nil {
		return nil, err
	}
	entries, err := readRootDirectory(authority.root, MaxRepositoryDirectoryEntries)
	if err != nil {
		authority.close()
		return nil, err
	}
	inventory := &ArtifactInventory{
		root: root, repository: repository, directory: directory,
		byPair: make(map[string][]string), entryCount: len(entries), authority: authority,
	}
	for index, entry := range entries {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				authority.close()
				return nil, err
			}
		}
		name := entry.Name()
		if validStageDirectoryName(name) {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				authority.close()
				return nil, fmt.Errorf(
					"%w: invalid stage entry %q", ErrInvalidArtifact, name,
				)
			}
			continue
		}
		if reservedCallerPublicationEntry(entry) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() ||
			!validArtifactFileName(name) {
			authority.close()
			return nil, fmt.Errorf(
				"%w: unexpected repository artifact %q", ErrInvalidArtifact, name,
			)
		}
		pairDigest := "sha256:" + name[len("leaf-v1-"):len("leaf-v1-")+64]
		inventory.byPair[pairDigest] = append(inventory.byPair[pairDigest], name)
	}
	return inventory, nil
}

func RemoveArtifactName(root, repository, name string) error {
	if !validArtifactFileName(name) {
		return fmt.Errorf("%w: invalid artifact name", ErrInvalidArtifact)
	}
	_, authority, err := openRepositoryAuthority(root, repository, false)
	if err != nil {
		return err
	}
	defer authority.close()
	if err := authority.root.Remove(name); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

func CleanupStages(ctx context.Context, root string) (int, error) {
	if err := ensureRealDirectory(root, true); err != nil {
		return 0, err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return 0, err
	}
	defer rootAuthority.close()
	repositories, err := readRootDirectory(
		rootAuthority.root, MaxRepositoryDirectoryEntries,
	)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, repository := range repositories {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !repository.IsDir() || repository.Type()&os.ModeSymlink != 0 ||
			!validRepositoryDirectoryName(repository.Name()) {
			continue
		}
		repositoryAuthority, err := openChildDirectoryAuthority(
			rootAuthority, repository.Name(), nil,
		)
		if err != nil {
			return removed, err
		}
		entries, err := readRootDirectory(
			repositoryAuthority.root, MaxRepositoryDirectoryEntries,
		)
		if err != nil {
			repositoryAuthority.close()
			return removed, err
		}
		for _, entry := range entries {
			if !validStageDirectoryName(entry.Name()) {
				continue
			}
			if err := removeOwnedStageAt(
				repositoryAuthority, entry.Name(), nil,
			); err != nil {
				repositoryAuthority.close()
				return removed, err
			}
			removed++
		}
		repositoryAuthority.close()
	}
	return removed, nil
}

func RemoveRepository(ctx context.Context, root, repository string) error {
	if err := ensureRealDirectory(root, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return err
	}
	defer rootAuthority.close()
	repositoryName := callerleafid.RepositoryDirectory(repository)
	repositoryAuthority, err := openChildDirectoryAuthority(
		rootAuthority, repositoryName, nil,
	)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer repositoryAuthority.close()
	entries, err := readRootDirectory(
		repositoryAuthority.root, MaxRepositoryDirectoryEntries,
	)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if validStageDirectoryName(name) {
			if err := validateOwnedStageAt(repositoryAuthority, name, nil); err != nil {
				return err
			}
			continue
		}
		if callerpublicationid.ValidStageName(name) {
			if err := validateCallerPublicationStageAt(
				repositoryAuthority, name, nil,
			); err != nil {
				return err
			}
			continue
		}
		if reservedCallerPublicationRegular(entry) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() ||
			!validArtifactFileName(name) {
			return fmt.Errorf("%w: unexpected repository artifact %q", ErrInvalidArtifact, name)
		}
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if validStageDirectoryName(name) {
			if err := removeOwnedStageAt(repositoryAuthority, name, nil); err != nil {
				return err
			}
			continue
		}
		if callerpublicationid.ValidStageName(name) {
			if err := removeCallerPublicationStageAt(
				repositoryAuthority, name, nil,
			); err != nil {
				return err
			}
			continue
		}
		if reservedCallerPublicationRegular(entry) {
			if err := repositoryAuthority.root.Remove(name); err != nil {
				return err
			}
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() ||
			!validArtifactFileName(name) {
			return fmt.Errorf("%w: unexpected repository artifact %q", ErrInvalidArtifact, name)
		}
		if err := repositoryAuthority.root.Remove(name); err != nil {
			return err
		}
	}
	if err := syncRootDirectory(repositoryAuthority.root, "."); err != nil {
		return err
	}
	repositoryInfo := repositoryAuthority.info
	repositoryAuthority.close()
	current, err := rootAuthority.root.Lstat(repositoryName)
	if err != nil || !sameDirectory(repositoryInfo, current) {
		return fmt.Errorf("%w: repository directory changed during removal", ErrInvalidArtifact)
	}
	if err := rootAuthority.root.Remove(repositoryName); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(rootAuthority.root, ".")
}

func ensureRepositoryRoot(root, repository string) (string, error) {
	if err := EnsureRepository(root, repository); err != nil {
		return "", err
	}
	directory := filepath.Join(root, callerleafid.RepositoryDirectory(repository))
	return directory, nil
}

var repositoryCreationMu sync.Mutex

// EnsureRepository creates one caller-owned repository directory without
// allowing concurrent leaf and complete-publication writers to cross the
// installation-wide repository ceiling. Existing exact directories remain
// usable at the ceiling.
func EnsureRepository(root, repository string) error {
	return ensureRepositoryWithinLimit(
		root, repository, callerpublicationid.InstallationPublicationRepositories,
	)
}

func ensureRepositoryWithinLimit(root, repository string, limit int) error {
	if err := reponame.Validate(repository); err != nil {
		return err
	}
	if limit <= 0 {
		return ErrCapacity
	}
	repositoryCreationMu.Lock()
	defer repositoryCreationMu.Unlock()
	if err := ensureRealDirectory(root, true); err != nil {
		return err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return err
	}
	defer rootAuthority.close()
	repositoryName := callerleafid.RepositoryDirectory(repository)
	if _, err := rootAuthority.root.Lstat(repositoryName); err == nil {
		authority, openErr := openChildDirectoryAuthority(
			rootAuthority, repositoryName, nil,
		)
		if openErr == nil {
			authority.close()
		}
		return openErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := readRootDirectory(rootAuthority.root, limit)
	if errors.Is(err, ErrLimit) {
		return ErrCapacity
	}
	if err != nil {
		return err
	}
	if len(entries) >= limit {
		return ErrCapacity
	}
	if err := rootAuthority.root.Mkdir(repositoryName, 0o700); err != nil &&
		!errors.Is(err, os.ErrExist) {
		return err
	}
	if err := syncRootDirectory(rootAuthority.root, "."); err != nil {
		return err
	}
	authority, err := openChildDirectoryAuthority(rootAuthority, repositoryName, nil)
	if err != nil {
		return err
	}
	authority.close()
	return nil
}

func repositoryRoot(root, repository string) (string, error) {
	if err := ensureRealDirectory(root, false); err != nil {
		return "", err
	}
	directory := filepath.Join(root, callerleafid.RepositoryDirectory(repository))
	if err := ensureRealDirectory(directory, false); err != nil {
		return "", err
	}
	return directory, nil
}

func ensureRealDirectory(path string, create bool) error {
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Clean(absolute) != filepath.Clean(path) {
		return errors.New("caller leaf root must be absolute and clean")
	}
	if create {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("caller leaf path is not a real directory")
	}
	return nil
}

func openRepositoryAuthority(
	root, repository string,
	create bool,
) (string, *directoryAuthority, error) {
	if err := ensureRealDirectory(root, create); err != nil {
		return "", nil, err
	}
	rootAuthority, err := openStableDirectoryRoot(root)
	if err != nil {
		return "", nil, err
	}
	repositoryName := callerleafid.RepositoryDirectory(repository)
	repositoryAuthority, err := openChildDirectoryAuthority(
		rootAuthority, repositoryName, nil,
	)
	rootAuthority.close()
	if create && errors.Is(err, os.ErrNotExist) {
		if err := EnsureRepository(root, repository); err != nil {
			return "", nil, err
		}
		return openRepositoryAuthority(root, repository, false)
	}
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(root, repositoryName), repositoryAuthority, nil
}

func openStableDirectoryRoot(directory string) (*directoryAuthority, error) {
	before, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf(
			"%w: caller leaf directory is not real",
			errCallerLeafDirectoryTransition,
		)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	opened, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	after, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil {
		_ = root.Close()
		return nil, statErr
	}
	if closeErr != nil {
		_ = root.Close()
		return nil, closeErr
	}
	if !sameDirectory(before, after) {
		_ = root.Close()
		return nil, fmt.Errorf(
			"%w: caller leaf directory changed while opening",
			errCallerLeafDirectoryTransition,
		)
	}
	return &directoryAuthority{root: root, info: after}, nil
}

func openChildDirectoryAuthority(
	parent *directoryAuthority,
	name string,
	expected os.FileInfo,
) (*directoryAuthority, error) {
	if parent == nil || parent.root == nil || filepath.Base(name) != name {
		return nil, errors.New("caller leaf child directory authority is invalid")
	}
	before, err := parent.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 ||
		expected != nil && !sameDirectory(expected, before) {
		return nil, errors.New("caller leaf child directory is not stable")
	}
	root, err := parent.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	after, statErr := opened.Stat()
	closeErr := opened.Close()
	if statErr != nil {
		_ = root.Close()
		return nil, statErr
	}
	if closeErr != nil {
		_ = root.Close()
		return nil, closeErr
	}
	if !sameDirectory(before, after) {
		_ = root.Close()
		return nil, errors.New("caller leaf child directory changed while opening")
	}
	return &directoryAuthority{root: root, info: after}, nil
}

func createStageDirectory(
	authority *directoryAuthority,
) (string, os.FileInfo, error) {
	if authority == nil || authority.root == nil {
		return "", nil, errors.New("caller leaf stage authority is unavailable")
	}
	for attempt := 0; attempt < 128; attempt++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := callerleafid.StagePrefix + hex.EncodeToString(random[:])
		if err := authority.root.Mkdir(name, 0o700); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", nil, err
		}
		info, err := authority.root.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			_ = authority.root.Remove(name)
			return "", nil, errors.New("caller leaf stage creation changed identity")
		}
		return name, info, nil
	}
	return "", nil, errors.New("caller leaf stage name collisions exceeded retry bound")
}

func validateStageAuthority(
	authority *directoryAuthority,
	name string,
	expected os.FileInfo,
) error {
	if authority == nil || authority.root == nil ||
		!validStageDirectoryName(name) || expected == nil {
		return errors.New("caller leaf stage authority is invalid")
	}
	current, err := authority.root.Lstat(name)
	if err != nil {
		return err
	}
	if !sameDirectory(expected, current) {
		return errors.New("caller leaf stage changed identity")
	}
	return nil
}

func validateOwnedStageAt(
	authority *directoryAuthority,
	name string,
	expected os.FileInfo,
) error {
	if authority == nil || authority.root == nil || !validStageDirectoryName(name) {
		return errors.New("caller leaf stage path is not package-owned")
	}
	info, err := authority.root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		expected != nil && !sameDirectory(expected, info) {
		return errors.New("caller leaf stage is not a real directory")
	}
	stageAuthority, err := openChildDirectoryAuthority(authority, name, info)
	if err != nil {
		return err
	}
	defer stageAuthority.close()
	entries, err := readRootDirectory(stageAuthority.root, 2)
	if err != nil {
		return err
	}
	if len(entries) > 1 {
		return errors.New("caller leaf stage has unexpected contents")
	}
	for _, entry := range entries {
		if entry.Name() != stageArtifactName || entry.Type()&os.ModeSymlink != 0 ||
			!entry.Type().IsRegular() {
			return errors.New("caller leaf stage has unexpected contents")
		}
	}
	return nil
}

func removeOwnedStageAt(
	authority *directoryAuthority,
	name string,
	expected os.FileInfo,
) error {
	if !validStageDirectoryName(name) {
		return errors.New("caller leaf stage path is not package-owned")
	}
	info, err := authority.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		expected != nil && !sameDirectory(expected, info) {
		return errors.New("caller leaf stage is not a real directory")
	}
	stageAuthority, err := openChildDirectoryAuthority(authority, name, info)
	if err != nil {
		return err
	}
	entries, err := readRootDirectory(stageAuthority.root, 2)
	if err != nil {
		stageAuthority.close()
		return err
	}
	if len(entries) > 1 {
		stageAuthority.close()
		return errors.New("caller leaf stage has unexpected contents")
	}
	for _, entry := range entries {
		if entry.Name() != stageArtifactName || entry.Type()&os.ModeSymlink != 0 ||
			!entry.Type().IsRegular() {
			stageAuthority.close()
			return errors.New("caller leaf stage has unexpected contents")
		}
		if err := stageAuthority.root.Remove(entry.Name()); err != nil {
			stageAuthority.close()
			return err
		}
	}
	stageAuthority.close()
	current, err := authority.root.Lstat(name)
	if err != nil || !sameDirectory(info, current) {
		return errors.New("caller leaf stage changed before removal")
	}
	if err := authority.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

func reservedCallerPublicationRegular(entry os.DirEntry) bool {
	if entry == nil || entry.Type()&os.ModeSymlink != 0 ||
		!entry.Type().IsRegular() {
		return false
	}
	name := entry.Name()
	return callerpublicationid.ValidManifestName(name) ||
		name == callerpublicationid.PublishingName ||
		name == callerpublicationid.DeletingName
}

func reservedCallerPublicationEntry(entry os.DirEntry) bool {
	if entry == nil {
		return false
	}
	if callerpublicationid.ValidStageName(entry.Name()) {
		return entry.IsDir() && entry.Type()&os.ModeSymlink == 0
	}
	return reservedCallerPublicationRegular(entry)
}

func validateCallerPublicationStageAt(
	authority *directoryAuthority,
	name string,
	expected os.FileInfo,
) error {
	if authority == nil || authority.root == nil ||
		!callerpublicationid.ValidStageName(name) {
		return errors.New("caller publication stage path is not package-owned")
	}
	info, err := authority.root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		expected != nil && !sameDirectory(expected, info) {
		return errors.New("caller publication stage is not a real directory")
	}
	stageAuthority, err := openChildDirectoryAuthority(authority, name, info)
	if err != nil {
		return err
	}
	defer stageAuthority.close()
	entries, err := readRootDirectory(stageAuthority.root, 3)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if (entry.Name() != "manifest.json" && entry.Name() != "marker.json") ||
			entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errors.New("caller publication stage has unexpected contents")
		}
	}
	return nil
}

func removeCallerPublicationStageAt(
	authority *directoryAuthority,
	name string,
	expected os.FileInfo,
) error {
	if err := validateCallerPublicationStageAt(authority, name, expected); err != nil {
		return err
	}
	info, err := authority.root.Lstat(name)
	if err != nil {
		return err
	}
	stageAuthority, err := openChildDirectoryAuthority(authority, name, info)
	if err != nil {
		return err
	}
	entries, err := readRootDirectory(stageAuthority.root, 3)
	if err != nil {
		stageAuthority.close()
		return err
	}
	for _, entry := range entries {
		if err := stageAuthority.root.Remove(entry.Name()); err != nil {
			stageAuthority.close()
			return err
		}
	}
	stageAuthority.close()
	current, err := authority.root.Lstat(name)
	if err != nil || !sameDirectory(info, current) {
		return errors.New("caller publication stage changed before removal")
	}
	if err := authority.root.Remove(name); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

func removeEmptyStageAt(
	authority *directoryAuthority,
	name string,
	expected os.FileInfo,
) error {
	if err := validateStageAuthority(authority, name, expected); err != nil {
		return err
	}
	stageAuthority, err := openChildDirectoryAuthority(authority, name, expected)
	if err != nil {
		return err
	}
	entries, err := readRootDirectory(stageAuthority.root, 1)
	stageAuthority.close()
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("caller leaf installed stage is not empty")
	}
	current, err := authority.root.Lstat(name)
	if err != nil || !sameDirectory(expected, current) {
		return errors.New("caller leaf installed stage changed before removal")
	}
	if err := authority.root.Remove(name); err != nil {
		return err
	}
	return syncRootDirectory(authority.root, ".")
}

func readRootDirectory(root *os.Root, limit int) ([]os.DirEntry, error) {
	if root == nil || limit <= 0 {
		return nil, ErrLimit
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	entries := make([]os.DirEntry, 0, 256)
	for {
		batch, readErr := directory.ReadDir(256)
		if len(entries)+len(batch) > limit {
			return nil, ErrLimit
		}
		entries = append(entries, batch...)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	return entries, nil
}

func syncRootDirectory(root *os.Root, name string) error {
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		return errors.New("caller leaf sync target is not a directory")
	}
	return directory.Sync()
}

func sameDirectory(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.IsDir() && right.IsDir() &&
		left.Mode()&os.ModeSymlink == 0 && right.Mode()&os.ModeSymlink == 0 &&
		os.SameFile(left, right)
}

func readLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > limit-len(line) {
			return nil, fmt.Errorf(
				"%w: caller leaf record exceeds byte limit", ErrInvalidArtifact,
			)
		}
		line = append(line, fragment...)
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return nil, fmt.Errorf(
				"%w: caller leaf artifact has partial trailing record", ErrInvalidArtifact,
			)
		}
		return line, err
	}
}

func decodeCanonicalLine(line []byte, destination any) error {
	if len(line) == 0 || line[len(line)-1] != '\n' {
		return errors.New("caller leaf record is partial")
	}
	decoder := json.NewDecoder(bytes.NewReader(line[:len(line)-1]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("caller leaf record has trailing JSON")
	}
	canonical, err := json.Marshal(destination)
	if err != nil {
		return err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(canonical, line) {
		return errors.New("caller leaf record is not canonical")
	}
	return nil
}

func sameFile(left, right os.FileInfo) bool {
	return left != nil && right != nil && left.Mode().IsRegular() &&
		right.Mode().IsRegular() && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime()) &&
		os.SameFile(left, right)
}

func validArtifactFileName(name string) bool {
	const (
		prefix = "leaf-v1-"
		suffix = ".ndjson"
	)
	if filepath.Base(name) != name || !strings.HasPrefix(name, prefix) ||
		!strings.HasSuffix(name, suffix) || len(name) != len(prefix)+64+1+64+len(suffix) {
		return false
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(hexPart) != 129 || hexPart[64] != '-' {
		return false
	}
	for index, current := range hexPart {
		if index == 64 {
			continue
		}
		if current < '0' || current > '9' && current < 'a' || current > 'f' {
			return false
		}
	}
	return true
}

func validRepositoryDirectoryName(name string) bool {
	const prefix = "phebs-caller-overlay-"
	return filepath.Base(name) == name && strings.HasPrefix(name, prefix) &&
		len(name) == len(prefix)+64 && validLowerHex(name[len(prefix):])
}

func validStageDirectoryName(name string) bool {
	return filepath.Base(name) == name &&
		strings.HasPrefix(name, callerleafid.StagePrefix) &&
		len(name) == len(callerleafid.StagePrefix)+32 &&
		validLowerHex(name[len(callerleafid.StagePrefix):])
}

func validLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, current := range value {
		if current < '0' || current > '9' && current < 'a' || current > 'f' {
			return false
		}
	}
	return true
}
