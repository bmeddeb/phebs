package launcher

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestRunNativeCompatibilityAsNobody(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Getuid() != 65534 {
		t.Skip("Linux amd64 uid 65534 attempt")
	}
	plan, roots := nativeCompatibilityFixture("amd64")
	prepared, err := PrepareCompatibility(plan, roots, "load")
	if err != nil {
		t.Fatal(err)
	}
	matches := nativeRows(prepared, plan)
	_, err = RunNativeCompatibility(context.Background(), plan, roots, "load", matches)
	if err == nil || err.Error() != "open /scratch/workspace/lib/lib.go: no such file or directory" {
		t.Fatalf("uid=%d driver=%s err=%v", os.Getuid(), NativeCompatDriverDigest("amd64"), err)
	}
	if strings.Contains(err.Error(), "admitted Linux worker") {
		t.Fatal(err)
	}
}
