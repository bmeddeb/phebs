//go:build linux

package t421

import (
	"os"
	"path/filepath"
	"testing"
)

// Only the workspace graph is mechanical here. These host placeholders are
// never presented as protected trees, provenance, resources or launch authority.
func linuxEpochWorkspaceFixture(t *testing.T) *linuxEpochHeldFlow {
	t.Helper()
	base := filepath.Join(t.TempDir(), "workspace")
	root := func(path string) linuxAuthorRoot {
		t.Helper()
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		r, err := openLinuxAuthorOwnedRoot(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.file.Close() })
		return r
	}
	_ = root(base)
	inputs, epochParent := root(filepath.Join(base, "inputs")), root(filepath.Join(base, "epochs"))
	author := &LinuxExecutionAuthorCustody{roots: []linuxAuthorRoot{inputs}}
	lease := filepath.Join(inputs.path, linuxAuthorLeaseName)
	if os.WriteFile(lease, nil, 0o600) != nil {
		t.Fatal("fixture lease")
	}
	author.leaseInfo, _ = os.Lstat(lease)
	for _, name := range []string{"source", "home", "tmp"} {
		author.roots = append(author.roots, root(filepath.Join(inputs.path, name)))
	}
	stage := root(filepath.Join(epochParent.path, "stage"))
	epochs := &LinuxExecutionEpochConfigCustody{author: author, parent: epochParent, stage: stage}
	for _, name := range []string{"data", "home", "tmp", "backup"} {
		epochs.roots = append(epochs.roots, root(filepath.Join(stage.path, name)))
	}
	buildsRoot, gitRoot := root(filepath.Join(inputs.path, "build")), root(filepath.Join(inputs.path, "git"))
	buildsStage, err := os.OpenRoot(buildsRoot.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = buildsStage.Close() })
	gitStage, err := os.OpenRoot(gitRoot.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gitStage.Close() })
	held := &linuxEpochHeldFlow{author: author, epochs: epochs, builds: &LinuxExecutionGoBuildCustody{directory: buildsRoot.path, stage: buildsStage, stageInfo: buildsRoot.info}, git: &LinuxExecutionGitCustody{directory: gitRoot.path, stage: gitStage, stageInfo: gitRoot.info}}
	stages := []string{filepath.Join(buildsRoot.path, "tree"), filepath.Join(gitRoot.path, "tree"), filepath.Join(stage.path, "catalog-tree"), filepath.Join(stage.path, "config-tree")}
	trees := make([]*LinuxExecutionInputTree, 4)
	for i, path := range stages {
		r := root(path)
		trees[i] = &LinuxExecutionInputTree{stage: r.file, stageInfo: r.info, stagePath: r.path}
	}
	held.builds.tree, held.git.tree, held.epochs.catalogs, held.epochs.configs = trees[0], trees[1], trees[2], trees[3]
	return held
}
func TestLinuxEpochWorkspaceRequiresCompleteOwnedGraph(t *testing.T) {
	for _, name := range []string{"complete", "outside-tree", "broad-tree-ancestor", "non-sibling-parent", "replacement", "unknown-sibling"} {
		t.Run(name, func(t *testing.T) {
			held := linuxEpochWorkspaceFixture(t)
			switch name {
			case "unknown-sibling":
				if os.Mkdir(filepath.Join(filepath.Dir(held.author.roots[0].path), "foreign"), 0o700) != nil {
					t.Fatal("foreign sibling")
				}
			case "outside-tree":
				held.epochs.configs.stagePath = filepath.Join(t.TempDir(), "foreign")
			case "broad-tree-ancestor":
				held.epochs.configs.stagePath = filepath.Dir(filepath.Dir(held.author.roots[0].path))
			case "non-sibling-parent":
				held.epochs.parent = held.epochs.stage
			}
			err := held.openWorkspace()
			if name == "complete" || name == "replacement" {
				if err != nil {
					t.Fatal("complete graph refused", err)
				}
				defer func() { _ = held.workspace.file.Close() }()
			} else if err == nil {
				t.Fatal("partial or unrelated scope accepted")
			}
			if name == "replacement" {
				path := held.workspace.path
				if os.Rename(path, path+"-moved") != nil || os.Mkdir(path, 0o700) != nil {
					t.Fatal("replace workspace")
				}
				if held.checkWorkspace() == nil {
					t.Fatal("replaced workspace admitted")
				}
			}
		})
	}
}
