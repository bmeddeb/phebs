package focusedindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/store"
)

const (
	SearchGenerationReceiptSchema = "phebs-search-generation-receipt-v1"
	SearchGenerationRootSchema    = "phebs-search-generation-root-v1"
	SearchGenerationMarkerSchema  = "phebs-search-generation-marker-v1"
	SearchBlobReaderGoGit         = "go_git"
	SearchBlobReaderLegacy        = "legacy_unrecorded"

	// The retained 1.61-million-owner diagnostic measured 28.8 GB for one
	// generation and 57.5 GB for current plus replacement. The frozen T40.1
	// target has 2.0 million owners. These prospective limits keep roughly
	// forty percent headroom at that scale without inheriting T35's unrelated
	// 50-GiB retention placeholder.
	MaxSearchGenerationLogicalBytes   int64 = 48 << 30
	MaxSearchGenerationAllocatedBytes int64 = 48 << 30
	MaxRetainedSearchLogicalBytes     int64 = 96 << 30
	MaxRetainedSearchAllocatedBytes   int64 = 96 << 30
	MaxSearchShardLogicalBytes        int64 = 512 << 20
	MaxSearchGenerationShards               = 256
	MaxSearchGenerationFiles                = repositoryindex.MaxSourceMembers + MaxSearchGenerationShards + 4
	MaxSearchLifecycleRepositories          = 4_096
	MaxSearchRepositoryGenerations          = 64
	SearchGenerationMaxAge                  = 14 * 24 * time.Hour

	searchGenerationDirectoryName = "search-generations"
	searchGenerationReceiptName   = "generation.json"
)

var ErrSearchPublicationRevisionMismatch = errors.New(
	"search publication marker matches no committed generation",
)

// SearchGenerationRef is the small immutable identity used by publication,
// recovery, readers, and lifecycle. Logical and allocated bytes are distinct;
// allocated bytes are unavailable rather than guessed on unsupported hosts.
type SearchGenerationRef struct {
	GenerationDigest string                  `json:"generation_digest"`
	Directory        string                  `json:"directory"`
	Revisions        []store.IndexedRevision `json:"revisions"`
	LogicalBytes     int64                   `json:"logical_bytes"`
	AllocatedBytes   int64                   `json:"allocated_bytes,omitempty"`
	AllocatedState   string                  `json:"allocated_state"`
	ShardCount       int                     `json:"shard_count"`
	FileCount        int                     `json:"file_count"`
}

type SearchGenerationReceipt struct {
	Schema            string                  `json:"schema"`
	Repository        string                  `json:"repository"`
	SearchDigest      string                  `json:"search_digest"`
	SourceDigest      string                  `json:"source_digest"`
	Revisions         []store.IndexedRevision `json:"revisions"`
	BlobReaderMode    string                  `json:"blob_reader_mode"`
	FilesOffered      int                     `json:"files_offered"`
	BatchReadCount    int                     `json:"batch_read_count"`
	FallbackReadCount int                     `json:"fallback_read_count"`
	LogicalBytes      int64                   `json:"logical_bytes"`
	AllocatedBytes    int64                   `json:"allocated_bytes,omitempty"`
	AllocatedState    string                  `json:"allocated_state"`
	ShardCount        int                     `json:"shard_count"`
	FileCount         int                     `json:"file_count"`
}

// SearchGenerationControls is one exact immutable search-generation view.
// Directory names the already lifecycle-pinned root used by hot readers;
// Search and Source are strict control-only reads from that root.
type SearchGenerationControls struct {
	Directory string
	Receipt   SearchGenerationReceipt
	Search    repositoryindex.SearchManifest
	Source    repositoryindex.SourceManifest
}

type SearchGenerationRoot struct {
	Schema     string               `json:"schema"`
	Repository string               `json:"repository"`
	Current    SearchGenerationRef  `json:"current"`
	Prior      *SearchGenerationRef `json:"prior,omitempty"`
}

type searchGenerationMarker struct {
	Schema     string                `json:"schema"`
	Repository string                `json:"repository"`
	Candidate  SearchGenerationRef   `json:"candidate"`
	Previous   *SearchGenerationRoot `json:"previous,omitempty"`
}

func SearchGenerationRootName(repository string) string {
	return "phebs-search-lifecycle-" + repositoryKey(repository) + ".json"
}

