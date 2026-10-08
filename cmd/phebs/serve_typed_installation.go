package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
)

const typedInstallationSchema = "phebs-managed-scip-installation-v1"
const maxTypedInstalledRepositories = 8
const maxTypedInstallationBytes = 32 << 10
const maxTypedInstalledInventoryBytes = 32 << 20

type typedInstallationManifest struct {
	Schema       string                     `json:"schema"`
	Workspace    string                     `json:"workspace"`
	Socket       string                     `json:"socket"`
	Image        string                     `json:"image"`
	Repositories []typedInstalledRepository `json:"repositories"`
}

// A directory is a single trusted basename beneath the manifest directory.
// profile.json and inventory.json are private controls; bundle/ is untrusted
// input which the existing executor copies and verifies before execution.
type typedInstalledRepository struct {
	Source          typedindex.Source `json:"source"`
	ProfileEpoch    uint64            `json:"profile_epoch"`
	UniverseDigest  string            `json:"universe_digest"`
	ProfileDigest   string            `json:"profile_digest"`
	InventoryDigest string            `json:"inventory_digest"`
	Directory       string            `json:"directory"`
}

type typedInstalledProfile struct {
	entry     typedInstalledRepository
	profile   typedindex.Profile
	directory string
}
type typedInstallationRegistry struct {
	entries map[string]typedInstalledProfile
	order   []string
}

var typedInstallationName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func typedInstallationPath(p string) bool {
	return len(p) <= 4096 && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, ":\x00\r\n")
}
func typedInstallationOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

func loadTypedServeInstallation(ctx context.Context, selected *config.ManagedSCIP) (*typedServeInstallation, error) {
	if selected == nil {
		return nil, nil
	}
	if !typedindex.AdmittedNativeWorker() || !typedInstallationPath(selected.Manifest) {
		return nil, typedindex.Unsupported
	}
	base := filepath.Dir(selected.Manifest)
	raw, err := typedworkspace.ReadInstallationControl(ctx, base, filepath.Base(selected.Manifest), selected.SHA256, maxTypedInstallationBytes)
	if err != nil {
		return nil, err
	}
	var manifest typedInstallationManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil {
		return nil, typedindex.Invalid
	}
	canonical, err := json.Marshal(manifest)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, raw) != nil || !bytes.Equal(canonical, compact.Bytes()) || manifest.Schema != typedInstallationSchema || !typedInstallationPath(manifest.Workspace) || !typedInstallationPath(manifest.Socket) || typedInstallationOverlap(base, manifest.Workspace) || len(manifest.Repositories) == 0 || len(manifest.Repositories) > maxTypedInstalledRepositories {
		return nil, typedindex.Invalid
	}
	registry := &typedInstallationRegistry{entries: make(map[string]typedInstalledProfile)}
	directories := make(map[string]bool)
	var controlBytes, files int
	var bundleBytes int64
	for _, entry := range manifest.Repositories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Source.Validate() != nil || entry.ProfileEpoch == 0 || entry.ProfileEpoch > math.MaxInt64 || !typedInstallationName.MatchString(entry.Directory) || directories[entry.Directory] || registry.entries[entry.Source.Repository].profile.Digest() != "" {
			return nil, typedindex.Invalid
		}
		directory := filepath.Join(base, entry.Directory)
		profileRaw, err := typedworkspace.ReadInstallationControl(ctx, directory, "profile.json", entry.ProfileDigest, typedindex.MaxProfileBytes)
		if err != nil {
			return nil, err
		}
		profile, err := typedindex.DecodeProfile(ctx, profileRaw)
		if err != nil {
			return nil, err
		}
		if profile.Digest() != entry.ProfileDigest || profile.Definition().BundleDigest != entry.InventoryDigest || profile.Definition().ImageDigest != manifest.Image || profile.Definition().Config.GOARCH != runtime.GOARCH {
			return nil, typedindex.Stale
		}
		if _, err = typedindex.Admit(ctx, typedindex.Authority{Enabled: true, Administrator: true, Source: entry.Source, Profile: typedindex.Epoch{Number: entry.ProfileEpoch, Digest: entry.ProfileDigest}, UniverseDigest: entry.UniverseDigest}, profile, mustTypedInstallationRequest(entry, profile)); err != nil {
			return nil, err
		}
		inventoryRaw, err := typedworkspace.ReadInstallationControl(ctx, directory, "inventory.json", entry.InventoryDigest, int64(min(typedindex.MaxInventoryBytes, maxTypedInstalledInventoryBytes-controlBytes)))
		if err != nil {
			return nil, err
		}
		inventory, err := typedindex.DecodeInventory(ctx, inventoryRaw, entry.InventoryDigest)
		if err != nil {
			return nil, err
		}
		controlBytes += len(inventoryRaw)
		files += len(inventory.Files())
		bundleBytes += inventory.Bytes()
		if files > typedindex.MaxInventoryFiles || bundleBytes > typedindex.MaxBundleBytes {
			return nil, typedindex.Capacity
		}
		name, _ := typedindex.SelectionFile(profile.Provider())
		if row, ok := inventory.File(name); !ok || row.Executable || row.Bytes < 1 || row.Bytes > typedindex.MaxPlanBytes {
			return nil, typedindex.Unprepared
		}
		registry.entries[entry.Source.Repository] = typedInstalledProfile{entry, profile, directory}
		registry.order = append(registry.order, entry.Source.Repository)
		directories[entry.Directory] = true
	}
	return &typedServeInstallation{Workspace: manifest.Workspace, Socket: manifest.Socket, Image: manifest.Image, Bundle: registry.bundle, Registry: registry}, ctx.Err()
}

