//go:build linux

package loopbackowner

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestCheckAttributesListenerToProcess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	// An established client socket holds a port that no process listens on.
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })

	for _, tc := range []struct {
		name string
		pid  int
		addr string
		want error
		fail bool
	}{
		{name: "owner", pid: os.Getpid(), addr: listener.Addr().String()},
		{name: "foreign", pid: other.Process.Pid, addr: listener.Addr().String(), want: ErrForeign},
		{name: "not listening", pid: os.Getpid(), addr: client.LocalAddr().String(), want: ErrNotListening},
		{name: "no port", pid: os.Getpid(), addr: "127.0.0.1", fail: true},
		{name: "zero port", pid: os.Getpid(), addr: "127.0.0.1:0", fail: true},
		{name: "invalid pid", pid: 0, addr: listener.Addr().String(), fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Check(tc.pid, tc.addr)
			switch {
			case tc.fail:
				if err == nil || errors.Is(err, ErrForeign) || errors.Is(err, ErrNotListening) {
					t.Fatalf("Check = %v, want an input refusal", err)
				}
			case !errors.Is(err, tc.want) || (tc.want == nil && err != nil):
				t.Fatalf("Check = %v, want %v", err, tc.want)
			}
		})
	}
}
