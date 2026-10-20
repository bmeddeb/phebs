//go:build linux

package t421

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// LinuxGoReferenceHelperMain owns one fixed implemented-reference transaction.
// It never executes the supplied candidate and accepts no command selection.
func LinuxGoReferenceHelperMain() (status int) {
	status = 125
	stage := "entry"
	defer func() {
		if status != 0 {
			_, _ = fmt.Fprintf(os.Stderr, "go-reference-helper refused stage=%s\n", stage)
		}
	}()
	if runtime.GOARCH != "amd64" || len(os.Args) != 2 || os.Args[1] != linuxGoReferenceRecipe || os.Getpid() != 1 || os.Getppid() != 0 ||
		os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 {
		return status
	}
	for _, fd := range []int{3, 4, 5, 6, 7} {
		unix.CloseOnExec(fd)
	}
	stage = "exclusive-control"
	kind, kindErr := unix.GetsockoptInt(4, unix.SOL_SOCKET, unix.SO_TYPE)
	peer, peerErr := unix.GetsockoptUcred(4, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if kindErr != nil || peerErr != nil || kind != unix.SOCK_SEQPACKET || peer.Pid != 0 || peer.Uid != 0 || peer.Gid != 0 {
		return status
	}
	wire := &inputTreeSocket{fd: 4}
	startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	raw, _, err := wire.receive(startup, treeWireInit, false)
	cancel()
	if err != nil {
		return status
	}
	initial := bytes.Clone(raw)
	ctx, cancel, err := inputTreeDeadline(initial)
	if err != nil {
		return status
	}
	defer cancel()
	stage = "closed-plan"
	plan, err := receiveGoReferencePlan(ctx, wire)
	if err != nil || !inputTreeHelperMappings(initial) {
		return status
	}
	stage = "private-mounts"
	if setupInputTreeMount() != nil || unix.Mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "") != nil {
		return status
	}
	if self, err := os.Readlink("/proc/self"); err != nil || self != "1" {
		return status
	}
	workspace, err := goBuildHelperWorkspace()
	if err != nil {
		return status
	}
	stage = "original-masks"
	if maskGoReferenceOriginals(plan, workspace) != nil {
		return status
	}
	stage = "protected-copy"
	protected, err := receiveProtectedInputTree(ctx, wire)
	if err != nil {
		return status
	}
	defer func() {
		if protected.close() != nil {
			status = 126
		}
	}()
	stage = "input-shape"
	gitDigest, ok := goReferenceInputShape(protected.census)
	if !ok {
		return status
	}
	candidate := os.NewFile(7, "sealed-reference-candidate")
	defer func() {
		if candidate.Close() != nil {
			status = 126
		}
	}()
	stage = "candidate"
	if checkGoReferenceCandidate(ctx, candidate, plan) != nil {
		return status
	}
	stage = "exact-source"
	reference, err := inspectGoReferenceSource(ctx, plan, gitDigest, workspace)
	if err != nil {
		return status
	}
	ready := append(bytes.Clone(initial[16:]), protected.manifest...)
	stage = "ready-ack"
	if wire.send(ctx, treeWireReady, ready, protected.view) != nil {
		return status
	}
	ack, _, err := wire.receive(ctx, treeWireAck, false)
	if err != nil || !bytes.Equal(ack, initial[16:]) {
		return status
	}
	stage = "reference-build"
	digest, err := buildGoReference(ctx, plan, workspace, reference, candidate)
	if err != nil {
		return status
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if err != nil || len(decoded) != sha256.Size {
		return status
	}
	stage = "result-ack"
	if wire.send(ctx, treeWireEnd, append(bytes.Clone(ready), decoded...), nil) != nil {
		return status
	}
	ack, _, err = wire.receive(ctx, treeWireAck, false)
	if err != nil || !bytes.Equal(ack, initial[16:]) {
		return status
	}
	return 0
}
