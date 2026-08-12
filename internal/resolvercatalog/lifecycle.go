package resolvercatalog

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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
)

const stageDirectoryPrefix = ".phebs-resolver-catalog-stage-"

type Stage struct {
	root         string
	path         string
	identity     Identity
	members      []MemberReceipt
	memberKeys   map[string]struct{}
	contentBytes int64
	records      int
	sealed       bool
}

type Prepared struct {
	root           string
	stage          string
	manifest       Manifest
	installStarted bool
}

// Publication retains descriptor identities from one cold validation. Current
// is therefore a metadata-only warm no-op check: it never opens or hashes
// member content.
type Publication struct {
	root         string
	manifest     Manifest
	fingerprints map[string]fileFingerprint
}

// RecordVisitor observes one canonical record while OpenWithVisitor performs
// the descriptor-stable cold validation pass. The raw record is valid only
// for the duration of the call; visitors that retain it must copy or decode
// it. A successful visit is provisional until OpenWithVisitor itself returns
// successfully because later receipt and path-fingerprint checks may still
// reject the publication.
type RecordVisitor func(
	member MemberReceipt,
	recordIndex int,
	record json.RawMessage,
) error

// testAfterStableOpen is an adversarial descriptor-swap seam. Production
// leaves it nil; package tests use it synchronously.
var testAfterStableOpen func(string)

func (publication *Publication) Manifest() Manifest {
	return cloneManifest(publication.manifest)
}
func (publication *Publication) State() State { return publication.manifest.State() }

func cloneManifest(manifest Manifest) Manifest {
	manifest.Identity.Declarations = append(
		[]DeclarationPublication{}, manifest.Identity.Declarations...,
	)
	manifest.Identity.ResolverPacks = append(
		[]ResolverPack{}, manifest.Identity.ResolverPacks...,
	)
	manifest.Members = append([]MemberReceipt{}, manifest.Members...)
	for index := range manifest.Members {
		manifest.Members[index].Metadata = append(
			json.RawMessage{}, manifest.Members[index].Metadata...,
		)
	}
	return manifest
}

// NewStage creates an fsync'd, package-owned stage on the catalog filesystem.
func NewStage(root string, identity Identity) (*Stage, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(absoluteRoot)
	if err := ensureRealDirectory(root); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(root, stageDirectoryPrefix)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(stage, 0o700); err != nil {
		_ = os.Remove(stage)
		return nil, err
	}
	if err := syncDirectory(root); err != nil {
		_ = os.Remove(stage)
		return nil, err
	}
	return &Stage{
		root: root, path: stage, identity: identity,
		memberKeys: make(map[string]struct{}),
	}, nil
}

// Discard removes only this Stage's package-created, flat unpublished
// directory. It is idempotent on the same Stage and never inventories or
// cleans the catalog root. Once Seal succeeds, the returned Prepared owns the
// directory and must be discarded instead.
func (stage *Stage) Discard() error {
	if stage == nil || stage.path == "" {
		return nil
	}
	if stage.sealed {
		return errors.New(
			"resolver catalog stage is sealed; discard the prepared publication",
		)
	}
	if err := discardOwnedStage(stage.root, stage.path); err != nil {
		return err
	}
	stage.path = ""
	stage.sealed = true
	return nil
}