// RetireSearchGenerationRoot releases whole-search lifecycle authority after
// the durable repository row has committed a focused posture. Immutable bytes
// remain lease-protected and are reclaimed by the bounded lifecycle owner.
func RetireSearchGenerationRoot(indexDir, repository string) error {
	err := os.Remove(filepath.Join(indexDir, SearchGenerationRootName(repository)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return syncDirectory(indexDir)
}

func searchGenerationMarkerName(repository string) string {
	return "phebs-search-transition-" + repositoryKey(repository) + ".json"
}

func SearchGenerationRootDirectory(indexDir string) string {
	return filepath.Join(indexDir, searchGenerationDirectoryName)
}

func searchGenerationRepositoryDirectory(indexDir, repository string) string {
	return filepath.Join(SearchGenerationRootDirectory(indexDir), repositoryKey(repository))
}

func searchGenerationDirectory(indexDir, repository, digest string) (string, error) {
	if !validDigest(digest) {
		return "", errors.New("invalid search generation digest")
	}
	return filepath.Join(
		searchGenerationRepositoryDirectory(indexDir, repository),
		strings.TrimPrefix(digest, "sha256:"),
	), nil
}

func SearchGenerationReservation(source repositoryindex.SourceManifest) (int64, error) {
	if err := repositoryindex.ValidateSourceManifest(source); err != nil {
		return 0, err
	}
	if source.RegularDeclaredBytes < 0 || source.EncodedMemberBytes < 0 ||
		source.RegularDeclaredBytes > (math.MaxInt64-source.EncodedMemberBytes)/(3) {
		return 0, errors.New("search generation reservation overflows")
	}
	reservation := source.RegularDeclaredBytes*3 + source.EncodedMemberBytes
	if reservation > MaxSearchGenerationLogicalBytes {
		reservation = MaxSearchGenerationLogicalBytes
	}
	return reservation, nil
}

func filesOffered(source repositoryindex.SourceManifest) (int, error) {
	total := 0
	for _, revision := range source.RevisionMembers {
		if revision.RegularCount < 0 || total > math.MaxInt-revision.RegularCount {
			return 0, errors.New("search offered-file count overflows")
		}
		total += revision.RegularCount
	}
	return total, nil
}

func SearchGenerationReaderCounts(source repositoryindex.SourceManifest) (batch, fallback int, err error) {
	offered, err := filesOffered(source)
	if err != nil {
		return 0, 0, err
	}
	return 0, offered, nil
}

func ReadSearchGenerationRoot(indexDir, repository string) (SearchGenerationRoot, error) {
	return ReadSearchGenerationRootContext(context.Background(), indexDir, repository)
}

// ReadSearchGenerationRootContext is the request-accounted form of
// ReadSearchGenerationRoot. Directory metadata remains outside control-file
// accounting; the bounded root read is charged immediately before its attempt.
func ReadSearchGenerationRootContext(
	ctx context.Context,
	indexDir, repository string,
) (SearchGenerationRoot, error) {
	var root SearchGenerationRoot
	path := filepath.Join(indexDir, SearchGenerationRootName(repository))
	if _, err := os.Lstat(path); err != nil {
		return SearchGenerationRoot{}, err
	}
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return SearchGenerationRoot{}, err
	}
	if err := readControlFile(path, &root); err != nil {
		return SearchGenerationRoot{}, err
	}
	if err := validateSearchGenerationRoot(root, repository); err != nil {
		return SearchGenerationRoot{}, err
	}
	return root, nil
}

func validateSearchGenerationRoot(root SearchGenerationRoot, repository string) error {
	if root.Schema != SearchGenerationRootSchema || root.Repository != repository {
		return errors.New("search generation root identity mismatch")
	}
	if err := validateSearchGenerationRef(root.Current); err != nil {
		return err
	}
	if root.Prior != nil {
		if err := validateSearchGenerationRef(*root.Prior); err != nil {
			return err
		}
		if root.Prior.GenerationDigest == root.Current.GenerationDigest {
			return errors.New("search generation root repeats current as prior")
		}
		if root.Current.LogicalBytes > MaxRetainedSearchLogicalBytes-root.Prior.LogicalBytes {
			return errors.New("retained search logical bytes exceed policy")
		}
		if root.Current.AllocatedState == "exact" && root.Prior.AllocatedState == "exact" &&
			root.Current.AllocatedBytes > MaxRetainedSearchAllocatedBytes-root.Prior.AllocatedBytes {
			return errors.New("retained search allocated bytes exceed policy")
		}
	}
	return nil
}

func validateSearchGenerationRef(ref SearchGenerationRef) error {
	if !validDigest(ref.GenerationDigest) ||
		ref.Directory != strings.TrimPrefix(ref.GenerationDigest, "sha256:") ||
		ValidateRevisions(ref.Revisions) != nil ||
		ref.LogicalBytes < 1 || ref.LogicalBytes > MaxSearchGenerationLogicalBytes ||
		ref.ShardCount < 1 || ref.ShardCount > MaxSearchGenerationShards ||
		ref.FileCount < 1 || ref.FileCount > MaxSearchGenerationFiles ||
		(ref.AllocatedState != "exact" && ref.AllocatedState != "unavailable") ||
		(ref.AllocatedState == "exact" && (ref.AllocatedBytes < 1 || ref.AllocatedBytes > MaxSearchGenerationAllocatedBytes)) ||
		(ref.AllocatedState == "unavailable" && ref.AllocatedBytes != 0) {
		return errors.New("invalid search generation reference")
	}
	return nil
}

func readSearchGenerationReceipt(directory, repository string) (SearchGenerationReceipt, error) {
	return readSearchGenerationReceiptFile(
		filepath.Join(directory, searchGenerationReceiptName), repository,
	)
}

func readSearchGenerationReceiptContext(
	ctx context.Context,
	directory, repository string,
) (SearchGenerationReceipt, error) {
	if err := readaccounting.Charge(ctx, readaccounting.ControlFileRead, 1); err != nil {
		return SearchGenerationReceipt{}, err
	}
	return readSearchGenerationReceipt(directory, repository)
}

func readSearchGenerationReceiptFile(path, repository string) (SearchGenerationReceipt, error) {
	var receipt SearchGenerationReceipt
	if err := readControlFile(path, &receipt); err != nil {
		return SearchGenerationReceipt{}, err
	}
	if receipt.Schema != SearchGenerationReceiptSchema || receipt.Repository != repository ||
		!validDigest(receipt.SearchDigest) || !validDigest(receipt.SourceDigest) ||
		ValidateRevisions(receipt.Revisions) != nil ||
		(receipt.BlobReaderMode != SearchBlobReaderGoGit && receipt.BlobReaderMode != SearchBlobReaderLegacy) ||
		receipt.FilesOffered < 0 || receipt.BatchReadCount < 0 || receipt.FallbackReadCount < 0 {
		return SearchGenerationReceipt{}, errors.New("invalid search generation receipt")
	}
	ref := SearchGenerationRef{
		GenerationDigest: receipt.SearchDigest,
		Directory:        strings.TrimPrefix(receipt.SearchDigest, "sha256:"),
		Revisions:        receipt.Revisions, LogicalBytes: receipt.LogicalBytes,
		AllocatedBytes: receipt.AllocatedBytes, AllocatedState: receipt.AllocatedState,
		ShardCount: receipt.ShardCount, FileCount: receipt.FileCount,
	}
	if err := validateSearchGenerationRef(ref); err != nil {
		return SearchGenerationReceipt{}, err
	}
	if receipt.BlobReaderMode == SearchBlobReaderGoGit &&
		(receipt.BatchReadCount != 0 || receipt.FallbackReadCount != receipt.FilesOffered) {
		return SearchGenerationReceipt{}, errors.New("invalid go-git search reader accounting")
	}
	return receipt, nil
}

func searchGenerationArchiveReceiptName(repository string) string {
	return "phebs-search-receipt-" + repositoryKey(repository) + ".json"
}

func validateFlatSearchGenerationReceipt(
	indexDir, repository string,
	search repositoryindex.SearchManifest,
) (SearchGenerationReceipt, error) {
	receipt, err := readSearchGenerationReceiptFile(
		filepath.Join(indexDir, searchGenerationArchiveReceiptName(repository)), repository,
	)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	source, err := repositoryindex.ReadSourceManifest(indexDir, repository)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	whole, err := ReadWholeManifest(indexDir, repository, search.Revisions)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	files := searchGenerationFiles(source, whole)
	shards := make(map[string]bool, len(whole.Members))
	for _, member := range whole.Members {
		shards[member.Name] = true
	}
	logical, _, _, err := measureSearchGeneration(indexDir, files, shards)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	offered, err := filesOffered(source)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	if receipt.SearchDigest != search.Digest || receipt.SourceDigest != source.Digest ||
		!sameSearchRevisions(receipt.Revisions, search.Revisions) ||
		receipt.FilesOffered != offered || receipt.LogicalBytes != logical ||
		receipt.ShardCount != len(whole.Members) || receipt.FileCount != len(files) {
		return SearchGenerationReceipt{}, errors.New("archived search receipt differs from flat authority")
	}
	return receipt, nil
}

func removeSearchGenerationArchiveReceipt(indexDir, repository string) error {
	err := os.Remove(filepath.Join(indexDir, searchGenerationArchiveReceiptName(repository)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDirectory(indexDir)
}

func searchGenerationFiles(
	source repositoryindex.SourceManifest,
	whole WholeManifest,
) []string {
	files := make([]string, 0, len(source.Members)+len(whole.Members)+3)
	for _, member := range source.Members {
		files = append(files, member.Name)
	}
	files = append(files, repositoryindex.SourceManifestName(source.Repository))
	for _, member := range whole.Members {
		files = append(files, member.Name)
	}
	files = append(files, WholeManifestName(source.Repository))
	files = append(files, repositoryindex.SearchManifestName(source.Repository))
	return files
}

func measureSearchGeneration(directory string, files []string, shardNames map[string]bool) (logical, allocated int64, allocatedState string, err error) {
	if len(files) < 1 || len(files) > MaxSearchGenerationFiles {
		return 0, 0, "", errors.New("search generation file inventory exceeds policy")
	}
	allocatedState = "exact"
	seen := make(map[string]bool, len(files))
	for _, name := range files {
		if filepath.Base(name) != name || seen[name] {
			return 0, 0, "", errors.New("invalid search generation file inventory")
		}
		seen[name] = true
		info, statErr := os.Lstat(filepath.Join(directory, name))
		if statErr != nil || !info.Mode().IsRegular() {
			return 0, 0, "", errors.New("search generation contains a missing or special file")
		}
		if shardNames[name] && info.Size() > MaxSearchShardLogicalBytes {
			return 0, 0, "", errors.New("search shard exceeds logical-byte policy")
		}
		if info.Size() < 0 || logical > MaxSearchGenerationLogicalBytes-info.Size() {
			return 0, 0, "", errors.New("search generation exceeds logical-byte policy")
		}
		logical += info.Size()
		blocks, ok := allocatedFileBytes(info)
		if !ok {
			allocatedState = "unavailable"
			allocated = 0
			continue
		}
		if allocatedState == "exact" {
			if blocks < 0 || allocated > MaxSearchGenerationAllocatedBytes-blocks {
				return 0, 0, "", errors.New("search generation exceeds allocated-byte policy")
			}
			allocated += blocks
		}
	}
	return logical, allocated, allocatedState, nil
}

func writeSearchGenerationRoot(indexDir string, root SearchGenerationRoot) error {
	if err := validateSearchGenerationRoot(root, root.Repository); err != nil {
		return err
	}
	return replaceSearchControlFile(
		filepath.Join(indexDir, SearchGenerationRootName(root.Repository)), root,
	)
}

func replaceSearchControlFile(path string, value any) error {
	raw, err := canonicalJSON(value)
	if err != nil {
		return err
	}
	if len(raw) > maxControlBytes {
		return errors.New("search lifecycle control exceeds its byte limit")
	}
	directory := filepath.Dir(path)
	if err := ensureRealDirectory(directory); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func writeSearchGenerationMarker(indexDir string, marker searchGenerationMarker) error {
	if marker.Schema != SearchGenerationMarkerSchema || marker.Repository == "" ||
		validateSearchGenerationRef(marker.Candidate) != nil ||
		(marker.Previous != nil && validateSearchGenerationRoot(*marker.Previous, marker.Repository) != nil) {
		return errors.New("invalid search generation transition")
	}
	return WriteControlFile(filepath.Join(indexDir, searchGenerationMarkerName(marker.Repository)), marker)
}

func readSearchGenerationMarker(indexDir, repository string) (searchGenerationMarker, error) {
	var marker searchGenerationMarker
	path := filepath.Join(indexDir, searchGenerationMarkerName(repository))
	if _, err := os.Lstat(path); err != nil {
		return searchGenerationMarker{}, err
	}
	if err := readControlFile(path, &marker); err != nil {
		return searchGenerationMarker{}, err
	}
	if marker.Schema != SearchGenerationMarkerSchema || marker.Repository != repository ||
		validateSearchGenerationRef(marker.Candidate) != nil ||
		(marker.Previous != nil && validateSearchGenerationRoot(*marker.Previous, repository) != nil) {
		return searchGenerationMarker{}, errors.New("invalid search generation transition")
	}
	return marker, nil
}

func generationRefFromReceipt(receipt SearchGenerationReceipt) SearchGenerationRef {
	return SearchGenerationRef{
		GenerationDigest: receipt.SearchDigest,
		Directory:        strings.TrimPrefix(receipt.SearchDigest, "sha256:"),
		Revisions:        slices.Clone(receipt.Revisions),
		LogicalBytes:     receipt.LogicalBytes, AllocatedBytes: receipt.AllocatedBytes,
		AllocatedState: receipt.AllocatedState, ShardCount: receipt.ShardCount,
		FileCount: receipt.FileCount,
	}
}

func sameSearchRevisions(left, right []store.IndexedRevision) bool {
	return slices.Equal(left, right)
}

// SearchGenerationPins is the process-local reader fence shared by search and
// lifecycle. Publication roots remain the durable authority; pins only delay
// retirement after a reader has already bound an exact immutable generation.
type SearchGenerationPins struct {
	mu       sync.Mutex
	pins     map[string]int
	retiring map[string]struct{}
}

type SearchGenerationLease struct {
	pins *SearchGenerationPins
	key  string
	once sync.Once
}

func (pins *SearchGenerationPins) Acquire(repository, generation string) (*SearchGenerationLease, error) {
	if repository == "" || !validDigest(generation) {
		return nil, errors.New("invalid search generation lease")
	}
	key := repository + "\x00" + generation
	pins.mu.Lock()
	if _, retiring := pins.retiring[key]; retiring {
		pins.mu.Unlock()
		return nil, errSearchGenerationPinned
	}
	if pins.pins == nil {
		pins.pins = make(map[string]int)
	}
	pins.pins[key]++
	pins.mu.Unlock()
	return &SearchGenerationLease{pins: pins, key: key}, nil
}

// BeginRetire atomically excludes a new lease after proving that no reader is
// pinned. The caller holds the returned guard through the durable rename.
func (pins *SearchGenerationPins) BeginRetire(repository, generation string) (func(), bool) {
	if pins == nil {
		return nil, false
	}
	key := repository + "\x00" + generation
	pins.mu.Lock()
	if pins.pins[key] != 0 {
		pins.mu.Unlock()
		return nil, false
	}
	if pins.retiring == nil {
		pins.retiring = make(map[string]struct{})
	}
	if _, present := pins.retiring[key]; present {
		pins.mu.Unlock()
		return nil, false
	}
	pins.retiring[key] = struct{}{}
	pins.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			pins.mu.Lock()
			delete(pins.retiring, key)
			pins.mu.Unlock()
		})
	}, true
}

func (pins *SearchGenerationPins) Pinned(repository, generation string) bool {
	if pins == nil {
		return false
	}
	pins.mu.Lock()
	defer pins.mu.Unlock()
	return pins.pins[repository+"\x00"+generation] > 0
}

func (lease *SearchGenerationLease) Release() {
	if lease == nil || lease.pins == nil {
		return
	}
	lease.once.Do(func() {
		lease.pins.mu.Lock()
		if lease.pins.pins[lease.key] <= 1 {
			delete(lease.pins.pins, lease.key)
		} else {
			lease.pins.pins[lease.key]--
		}
		lease.pins.mu.Unlock()
	})
}

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func removeSearchTransition(indexDir, repository string) error {
	err := os.Remove(filepath.Join(indexDir, searchGenerationMarkerName(repository)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return syncDirectory(indexDir)
}

func admitSearchGenerationGrowth(repositoryDirectory string) error {
	entries, err := boundedSearchDirectory(
		repositoryDirectory, MaxSearchRepositoryGenerations+8,
	)
	if err != nil {
		return err
	}
	if len(entries) >= MaxSearchRepositoryGenerations+8 {
		return errors.New("search generation namespace has no staging headroom")
	}
	generationCount := 0
	stageCount := 0
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 &&
			isIgnorableSearchMetadata(entry.Name()) {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("invalid search generation inventory")
		}
		name := entry.Name()
		raw := strings.TrimPrefix(name, "collecting-")
		if len(raw) == 64 && isLowerHex(raw) {
			generationCount++
			continue
		}
		if strings.HasPrefix(name, ".stage-") || strings.HasPrefix(name, ".legacy-") {
			stageCount++
			continue
		}
		return errors.New("invalid search generation inventory")
	}
	if generationCount >= MaxSearchRepositoryGenerations {
		return errors.New("search generation inventory limit exceeded")
	}
	if stageCount >= 8 {
		return errors.New("search generation staging inventory limit exceeded")
	}
	return nil
}

func createImmutableSearchGeneration(
	ctx context.Context,
	indexDir, shardStageDir, sourceStageDir, repository string,
	revisions []store.IndexedRevision,
	source repositoryindex.SourceManifest,
	whole WholeManifest,
	search repositoryindex.SearchManifest,
	readerMode string,
) (SearchGenerationRef, error) {
	if search.Repository != repository || search.Digest == "" ||
		search.SourceGenerationDigest != source.Digest ||
		!sameSearchRevisions(search.Revisions, revisions) {
		return SearchGenerationRef{}, errors.New("search generation stage identity mismatch")
	}
	root := SearchGenerationRootDirectory(indexDir)
	repositoryDirectory := searchGenerationRepositoryDirectory(indexDir, repository)
	for _, directory := range []string{root, repositoryDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return SearchGenerationRef{}, err
		}
		if err := ensureRealDirectory(directory); err != nil {
			return SearchGenerationRef{}, err
		}
	}
	destination, err := searchGenerationDirectory(indexDir, repository, search.Digest)
	if err != nil {
		return SearchGenerationRef{}, err
	}
	if _, err := os.Lstat(destination); err == nil {
		receipt, validateErr := validateImmutableSearchGeneration(
			ctx, indexDir, repository, search.Digest,
		)
		if validateErr != nil {
			return SearchGenerationRef{}, validateErr
		}
		return generationRefFromReceipt(receipt), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return SearchGenerationRef{}, err
	}
	if err := admitSearchGenerationGrowth(repositoryDirectory); err != nil {
		return SearchGenerationRef{}, err
	}
	stage, err := os.MkdirTemp(repositoryDirectory, ".stage-"+lifecycleOwner+"-")
	if err != nil {
		return SearchGenerationRef{}, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(stage)
		}
	}()
	files := searchGenerationFiles(source, whole)
	shards := make(map[string]bool, len(whole.Members))
	for _, member := range whole.Members {
		shards[member.Name] = true
	}
	for _, name := range files {
		from := sourceStageDir
		if shards[name] || name == WholeManifestName(repository) {
			from = shardStageDir
		}
		if err := moveRegular(filepath.Join(from, name), filepath.Join(stage, name)); err != nil {
			return SearchGenerationRef{}, err
		}
	}
	if _, err := ValidateRepositorySearchGeneration(ctx, stage, repository, revisions); err != nil {
		return SearchGenerationRef{}, fmt.Errorf("validate immutable search generation: %w", err)
	}
	logical, allocated, allocatedState, err := measureSearchGeneration(stage, files, shards)
	if err != nil {
		return SearchGenerationRef{}, err
	}
	offered, err := filesOffered(source)
	if err != nil {
		return SearchGenerationRef{}, err
	}
	receipt := SearchGenerationReceipt{
		Schema: SearchGenerationReceiptSchema, Repository: repository,
		SearchDigest: search.Digest, SourceDigest: source.Digest,
		Revisions: slices.Clone(revisions), BlobReaderMode: readerMode,
		FilesOffered: offered, LogicalBytes: logical,
		AllocatedBytes: allocated, AllocatedState: allocatedState,
		ShardCount: len(whole.Members), FileCount: len(files),
	}
	if readerMode == SearchBlobReaderGoGit {
		receipt.FallbackReadCount = offered
	}
	if err := WriteControlFile(filepath.Join(stage, searchGenerationReceiptName), receipt); err != nil {
		return SearchGenerationRef{}, err
	}
	if err := syncDirectory(stage); err != nil {
		return SearchGenerationRef{}, err
	}
	if err := os.Rename(stage, destination); err != nil {
		if _, statErr := os.Lstat(destination); statErr == nil {
			existing, validateErr := validateImmutableSearchGeneration(
				ctx, indexDir, repository, search.Digest,
			)
			if validateErr == nil {
				complete = true
				_ = os.RemoveAll(stage)
				return generationRefFromReceipt(existing), nil
			}
		}
		return SearchGenerationRef{}, err
	}
	if err := syncDirectory(repositoryDirectory); err != nil {
		return SearchGenerationRef{}, err
	}
	complete = true
	return generationRefFromReceipt(receipt), nil
}

