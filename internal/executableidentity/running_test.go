package executableidentity

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRunningDigestReplacementHelper(t *testing.T) {
	if os.Getenv("PHEBS_RUNNING_IMAGE_HELPER") != "1" {
		return
	}
	for i := range 2 {
		if i > 0 {
			var signal [1]byte
			if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
				t.Fatal(err)
			}
		}
		digest, err := RunningDigest()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(digest)
	}
}

func TestRunningDigestSurvivesSymlinkLaunchAndPathReplacement(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	dir := t.TempDir()
	path := filepath.Join(dir, "image")
	target, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy image: %v, %v", copyErr, closeErr)
	}
	want, err := Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "launch")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, link, "-test.run=^TestRunningDigestReplacementHelper$")
	child.Env = append(os.Environ(), "PHEBS_RUNNING_IMAGE_HELPER=1")
	child.Stderr = os.Stderr
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = child.Wait() }()
	lines := bufio.NewScanner(output)
	if !lines.Scan() || lines.Text() != want {
		t.Fatalf("symlink launch digest = %q, want %q (%v)", lines.Text(), want, lines.Err())
	}
	// Deploy different bytes at the launch path while the child stays alive.
	// Linux must still digest an unlinked image. Darwin requires the original
	// vnode to remain reachable and must never digest the replacement instead.
	if runtime.GOOS == "darwin" {
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
	}
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, []byte("replacement image"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if !lines.Scan() || lines.Text() != want {
		t.Fatalf("post-deployment digest = %q, want running image %q (%v)", lines.Text(), want, lines.Err())
	}
	for lines.Scan() {
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
}
