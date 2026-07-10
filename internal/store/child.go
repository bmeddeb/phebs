package store

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// startLocal launches a supervised `surreal` child with surrealkv storage
// under dataDir, waits until it is healthy, and returns its endpoint plus a
// stop func. This replaces in-process embedding — see the 2026-07-09 ADR:
// the official Go SDK has no embedded engine and surrealdb.c.go (v0.1.0)
// would drag a Rust/CGo build into every dev and CI environment.
func startLocal(ctx context.Context, dataDir string) (endpoint string, stop func(), err error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("pick port: %w", err)
	}
	addr := l.Addr().String()
	// ponytail: close-then-reuse leaves a tiny port race; fine for a local
	// child. Revisit only if flaky in CI.
	_ = l.Close()

	// ponytail: root/root is loopback-only for the supervised child; real
	// creds arrive with the fleet profile's server mode (P6).
	cmd := exec.CommandContext(ctx, "surreal", "start",
		"--bind", addr,
		"--user", "root", "--pass", "root",
		"--log", "warn",
		"surrealkv:"+filepath.Join(dataDir, "db"),
	)
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start surreal child: %w", err)
	}
	stop = func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}

	if err := waitHealthy(ctx, addr); err != nil {
		stop()
		return "", nil, err
	}
	return "ws://" + addr, stop, nil
}

func waitHealthy(ctx context.Context, addr string) error {
	deadline := time.Now().Add(15 * time.Second)
	url := "http://" + addr + "/health"
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp, err := http.Get(url) //nolint:gosec // loopback child, constructed addr
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("surreal child on %s not healthy after 15s", addr)
}