func validateImmutableSearchGeneration(
	ctx context.Context, indexDir, repository, digest string,
) (SearchGenerationReceipt, error) {
	directory, err := searchGenerationDirectory(indexDir, repository, digest)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	if err := ensureRealDirectory(directory); err != nil {
		return SearchGenerationReceipt{}, err
	}
	receipt, err := readSearchGenerationReceipt(directory, repository)
	if err != nil || receipt.SearchDigest != digest {
		if err != nil {
			return SearchGenerationReceipt{}, err
		}
		return SearchGenerationReceipt{}, errors.New("search generation directory identity mismatch")
	}
	search, err := ValidateRepositorySearchGeneration(
		ctx, directory, repository, receipt.Revisions,
	)
	if err != nil || search.Digest != digest || search.SourceGenerationDigest != receipt.SourceDigest {
		if err != nil {
			return SearchGenerationReceipt{}, err
		}
		return SearchGenerationReceipt{}, errors.New("search generation receipt disagrees with authority")
	}
	source, err := repositoryindex.ReadSourceManifest(directory, repository)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	whole, err := ReadWholeManifest(directory, repository, receipt.Revisions)
	if err != nil {
		return SearchGenerationReceipt{}, err
	}
	files := searchGenerationFiles(source, whole)
	shards := make(map[string]bool, len(whole.Members))
	for _, member := range whole.Members {
		shards[member.Name] = true
	}
	logical, allocated, allocatedState, err := measureSearchGeneration(directory, files, shards)
	if err != nil || logical != receipt.LogicalBytes ||
		len(files) != receipt.FileCount || len(whole.Members) != receipt.ShardCount {
		if err != nil {
			return SearchGenerationReceipt{}, err
		}
		return SearchGenerationReceipt{}, errors.New("search generation accounting changed")
	}
	// Filesystem allocation is a current-host pressure measurement, not
	// content identity. A stopped data-directory relocation may preserve every
	// byte while changing st_blocks or support for allocated-byte reporting.
	// measureSearchGeneration has already enforced the current-host ceiling;
	// return its values to callers without rewriting the immutable receipt.
	receipt.AllocatedState = allocatedState
	receipt.AllocatedBytes = allocated
	return receipt, nil
}

