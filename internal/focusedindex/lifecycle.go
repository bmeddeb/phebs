package focusedindex

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	buildWorkspacePrefix   = ".phebs-build-"
	restoreWorkspacePrefix = ".phebs-restore-"
	lifecycleOwnerFile     = ".phebs-lifecycle-owner"
)

var lifecycleOwner = newLifecycleOwner()

// LifecycleCleanupReport describes private staging residue reclaimed from a
// prior phebs process. Names carry the current process token from creation, so
// runtime reconciliation cannot mistake an active same-process workspace for
// abandoned state.
type LifecycleCleanupReport struct {
	Workspaces       int
	TemporaryMarkers int
}

func newRestoreWorkspace(indexDir string) (string, error) {
	return newLifecycleWorkspace(indexDir, restoreWorkspacePrefix)
}

func newLifecycleWorkspace(indexDir, prefix string) (string, error) {
	if err := ensureRealDirectory(indexDir); err != nil {
		return "", err
	}
	workspace, err := os.MkdirTemp(indexDir, prefix+lifecycleOwner+"-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return "", err
	}
	ownerPath := filepath.Join(workspace, lifecycleOwnerFile)
	owner, err := os.OpenFile(ownerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(workspace)
		return "", err
	}
	if _, err := owner.WriteString(lifecycleOwner + "\n"); err != nil {
		_ = owner.Close()
		_ = os.RemoveAll(workspace)
		return "", err
	}
	if err := owner.Sync(); err != nil {
		_ = owner.Close()
		_ = os.RemoveAll(workspace)
		return "", err
	}
	if err := owner.Close(); err != nil {
		_ = os.RemoveAll(workspace)
		return "", err
	}
	if err := syncDirectory(workspace); err != nil {
		_ = os.RemoveAll(workspace)
		return "", err
	}
	return workspace, nil
}

// CleanupAbandonedLifecycle removes only staging paths whose names prove that
// they were created by a different phebs process. Callers hold the index
// mutation lock, but builds can run before taking that lock; the name token is
// therefore the primary active-work guard.
func CleanupAbandonedLifecycle(indexDir string) (LifecycleCleanupReport, error) {
	var report LifecycleCleanupReport
	entries, err := os.ReadDir(indexDir)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	changed := false
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(indexDir, name)
		if lifecycleWorkspaceKind(name) != "" {
			if lifecycleNameOwnedByCurrentProcess(name) &&
				entry.Type()&os.ModeSymlink == 0 && entry.IsDir() {
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return report, err
				}
			} else if err := os.RemoveAll(path); err != nil {
				return report, err
			}
			report.Workspaces++
			changed = true
			continue
		}
		if !isTemporaryPublicationMarker(name) ||
			temporaryMarkerOwnedByCurrentProcess(name) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return report, err
		}
		report.TemporaryMarkers++
		changed = true
	}
	if changed {
		if err := syncDirectory(indexDir); err != nil {
			return report, err
		}
	}
	return report, nil
}

func lifecycleWorkspaceKind(name string) string {
	for _, prefix := range []string{
		buildWorkspacePrefix,
		restoreWorkspacePrefix,
	} {
		if strings.HasPrefix(name, prefix) {
			return prefix
		}
	}
	return ""
}

func lifecycleNameOwnedByCurrentProcess(name string) bool {
	prefix := lifecycleWorkspaceKind(name)
	return prefix != "" && strings.HasPrefix(
		strings.TrimPrefix(name, prefix), lifecycleOwner+"-",
	)
}

func isTemporaryPublicationMarker(name string) bool {
	if !strings.HasPrefix(name, ".phebs-focus-") {
		return false
	}
	rest := strings.TrimPrefix(name, ".phebs-focus-")
	if len(rest) < 64+len(".publishing.") || !isLowerHex(rest[:64]) {
		return false
	}
	return strings.HasPrefix(rest[64:], ".publishing.")
}

func temporaryMarkerOwnedByCurrentProcess(name string) bool {
	marker := strings.Index(name, ".publishing.")
	if marker < 0 {
		return false
	}
	return strings.HasPrefix(name[marker+len(".publishing."):], lifecycleOwner+".")
}

func publicationMarkerOwnedByCurrentProcess(indexDir, repository string) bool {
	path := filepath.Join(indexDir, PublishingName(repository))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512 {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	return len(lines) == 2 && lines[0] == repository && lines[1] == lifecycleOwner
}

// PublicationMarkerOwnedByCurrentProcess reports whether the marker belongs
// to an active publication created by this phebs process.
func PublicationMarkerOwnedByCurrentProcess(indexDir, repository string) bool {
	return publicationMarkerOwnedByCurrentProcess(indexDir, repository)
}

func newLifecycleOwner() string {
	var token [16]byte
	if _, err := rand.Read(token[:]); err == nil {
		return hex.EncodeToString(token[:])
	}
	// Ownership tokens are process-local race guards, not secrets.
	return fmt.Sprintf("%x-%x", os.Getpid(), time.Now().UnixNano())
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return value != ""
}
