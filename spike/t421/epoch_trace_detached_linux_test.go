//go:build linux

package t421

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

type linuxEpochDetachedReadControl struct {
	io.Reader
	closeErr error
	closes   int
}

func (control *linuxEpochDetachedReadControl) Close() error {
	control.closes++
	return control.closeErr
}

type linuxEpochDetachedReadError struct{ err error }

func (control linuxEpochDetachedReadError) Read([]byte) (int, error) { return 0, control.err }

func TestLinuxEpochTraceDetachedReadRefusals(t *testing.T) {
	for _, test := range []struct {
		name     string
		body     string
		readErr  error
		closeErr error
		gone     bool
		wantErr  bool
	}{
		{name: "current", body: "current stat"},
		{name: "empty-is-not-disappearance"},
		{name: "read-esrch", readErr: &os.PathError{Op: "read", Path: "/proc/control/stat", Err: unix.ESRCH}, gone: true},
		{name: "read-enoent", readErr: &os.PathError{Op: "read", Path: "/proc/control/stat", Err: unix.ENOENT}, gone: true},
		{name: "permission", readErr: unix.EACCES, wantErr: true},
		{name: "close", body: "current stat", closeErr: unix.EIO, wantErr: true},
		{name: "esrch-and-close", readErr: unix.ESRCH, closeErr: unix.EIO, wantErr: true},
		{name: "enoent-and-close", readErr: unix.ENOENT, closeErr: unix.EIO, wantErr: true},
		{name: "overflow", body: strings.Repeat("x", 4097), wantErr: true},
		{name: "overflow-and-esrch", body: strings.Repeat("x", 4097), readErr: unix.ESRCH, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := io.Reader(strings.NewReader(test.body))
			if test.readErr != nil {
				reader = io.MultiReader(reader, linuxEpochDetachedReadError{test.readErr})
			}
			control := &linuxEpochDetachedReadControl{Reader: reader, closeErr: test.closeErr}
			raw, gone, err := linuxEpochTraceDetachedRead(control)
			if control.closes != 1 || gone != test.gone || (err != nil) != test.wantErr {
				t.Fatal("bounded read/close classification", control.closes, gone, err)
			}
			if test.closeErr != nil && !errors.Is(err, test.closeErr) || test.wantErr && !errors.Is(err, ErrExecutionEpochOne) {
				t.Fatal("refusal lost its independent error", err)
			}
			if !gone && err == nil && string(raw) != test.body {
				t.Fatal("successful metadata changed", string(raw))
			}
		})
	}
}

func TestLinuxEpochTraceDetachedCurrentBindings(t *testing.T) {
	original, err := linuxEpochTraceIdentityOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		change  func(*linuxEpochTraceIdentity)
		present bool
		wantErr bool
	}{
		{name: "same-current-lifetime", change: func(*linuxEpochTraceIdentity) {}, present: true},
		{name: "new-birth-retires-only-metadata", change: func(value *linuxEpochTraceIdentity) { value.birth += "0" }},
		{name: "changed-parent", change: func(value *linuxEpochTraceIdentity) { value.parent++ }, wantErr: true},
		{name: "changed-session", change: func(value *linuxEpochTraceIdentity) { value.sid++ }, wantErr: true},
		{name: "changed-name", change: func(value *linuxEpochTraceIdentity) { value.name += "x" }, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := original
			test.change(&value)
			present, err := linuxEpochTraceDetachedCurrent(value)
			if present != test.present || (err != nil) != test.wantErr {
				t.Fatal("current detached metadata binding", present, err)
			}
		})
	}
}
