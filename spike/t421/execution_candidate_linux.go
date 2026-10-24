//go:build linux

package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// Duplicate the inherited kernel object, never copy it into a different image
// or relax the public canonical-path constructors. This issues bytes only;
// protected reference comparison must still derive the executor identity.
func protectLinuxExecutorCandidateFD(ctx context.Context, image *os.File) (_ *LinuxExecutionToolCustody, retErr error) {
	return protectLinuxExecutorCandidateFDObserved(ctx, image, nil)
}

func protectLinuxExecutorCandidateFDObserved(ctx context.Context, image *os.File, observation *executionPreworkObservation) (_ *LinuxExecutionToolCustody, retErr error) {
	if runtime.GOARCH != "amd64" || ctx == nil || ctx.Err() != nil || image == nil {
		return nil, ErrExecutionToolCustody
	}
	var original unix.Stat_t
	if unix.Fstat(int(image.Fd()), &original) != nil {
		return nil, ErrExecutionToolCustody
	}
	fd, err := unix.FcntlInt(image.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, ErrExecutionToolCustody
	}
	file := os.NewFile(uintptr(fd), "executor-candidate")
	defer func() {
		if retErr != nil {
			_ = observation.close(file.Close())
		}
	}()
	entry := linuxExecutionInput{file: file}
	if unix.Fstat(fd, &entry.stat) != nil || !sameLinuxInputStat(original, entry.stat) || entry.stat.Size <= 0 || entry.stat.Size > maxInputCustodyFileBytes {
		return nil, ErrExecutionToolCustody
	}
	entry.identity = LinuxExecutionInputIdentity{Name: "t422-execute", Bytes: entry.stat.Size, Executable: true}
	if !linuxInputProtected(file, entry) || !linuxInputELF(file, entry.stat.Size) {
		return nil, ErrExecutionToolCustody
	}
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	size, err := io.CopyBuffer(hash, executionInputReader{ctx, io.NewSectionReader(file, 0, entry.stat.Size)}, buffer)
	if err != nil || size != entry.stat.Size || !linuxInputProtected(file, entry) || ctx.Err() != nil {
		return nil, ErrExecutionToolCustody
	}
	entry.identity.SHA256 = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return &LinuxExecutionToolCustody{input: &LinuxExecutionInputCustody{inputs: map[string]linuxExecutionInput{"t422-execute": entry}, observation: observation}}, nil
}
