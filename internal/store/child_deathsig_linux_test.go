//go:build linux

package store

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const deathsigHelperEnv = "PHEBS_STORE_DEATHSIG_HELPER_DIR"

// TestStoreChildDiesWithItsParent kills only the process that owns a real
// store child, as kill -9 or an OOM kill of phebs alone would, and requires
// the child to die rather than strand the data directory.
func TestStoreChildDiesWithItsParent(t *testing.T) {
	if dir := os.Getenv(deathsigHelperEnv); dir != "" {
		s, err := OpenLocal(context.Background(), dir)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(2)
		}
		fmt.Println("child", s.engine.process.Pid)
		select {} // wait to be killed
	}
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestStoreChildDiesWithItsParent$", "-test.count=1")
	helper.Env = append(os.Environ(), deathsigHelperEnv+"="+t.TempDir())
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = helper.Process.Kill(); _ = helper.Wait() }()
	lines := bufio.NewScanner(stdout)
	child := 0
	for child == 0 && lines.Scan() {
		if fields := strings.Fields(lines.Text()); len(fields) == 2 && fields[0] == "child" {
			child, _ = strconv.Atoi(fields[1])
		} else if strings.HasPrefix(lines.Text(), "error") {
			t.Fatal(lines.Text())
		}
	}
	if child == 0 {
		t.Fatal("helper never reported its store child")
	}
	if err := helper.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for syscall.Kill(child, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatalf("store child %d outlived its killed parent", child)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
