//go:build linux

package t421

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func linuxEpochStageFixture(t *testing.T) *linuxEpochInputStage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stage")
	if os.Mkdir(path, 0o700) != nil {
		t.Fatal("private stage")
	}
	root, err := openLinuxAuthorOwnedRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.file.Close() })
	return &linuxEpochInputStage{root: root, files: map[string]os.FileInfo{}}
}
func TestLinuxEpochStageExactCleanup(t *testing.T) {
	for _, mode := range []string{"exact", "unexpected", "replacement", "symlink", "hardlink", "changed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			stage := linuxEpochStageFixture(t)
			if stage.write(t.Context(), []epochGeneratedInput{{name: "catalog-a", raw: []byte("neutral\n")}}, maxEpochCatalogBytes) != nil {
				t.Fatal("stage write")
			}
			path := filepath.Join(stage.root.path, "catalog-a")
			ctx := t.Context()
			switch mode {
			case "unexpected":
				if os.WriteFile(filepath.Join(stage.root.path, "other"), nil, 0o600) != nil {
					t.Fatal("unexpected")
				}
			case "replacement":
				if os.Rename(stage.root.path, stage.root.path+"-held") != nil || os.Mkdir(stage.root.path, 0o700) != nil {
					t.Fatal("replace")
				}
			case "symlink":
				if os.Rename(stage.root.path, stage.root.path+"-held") != nil || os.Symlink(stage.root.path+"-held", stage.root.path) != nil {
					t.Fatal("symlink")
				}
			case "hardlink":
				if os.Link(path, filepath.Join(filepath.Dir(stage.root.path), "alias")) != nil {
					t.Fatal("hardlink")
				}
			case "changed":
				if os.WriteFile(path, []byte("changed\n"), 0o600) != nil {
					t.Fatal("change")
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := stage.remove(ctx)
			if mode == "exact" {
				if err != nil || len(stage.files) != 0 {
					t.Fatal("exact cleanup refused", err)
				}
			} else {
				if err == nil {
					t.Fatal("changed stage removed")
				}
				heldPath := path
				if mode == "replacement" || mode == "symlink" {
					heldPath = filepath.Join(stage.root.path+"-held", "catalog-a")
				}
				if _, err := os.Lstat(heldPath); err != nil {
					t.Fatal("refusal deleted original input")
				}
			}
		})
	}
}
func TestLinuxEpochStageRefusesUnboundedNamesAndBytes(t *testing.T) {
	for _, input := range []epochGeneratedInput{{name: "../escape", raw: []byte("x")}, {name: "config-1", raw: nil}, {name: "config-1", raw: make([]byte, maxEpochConfigBytes+1)}} {
		stage := linuxEpochStageFixture(t)
		if stage.write(t.Context(), []epochGeneratedInput{input}, maxEpochConfigBytes) == nil {
			t.Fatal("malformed input accepted")
		}
		if rows, err := os.ReadDir(stage.root.path); err != nil || len(rows) != 0 {
			t.Fatal("refusal wrote input")
		}
	}
}
func TestLinuxEpochPublicMissingIssuerRefusal(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if os.Mkdir(parent, 0o700) != nil {
		t.Fatal("parent")
	}
	for _, author := range []*LinuxExecutionAuthorCustody{nil, {}, {request: LinuxExecutionAuthorRequest{Builds: &LinuxExecutionGoBuildCustody{}, Author: &LinuxExecutionToolCustody{}, Plan: &LinuxExecutionInputCustody{}}}} {
		for _, ctx := range []context.Context{nil, t.Context()} {
			if owner, err := PrepareLinuxExecutionEpochConfigs(ctx, parent, author, &LinuxInputTreeHelper{tool: &LinuxExecutionToolCustody{}}); owner != nil || err == nil {
				t.Fatal("missing qualified issuer accepted")
			}
		}
	}
	for _, helper := range []*LinuxInputTreeHelper{nil, {}} {
		if owner, err := PrepareLinuxExecutionEpochConfigs(t.Context(), parent, &LinuxExecutionAuthorCustody{}, helper); owner != nil || err == nil {
			t.Fatal("missing helper accepted")
		}
	}
	for _, owner := range []*LinuxExecutionEpochConfigCustody{nil, {}} {
		if value, err := owner.Check(t.Context(), 1); err == nil || value.ConfigPath != "" {
			t.Fatal("zero owner yielded input")
		}
		if owner.ReleaseListener(t.Context(), 1) == nil {
			t.Fatal("zero owner released reservation")
		}
	}
}
func TestLinuxEpochCloseRetainsActiveUse(t *testing.T) {
	owner := &LinuxExecutionEpochConfigCustody{active: true}
	if owner.Close(t.Context()) == nil || owner.closed || owner.closing {
		t.Fatal("active use released")
	}
	owner.active = false
	if owner.Close(t.Context()) != nil || !owner.closed {
		t.Fatal("joined empty use did not close")
	}
}
func TestLinuxEpochCloseJoinsRetainedPreparationBeforeStaging(t *testing.T) {
	// A real owned empty placeholder plus an unfinished constructor barrier; no
	// child or successful protected identity is fabricated by this refusal test.
	parentPath := filepath.Join(t.TempDir(), "parent")
	if os.Mkdir(parentPath, 0o700) != nil {
		t.Fatal("parent")
	}
	parent, err := openLinuxAuthorOwnedRoot(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	path, err := os.MkdirTemp(parentPath, "epoch-")
	if err != nil {
		t.Fatal(err)
	}
	stage, err := openLinuxAuthorOwnedRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	rawPath := filepath.Join(path, "raw")
	if os.Mkdir(rawPath, 0o700) != nil {
		t.Fatal("raw")
	}
	rawRoot, err := openLinuxAuthorOwnedRoot(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	raw := &linuxEpochInputStage{root: rawRoot, files: map[string]os.FileInfo{}}
	if raw.write(t.Context(), []epochGeneratedInput{{name: "config-1", raw: []byte("neutral")}}, 64) != nil {
		t.Fatal("raw write")
	}
	treePath := filepath.Join(path, "pending")
	if os.Mkdir(treePath, 0o700) != nil {
		t.Fatal("tree")
	}
	file, err := os.Open(treePath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	tree := &LinuxExecutionInputTree{done: make(chan struct{}), cancel: cancel, stagePath: treePath, stage: file, stageInfo: info, err: ErrLinuxInputTree}
	owner := &LinuxExecutionEpochConfigCustody{parent: parent, stage: stage, catalogStage: raw, catalogs: tree, err: ErrExecutionEpochConfigs}
	ctx, stop := context.WithTimeout(t.Context(), time.Millisecond)
	err = owner.Close(ctx)
	stop()
	if err == nil || owner.closed || tree.closed {
		t.Fatal("pending constructor counted as join")
	}
	if _, err := os.Stat(filepath.Join(rawPath, "config-1")); err != nil {
		t.Fatal("pending copy input removed")
	}
	if !owner.mu.TryLock() {
		t.Fatal("Close retained hook mutex")
	}
	owner.mu.Unlock()
	close(tree.done)
	if owner.Close(t.Context()) == nil || !tree.closed || !owner.closed {
		t.Fatal("joined failed prefix lost cleanup or sticky error")
	}
	if _, err := os.Stat(filepath.Join(rawPath, "config-1")); !os.IsNotExist(err) {
		t.Fatal("joined preparation not retired")
	}
}

func TestLinuxEpochPublicRefusesUnissuedLineageAfterImageCheck(t *testing.T) {
	authorParent := filepath.Join(t.TempDir(), "author")
	epochParent := filepath.Join(t.TempDir(), "epochs")
	for _, path := range []string{authorParent, epochParent} {
		if os.Mkdir(path, 0o700) != nil {
			t.Fatal("private parent")
		}
	}
	author := &LinuxExecutionAuthorCustody{}
	if author.createRoots(t.Context(), authorParent) != nil {
		t.Fatal("native source and lease")
	}
	defer func() { _ = author.Close(context.Background()) }()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(epochParent, "plan")
	if os.WriteFile(planPath, []byte("sealed plan fixture"), 0o600) != nil {
		t.Fatal("plan")
	}
	inputs := requireLinuxInputCustody(t, linuxInputSelection(t, executable, "t422-author", true), linuxInputSelection(t, planPath, "plan", false))
	if os.Remove(planPath) != nil {
		t.Fatal("retire mutable plan fixture")
	}
	selected, err := inputs.Check(t.Context(), "t422-author")
	if err != nil {
		t.Fatal(err)
	}
	// Actual sealed diagnostic bytes pass image custody; these private fields
	// deliberately lack the reference issuer's pointer binding. No tool is issued.
	identity := ExecutionToolIdentity{Role: "t422-author", SHA256: selected.SHA256}
	tool := &LinuxExecutionToolCustody{input: inputs, identity: identity}
	author.authorIdentity = identity
	author.request = LinuxExecutionAuthorRequest{Builds: &LinuxExecutionGoBuildCustody{}, Author: tool, Plan: &LinuxExecutionInputCustody{}}
	if author.checkRoots(t.Context()) != nil || author.checkSource(t.Context(), nil) != nil || tool.WithImage(t.Context(), "t422-author", func(_ ExecutionToolIdentity, _ *os.File) error { return nil }) != nil {
		t.Fatal("forged-lineage fixture failed before issuer boundary")
	}
	if owner, err := PrepareLinuxExecutionEpochConfigs(t.Context(), epochParent, author, &LinuxInputTreeHelper{tool: &LinuxExecutionToolCustody{}}); owner != nil || err == nil {
		t.Fatal("unissued image acquired epoch authority")
	}
	if inputs.err != nil {
		t.Fatal("issuer refusal was a sealed image failure")
	}
	var snapshots linuxEpochAuthorInputs
	if inputs.WithInputs(t.Context(), []string{"plan", "t422-author"}, func(_ []*os.File) error {
		snapshots = linuxEpochAuthorInputs{plan: inputs.inputs["plan"], image: inputs.inputs["t422-author"]}
		return nil
	}) != nil || snapshots.check() != nil {
		t.Fatal("genuine sealed keeper snapshots")
	}
	author.planSHA256 = snapshots.plan.identity.SHA256
	partial, err := prepareLinuxEpochConfigs(t.Context(), authorParent, author, Plan{}, nil, snapshots)
	if err == nil || partial == nil || partial.stage.path != "" || author.checkSource(t.Context(), nil) != nil {
		t.Fatal("overlapping epoch parent mutated borrowed source")
	}
	if partial.Close(t.Context()) == nil || !partial.closed || author.checkRoots(t.Context()) != nil {
		t.Fatal("partial constructor released borrowed native root")
	}
	if inputs.Close() != nil || snapshots.check() == nil {
		t.Fatal("closed prerequisite keeper admitted")
	}
	if rows, err := os.ReadDir(epochParent); err != nil || len(rows) != 0 {
		t.Fatal("issuer refusal created native stage")
	}
}

func TestLinuxEpochNativeRootAndLeaseDrift(t *testing.T) {
	for _, mode := range []string{"intact", "data-root", "home-symlink", "stage", "constructor-parent", "source-lease", "source-root", "epoch-parent"} {
		t.Run(mode, func(t *testing.T) {
			authorParent := filepath.Join(t.TempDir(), "author")
			epochParent := filepath.Join(t.TempDir(), "epochs")
			for _, path := range []string{authorParent, epochParent} {
				if os.Mkdir(path, 0o700) != nil {
					t.Fatal("parent")
				}
			}
			author := &LinuxExecutionAuthorCustody{}
			if author.createRoots(t.Context(), authorParent) != nil {
				t.Fatal("author roots")
			}
			t.Cleanup(func() { _ = author.Close(context.Background()) })
			parent, err := openLinuxAuthorOwnedRoot(epochParent)
			if err != nil {
				t.Fatal(err)
			}
			directory, err := os.MkdirTemp(epochParent, "epoch-")
			if err != nil {
				t.Fatal(err)
			}
			stage, err := openLinuxAuthorOwnedRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			owner := &LinuxExecutionEpochConfigCustody{author: author, parent: parent, stage: stage}
			t.Cleanup(func() {
				for _, root := range append(append([]linuxAuthorRoot{}, owner.roots...), owner.constructors, owner.stage, owner.parent) {
					if root.file != nil {
						_ = root.file.Close()
					}
				}
			})
			for _, name := range []string{"constructors", "data", "home", "tmp", "backup"} {
				path := filepath.Join(directory, name)
				if os.Mkdir(path, 0o700) != nil {
					t.Fatal("mkdir")
				}
				root, err := openLinuxAuthorOwnedRoot(path)
				if err != nil {
					t.Fatal(err)
				}
				if name == "constructors" {
					owner.constructors = root
				} else {
					owner.roots = append(owner.roots, root)
				}
			}
			path := ""
			switch mode {
			case "data-root":
				path = owner.roots[0].path
			case "home-symlink":
				path = owner.roots[1].path
			case "stage":
				path = owner.stage.path
			case "constructor-parent":
				path = owner.constructors.path
			case "source-root":
				path = author.roots[1].path
			case "epoch-parent":
				path = owner.parent.path
			case "source-lease":
				lease := filepath.Join(authorParent, linuxAuthorLeaseName)
				if os.Rename(lease, lease+"-held") != nil || os.WriteFile(lease, nil, 0o600) != nil {
					t.Fatal("replace lease")
				}
			}
			if path != "" {
				if os.Rename(path, path+"-held") != nil {
					t.Fatal("move root")
				}
				if mode == "home-symlink" {
					err = os.Symlink(path+"-held", path)
				} else {
					err = os.Mkdir(path, 0o700)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			err = owner.checkNativeRoots(t.Context())
			if (mode == "intact") != (err == nil) {
				t.Fatal("native root/lease replacement gate", mode, err)
			}

			if mode == "data-root" || mode == "source-lease" {
				listener, e := net.Listen("tcp4", "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				defer func() { _ = listener.Close() }()
				owner.listeners[0] = listener
				if owner.ReleaseListener(t.Context(), 2) == nil || owner.err != nil || owner.released != 0 || owner.listeners[0] != listener {
					t.Fatal("wrong order changed reservation or latched input state")
				}
				if owner.ReleaseListener(t.Context(), 1) == nil || owner.err != ErrExecutionEpochConfigs || owner.released != 0 || owner.listeners[0] != listener {
					t.Fatal("correct-order drift did not latch before release")
				}
				probe, e := net.Listen("tcp4", listener.Addr().String())
				if probe != nil {
					_ = probe.Close()
				}
				if !errors.Is(e, unix.EADDRINUSE) {
					t.Fatal("failed input check released kernel reservation", e)
				}
				restore := path
				if mode == "source-lease" {
					restore = filepath.Join(authorParent, linuxAuthorLeaseName)
				}
				if os.Remove(restore) != nil || os.Rename(restore+"-held", restore) != nil {
					t.Fatal("restore original native inode")
				}
				observed := owner.checkNativeRoots(t.Context())
				if mode == "data-root" && observed != nil {
					t.Fatal("restored directory observation refused", observed)
				}
				// Renaming the lease changes ctime. Its original saved metadata
				// must remain authoritative even after that inode moves back.
				if mode == "source-lease" && observed == nil {
					t.Fatal("changed lease metadata silently readmitted")
				}
				if owner.ReleaseListener(t.Context(), 1) == nil || owner.err != ErrExecutionEpochConfigs || owner.released != 0 || owner.listeners[0] != listener {
					t.Fatal("restoration erased uncertainty")
				}
			}
		})
	}
}

func TestLinuxEpochReleasePastFinalReservationIsPreflight(t *testing.T) {
	owner := &LinuxExecutionEpochConfigCustody{author: &LinuxExecutionAuthorCustody{}, released: 5}
	for _, epoch := range []uint64{0, 6, ^uint64(0)} {
		if owner.ReleaseListener(t.Context(), epoch) == nil || owner.err != nil || owner.released != 5 || owner.closed || owner.closing {
			t.Fatal("out-of-range reservation request poisoned completed prefix", epoch)
		}
	}
}
