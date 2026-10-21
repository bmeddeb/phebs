//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func foregroundGitTestRecord(producer uint32) ProductionBootstrap {
	record := serialIndexTestRecord(producer)
	for _, index := range []int{0, 2} {
		tool := &record.Tools[index]
		tool.RuntimeRecipe = ProductionLinuxForegroundGitV1
		for i, entry := range tool.Environment {
			if entry == "GIT_CONFIG_COUNT=3" {
				tool.Environment[i] = "GIT_CONFIG_COUNT=5"
			}
		}
		tool.Environment = append(tool.Environment, "GIT_CONFIG_KEY_3=gc.autoDetach", "GIT_CONFIG_VALUE_3=false",
			"GIT_CONFIG_KEY_4=maintenance.autoDetach", "GIT_CONFIG_VALUE_4=false")
	}
	return record
}

func TestProductionLinuxForegroundGitOverridesRefuseBeforeStart(t *testing.T) {
	tool := foregroundGitTestRecord(2).Tools[0]
	for _, args := range [][]string{
		{"-c", "maintenance.autoDetach=true", "fetch", "origin"},
		{"-cgc.autoDetach=true", "fetch", "origin"},
		{"-c", "MAINTENANCE.AUTODETACH=true", "fetch", "origin"},
		{"--config-env=maintenance.autoDetach=HOME", "fetch", "origin"},
		{"--config-env", "gc.autoDetach=HOME", "fetch", "origin"},
		{"maintenance", "run", "--auto", "--detach"},
		{"gc", "--auto"},
		{"clone", "-c", "maintenance.autoDetach=true", "/private/source", "/private/repo"},
		{"fetch", "--detach=true", "origin"},
		{"-c", "alias.fetch=!false", "fetch", "origin"},
		{"-c"},
		{},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			argv := append([]string{tool.Path}, args...)
			command := &exec.Cmd{Path: tool.Path, Args: slices.Clone(argv)}
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			client := &Client{ctx: ctx, cancel: cancel, changed: make(chan struct{})}
			lifetime := &ProductionLifetime{program: ProgramPhebs, semanticMode: ProductionSemanticV3, client: client, tools: map[string]ProductionToolBinding{tool.Role: tool}}
			_, err := startProductionCommandWithEnv(t.Context(), lifetime, SiteSyncGit, tool.Role, command, nil)
			if !errors.Is(err, ErrProductionBootstrap) || !errors.Is(client.err, ErrProductionBootstrap) || command.Process != nil || !slices.Equal(command.Args, argv) || ctx.Err() == nil {
				t.Fatal("foreground override reached Start or lost sticky refusal", err)
			}
		})
	}
	for _, args := range [][]string{
		{"--no-replace-objects", "-c", "core.quotePath=false", "--literal-pathspecs", "ls-tree", "-rz", "HEAD"},
		{"-c", "http.extraheader=Authorization: Basic neutral", "fetch", "--prune", "origin"},
		{"clone", "--mirror", "file:///private/source", "/private/repo"},
		{"config", "zoekt.name", "neutral"},
		{"diff-tree", "--root", "--no-commit-id", "-r", "HEAD"},
		{"--no-replace-objects", "-c", "core.quotePath=false", "--literal-pathspecs", "blame", "--line-porcelain", "--root", "-M", "-C", "-L", "1,10", "HEAD", "--", "file.go"},
	} {
		if !foregroundProductionGitArgs(args) {
			t.Fatal("existing source command refused", args)
		}
	}
}