// AddMember streams canonical JSON records to one member. emit may retain no
// lifecycle-owned resource after returning; at most one record plus small
// buffers is resident and only one member descriptor is open.
func (stage *Stage) AddMember(
	ctx context.Context,
	name string,
	metadata json.RawMessage,
	emit func(func(json.RawMessage) error) error,
) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if stage.sealed {
		return errors.New("resolver catalog stage is sealed")
	}
	if !validMemberName(name) {
		return errors.New("invalid resolver catalog member name")
	}
	if len(stage.members) >= MaxMembers {
		return ErrLimit
	}
	key := portableMemberNameKey(name)
	if _, duplicate := stage.memberKeys[key]; duplicate {
		return errors.New("resolver catalog member names collide on a portable filesystem")
	}
	if len(stage.members) > 0 && stage.members[len(stage.members)-1].Name >= name {
		return errors.New("resolver catalog members must be added in unique name order")
	}
	canonicalMetadata, err := canonicalRaw(metadata, MaxMetadataBytes)
	if err != nil {
		return fmt.Errorf("canonicalize member metadata: %w", err)
	}
	metadataDigest, err := digestCanonical(json.RawMessage(canonicalMetadata))
	if err != nil {
		return err
	}
	fileName := stageMemberName(len(stage.members))
	target := filepath.Join(stage.path, fileName)
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := file.Close(); resultErr == nil && closeErr != nil {
				resultErr = closeErr
			}
		}
		if resultErr != nil {
			_ = os.Remove(target)
		}
	}()
	hash := sha256.New()
	writer := bufio.NewWriterSize(io.MultiWriter(file, hash), 64<<10)
	var count int
	var contentBytes int64
	writeRecord := func(raw json.RawMessage) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count >= MaxRecordsPerMember || stage.records+count >= MaxRecords {
			return ErrLimit
		}
		canonical, err := canonicalRaw(raw, MaxRecordBytes)
		if err != nil {
			return fmt.Errorf("canonicalize record %d: %w", count, err)
		}
		if err := validateRecordEnvelope(canonical); err != nil {
			return fmt.Errorf("validate record %d: %w", count, err)
		}
		nextBytes := contentBytes + int64(len(canonical)+1)
		if nextBytes > MaxMemberContentBytes ||
			stage.contentBytes+nextBytes > MaxCatalogContentBytes ||
			stage.contentBytes+nextBytes > MaxStagingDiskBytes {
			return ErrLimit
		}
		if _, err := writer.Write(canonical); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
		contentBytes = nextBytes
		count++
		return nil
	}
	if emit != nil {
		if err := emit(writeRecord); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	stage.members = append(stage.members, MemberReceipt{
		Name: name, Metadata: json.RawMessage(canonicalMetadata),
		RecordCount: count, ContentBytes: contentBytes,
		ContentDigest:  "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		MetadataDigest: metadataDigest,
	})
	stage.memberKeys[key] = struct{}{}
	stage.records += count
	stage.contentBytes += contentBytes
	return nil
}

func stageMemberName(index int) string {
	return fmt.Sprintf("member-%03d.ndjson", index)
}

// Seal writes and syncs the canonical manifest in the stage. The returned
// Prepared value can be installed exactly once.
func (stage *Stage) Seal(ctx context.Context) (*Prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stage.sealed {
		return nil, errors.New("resolver catalog stage is sealed")
	}
	manifest := Manifest{
		Schema: ManifestSchema, Identity: stage.identity,
		Members: append([]MemberReceipt{}, stage.members...),
	}
	unsigned := manifest
	unsigned.Digest = ""
	digest, err := digestCanonical(unsigned)
	if err != nil {
		return nil, err
	}
	manifest.Digest = digest
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > maxManifestBytes {
		return nil, ErrLimit
	}
	manifestPath := filepath.Join(stage.path, "manifest.json")
	if err := writeExclusiveSynced(manifestPath, raw); err != nil {
		return nil, err
	}
	if err := syncDirectory(stage.path); err != nil {
		return nil, err
	}
	stage.sealed = true
	return &Prepared{
		root: stage.root, stage: stage.path, manifest: manifest,
	}, nil
}

// Discard removes only this Prepared publication's package-created, flat
// stage before Install begins. It is idempotent on the same Prepared and
// deliberately refuses after an install attempt: at that point the marker and
// any already-renamed live artifacts belong to marked-publication recovery.
func (prepared *Prepared) Discard() error {
	if prepared == nil || prepared.stage == "" {
		return nil
	}
	if prepared.installStarted {
		return errors.New(
			"resolver catalog installation has started; recover the marked publication",
		)
	}
	if err := discardOwnedStage(prepared.root, prepared.stage); err != nil {
		return err
	}
	prepared.stage = ""
	return nil
}

