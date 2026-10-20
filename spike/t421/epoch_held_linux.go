//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bmeddeb/phebs/spike/t4013"
	"golang.org/x/sys/unix"
)

type linuxEpochHeldFlow struct {
	builds                                      *LinuxExecutionGoBuildCustody
	git                                         *LinuxExecutionGitCustody
	epochs                                      *LinuxExecutionEpochConfigCustody
	author                                      *LinuxExecutionAuthorCustody
	flow                                        *LinuxExecutionEpochFlow
	buildRoot, gitRoot, catalogRoot, configRoot *os.File
	images                                      [4]*os.File
	tools                                       [4]*LinuxExecutionToolCustody
	imageIdentities                             [4]ExecutionToolIdentity
	imageEntries                                [4]linuxExecutionInput
	plan                                        *os.File
	planEntry                                   linuxExecutionInput
	controllerPID                               int
	controllerStart                             string
	resources                                   *linuxEpochResourceAdmission
	whole                                       *epochProcessObservation
	controllerImage, shellImage                 *os.File
	shellPath                                   string
	shellInfo                                   os.FileInfo
	pidNamespace                                os.FileInfo
	servers                                     map[*ExecutionEpochOneRun]*linuxEpochServer
	gitImages                                   [7]*os.File
	workspace                                   linuxAuthorRoot
}