// ValidateSearchGeneration strict-validates one retained immutable generation
// by exact digest. Runtime selectors use it for startup and backup fencing;
// unlike the current-root reader, it also admits the lease-protected prior.
func ValidateSearchGeneration(
	ctx context.Context, indexDir, repository, digest string,
) (SearchGenerationReceipt, error) {
	return validateImmutableSearchGeneration(ctx, indexDir, repository, digest)
}

// ReadSearchGenerationControls opens one retained immutable generation by
// digest without consulting the mutable flat publication or lifecycle root.
// It validates only bounded controls; whole-reader cache fill owns the one
// complete member validation before serving queries.
func ReadSearchGenerationControls(
	ctx context.Context,
	indexDir, repository, digest string,
) (SearchGenerationControls, error) {
	if err := ctx.Err(); err != nil {
		return SearchGenerationControls{}, err
	}
	directory, err := searchGenerationDirectory(indexDir, repository, digest)
	if err != nil {
		return SearchGenerationControls{}, err
	}
	if err := ensureRealDirectory(directory); err != nil {
		return SearchGenerationControls{}, err
	}
	receipt, err := readSearchGenerationReceiptContext(ctx, directory, repository)
	if err != nil {
		return SearchGenerationControls{}, err
	}
	if receipt.SearchDigest != digest {
		return SearchGenerationControls{}, errors.New(
			"search generation directory identity mismatch",
		)
	}
	search, source, err := readRepositorySearchGenerationContext(
		ctx, directory, repository, receipt.Revisions,
	)
	if err != nil {
		return SearchGenerationControls{}, err
	}
	if search.Digest != receipt.SearchDigest ||
		search.SourceGenerationDigest != receipt.SourceDigest ||
		source.Digest != receipt.SourceDigest ||
		!sameSearchRevisions(search.Revisions, receipt.Revisions) ||
		!sameSearchRevisions(source.Revisions, receipt.Revisions) {
		return SearchGenerationControls{}, errors.New(
			"search generation controls disagree with receipt",
		)
	}
	return SearchGenerationControls{
		Directory: directory, Receipt: receipt, Search: search, Source: source,
	}, nil
}