// Install makes all members durable, then atomically renames the stable
// manifest last while the publication marker remains present. The caller must
// commit State to the store before calling ClearPublishing.
func (prepared *Prepared) Install(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if err := validatePrepared(prepared); err != nil {
		return State{}, err
	}
	if prepared.installStarted {
		return State{}, errors.New("resolver catalog installation already started")
	}
	prepared.installStarted = true
	repository := prepared.manifest.Identity.Repository
	if err := installMarker(prepared.root, repository); err != nil {
		return State{}, err
	}
	for index, member := range prepared.manifest.Members {
		if err := ctx.Err(); err != nil {
			return State{}, err
		}
		source := filepath.Join(prepared.stage, stageMemberName(index))
		target := filepath.Join(prepared.root, memberArtifactName(
			prepared.manifest.Identity, member.Name,
		))
		if err := os.Rename(source, target); err != nil {
			return State{}, fmt.Errorf("install member %q: %w", member.Name, err)
		}
	}
	if err := syncDirectory(prepared.root); err != nil {
		return State{}, err
	}
	manifestTarget := filepath.Join(
		prepared.root, resolvercatalogid.ManifestName(repository),
	)
	if err := os.Rename(
		filepath.Join(prepared.stage, "manifest.json"), manifestTarget,
	); err != nil {
		return State{}, fmt.Errorf("install manifest: %w", err)
	}
	if err := syncDirectory(prepared.root); err != nil {
		return State{}, err
	}
	if err := os.Remove(prepared.stage); err != nil {
		return State{}, fmt.Errorf("remove empty catalog stage: %w", err)
	}
	if err := syncDirectory(prepared.root); err != nil {
		return State{}, err
	}
	prepared.stage = ""
	return prepared.manifest.State(), nil
}

// Publish is the ordinary lifecycle entrypoint. A failed store commit leaves
// the canonical marker installed for Reconcile; the marker is removed only
// after commit returns nil.
func (prepared *Prepared) Publish(
	ctx context.Context,
	commit func(context.Context, State) error,
) (State, error) {
	if commit == nil {
		return State{}, errors.New("resolver catalog store commit is required")
	}
	state, err := prepared.Install(ctx)
	if err != nil {
		return State{}, err
	}
	if err := commit(ctx, state); err != nil {
		return state, fmt.Errorf("commit resolver catalog pointer: %w", err)
	}
	if err := ClearPublishing(prepared.root, state.Repository); err != nil {
		return state, fmt.Errorf("clear resolver catalog marker: %w", err)
	}
	if err := CleanupRetiredContext(ctx, prepared.root, prepared.manifest); err != nil {
		return state, fmt.Errorf("cleanup retired resolver catalog artifacts: %w", err)
	}
	return state, nil
}

// CleanupRetired removes only generation-member names owned by repository and
// not referenced by the exact current manifest. The stable manifest and all
// foreign repository namespaces are untouched.
func CleanupRetired(root string, current Manifest) error {
	return CleanupRetiredContext(context.Background(), root, current)
}

// CleanupRetiredContext is CleanupRetired with caller-owned cancellation. It
// is used by worker publication so the bounded directory inventory and retired
// artifact removal remain inside that worker's post-lock deadline.
func CleanupRetiredContext(ctx context.Context, root string, current Manifest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateManifest(current); err != nil {
		return err
	}
	cleanup := newReconcileCleanup()
	cleanup.keep(current)
	return cleanup.apply(ctx, root)
}

func validatePrepared(prepared *Prepared) error {
	if prepared == nil || prepared.root == "" || prepared.stage == "" {
		return errors.New("invalid resolver catalog prepared publication")
	}
	if filepath.Dir(prepared.stage) != prepared.root ||
		!strings.HasPrefix(filepath.Base(prepared.stage), stageDirectoryPrefix) {
		return errors.New("resolver catalog stage is outside its owned root")
	}
	if err := validateManifest(prepared.manifest); err != nil {
		return err
	}
	return nil
}

