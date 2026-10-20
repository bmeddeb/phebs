//go:build linux

package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxAuthorCaptureFixedChild(t *testing.T) {
	mode := os.Getenv("PHEBS_T42_AUTHOR_CAPTURE_CHILD")
	if mode == "" {
		return
	}
	var before5, before6, after unix.Stat_t
	if unix.Fstat(5, &before5) != nil || unix.Fstat(6, &before6) != nil {
		os.Exit(125)
	}
	inputs, err := CaptureLinuxExecutionAuthorInputs()
	if inputs != nil || err == nil || unix.Fstat(6, &after) != nil || !sameLinuxInputStat(before6, after) {
		os.Exit(125)
	}
	if mode == "ordinary" {
		if unix.Fstat(5, &after) != nil || !sameLinuxInputStat(before5, after) {
			os.Exit(125)
		}
	} else if mode == "partial" {
		if !errors.Is(unix.Fstat(5, &after), unix.EBADF) {
			os.Exit(125)
		}
	} else {
		os.Exit(125)
	}
	_, _ = fmt.Fprintln(os.Stdout, "unproven descriptors preserved; owned partial input released")
	os.Exit(0)
}

func TestLinuxAuthorCaptureRefusalPreservesUnprovenFDs(t *testing.T) {
	ordinary, err := os.OpenFile(filepath.Join(t.TempDir(), "ordinary"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ordinary.Close() }()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image := requireLinuxInputCustody(t, linuxInputSelection(t, executable, "image", true))
	for _, mode := range []string{"ordinary", "partial"} {
		t.Run(mode, func(t *testing.T) {
			err := image.WithInput(t.Context(), "image", func(image *os.File) error {
				selected := ordinary
				if mode == "partial" {
					selected = image
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				command := exec.Command(os.Args[0], "-test.run=^TestLinuxAuthorCaptureFixedChild$")
				command.Env = []string{"PHEBS_T42_AUTHOR_CAPTURE_CHILD=" + mode, "GOMAXPROCS=2", "GORACE=atexit_sleep_ms=0"}
				command.ExtraFiles = []*os.File{ordinary, ordinary, selected, ordinary}
				output := &checkoutCommandOutput{remaining: 4096, cancel: cancel}
				command.Stdout, command.Stderr = output, output
				if err := command.Start(); err != nil {
					return err
				}
				fd, waited, pinErr := pinExecutionProcessRoot(command)
				if pinErr == nil {
					defer func() { _ = unix.Close(fd) }()
				}
				waitErr := <-waited
				if pinErr != nil || waitErr != nil || output.err != nil || output.buffer.String() != "unproven descriptors preserved; owned partial input released\n" {
					return errors.New("capture refusal closed an unproven descriptor or leaked owned partial")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
