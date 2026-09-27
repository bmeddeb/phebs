package typedexecutor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

func TestTypedLifecycleCursorAndAdmission(t *testing.T) {
	for _, v := range []lifecycleCursor{{}, {After: "sha256:" + strings.Repeat("a", 64)}, {Root: "sha256:" + strings.Repeat("b", 64)}, {After: "sha256:" + strings.Repeat("a", 64), Root: "sha256:" + strings.Repeat("b", 64)}} {
		raw := encodeLifecycleCursor(v)
		got, e := decodeLifecycleCursor(raw)
		if e != nil || got != v {
			t.Fatal(raw, got, e)
		}
	}
	for _, raw := range []string{"{}", " ", `{"after":"foreign","root":""}`, `{"after":"","root":"","extra":1}`, strings.Repeat("x", 181)} {
		if _, e := decodeLifecycleCursor(raw); e == nil {
			t.Fatal(raw)
		}
	}
	result := (LifecycleOwner{}).Sweep(context.Background(), time.Now(), "", lifecycle.DefaultLimits())
	if result.Err == nil || result.Completeness != lifecycle.Unavailable || result.Deleted != 0 {
		t.Fatal(result)
	}
}
