package t4013

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCeremonyDriverCleanupTrapSurvivesFunctionScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ceremony driver is a Bash script")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(root, "run-large-mac-ceremony.sh")
	marker := filepath.Join(t.TempDir(), "cleanup marker")
	plan := filepath.Join(t.TempDir(), "plan with spaces.json")
	prepared := filepath.Join(t.TempDir(), "prepared 'quoted'.json")
	script := `
source "$1"
cleanup_prepared() {
  printf '%s\n%s\n' "$1" "$2" > "$MARKER"
}
exercise_scope() {
  local plan_path="$PLAN_PATH" prepared_path="$PREPARED_PATH" cleanup_trap
  cleanup_trap="$(cleanup_trap_command "$plan_path" "$prepared_path")"
  trap "$cleanup_trap" EXIT
  false
}
exercise_scope
`
	command := exec.Command("bash", "-c", script, "take11-cleanup-test", driver)
	command.Env = append(os.Environ(),
		"MARKER="+marker,
		"PLAN_PATH="+plan,
		"PREPARED_PATH="+prepared,
	)
	output, runErr := command.CombinedOutput()
	if runErr == nil {
		t.Fatal("synthetic preparation failure unexpectedly succeeded")
	}
	if bytes.Contains(output, []byte("unbound variable")) {
		t.Fatalf("cleanup trap lost its scoped state: %s", output)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("cleanup trap did not run: %v; output=%s", err, output)
	}
	want := plan + "\n" + prepared + "\n"
	if string(raw) != want {
		t.Fatalf("cleanup arguments = %q, want %q", raw, want)
	}
}

func TestCeremonyDriverRetiresTakeTen(t *testing.T) {
	raw, err := os.ReadFile("run-large-mac-ceremony.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `REVIEW_STOPPED_CEREMONY_ID_10="t40r1-neutral-10"`) {
		t.Fatal("neutral-10 is not permanently retired")
	}
}
