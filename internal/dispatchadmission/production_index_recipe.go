package dispatchadmission

import (
	"runtime"
	"slices"
	"strings"
)

// ProductionLinuxSerialIndexV1 narrows the source-owned Linux rehearsal recipe.
// It grants no workspace, tool, resource, freeze or ceremony authority.
const ProductionLinuxSerialIndexV1 = "t42-linux-head-serial-index-v1"

// ProductionLinuxForegroundGitV1 keeps automatic Git maintenance parent-owned
// and retains the serial HEAD index recipe in the same selected server scope.
const ProductionLinuxForegroundGitV1 = "t42-linux-foreground-git-serial-index-v1"

func (record ProductionBootstrap) validIndexRuntimeRecipe(tool ProductionToolBinding) bool {
	if tool.RuntimeRecipe == "" {
		return !foregroundGitSelected(tool.Environment)
	}
	foreground := foregroundGitSelected(tool.Environment)
	if tool.RuntimeRecipe == ProductionLinuxForegroundGitV1 {
		if !foreground || tool.Role != "git" && tool.Role != "zoekt-git-index" || len(record.Tools) != 3 ||
			record.Tools[0].RuntimeRecipe != ProductionLinuxForegroundGitV1 || record.Tools[2].RuntimeRecipe != ProductionLinuxForegroundGitV1 {
			return false
		}
	} else if tool.RuntimeRecipe != ProductionLinuxSerialIndexV1 || foreground || tool.Role != "zoekt-git-index" {
		return false
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || record.Program != ProgramPhebs ||
		record.SemanticMode != ProductionSemanticV3 || record.Store == nil || !record.Control.OwnerControl ||
		tool.Role == "zoekt-git-index" && !hasProductionIndexMode(tool.Environment) {
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
		if foregroundGitSelected(tool.Environment) {
			return nil, ErrProductionBootstrap
		}
		return args, nil
	}
	if tool.RuntimeRecipe == ProductionLinuxSerialIndexV1 && foregroundGitSelected(tool.Environment) {
		return nil, ErrProductionBootstrap
	}
	if tool.RuntimeRecipe == ProductionLinuxForegroundGitV1 {
		if !foregroundGitSelected(tool.Environment) || !validProductionToolEnvironment(tool.Environment, tool.Role, ProductionSemanticV3) {
			return nil, ErrProductionBootstrap
		}
		if tool.Role == "git" {
			if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || !validProductionPath(tool.Path) || productionRole(site) != "git" || len(args) == 0 || args[0] != tool.Path || !foregroundProductionGitArgs(args[1:]) {
				return nil, ErrProductionBootstrap
			}
			return args, nil
		}
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || tool.RuntimeRecipe != ProductionLinuxSerialIndexV1 && tool.RuntimeRecipe != ProductionLinuxForegroundGitV1 ||
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

func foregroundProductionGitArgs(args []string) bool {
	// Only the existing source-owned prefixes and commands may precede Git's
	// environment configuration. Never permit a caller's higher-priority override.
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-replace-objects", "--literal-pathspecs":
		case "-c":
			i++
			if i >= len(args) || args[i] != "core.quotePath=false" && !strings.HasPrefix(args[i], "http.extraheader=Authorization: Basic ") {
				return false
			}
		default:
			switch args[i] {
			case "cat-file", "ls-tree", "rev-parse", "rev-list", "log", "show", "diff", "diff-tree", "blame", "merge-base", "symbolic-ref", "config", "clone", "fetch", "remote", "for-each-ref":
			default:
				return false
			}
			for _, argument := range args[i+1:] {
				if strings.HasPrefix(argument, "-c") || strings.HasPrefix(argument, "--config-env") || strings.HasPrefix(argument, "--detach") {
					return false
				}
			}
			return true
		}
	}
	return false
}