func mustTypedInstallationRequest(entry typedInstalledRepository, profile typedindex.Profile) []byte {
	raw, _ := json.Marshal(typedindex.NewManagedRequest(entry.Source, profile, entry.ProfileEpoch, entry.UniverseDigest, typedindex.Publish))
	return raw
}

func (r *typedInstallationRegistry) admits(snapshot store.TypedIndexOperator) bool {
	installed, ok := r.entries[snapshot.Source.Repository]
	return ok && installed.entry.Source == snapshot.Source && installed.entry.ProfileEpoch == snapshot.ProfileEpoch && installed.entry.UniverseDigest == snapshot.UniverseDigest && installed.profile.Digest() == snapshot.Profile.Digest()
}
func (r *typedInstallationRegistry) provider(id string) bool {
	for _, installed := range r.entries {
		if installed.profile.Provider() == id {
			return true
		}
	}
	return false
}
func (r *typedInstallationRegistry) bundle(ctx context.Context, admitted typedindex.Admission) (string, []byte, error) {
	request := admitted.Request()
	installed, ok := r.entries[request.Source.Repository]
	if !ok || request.Action != typedindex.Plan || admitted.Digest() == "" || request.Source != installed.entry.Source || request.ProfileEpoch != installed.entry.ProfileEpoch || request.ProfileDigest != installed.entry.ProfileDigest || request.UniverseDigest != installed.entry.UniverseDigest || request.BundleDigest != installed.entry.InventoryDigest {
		return "", nil, typedindex.Stale
	}
	raw, err := typedworkspace.ReadInstallationControl(ctx, installed.directory, "inventory.json", installed.entry.InventoryDigest, typedindex.MaxInventoryBytes)
	return filepath.Join(installed.directory, "bundle"), raw, err
}

type typedInstallationStore interface {
	GetTypedSource(context.Context, string) (typedindex.Source, error)
	GetTypedIndexIntent(context.Context, string) (store.TypedIndexIntent, error)
	InstallTypedProfileExpectedSource(context.Context, typedindex.Source, typedindex.Profile, string, int64) (store.TypedIndexIntent, error)
}