// ClearPublishing removes the marker only after the matching store pointer is
// durable. A missing marker is idempotent for recovery.
func ClearPublishing(root, repository string) error {
	rootHandle, rootDirectory, err := openStableDirectoryRoot(root)
	if err != nil {
		return err
	}
	defer func() {
		_ = rootDirectory.Close()
		_ = rootHandle.Close()
	}()
	if err := rootHandle.Remove(
		resolvercatalogid.PublishingName(repository),
	); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return rootDirectory.Sync()
}

// Publishing reports whether repository has a publication marker while
// preserving operational filesystem failures. A missing marker is ordinary
// state; other Lstat failures must not be collapsed into "not publishing"
// because recovery could then clear still-authoritative derived bytes.
func Publishing(root, repository string) (bool, error) {
	marker := filepath.Join(root, resolvercatalogid.PublishingName(repository))
	if testCatalogPathError != nil {
		if err := testCatalogPathError(marker); err != nil {
			return false, catalogIOError(err)
		}
	}
	_, err := os.Lstat(marker)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, catalogIOError(err)
}

func IsPublishing(root, repository string) bool {
	publishing, _ := Publishing(root, repository)
	return publishing
}

// CleanupStages removes only package-owned prior-process stages and temporary
// marker files. Symlinks are unlinked, never followed.
func CleanupStages(ctx context.Context, root string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := ensureRealDirectory(root); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	entries, err := readBoundedDirectory(root)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		name := entry.Name()
		target := filepath.Join(root, name)
		if strings.HasPrefix(name, stageDirectoryPrefix) {
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				if err := removeOwnedStage(target); err != nil {
					return removed, err
				}
			} else if err := os.Remove(target); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
			removed++
			continue
		}
		if strings.HasPrefix(name, ".phebs-resolver-catalog-marker-") {
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			if err := os.Remove(target); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
			removed++
		}
	}
	if removed > 0 {
		if err := syncDirectory(root); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// removeOwnedStage removes only the flat shape emitted by Stage. It refuses a
// nested directory or an oversized inventory instead of recursively walking
// attacker- or corruption-controlled residue during startup.
func removeOwnedStage(target string) error {
	stageRoot, stageDirectory, err := openStableDirectoryRoot(target)
	if err != nil {
		return err
	}
	defer func() {
		_ = stageDirectory.Close()
		_ = stageRoot.Close()
	}()
	entries, err := readOpenDirectoryUpTo(stageDirectory, MaxMembers+1)
	if err != nil {
		return err
	}
	// Validate the complete bounded inventory before unlinking anything. A
	// damaged stage containing a real directory is therefore refused without
	// partially deleting otherwise valid flat entries that sort before it.
	for _, entry := range entries {
		info, err := stageRoot.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf(
				"resolver catalog stage path %q is a directory", entry.Name(),
			)
		}
	}
	for _, entry := range entries {
		if err := stageRoot.Remove(entry.Name()); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	openedInfo, err := stageDirectory.Stat()
	if err != nil {
		return err
	}
	if err := stageDirectory.Sync(); err != nil {
		return err
	}
	if err := stageDirectory.Close(); err != nil {
		return err
	}
	if err := stageRoot.Close(); err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(openedInfo, info) {
		return errors.New("resolver catalog stage changed during cleanup")
	}
	return os.Remove(target)
}

func discardOwnedStage(root, target string) error {
	if root == "" || target == "" || filepath.Dir(target) != root ||
		!strings.HasPrefix(filepath.Base(target), stageDirectoryPrefix) {
		return errors.New("resolver catalog discard target is outside its owned root")
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("resolver catalog discard target is not its owned directory")
	}
	if err := removeOwnedStage(target); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return syncDirectory(root)
}

func installMarker(root, repository string) error {
	// A pre-existing canonical marker may belong to a live publisher. Never
	// replace it here: startup or a worker holding the repository lock must
	// reconcile the marked generation before it may stage another one.
	if IsPublishing(root, repository) {
		return ErrPublishing
	}
	temp, err := os.CreateTemp(root, ".phebs-resolver-catalog-marker-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	raw := []byte(repository + "\n")
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	marker := filepath.Join(root, resolvercatalogid.PublishingName(repository))
	if err := os.Link(tempName, marker); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrPublishing
		}
		return err
	}
	if err := os.Remove(tempName); err != nil {
		return err
	}
	return syncDirectory(root)
}

