package t4013

import (
	"strings"
	"testing"
)

func TestHostToolchainMismatchNamesOnlyTheClosedTool(t *testing.T) {
	expected := fakeHostToolchain()
	for index := range expected {
		observed := append([]HostToolObservation(nil), expected...)
		observed[index].SHA256 = "sha256:" + strings.Repeat("f", 64)
		err := compareHostToolchain(expected, observed)
		want := "T40.13 host toolchain differs from the frozen plan: " + expected[index].Name
		if err == nil || err.Error() != want ||
			strings.Contains(err.Error(), observed[index].Version) ||
			strings.Contains(err.Error(), observed[index].SHA256) {
			t.Fatalf("mismatch %d = %v, want %q", index, err, want)
		}
	}

	short := append([]HostToolObservation(nil), expected[:len(expected)-1]...)
	if err := compareHostToolchain(expected, short); err == nil ||
		err.Error() != "T40.13 host toolchain inventory differs from the frozen plan" {
		t.Fatalf("short inventory mismatch = %v", err)
	}
}