func adoptLegacySearchGeneration(
	ctx context.Context, indexDir, repository string,
) (*SearchGenerationRef, error) {
	if _, err := os.Lstat(filepath.Join(indexDir, repositoryindex.SearchManifestName(repository))); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	search, err := repositoryindex.ReadSearchManifest(indexDir, repository)
	if err != nil {
		return nil, err
	}
	if _, err := ValidateRepositorySearchGeneration(
		ctx, indexDir, repository, search.Revisions,
	); err != nil {
		return nil, err
	}
	source, err := repositoryindex.ReadSourceManifest(indexDir, repository)
	if err != nil {
		return nil, err
	}
	whole, err := ReadWholeManifest(indexDir, repository, search.Revisions)
	if err != nil {
		return nil, err
	}
	var archivedReceipt *SearchGenerationReceipt
	archiveReceiptPath := filepath.Join(
		indexDir, searchGenerationArchiveReceiptName(repository),
	)
	if _, statErr := os.Lstat(archiveReceiptPath); statErr == nil {
		receipt, receiptErr := validateFlatSearchGenerationReceipt(
			indexDir, repository, search,
		)
		if receiptErr != nil {
			return nil, receiptErr
		}
		archivedReceipt = &receipt
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	root := SearchGenerationRootDirectory(indexDir)
	repositoryDirectory := searchGenerationRepositoryDirectory(indexDir, repository)
	for _, directory := range []string{root, repositoryDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
		if err := ensureRealDirectory(directory); err != nil {
			return nil, err
		}
	}
	destination, err := searchGenerationDirectory(indexDir, repository, search.Digest)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(destination); err == nil {
		receipt, err := validateImmutableSearchGeneration(ctx, indexDir, repository, search.Digest)
		if err != nil {
			return nil, err
		}
		ref := generationRefFromReceipt(receipt)
		return &ref, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := admitSearchGenerationGrowth(repositoryDirectory); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(repositoryDirectory, ".legacy-"+lifecycleOwner+"-")
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(stage)
		}
	}()
	files := searchGenerationFiles(source, whole)
	shards := make(map[string]bool, len(whole.Members))
	for _, member := range whole.Members {
		shards[member.Name] = true
	}
	for _, name := range files {
		from := filepath.Join(indexDir, name)
		info, err := os.Lstat(from)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("legacy search generation contains a missing or special file")
		}
		if err := os.Link(from, filepath.Join(stage, name)); err != nil {
			return nil, err
		}
	}
	logical, allocated, allocatedState, err := measureSearchGeneration(stage, files, shards)
	if err != nil {
		return nil, err
	}
	offered, err := filesOffered(source)
	if err != nil {
		return nil, err
	}
	var receipt SearchGenerationReceipt
	if archivedReceipt != nil {
		receipt = *archivedReceipt
	} else {
		receipt = SearchGenerationReceipt{
			Schema: SearchGenerationReceiptSchema, Repository: repository,
			SearchDigest: search.Digest, SourceDigest: source.Digest,
			Revisions: slices.Clone(search.Revisions), BlobReaderMode: SearchBlobReaderLegacy,
			FilesOffered: offered, LogicalBytes: logical, AllocatedBytes: allocated,
			AllocatedState: allocatedState, ShardCount: len(whole.Members), FileCount: len(files),
		}
	}
	if err := WriteControlFile(filepath.Join(stage, searchGenerationReceiptName), receipt); err != nil {
		return nil, err
	}
	if err := syncDirectory(stage); err != nil {
		return nil, err
	}
	if err := os.Rename(stage, destination); err != nil {
		return nil, err
	}
	if err := syncDirectory(repositoryDirectory); err != nil {
		return nil, err
	}
	complete = true
	ref := generationRefFromReceipt(receipt)
	return &ref, nil
}

