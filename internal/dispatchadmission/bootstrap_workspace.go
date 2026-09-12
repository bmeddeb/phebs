package dispatchadmission

import (
	"context"
	"os"
	"slices"
	"time"
)

// ProductionWorkspaceBinding is private mechanical descriptor identity, not
// a caller-issued custody/admission claim. The authenticated parent holds the
// actual directory through native join. FD6 is fixed and never caller-selected.
type ProductionWorkspaceBinding struct {
	Path   string
	Device uint64
	Inode  uint64
	FSID   [2]int32
}

type productionWorkspace struct {
	file    *os.File
	info    os.FileInfo
	binding ProductionWorkspaceBinding
}

func (record ProductionBootstrap) validateWorkspace() error {
	if record.Workspace == nil {
		if record.ArchiveDeadlineUnixNano != 0 {
			return ErrProductionBootstrap
		}
		return nil
	}
	w := record.Workspace
	if record.Program != ProgramPhebs || record.InputSHA256 == ([32]byte{}) ||
		record.Store == nil || !validProductionPath(w.Path) || w.Inode == 0 || w.FSID == ([2]int32{}) {
		return ErrProductionBootstrap
	}
	if record.SemanticMode == "" && (record.Producer.ID == 10 || record.Producer.ID == 11) &&
		record.Phase == 12 && record.Control.MaximumPhases == 1 && !record.Control.OwnerControl &&
		slices.Equal(record.Control.Phases, []uint32{12}) && record.ArchiveDeadlineUnixNano > 0 {
		return nil
	}
	if record.SemanticMode != ProductionSemanticV3 || record.ArchiveDeadlineUnixNano != 0 {
		return ErrProductionBootstrap
	}
	if record.Producer.ID == 5 && record.Phase == 8 && record.Control.MaximumPhases == 4 && slices.Equal(record.Control.Phases, []uint32{8, 9, 10, 11}) ||
		record.Producer.ID == 6 && record.Phase == 12 && record.Control.MaximumPhases == 3 && slices.Equal(record.Control.Phases, []uint32{12, 13, 14}) {
		return nil
	}
	return ErrProductionBootstrap
}

// The parent remains the original monotonic deadline owner. The authenticated
// wall-clock value transfers its deadline, not a duration to restart. A shorter
// inherited context still wins, and transport loss still cancels the child.
func (record ProductionBootstrap) archiveContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil || ctx.Err() != nil || record.validateWorkspace() != nil {
		return nil, nil, ErrProductionBootstrap
	}
	if record.ArchiveDeadlineUnixNano == 0 {
		return ctx, nil, nil
	}
	deadline := time.Unix(0, record.ArchiveDeadlineUnixNano)
	if !time.Now().Before(deadline) {
		return nil, nil, ErrProductionBootstrap
	}
	operation, cancel := context.WithDeadline(ctx, deadline)
	return operation, cancel, nil
}

// ProductionWorkspace borrows the authenticated held root, never transfers
// ownership. Its consumer must join before ProductionLifetime.Close. The native
// walker rechecks this prior identity; these fields do not imply a snapshot.
func ProductionWorkspace() (*os.File, string, os.FileInfo, [2]int32, error) {
	lifetime := productionRuntime.Load()
	if lifetime == nil {
		return nil, "", nil, [2]int32{}, ErrProductionBootstrap
	}
	lifetime.workspaceMu.Lock()
	defer lifetime.workspaceMu.Unlock()
	w := lifetime.workspace
	if w == nil || lifetime.client == nil || lifetime.client.Context().Err() != nil {
		return nil, "", nil, [2]int32{}, ErrProductionBootstrap
	}
	return w.file, w.binding.Path, w.info, w.binding.FSID, nil
}

func (lifetime *ProductionLifetime) closeWorkspace() error {
	lifetime.workspaceMu.Lock()
	defer lifetime.workspaceMu.Unlock()
	if lifetime.workspace == nil {
		return nil
	}
	err := lifetime.workspace.file.Close()
	lifetime.workspace = nil
	return err
}
