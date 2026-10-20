package dispatchadmission

import (
	"runtime"
	"slices"
)

// ProductionLinuxSerialIndexV1 narrows the source-owned Linux rehearsal recipe.
// It grants no workspace, tool, resource, freeze or ceremony authority.
const ProductionLinuxSerialIndexV1 = "t42-linux-head-serial-index-v1"

func (record ProductionBootstrap) validIndexRuntimeRecipe(tool ProductionToolBinding) bool {
	if tool.RuntimeRecipe == "" {
		return true
	}
	if tool.RuntimeRecipe != ProductionLinuxSerialIndexV1 || tool.Role != "zoekt-git-index" ||
		runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || record.Program != ProgramPhebs ||
		record.SemanticMode != ProductionSemanticV3 || record.Store == nil || !record.Control.OwnerControl ||
		!hasProductionIndexMode(tool.Environment) {
		return false
	}
	var phases []uint32
	switch record.Producer.ID {
	case 2:
		phases = []uint32{2, 3, 4}
	case 3:
		phases = []uint32{5}
	case 4:
		phases = []uint32{6, 7, 8}
	default:
		return false
	}
	// Return-A's existing producer4 launch has no Workspace. This recipe adds no FD6.
	return record.Phase == phases[0] && record.Control.InitialPhase == phases[0] &&
		record.Control.MaximumPhases == len(phases) && slices.Equal(record.Control.Phases, phases)
}

func productionIndexRuntimeArgs(site uint32, tool ProductionToolBinding, args []string) ([]string, error) {
	if tool.RuntimeRecipe == "" {
		return args, nil
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || tool.RuntimeRecipe != ProductionLinuxSerialIndexV1 ||
		tool.Role != "zoekt-git-index" || !validProductionPath(tool.Path) || site != SiteIndexBuild || len(args) != 9 || args[0] != tool.Path ||
		args[1] != "-index" || !validProductionPath(args[2]) || !validProductionPath(args[8]) ||
		args[3] != "-incremental=false" || args[4] != "-submodules=false" ||
		args[5] != "-file_limit=2097152" || args[6] != "-shard_limit=104857600" || args[7] != "-max_trigram_count=20000" {
		return nil, ErrProductionBootstrap
	}
	// Validate the complete HEAD-only recipe before making a detached argv.
	selected := make([]string, 0, 10)
	selected = append(selected, args[:8]...)
	return append(selected, "-parallelism=1", args[8]), nil
}