// Open performs descriptor-stable cold validation of the exact store state.
// The manifest is the sole visibility authority: same-namespace residue is
// not admitted and is swept by CleanupRetired only after this declared set is
// valid, preserving the store-commit-before-cleanup crash boundary.
func Open(ctx context.Context, root string, expected State) (*Publication, error) {
	return open(ctx, root, expected, false, nil)
}

// OpenWithVisitor performs the same descriptor-stable cold validation as
// Open and visits each canonical record during that one content pass. It is
// the bounded projection hook for typed resolver consumers: member content is
// never reopened merely to decode already-validated records.
func OpenWithVisitor(
	ctx context.Context,
	root string,
	expected State,
	visit RecordVisitor,
) (*Publication, error) {
	return open(ctx, root, expected, false, visit)
}

// OpenPublishing is restricted to recovery of a manifest made durable before
// its store commit; it requires and rechecks the canonical marker.
func OpenPublishing(ctx context.Context, root string, expected State) (*Publication, error) {
	return open(ctx, root, expected, true, nil)
}

func open(
	ctx context.Context,
	root string,
	expected State,
	allowPublishing bool,
	visit RecordVisitor,
) (*Publication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateState(expected); err != nil {
		return nil, err
	}
	if allowPublishing {
		if err := validateMarker(root, expected.Repository); err != nil {
			return nil, err
		}
	} else {
		publishing, err := Publishing(root, expected.Repository)
		if err != nil {
			return nil, err
		}
		if publishing {
			return nil, ErrPublishing
		}
	}
	manifestPath := filepath.Join(root, expected.Manifest)
	raw, fingerprint, err := readStableRegular(manifestPath, maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: manifest: %w", ErrInvalidManifest, err)
	}
	var manifest Manifest
	if err := decodeCanonical(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%w: manifest encoding: %v", ErrInvalidManifest, err)
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	if !statesEqual(manifest.State(), expected) {
		return nil, fmt.Errorf("%w: manifest differs from expected state", ErrInvalidManifest)
	}
	fingerprints := map[string]fileFingerprint{expected.Manifest: fingerprint}
	var totalRecords int
	var totalBytes int64
	for _, member := range manifest.Members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := memberArtifactName(manifest.Identity, member.Name)
		count, digest, fp, err := validateMember(
			ctx, filepath.Join(root, name), member, visit,
		)
		if err != nil {
			return nil, err
		}
		if count != member.RecordCount || digest != member.ContentDigest {
			return nil, fmt.Errorf("%w: member %q receipt mismatch", ErrInvalidManifest, member.Name)
		}
		totalRecords += count
		totalBytes += member.ContentBytes
		if totalRecords > MaxRecords || totalBytes > MaxCatalogContentBytes {
			return nil, fmt.Errorf("%w: aggregate bounds: %w", ErrInvalidManifest, ErrLimit)
		}
		fingerprints[name] = fp
	}
	if allowPublishing {
		if err := validateMarker(root, expected.Repository); err != nil {
			return nil, err
		}
	} else {
		publishing, err := Publishing(root, expected.Repository)
		if err != nil {
			return nil, err
		}
		if publishing {
			return nil, ErrPublishing
		}
	}
	return &Publication{
		root: root, manifest: manifest, fingerprints: fingerprints,
	}, nil
}