func installStableSearchGeneration(
	ctx context.Context, indexDir, repository string, ref SearchGenerationRef,
) error {
	receipt, err := validateImmutableSearchGeneration(
		ctx, indexDir, repository, ref.GenerationDigest,
	)
	if err != nil || !sameSearchRevisions(receipt.Revisions, ref.Revisions) {
		if err != nil {
			return err
		}
		return errors.New("search generation reference changed before installation")
	}
	directory, err := searchGenerationDirectory(indexDir, repository, ref.GenerationDigest)
	if err != nil {
		return err
	}
	source, err := repositoryindex.ReadSourceManifest(directory, repository)
	if err != nil {
		return err
	}
	whole, err := ReadWholeManifest(directory, repository, ref.Revisions)
	if err != nil {
		return err
	}
	if err := removeRepositoryArtifacts(ctx, indexDir, repository, true); err != nil {
		return err
	}
	for _, name := range searchGenerationFiles(source, whole) {
		if err := os.Link(filepath.Join(directory, name), filepath.Join(indexDir, name)); err != nil {
			return err
		}
		if err := syncFile(filepath.Join(indexDir, name)); err != nil {
			return err
		}
	}
	return syncDirectory(indexDir)
}

func prepareSearchGenerationTransition(
	ctx context.Context, indexDir, repository string, candidate SearchGenerationRef,
) (searchGenerationMarker, error) {
	var previous *SearchGenerationRoot
	root, err := ReadSearchGenerationRoot(indexDir, repository)
	if err == nil {
		if _, err := validateImmutableSearchGeneration(ctx, indexDir, repository, root.Current.GenerationDigest); err != nil {
			return searchGenerationMarker{}, err
		}
		previous = &root
	} else if errors.Is(err, os.ErrNotExist) {
		legacy, adoptErr := adoptLegacySearchGeneration(ctx, indexDir, repository)
		if adoptErr != nil {
			return searchGenerationMarker{}, adoptErr
		}
		if legacy != nil {
			previous = &SearchGenerationRoot{
				Schema: SearchGenerationRootSchema, Repository: repository, Current: *legacy,
			}
		}
	} else {
		return searchGenerationMarker{}, err
	}
	marker := searchGenerationMarker{
		Schema: SearchGenerationMarkerSchema, Repository: repository,
		Candidate: candidate, Previous: previous,
	}
	if _, err := prospectiveSearchGenerationRoot(marker); err != nil {
		return searchGenerationMarker{}, err
	}
	if err := writeSearchGenerationMarker(indexDir, marker); err != nil {
		return searchGenerationMarker{}, err
	}
	return marker, nil
}

