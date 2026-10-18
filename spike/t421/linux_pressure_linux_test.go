//go:build linux

package t421

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLinuxPressureRefusesBeforeMutation(t *testing.T) {
	request := LinuxPressureRequest{Parent: "/never-created-pressure-parent", ImageBytes: linuxPressureMinimumImage,
		FormatterSHA256: "sha256:" + strings.Repeat("a", 64)}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, cancelExpired := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelExpired()
	for _, ctx := range []context.Context{nil, context.Background(), canceled, expired} {
		if owner, err := PrepareLinuxPressureVolume(ctx, request); owner != nil || !errors.Is(err, ErrLinuxPressure) {
			t.Fatal("missing/expired deadline reached native preparation", owner, err)
		}
	}
	ctx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	request.ImageBytes = 0
	if owner, err := PrepareLinuxPressureVolume(ctx, request); owner != nil || !errors.Is(err, ErrLinuxPressure) {
		t.Fatal("invalid request reached native preparation", owner, err)
	}
	for _, owner := range []*LinuxPressureVolume{nil, {closed: true}, {failed: true}, {removalUsed: true}} {
		if err := owner.Remove(ctx); !errors.Is(err, ErrLinuxPressure) {
			t.Fatal("unowned/closed/failed/spent state reached teardown", err)
		}
	}
	var absent *LinuxPressureVolume
	if absent.Close() != nil || absent.WithRoot(ctx, func(*os.File) error { t.Fatal("nil owner lent a root"); return nil }) == nil {
		t.Fatal("nil owner lifecycle changed")
	}
	removed := &LinuxPressureVolume{removed: true}
	if removed.Remove(ctx) != nil {
		t.Fatal("completed exact removal ceased to be idempotent")
	}
}