func validateMember(
	ctx context.Context,
	filePath string,
	receipt MemberReceipt,
	visit RecordVisitor,
) (int, string, fileFingerprint, error) {
	file, fingerprint, err := openStableRegular(filePath, receipt.ContentBytes)
	if err != nil {
		return 0, "", fileFingerprint{}, fmt.Errorf(
			"%w: member %q: %w", ErrInvalidManifest, receipt.Name, err,
		)
	}
	defer func() { _ = file.Close() }()
	if testAfterStableOpen != nil {
		testAfterStableOpen(filePath)
	}
	hash := sha256.New()
	reader := bufio.NewReaderSize(io.TeeReader(file, hash), 64<<10)
	count := 0
	var consumed int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, "", fileFingerprint{}, err
		}
		line, err := reader.ReadBytes('\n')
		consumed += int64(len(line))
		if len(line) > 0 {
			if line[len(line)-1] != '\n' || len(line) > MaxRecordBytes+1 {
				return 0, "", fileFingerprint{}, fmt.Errorf(
					"%w: member %q has a non-canonical record", ErrInvalidManifest, receipt.Name,
				)
			}
			canonical, canonicalErr := canonicalRaw(line[:len(line)-1], MaxRecordBytes)
			if canonicalErr != nil || !bytes.Equal(canonical, line[:len(line)-1]) {
				return 0, "", fileFingerprint{}, fmt.Errorf(
					"%w: member %q has a non-canonical record", ErrInvalidManifest, receipt.Name,
				)
			}
			if err := validateRecordEnvelope(canonical); err != nil {
				return 0, "", fileFingerprint{}, fmt.Errorf(
					"%w: member %q record schema: %v",
					ErrInvalidManifest, receipt.Name, err,
				)
			}
			if visit != nil {
				if err := visit(receipt, count, json.RawMessage(canonical)); err != nil {
					return 0, "", fileFingerprint{}, fmt.Errorf(
						"visit member %q record %d: %w", receipt.Name, count, err,
					)
				}
			}
			count++
			if count > MaxRecordsPerMember {
				return 0, "", fileFingerprint{}, ErrLimit
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, "", fileFingerprint{}, fmt.Errorf(
				"%w: member %q read: %w",
				ErrInvalidManifest, receipt.Name, catalogIOError(err),
			)
		}
	}
	if consumed != receipt.ContentBytes {
		return 0, "", fileFingerprint{}, fmt.Errorf(
			"%w: member %q length mismatch", ErrInvalidManifest, receipt.Name,
		)
	}
	if err := verifyFingerprint(file, fingerprint); err != nil {
		return 0, "", fileFingerprint{}, fmt.Errorf(
			"%w: member %q changed while reading: %w",
			ErrInvalidManifest, receipt.Name, err,
		)
	}
	if err := verifyPathFingerprint(filePath, fingerprint); err != nil {
		return 0, "", fileFingerprint{}, fmt.Errorf(
			"%w: member %q path changed while reading: %w",
			ErrInvalidManifest, receipt.Name, err,
		)
	}
	return count, "sha256:" + hex.EncodeToString(hash.Sum(nil)), fingerprint, nil
}

// Current performs only lstat/control identity checks over the paths captured
// by Open. It does not open or hash any member.
func (publication *Publication) Current() bool {
	if publication == nil {
		return false
	}
	publishing, err := Publishing(
		publication.root, publication.manifest.Identity.Repository,
	)
	if err != nil || publishing {
		return false
	}
	for name, expected := range publication.fingerprints {
		info, err := os.Lstat(filepath.Join(publication.root, name))
		if err != nil || !sameFingerprint(fingerprintFromInfo(info), expected) {
			return false
		}
	}
	return true
}

