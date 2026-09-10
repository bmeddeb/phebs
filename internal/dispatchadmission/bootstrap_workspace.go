package dispatchadmission

import (
	"os"
	"slices"
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
		return nil
	}
	w := record.Workspace
	if record.Program != ProgramPhebs || record.SemanticMode != ProductionSemanticV3 || record.InputSHA256 == ([32]byte{}) ||
		record.Store == nil || !validProductionPath(w.Path) || w.Inode == 0 || w.FSID == ([2]int32{}) {
		return ErrProductionBootstrap
	}
	if record.Producer.ID == 5 && record.Phase == 8 && record.Control.MaximumPhases == 4 && slices.Equal(record.Control.Phases, []uint32{8, 9, 10, 11}) ||
		record.Producer.ID == 6 && record.Phase == 12 && record.Control.MaximumPhases == 3 && slices.Equal(record.Control.Phases, []uint32{12, 13, 14}) {
		return nil
	}
	return ErrProductionBootstrap
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
