package t421

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Copied only from the actual held backup root and joined command outputs.
// The native reader resolves this single leaf from authenticated FD6, never
// from a request path or an ambient filesystem root.
type epochArchiveInput struct {
	BackupRoot           string   `json:"backup_root"`
	Device               uint64   `json:"device"`
	Inode                uint64   `json:"inode"`
	FSID                 [2]int32 `json:"fsid"`
	BackupCommandSHA256  string   `json:"backup_command_sha256"`
	RestoreCommandSHA256 string   `json:"restore_command_sha256"`
}

func (input epochArchiveInput) valid() bool {
	const prefix = "t422-backup-"
	if len(input.BackupRoot) <= len(prefix) || len(input.BackupRoot) > 128 || !strings.HasPrefix(input.BackupRoot, prefix) ||
		input.Inode == 0 || input.FSID == ([2]int32{}) || !validDigest(input.BackupCommandSHA256) || input.RestoreCommandSHA256 != input.BackupCommandSHA256 {
		return false
	}
	for _, char := range []byte(input.BackupRoot[len(prefix):]) {
		switch {
		case char >= '0' && char <= '9', char >= 'A' && char <= 'Z', char >= 'a' && char <= 'z':
		default:
			return false
		}
	}
	return true
}

// Called with flow.mu held while the joined predecessor still borrows all
// epoch roots. The successor receives identities, never a caller-selected path.
func (run *ExecutionEpochOneRun) restoredArchiveBinding(ctx context.Context) (*epochArchiveInput, *AuthorityPhaseResult, error) {
	flow := run.flow
	if flow.workspace == nil {
		return nil, nil, nil // Preserve the earlier startup-only prerequisite.
	}
	prior, err := archivePriorFromPressure(run.inspection)
	if err != nil {
		return nil, nil, err
	}
	author, epochs := flow.epochs.author, flow.epochs
	author.mu.Lock()
	defer author.mu.Unlock()
	epochs.mu.Lock()
	defer epochs.mu.Unlock()
	if !epochs.active || author.borrowedBy != run || epochs.checkLocked(ctx, 4) != nil ||
		run.archiveWorkspacePoint != archiveWorkspaceRestoreJoined {
		return nil, nil, ErrExecutionEpochOne
	}
	held := epochs.roots[3]
	binding, err := dispatchadmission.DescribeProductionWorkspace(held.file, held.path)
	current, statErr := held.file.Stat()
	if err != nil || statErr != nil || !os.SameFile(held.info, current) ||
		filepath.Dir(held.path) != flow.workspace.path || binding.FSID != flow.workspace.volume {
		return nil, nil, ErrExecutionEpochOne
	}
	input := &epochArchiveInput{BackupRoot: filepath.Base(held.path), Device: binding.Device, Inode: binding.Inode, FSID: binding.FSID,
		BackupCommandSHA256: run.backupManifestSHA256, RestoreCommandSHA256: run.restoreManifestSHA256}
	if !input.valid() {
		return nil, nil, ErrExecutionEpochOne
	}
	return input, prior, nil
}

// Reconstruct the same canonical native F once at handoff. Its retained hash
// prevents a mutated returned slice from becoming a new authority baseline.
// Decoding that bounded body also detaches the successor's detailed roots.
func archivePriorFromPressure(reader *executionEpochInspection) (*AuthorityPhaseResult, error) {
	if reader == nil {
		return nil, ErrExecutionEpochOne
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.err != nil || reader.projection.Phase != "pressure_75" || reader.pressure.step != 9 || !reader.finalUsed ||
		reader.pressureBaseline == nil || reader.staleAuthority.Phase != "stale_lease" || len(reader.evidence.rows) == 0 {
		return nil, ErrExecutionEpochOne
	}
	row := reader.evidence.rows[len(reader.evidence.rows)-1]
	if row.ServerEpoch != 4 || row.Phase != "pressure_75" || !row.SelectorAccepted || row.Final == nil ||
		!reflect.DeepEqual(row.Final.Authority, reader.staleAuthority.AuthorityState) || !reflect.DeepEqual(row.Final.Projection, reader.projection) {
		return nil, ErrExecutionEpochOne
	}
	value := epochFinalResponse{Schema: "t421-final-authority-source-free-v1", ExtractionRoots: reader.staleAuthority.ExtractionRoots}
	raw, err := json.Marshal(reader.staleAuthority.AuthorityState)
	if err != nil || json.Unmarshal(raw, &value.Authority) != nil {
		return nil, ErrExecutionEpochOne
	}
	raw, err = json.Marshal(reader.projection)
	if err != nil || json.Unmarshal(raw, &value.Projection) != nil {
		return nil, ErrExecutionEpochOne
	}
	value.Projection.Schema = "t421-final-state-projection-source-free-v1"
	raw, err = json.MarshalIndent(value, "", "  ")
	raw = append(raw, '\n')
	var detached epochFinalResponse
	if err != nil || len(raw) > epochFinalResponseBytes || sha256.Sum256(raw) != *reader.pressureBaseline || json.Unmarshal(raw, &detached) != nil {
		return nil, ErrExecutionEpochOne
	}
	prior := reader.staleAuthority
	prior.Phase, prior.ExtractionRoots = "pressure_75", detached.ExtractionRoots
	return &prior, nil
}