func validateState(state State) error {
	if state.Schema != StateSchema {
		return errors.New("unsupported resolver catalog state schema")
	}
	if err := revalidateStateDigests(state); err != nil {
		return err
	}
	if state.Manifest != resolvercatalogid.ManifestName(state.Repository) {
		return errors.New("resolver catalog state manifest name is not canonical")
	}
	return nil
}

func revalidateStateDigests(state State) error {
	if reponame.Validate(state.Repository) != nil || !validObjectID(state.Commit) ||
		(state.UnitDigest != "" && !validDigest(state.UnitDigest)) ||
		!validDigest(state.DeclarationSetDigest) ||
		!validDigest(state.CandidateManifestDigest) ||
		state.SourceLanePolicy != SourceLanePolicy ||
		!validDigest(state.ResolverPackSetDigest) ||
		!validDigest(state.CatalogPolicyDigest) ||
		!validDigest(state.GenerationDigest) ||
		!validDigest(state.ManifestDigest) {
		return errors.New("invalid resolver catalog state identity")
	}
	if state.Declarations == nil ||
		len(state.Declarations) > MaxDeclarationPublications {
		return errors.New("invalid resolver catalog state declaration set")
	}
	for index, declaration := range state.Declarations {
		if !validToken(declaration.Domain, 128) ||
			!validToken(declaration.RunID, 256) ||
			!validDeclarationAuthority(declaration) ||
			index > 0 && state.Declarations[index-1].Domain >= declaration.Domain {
			return errors.New("invalid resolver catalog state declaration identity")
		}
	}
	declarationDigest, err := digestCanonical(state.Declarations)
	if err != nil || declarationDigest != state.DeclarationSetDigest {
		return errors.New("invalid resolver catalog state declaration digest")
	}
	if state.ResolverPacks == nil || len(state.ResolverPacks) > MaxResolverPacks {
		return errors.New("invalid resolver catalog state resolver-pack set")
	}
	for index, pack := range state.ResolverPacks {
		if !validToken(pack.Name, 128) || !validToken(pack.Version, 64) ||
			index > 0 && state.ResolverPacks[index-1].Name >= pack.Name {
			return errors.New("invalid resolver catalog state resolver-pack identity")
		}
	}
	packDigest, err := digestCanonical(state.ResolverPacks)
	if err != nil || packDigest != state.ResolverPackSetDigest {
		return errors.New("invalid resolver catalog state resolver-pack digest")
	}
	return nil
}

func memberArtifactName(identity Identity, memberName string) string {
	return resolvercatalogid.ArtifactPrefix(
		identity.Repository, identity.GenerationDigest,
	) + memberName
}

func validateMarker(root, repository string) error {
	raw, _, err := readStableRegular(
		filepath.Join(root, resolvercatalogid.PublishingName(repository)),
		len(repository)+1,
	)
	if errors.Is(err, ErrCatalogIO) {
		return fmt.Errorf("%w: marker: %w", ErrPublishing, err)
	}
	if err != nil || string(raw) != repository+"\n" {
		return ErrPublishing
	}
	return nil
}

func decodeCanonical(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	canonical, err := json.Marshal(destination)
	if err != nil {
		return err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		return errors.New("JSON is not canonical")
	}
	return nil
}

type fileFingerprint struct {
	dev, inode, size    uint64
	mode                os.FileMode
	mtimeSec, mtimeNSec int64
}

func fingerprintFromInfo(info os.FileInfo) fileFingerprint {
	result := fileFingerprint{
		size: uint64(info.Size()), mode: info.Mode(),
		mtimeSec: info.ModTime().Unix(), mtimeNSec: int64(info.ModTime().Nanosecond()),
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		result.dev = uint64(stat.Dev)
		result.inode = uint64(stat.Ino)
	}
	return result
}

func sameFingerprint(left, right fileFingerprint) bool { return left == right }

// testCatalogPathError is an adversarial operational-I/O seam. Tests set it
// synchronously; production leaves it nil.
var testCatalogPathError func(string) error

func catalogIOError(err error) error {
	if err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrCatalogIO) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrCatalogIO, err)
}

