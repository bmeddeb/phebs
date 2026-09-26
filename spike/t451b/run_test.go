package t451b

import (
	"context"
	"os"
	"testing"
)

func TestRunImportedRefusesUnknownProfile(t *testing.T) {
	r := validNativeRequest(t)
	r.Profile = "native-linux-arm64-rules-go-059-v1"
	for _, raw := range [][]byte{nil, []byte(`{"schema":"unknown"}`), nativeRequestBytes(r)} {
		parent := t.TempDir()
		_, removed, stage, err := runImported(context.Background(), "", parent, "", "", "", nil, raw, nil)
		entries, readErr := os.ReadDir(parent)
		if err == nil || readErr != nil || removed || stage != "admission" || len(entries) != 0 {
			t.Fatal("invalid profile reached import or sandbox", stage, removed, err, readErr)
		}
	}
}