func (held *linuxEpochHeldFlow) withLoans(use func() error) error {
	held.builds.mu.Lock()
	defer held.builds.mu.Unlock()
	if held.builds.active != nil || held.builds.closing || held.builds.released || held.builds.err != nil || held.builds.tree == nil || !linuxAuthorBuildBinding(held.author.request, held.author.authorIdentity, held.builds.request.PlanSourceCommit) {
		return ErrExecutionEpochOne
	}
	return held.builds.tree.WithRoot(context.Background(), func(root *os.File) error {
		held.buildRoot = root
		held.git.mu.Lock()
		defer held.git.mu.Unlock()
		if held.git.closing || held.git.released || held.git.err != nil || held.git.tree == nil {
			return ErrExecutionEpochOne
		}
		return held.git.tree.WithRoot(context.Background(), func(root *os.File) error {
			held.gitRoot = root
			roles := [4]string{"t422-author", "phebs", "zoekt-git-index", "surreal"}
			seen := make(map[*LinuxExecutionInputCustody]bool, 4)
			for i, tool := range held.tools {
				if tool == nil || tool.input == nil || tool.identity.Role != roles[i] || seen[tool.input] {
					return ErrExecutionEpochOne
				}
				seen[tool.input] = true
			}
			var images func(int) error
			images = func(index int) error {
				if index < 4 {
					return held.tools[index].WithImage(context.Background(), roles[index], func(identity ExecutionToolIdentity, image *os.File) error {
						held.images[index] = image
						held.imageIdentities[index] = identity
						held.imageEntries[index] = held.tools[index].input.inputs[roles[index]]
						return images(index + 1)
					})
				}
				return held.author.request.Plan.WithInput(context.Background(), "plan", func(plan *os.File) error {
					held.plan = plan
					held.planEntry = held.author.request.Plan.inputs["plan"]
					return held.epochs.catalogs.WithRoot(context.Background(), func(root *os.File) error {
						held.catalogRoot = root
						return held.epochs.configs.WithRoot(context.Background(), func(root *os.File) (retErr error) {
							held.configRoot = root
							if held.openWorkspace() != nil {
								return ErrExecutionEpochOne
							}
							closeKeeper := func(file *os.File) {
								if file != nil && file.Close() != nil {
									retErr = errors.Join(retErr, ErrExecutionEpochOne)
									if held.flow.failureStep == "" {
										held.flow.failureStep = "cleanup"
									}
								}
							}
							defer func() { closeKeeper(held.workspace.file) }()
							if held.openClassifierImages() != nil {
								return ErrExecutionEpochOne
							}
							defer func() {
								closeKeeper(held.controllerImage)
								closeKeeper(held.shellImage)
								for _, image := range held.gitImages {
									if image != nil {
										closeKeeper(image)
									}
								}
							}()
							held.servers = make(map[*ExecutionEpochOneRun]*linuxEpochServer, 3)
							err := use()
							if err != nil && held.flow.failureStep == "" {
								held.flow.failureStep = held.flow.step
							}
							// No callback or input loan returns until every operational owner has
							// actually joined, including error prefixes with no public result.
							cleanup := held.join()
							if cleanup != nil && held.flow.failureStep == "" {
								held.flow.failureStep = "cleanup"
							}
							return errors.Join(err, cleanup)
						})
					})
				})
			}
			return images(0)
		})
	})
}
func (held *linuxEpochHeldFlow) openClassifierImages() error {
	var err error
	held.pidNamespace, err = os.Stat("/proc/self/ns/pid")
	if err != nil {
		return ErrExecutionEpochOne
	}
	held.controllerImage, err = os.Open("/proc/self/exe")
	if err != nil {
		return ErrExecutionEpochOne
	}
	held.shellPath, err = filepath.EvalSymlinks("/bin/sh")
	if err == nil {
		held.shellImage, err = t4013.OpenHostImage(held.shellPath)
	}
	if err == nil {
		held.shellInfo, err = held.shellImage.Stat()
	}
	if err != nil || held.shellInfo == nil || !held.shellInfo.Mode().IsRegular() || held.shellInfo.Mode().Perm()&0o111 == 0 {
		_ = held.controllerImage.Close()
		if held.shellImage != nil {
			_ = held.shellImage.Close()
		}
		return ErrExecutionEpochOne
	}
	for i, name := range executionGitImageNames() {
		fd, e := unix.Openat(int(held.gitRoot.Fd()), "core/"+name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			for _, image := range held.gitImages {
				if image != nil {
					_ = image.Close()
				}
			}
			_ = held.controllerImage.Close()
			_ = held.shellImage.Close()
			return ErrExecutionEpochOne
		}
		held.gitImages[i] = os.NewFile(uintptr(fd), "protected-git-alias")
	}
	return nil
}
func (held *linuxEpochHeldFlow) check(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || held == nil || held.builds == nil || held.builds.tree == nil || held.git == nil || held.git.tree == nil || held.epochs == nil || held.epochs.catalogs == nil || held.epochs.configs == nil || held.author == nil || held.flow == nil || held.flow.held != held || held.plan == nil || held.buildRoot == nil || held.gitRoot == nil || held.catalogRoot == nil || held.configRoot == nil || held.builds.tree.checkRoot() != nil || held.git.tree.checkRoot() != nil || held.epochs.catalogs.checkRoot() != nil || held.epochs.configs.checkRoot() != nil || !linuxInputProtected(held.plan, held.planEntry) || !linuxInputProtected(held.planEntry.file, held.planEntry) {
		return ErrExecutionEpochOne
	}
	for i := range held.images {
		if !linuxInputProtected(held.images[i], held.imageEntries[i]) || !linuxInputProtected(held.imageEntries[i].file, held.imageEntries[i]) || held.imageIdentities[i] != held.tools[i].identity {
			return ErrExecutionEpochOne
		}
	}
	if held.imageIdentities[0] != held.author.authorIdentity || held.tools[1].referenceInputs != held.builds || held.tools[2].referenceInputs != held.builds || !linuxAuthorBuildBinding(held.author.request, held.author.authorIdentity, held.builds.request.PlanSourceCommit) {
		return ErrExecutionEpochOne
	}
	held.author.mu.Lock()
	defer held.author.mu.Unlock()
	held.epochs.mu.Lock()
	defer held.epochs.mu.Unlock()
	if held.author.borrowedBy != held.flow || held.epochs.borrowedBy != held.flow || !held.epochs.active || held.author.checkRoots(ctx) != nil || held.epochs.checkNativeRoots(ctx) != nil || held.epochs.authorInputs.check() != nil || held.checkWorkspace() != nil {
		return ErrExecutionEpochOne
	}
	current, err := os.Lstat(held.shellPath)
	after, statErr := held.shellImage.Stat()
	selected, resolveErr := filepath.EvalSymlinks("/bin/sh")
	if err != nil || statErr != nil || resolveErr != nil || selected != held.shellPath || !inputCustodySame(held.shellInfo, current) || !inputCustodySame(held.shellInfo, after) {
		return ErrExecutionEpochOne
	}
	return nil
}
func linuxEpochFDRoute(pid int, file *os.File) string {
	return "/proc/" + strconv.Itoa(pid) + "/fd/" + strconv.FormatUint(uint64(file.Fd()), 10)
}
func (held *linuxEpochHeldFlow) startWholeObservation(ctx context.Context) error {
	gauge, name, err := held.gauge(ctx, held.controllerPID, held.controllerStart)
	if err != nil {
		return err
	}
	held.whole, err = held.resources.startProcessObservation(ctx, 2, name, gauge)
	return err
}
func (held *linuxEpochHeldFlow) join() error {
	held.flow.cancel()
	var result error
	for run := range held.servers {
		run.stopOnce.Do(func() { close(run.stop) })
		// Context cancellation is not a join; keep this worker and every loan if
		// the actual root, receiver, callback or descendant cannot settle.
		<-run.done
		for !run.joinedEmpty() {
			time.Sleep(time.Second)
		}
	}
	if held.flow.core != nil {
		result = errors.Join(result, held.flow.core.Close())
	}
	if held.whole != nil {
		_, err := held.whole.close()
		result = errors.Join(result, err)
	}
	if held.resources != nil {
		result = errors.Join(result, held.resources.Close())
	}
	return result
}