// ReactivatePriorSearchGeneration selects the one retained prior generation
// when its immutable revision identity exactly matches the requested source.
// Whole-search shard bytes include builder timestamps, so rebuilding the same
// Git authority would create a different physical generation and defeat exact
// A-to-B-to-A recovery even though the retained bytes are already complete.
func ReactivatePriorSearchGeneration(
	ctx context.Context,
	indexDir, repository string,
	revisions []store.IndexedRevision,
) (bool, error) {
	root, err := ReadSearchGenerationRoot(indexDir, repository)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if root.Prior == nil || !sameSearchRevisions(root.Prior.Revisions, revisions) {
		return false, nil
	}
	if _, err := validateImmutableSearchGeneration(
		ctx, indexDir, repository, root.Prior.GenerationDigest,
	); err != nil {
		return false, err
	}
	marker := searchGenerationMarker{
		Schema: SearchGenerationMarkerSchema, Repository: repository,
		Candidate: *root.Prior, Previous: &root,
	}
	if _, err := prospectiveSearchGenerationRoot(marker); err != nil {
		return false, err
	}
	if err := writeSearchGenerationMarker(indexDir, marker); err != nil {
		return false, err
	}
	if err := completeSearchGenerationTransition(ctx, indexDir, marker); err != nil {
		return false, err
	}
	return true, nil
}

