//go:build linux && phebs_t42_native

package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"golang.org/x/sys/unix"
)

// This required input-owner gate uses a fixed mechanical native source/lease
// and sealed admitted-test-plan fixture. It does not issue a reference-built
// author, execute physical authoring, launch a server, or qualify c.c.
func TestLinuxEpochInputsRequiredNativeOwnership(t *testing.T) {
	if os.Getuid() == 0 || os.Geteuid() == 0 || runtime.GOARCH != "amd64" {
		t.Fatal("required unprivileged Linux amd64 policy; no skip")
	}
	request := readNativeGoReferenceRequest(t, "PHEBS_T42_INPUT_TREE_REQUEST")
	baseline, err := goBuildImportHostRoot()
	if err != nil {
		t.Fatal("host propagation baseline")
	}
	t.Cleanup(func() {
		after, err := goBuildImportHostRoot()
		if err != nil || after != baseline {
			t.Error("host propagation bracket changed")
		}
	})
	base, err := os.MkdirTemp("", "phebs-linux-epochs-")
	if err != nil {
		t.Fatal(err)
	}
	authorParent, epochParent := filepath.Join(base, "author"), filepath.Join(base, "epochs")
	for _, path := range []string{authorParent, epochParent} {
		if os.Mkdir(path, 0o700) != nil {
			t.Fatal("private native parent; retain", base)
		}
	}
	author := &LinuxExecutionAuthorCustody{}
	var helper *LinuxInputTreeHelper
	var owner *LinuxExecutionEpochConfigCustody
	var planInput *LinuxExecutionInputCustody
	var planInfo os.FileInfo
	planPath := filepath.Join(base, "plan")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if owner != nil {
			_ = owner.Close(ctx)
			if !owner.closed {
				t.Error("uncertain epoch custody retained", base)
				return
			}
		}
		if helper != nil && helper.Close() != nil {
			t.Error("helper close refused; retain", base)
			return
		}
		if author.Close(ctx) != nil || !author.released {
			t.Error("source/lease cleanup refused; retain", base)
			return
		}
		if planInput != nil && planInput.Close() != nil {
			t.Error("plan cleanup refused; retain", base)
			return
		}
		// Only empty fixed directories and exact fixture files are removed. A new
		// entry makes Remove fail; no recursive cleanup of an uncertain prefix.
		if owner != nil && owner.stage.path != "" {
			expected := map[string]os.FileInfo{}
			if owner.catalogStage != nil {
				expected["catalogs"] = owner.catalogStage.root.info
			}
			if owner.configStage != nil {
				expected["configs"] = owner.configStage.root.info
			}
			expected["constructors"] = owner.constructors.info
			for _, root := range owner.roots {
				expected[filepath.Base(root.path)] = root.info
			}
			for _, name := range []string{"catalogs", "configs", "constructors", "data", "home", "tmp", "backup"} {
				path := filepath.Join(owner.stage.path, name)
				info, err := os.Lstat(path)
				if err != nil || expected[name] == nil || !info.IsDir() || !os.SameFile(expected[name], info) {
					t.Error("changed epoch directory retained", base)
					return
				}
				if err := os.Remove(path); err != nil {
					t.Error("epoch directory retirement refused; retain", base)
					return
				}
			}
			info, err := os.Lstat(owner.stage.path)
			if err != nil || owner.stage.info == nil || !os.SameFile(owner.stage.info, info) || os.Remove(owner.stage.path) != nil {
				t.Error("epoch stage retirement refused; retain", base)
				return
			}
		}
		for index, root := range author.roots {
			if index == 0 {
				continue
			}
			info, err := os.Lstat(root.path)
			if err != nil || root.info == nil || !os.SameFile(root.info, info) || os.Remove(root.path) != nil {
				t.Error("source directory retirement refused; retain", base)
				return
			}
		}
		if author.leaseInfo != nil {
			lease := filepath.Join(authorParent, linuxAuthorLeaseName)
			info, err := os.Lstat(lease)
			if err != nil || !inputCustodySame(author.leaseInfo, info) || os.Remove(lease) != nil {
				t.Error("exact lease retirement refused; retain", base)
				return
			}
		}
		if planInfo != nil {
			info, err := os.Lstat(planPath)
			if err != nil || !inputCustodySame(planInfo, info) || os.Remove(planPath) != nil {
				t.Error("plan fixture retirement refused; retain", base)
				return
			}
		}
		for _, path := range []string{authorParent, epochParent, base} {
			if os.Remove(path) != nil {
				t.Error("empty fixture retirement refused; retain", base)
				return
			}
		}
	})
	if author.createRoots(t.Context(), authorParent) != nil {
		t.Fatal("native source/lease fixture; retain", base)
	}
	plan := accountingTestPlan(t)
	raw, err := json.Marshal(plan)
	if err != nil || os.WriteFile(planPath, raw, 0o600) != nil {
		t.Fatal("fixed admitted-test-plan fixture")
	}
	planInfo, err = os.Lstat(planPath)
	if err != nil {
		t.Fatal("plan fixture identity")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("fixed own diagnostic image")
	}
	planInput, err = ProtectLinuxExecutionInputs(t.Context(), []ExecutionInputCopy{{Name: "plan", Path: planPath, SHA256: SHA256(raw)}, linuxInputSelection(t, executable, "image", true)})
	if err != nil {
		t.Fatal("sealed plan fixture")
	}
	author.request.Plan, author.planSHA256 = planInput, SHA256(raw)
	helper, err = ProtectLinuxInputTreeHelper(t.Context(), request)
	if err != nil {
		t.Fatal("required exact qualified H2k helper; no skip", err)
	}
	if unexpected, err := PrepareLinuxExecutionEpochConfigs(t.Context(), epochParent, author, helper); err == nil || unexpected != nil {
		t.Fatal("mechanical fixture acquired reference-author authority")
	}
	// This is the same fixed constructor reached after public genuine-lineage
	// validation; its fixture supplies only source/lease and already-admitted bytes.
	var authorInputs linuxEpochAuthorInputs
	if planInput.WithInputs(t.Context(), []string{"plan", "image"}, func(_ []*os.File) error {
		authorInputs = linuxEpochAuthorInputs{plan: planInput.inputs["plan"], image: planInput.inputs["image"]}
		return nil
	}) != nil {
		t.Fatal("fixed private keeper snapshots")
	}
	owner, err = prepareLinuxEpochConfigs(t.Context(), epochParent, author, plan, helper, authorInputs)
	if err != nil {
		if owner != nil {
			t.Logf("epoch input refusal step=%s retained=%v", owner.preparationStep, owner.RetainedPaths())
		}
		t.Fatal("required actual protected inputs; no skip", err)
	}
	if owner.logicalChanges == nil || owner.queryCatalog == nil || len(owner.roots) != 4 {
		t.Fatal("unpublished observed inputs")
	}
	for _, tree := range []*LinuxExecutionInputTree{owner.catalogs, owner.configs} {
		if !linuxEpochTreeReady(tree) {
			t.Fatal("unjoined protected tree")
		}
		t.Logf("helper_pid=%d actual_joined=%t session_empty=%t files=%d entries=%d bytes=%d manifest=%s helper_sha256=%s", tree.pid, tree.joined, tree.empty, tree.identity.Files, tree.identity.Entries, tree.identity.Bytes, tree.identity.ManifestSHA256, tree.identity.HelperSHA256)
	}
	for _, stage := range []*linuxEpochInputStage{owner.catalogStage, owner.configStage} {
		rows, err := os.ReadDir(stage.root.path)
		if err != nil || len(rows) != 0 {
			t.Fatal("mutable input staging survived successful construction")
		}
	}
	addresses := map[string]bool{}
	for index := range 5 {
		epoch, err := owner.Check(t.Context(), uint64(index+1))
		if err != nil {
			t.Fatal("protected config check", err)
		}
		if addresses[epoch.Listen] {
			t.Fatal("duplicate listener reservation")
		}
		addresses[epoch.Listen] = true
		if index == 0 {
			fifth, e := owner.Check(t.Context(), 5)
			if e != nil {
				t.Fatal("fifth metadata check")
			}
			probe, e := net.Listen("tcp4", fifth.Listen)
			if probe != nil {
				_ = probe.Close()
			}
			if !errors.Is(e, unix.EADDRINUSE) {
				t.Fatal("Check released fifth reservation", e)
			}
		}
		for later := index + 1; later < 5; later++ {
			probe, e := net.Listen("tcp4", owner.epochs[later].Listen)
			if probe != nil {
				_ = probe.Close()
			}
			if !errors.Is(e, unix.EADDRINUSE) {
				t.Fatal("later reservation released early", e)
			}
		}
		content, err := os.ReadFile(epoch.ConfigPath)
		if err != nil || SHA256(content) != epoch.ConfigSHA256 {
			t.Fatal("actual config bytes")
		}
		parsed, err := config.ParseLiteral(content)
		if err != nil || !reflect.DeepEqual(parsed, owner.parsedConfigs[index]) || parsed.ServiceCatalogs[epoch.Repository].Path != epoch.CatalogPath {
			t.Fatal("literal catalog alias changed")
		}
		catalog, err := os.ReadFile(epoch.CatalogPath)
		if err != nil || SHA256(catalog) != epoch.CatalogSHA256 {
			t.Fatal("actual catalog bytes")
		}
		for _, path := range []string{epoch.ConfigPath, epoch.CatalogPath} {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
				t.Fatal("route final component is not regular0400")
			}
			if err := os.WriteFile(path, []byte("forbidden"), 0o400); !errors.Is(err, unix.EROFS) {
				t.Fatal("protected payload writable", err)
			}
		}
		if index == 0 && owner.ReleaseListener(t.Context(), 2) == nil {
			t.Fatal("out-of-order release")
		}
		if owner.ReleaseListener(t.Context(), uint64(index+1)) != nil || owner.ReleaseListener(t.Context(), uint64(index+1)) == nil {
			t.Fatal("reservation release not ordered one-shot")
		}
		listener, err := net.Listen("tcp4", epoch.Listen)
		if err != nil {
			t.Fatal("released native address unavailable", err)
		}
		if listener.Close() != nil {
			t.Fatal("native listener close")
		}
		t.Logf("epoch=%d config_bytes=%d catalog_bytes=%d config_sha256=%s catalog_sha256=%s", index+1, len(content), len(catalog), epoch.ConfigSHA256, epoch.CatalogSHA256)
	}
	// This mechanical guard has no child: actual live borrower joining belongs
	// to c.c. It proves Close cannot retire either embedded route while active.
	owner.mu.Lock()
	owner.active = true
	owner.mu.Unlock()
	closeErr := owner.Close(t.Context())
	owner.mu.Lock()
	owner.active = false
	owner.mu.Unlock()
	if closeErr == nil || owner.closed {
		t.Fatal("active input use retired")
	}
	if _, err := owner.Check(t.Context(), 5); err != nil {
		t.Fatal("active refusal damaged tree custody")
	}
	if author.checkRoots(t.Context()) != nil || owner.parent.check() != nil {
		t.Fatal("source/lease or native parent drift")
	}
	if owner.Close(t.Context()) != nil || !owner.closed {
		t.Fatal("joined descriptor close")
	}
	if _, err := owner.Check(t.Context(), 1); err == nil {
		t.Fatal("closed inputs admitted")
	}
	if owner.ReleaseListener(t.Context(), 1) == nil {
		t.Fatal("closed reservation admitted")
	}
	if author.checkRoots(t.Context()) != nil || author.released {
		t.Fatal("epoch Close released borrowed source/lease")
	}
	if !bytes.Equal(raw, mustLinuxEpochPlan(t, planInput)) {
		t.Fatal("borrowed plan changed")
	}
	t.Logf("protected_files=8 writable_roots=4 ordered_listeners=5 source_plan_sha256=%s pid=%d", SHA256(raw), os.Getpid())
}
func mustLinuxEpochPlan(t *testing.T, input *LinuxExecutionInputCustody) []byte {
	t.Helper()
	var raw []byte
	if input.WithInput(t.Context(), "plan", func(file *os.File) error {
		var err error
		raw, err = io.ReadAll(executionInputReader{t.Context(), io.LimitReader(file, MaxPlanBytes+1)})
		return err
	}) != nil {
		t.Fatal("borrowed plan input")
	}
	return raw
}
