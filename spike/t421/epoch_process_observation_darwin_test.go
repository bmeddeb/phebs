//go:build darwin

package t421

import (
	"context"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t4013"
)

// Exercise the fixed /bin/sh entry point, not a mocked kernel name or a
// directly launched bash. Its ready byte follows the native dispatcher exec.
func TestEpochProcessNativeSystemShell(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "printf x; read answer")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdout.Close() }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	var ready [1]byte
	if _, err := io.ReadFull(stdout, ready[:]); err != nil || ready[0] != 'x' {
		t.Fatal("shell readiness", err)
	}
	rows, err := t4013.ObserveProcessTreeRecords(ctx, cmd.Process.Pid)
	if err != nil || len(rows) != 1 {
		t.Fatal("native shell census", rows, err)
	}
	names, err := epochProcessNames("/private/tools/phebs", []dispatchadmission.ProductionToolBinding{
		{Role: "git", Path: "/private/tools/git"}, {Role: "surreal", Path: "/private/tools/surreal"},
		{Role: "zoekt-git-index", Path: "/private/tools/zoekt-git-index"},
	})
	if err != nil || names[rows[0].ObservedName] != "sh" {
		t.Fatal("fixed shell native name is not classified", rows, err)
	}
	gauge, err := NewProcessObservationGauge(cmd.Process.Pid, rows[0].StartIdentity, names)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := gauge.acceptRows(rows)
	if err != nil || !observation.Available || observation.CompletedCensuses != 1 {
		t.Fatal("actual shell row refused", observation, err)
	}
	t.Logf("fixed /bin/sh observed as %q; name classification only", rows[0].ObservedName)
}