func TestProductionLinuxForegroundGitClosedBootstrap(t *testing.T) {
	for _, producer := range []uint32{2, 3, 4} {
		record := foregroundGitTestRecord(producer)
		err := record.validate()
		if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
			if err != nil {
				t.Fatal("closed foreground server recipe refused", producer, err)
			}
			args := []string{record.Tools[0].Path, "fetch", "--prune", "origin"}
			got, err := productionIndexRuntimeArgs(SiteSyncGit, record.Tools[0], args)
			if err != nil || !slices.Equal(got, args) || &got[0] != &args[0] {
				t.Fatal("foreground changed fetch argv or allocated", got, err)
			}
			indexed, err := productionIndexRuntimeArgs(SiteIndexBuild, record.Tools[2], serialIndexTestArgs(record.Tools[2].Path))
			if err != nil || len(indexed) != 10 || indexed[8] != "-parallelism=1" {
				t.Fatal("foreground lost serial HEAD recipe", indexed, err)
			}
		} else if !errors.Is(err, ErrProductionBootstrap) {
			t.Fatal("foreign platform admitted foreground recipe", err)
		}
	}
	for _, test := range []struct {
		name string
		edit func(*ProductionBootstrap)
	}{
		{"missing-git-token", func(r *ProductionBootstrap) { r.Tools[0].RuntimeRecipe = "" }},
		{"missing-index-token", func(r *ProductionBootstrap) { r.Tools[2].RuntimeRecipe = "" }},
		{"old-index-token", func(r *ProductionBootstrap) { r.Tools[2].RuntimeRecipe = ProductionLinuxSerialIndexV1 }},
		{"foreign-token", func(r *ProductionBootstrap) { r.Tools[0].RuntimeRecipe = "foreign" }},
		{"missing-pair", func(r *ProductionBootstrap) {
			r.Tools[0].Environment = r.Tools[0].Environment[:len(r.Tools[0].Environment)-1]
		}},
		{"duplicate-pair", func(r *ProductionBootstrap) {
			r.Tools[0].Environment = append(r.Tools[0].Environment, "GIT_CONFIG_VALUE_3=false")
		}},
		{"background-value", func(r *ProductionBootstrap) {
			r.Tools[0].Environment[len(r.Tools[0].Environment)-1] = "GIT_CONFIG_VALUE_4=true"
		}},
		{"disabled-maintenance", func(r *ProductionBootstrap) {
			r.Tools[0].Environment[len(r.Tools[0].Environment)-2] = "GIT_CONFIG_KEY_4=maintenance.auto"
		}},
		{"disabled-gc", func(r *ProductionBootstrap) {
			r.Tools[0].Environment[len(r.Tools[0].Environment)-4] = "GIT_CONFIG_KEY_3=gc.auto"
		}},
		{"unknown-config", func(r *ProductionBootstrap) {
			r.Tools[0].Environment = append(r.Tools[0].Environment, "GIT_CONFIG_KEY_5=alias.fetch")
		}},
		{"count3", func(r *ProductionBootstrap) {
			for i, entry := range r.Tools[0].Environment {
				if entry == "GIT_CONFIG_COUNT=5" {
					r.Tools[0].Environment[i] = "GIT_CONFIG_COUNT=3"
				}
			}
		}},
		{"duplicate-count", func(r *ProductionBootstrap) {
			r.Tools[0].Environment = append(r.Tools[0].Environment, "GIT_CONFIG_COUNT=5")
		}},
		{"wrong-producer", func(r *ProductionBootstrap) { r.Producer.ID = 5 }},
		{"wrong-phase", func(r *ProductionBootstrap) { r.Phase = 3 }},
		{"missing-store", func(r *ProductionBootstrap) { r.Store = nil }},
		{"no-owner-control", func(r *ProductionBootstrap) { r.Control.OwnerControl = false }},
		{"surreal-role", func(r *ProductionBootstrap) { r.Tools[1] = r.Tools[0]; r.Tools[1].Role = "surreal" }},
		{"ordinary-mode", func(r *ProductionBootstrap) { r.SemanticMode = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := foregroundGitTestRecord(2)
			test.edit(&record)
			if !errors.Is(record.validate(), ErrProductionBootstrap) {
				t.Fatal("changed foreground recipe accepted")
			}
		})
	}
	legacy := serialIndexTestRecord(2)
	for _, tool := range legacy.Tools {
		if foregroundGitSelected(tool.Environment) || strings.Contains(strings.Join(tool.Environment, "\n"), "autoDetach") {
			t.Fatal("historical COUNT3 environment changed")
		}
	}
}