func readStableRegular(filePath string, max int) ([]byte, fileFingerprint, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, fileFingerprint{}, catalogIOError(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() > int64(max) {
		return nil, fileFingerprint{}, errors.New("not a bounded regular file")
	}
	file, fingerprint, err := openStableRegular(filePath, info.Size())
	if err != nil {
		return nil, fileFingerprint{}, err
	}
	defer func() { _ = file.Close() }()
	if testAfterStableOpen != nil {
		testAfterStableOpen(filePath)
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(max)+1))
	if err != nil {
		return nil, fileFingerprint{}, catalogIOError(err)
	}
	if int64(len(raw)) != info.Size() {
		return nil, fileFingerprint{}, errors.New("file length changed")
	}
	if err := verifyFingerprint(file, fingerprint); err != nil {
		return nil, fileFingerprint{}, err
	}
	if err := verifyPathFingerprint(filePath, fingerprint); err != nil {
		return nil, fileFingerprint{}, err
	}
	return raw, fingerprint, nil
}

func openStableRegular(
	filePath string,
	expectedSize int64,
) (*os.File, fileFingerprint, error) {
	if testCatalogPathError != nil {
		if err := testCatalogPathError(filePath); err != nil {
			return nil, fileFingerprint{}, catalogIOError(err)
		}
	}
	before, err := os.Lstat(filePath)
	if err != nil {
		return nil, fileFingerprint{}, catalogIOError(err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() != expectedSize {
		return nil, fileFingerprint{}, errors.New("not the expected regular file")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fileFingerprint{}, catalogIOError(err)
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fileFingerprint{}, catalogIOError(err)
	}
	fingerprint := fingerprintFromInfo(before)
	if !sameFingerprint(fingerprint, fingerprintFromInfo(after)) {
		_ = file.Close()
		return nil, fileFingerprint{}, errors.New("descriptor identity changed while opening")
	}
	return file, fingerprint, nil
}

func verifyFingerprint(file *os.File, expected fileFingerprint) error {
	info, err := file.Stat()
	if err != nil {
		return catalogIOError(err)
	}
	if !sameFingerprint(fingerprintFromInfo(info), expected) {
		return errors.New("descriptor identity changed")
	}
	return nil
}

func verifyPathFingerprint(filePath string, expected fileFingerprint) error {
	info, err := os.Lstat(filePath)
	if err != nil {
		return catalogIOError(err)
	}
	if !sameFingerprint(fingerprintFromInfo(info), expected) {
		return errors.New("path identity changed")
	}
	return nil
}

func writeExclusiveSynced(filePath string, raw []byte) (resultErr error) {
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return err
	}
	return file.Sync()
}

func ensureRealDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("resolver catalog root is not a real directory")
	}
	return nil
}

func readBoundedDirectory(directory string) ([]os.DirEntry, error) {
	return readDirectoryUpTo(directory, MaxDirectoryEntries)
}

func readDirectoryUpTo(directory string, limit int) ([]os.DirEntry, error) {
	file, err := os.Open(directory)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return readOpenDirectoryUpTo(file, limit)
}

func readOpenDirectoryUpTo(file *os.File, limit int) ([]os.DirEntry, error) {
	if file == nil || limit <= 0 {
		return nil, ErrLimit
	}
	entries := make([]os.DirEntry, 0, 256)
	for {
		batch, readErr := file.ReadDir(256)
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

func openStableDirectoryRoot(directory string) (*os.Root, *os.File, error) {
	before, err := os.Lstat(directory)
	if err != nil {
		return nil, nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("resolver catalog root is not a real directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, err
	}
	opened, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, nil, err
	}
	after, err := opened.Stat()
	if err != nil || !after.IsDir() || !os.SameFile(before, after) {
		_ = opened.Close()
		_ = root.Close()
		return nil, nil, errors.New("resolver catalog directory changed while opening")
	}
	return root, opened, nil
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}