func prospectiveSearchGenerationRoot(marker searchGenerationMarker) (SearchGenerationRoot, error) {
	root := SearchGenerationRoot{
		Schema: SearchGenerationRootSchema, Repository: marker.Repository,
		Current: marker.Candidate,
	}
	if marker.Previous != nil && marker.Previous.Current.GenerationDigest != marker.Candidate.GenerationDigest {
		prior := marker.Previous.Current
		root.Prior = &prior
	}
	if err := validateSearchGenerationRoot(root, marker.Repository); err != nil {
		return SearchGenerationRoot{}, err
	}
	return root, nil
}

func completeSearchGenerationTransition(
	ctx context.Context, indexDir string, marker searchGenerationMarker,
) error {
	if err := startPublication(indexDir, marker.Repository); err != nil {
		_ = removeSearchTransition(indexDir, marker.Repository)
		return err
	}
	if err := installStableSearchGeneration(ctx, indexDir, marker.Repository, marker.Candidate); err != nil {
		return err
	}
	root, err := prospectiveSearchGenerationRoot(marker)
	if err != nil {
		return err
	}
	return writeSearchGenerationRoot(indexDir, root)
}

func RollbackSearchPublication(ctx context.Context, indexDir, repository string) error {
	marker, err := readSearchGenerationMarker(indexDir, repository)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("search generation transition is absent")
	}
	if err != nil {
		return err
	}
	if marker.Previous == nil {
		if err := removeRepositoryArtifacts(ctx, indexDir, repository, false); err != nil {
			return err
		}
		return FinishPublication(indexDir, repository)
	}
	if err := installStableSearchGeneration(ctx, indexDir, repository, marker.Previous.Current); err != nil {
		return err
	}
	if err := writeSearchGenerationRoot(indexDir, *marker.Previous); err != nil {
		return err
	}
	if err := removeSearchTransition(indexDir, repository); err != nil {
		return err
	}
	return FinishPublication(indexDir, repository)
}

// RecoverSearchPublication selects only the generation matching the durable
// repository row. It is the startup repair for every publication crash point.
func RecoverSearchPublication(
	ctx context.Context, indexDir, repository string, revisions []store.IndexedRevision,
) (bool, error) {
	marker, err := readSearchGenerationMarker(indexDir, repository)
	if errors.Is(err, os.ErrNotExist) {
		root, rootErr := ReadSearchGenerationRoot(indexDir, repository)
		if rootErr == nil {
			if !sameSearchRevisions(root.Current.Revisions, revisions) {
				return false, ErrSearchPublicationRevisionMismatch
			}
			return false, removeSearchGenerationArchiveReceipt(indexDir, repository)
		}
		if !errors.Is(rootErr, os.ErrNotExist) {
			return false, rootErr
		}
		// Backup deliberately retains only the selected complete flat search
		// publication. Reconstitute its derived lifecycle root when the durable
		// repository row selects that exact revision set. An unindexed row owns
		// no authority and therefore cannot adopt leftover flat bytes.
		if len(revisions) == 0 {
			return false, nil
		}
		legacy, adoptErr := adoptLegacySearchGeneration(ctx, indexDir, repository)
		if adoptErr != nil {
			return false, adoptErr
		}
		if legacy == nil {
			return false, nil
		}
		if !sameSearchRevisions(legacy.Revisions, revisions) {
			return false, ErrSearchPublicationRevisionMismatch
		}
		root = SearchGenerationRoot{
			Schema: SearchGenerationRootSchema, Repository: repository, Current: *legacy,
		}
		if err := writeSearchGenerationRoot(indexDir, root); err != nil {
			return false, err
		}
		if err := removeSearchGenerationArchiveReceipt(indexDir, repository); err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var selected SearchGenerationRef
	var root SearchGenerationRoot
	switch {
	case sameSearchRevisions(marker.Candidate.Revisions, revisions):
		selected = marker.Candidate
		var rootErr error
		root, rootErr = prospectiveSearchGenerationRoot(marker)
		if rootErr != nil {
			return false, rootErr
		}
	case marker.Previous != nil && sameSearchRevisions(marker.Previous.Current.Revisions, revisions):
		selected = marker.Previous.Current
		root = *marker.Previous
	default:
		if len(revisions) == 0 && marker.Previous == nil {
			return true, RollbackSearchPublication(ctx, indexDir, repository)
		}
		return false, ErrSearchPublicationRevisionMismatch
	}
	if err := installStableSearchGeneration(ctx, indexDir, repository, selected); err != nil {
		return false, err
	}
	if err := writeSearchGenerationRoot(indexDir, root); err != nil {
		return false, err
	}
	if err := removeSearchTransition(indexDir, repository); err != nil {
		return false, err
	}
	if err := FinishPublication(indexDir, repository); err != nil {
		return false, err
	}
	return true, nil
}
