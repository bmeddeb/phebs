//go:build linux

package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestStartOwnedEngineRejectsForeignListenerOnPickedPort reproduces the gate
// flake where another process took the closed probe port before the child
// bound it: the other SurrealDB answered /health, and the store then failed
// to sign in ("There was a problem with authentication"). A healthy answer
// from a listener the child does not hold must move startup to a fresh port,
// a bounded number of times, and never adopt the foreign engine.
func TestStartOwnedEngineRejectsForeignListenerOnPickedPort(t *testing.T) {
	identity, err := findSurrealBinary(true)
	if err != nil {
		t.Fatal("required real SurrealDB fixture:", err)
	}
	foreign := startForeignSurrealForTest(t, identity.Path)
	original := pickLoopbackAddr
	t.Cleanup(func() { pickLoopbackAddr = original })

	for _, tc := range []struct {
		name         string
		foreignPicks int
		wantPicks    int
		wantLost     bool
	}{
		{name: "lost once then fresh port", foreignPicks: 1, wantPicks: 2},
		{name: "lost on every bounded attempt", foreignPicks: localPortAttempts, wantPicks: localPortAttempts, wantLost: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), childPassTestTimeout)
			defer cancel()
			picks := 0
			pickLoopbackAddr = func() (string, error) {
				picks++
				if picks <= tc.foreignPicks {
					return foreign, nil
				}
				return original()
			}
			s, err := OpenLocalMemory(ctx, t.TempDir())
			if s != nil {
				t.Cleanup(func() { _ = s.Close(context.Background()) })
			}
			if picks != tc.wantPicks {
				t.Fatalf("picked %d ports, want %d (open error: %v)", picks, tc.wantPicks, err)
			}
			if tc.wantLost {
				if !errors.Is(err, errLoopbackPortLost) || s != nil {
					t.Fatalf("OpenLocalMemory = %v, %v; want the proven port loss", s, err)
				}
				return
			}
			// Open signs in with this start's random root password and
			// applies the schema, which only the store's own child accepts.
			if err != nil {
				t.Fatalf("OpenLocalMemory after a lost port: %v", err)
			}
		})
	}
}

// startForeignSurrealForTest runs an unrelated in-memory engine with its own
// root password and returns the address it serves.
func startForeignSurrealForTest(t *testing.T, binary string) string {
	t.Helper()
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	ctx, cancel := context.WithTimeout(t.Context(), childPassTestTimeout)
	defer cancel()
	addr, _ := startRawSurrealOnLoopbackForTest(t, ctx, 30*time.Second, func(addr string) *exec.Cmd {
		cmd := exec.Command(binary, "start", "--bind", addr, "--user", "root", "--log", "warn", "memory")
		cmd.Env = append(os.Environ(), "SURREAL_PASS="+hex.EncodeToString(secret))
		return cmd
	})
	return addr
}