// Installation first validates every exact source and profile epoch, then writes
// only missing/successor profiles. Equal restarts preserve desires and epochs.
// An interrupted prefix is idempotently completed on the next restart.
func (r *typedInstallationRegistry) install(ctx context.Context, s typedInstallationStore, data string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var pending []typedInstalledProfile
	for _, name := range r.order {
		installed := r.entries[name]
		source, err := s.GetTypedSource(ctx, name)
		if err != nil {
			return err
		}
		if source != installed.entry.Source {
			return typedindex.Stale
		}
		intent, err := s.GetTypedIndexIntent(ctx, name)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if intent.ProfileEpoch == int64(installed.entry.ProfileEpoch) {
			if intent.ProfileDigest != installed.entry.ProfileDigest || intent.UniverseDigest != installed.entry.UniverseDigest || intent.RestoreRequired {
				return typedindex.Stale
			}
		} else if intent.ProfileEpoch == int64(installed.entry.ProfileEpoch)-1 {
			pending = append(pending, installed)
		} else {
			return typedindex.Stale
		}
		if err = installed.verifySource(ctx, data); err != nil {
			return err
		}
	}
	for _, installed := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := s.InstallTypedProfileExpectedSource(ctx, installed.entry.Source, installed.profile, installed.entry.UniverseDigest, int64(installed.entry.ProfileEpoch)-1); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// One joined cat-file child verifies the selected immutable source blobs for one
// installed repository; bounded ls-tree batches admit only regular file modes.
// There is no full-repository census or tool-content scan.
func (p typedInstalledProfile) verifySource(ctx context.Context, data string) (err error) {
	raw, err := typedworkspace.ReadInstallationControl(ctx, p.directory, "inventory.json", p.entry.InventoryDigest, typedindex.MaxInventoryBytes)
	if err != nil {
		return err
	}
	inventory, err := typedindex.DecodeInventory(ctx, raw, p.entry.InventoryDigest)
	if err != nil {
		return err
	}
	mirror, err := phebssync.SafeRepoDir(data, p.entry.Source.Repository)
	if err != nil {
		return err
	}
	if err = gitobj.RejectAlternates(mirror); err != nil {
		return err
	}
	reader, err := gitobj.NewBatchBlobReader(ctx, mirror)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	var selected []typedindex.BundleFile
	for _, file := range inventory.Files() {
		if strings.HasPrefix(file.Path, "source/") {
			selected = append(selected, file)
		}
	}
	if len(selected) == 0 {
		return typedindex.Unprepared
	}
	for offset := 0; offset < len(selected); offset += 64 {
		batch := selected[offset:min(offset+64, len(selected))]
		args := []string{"--literal-pathspecs", "ls-tree", "-z", p.entry.Source.Commit, "--"}
		want := make(map[string]typedindex.BundleFile, len(batch))
		for _, file := range batch {
			name := strings.TrimPrefix(file.Path, "source/")
			args = append(args, name)
			want[name] = file
		}
		tree, err := gitobj.Output(ctx, mirror, 64*(4096+128), args...)
		if err != nil {
			return err
		}
		for len(tree) > 0 {
			row, rest, ok := bytes.Cut(tree, []byte{0})
			if !ok {
				return typedindex.Stale
			}
			tree = rest
			metadata, name, ok := bytes.Cut(row, []byte{'\t'})
			fields := strings.Fields(string(metadata))
			file, present := want[string(name)]
			if !ok || !present || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") || file.Executable != (fields[0] == "100755") {
				return typedindex.Stale
			}
			delete(want, string(name))
			blob, err := reader.ReadBlob(ctx, fields[2], file.Bytes)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(blob)
			if "sha256:"+hex.EncodeToString(sum[:]) != file.Digest {
				return typedindex.Stale
			}
		}
		if len(want) != 0 {
			return typedindex.Stale
		}
	}
	return ctx.Err()
}
