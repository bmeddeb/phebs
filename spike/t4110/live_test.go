package t4110

import (
	"testing"
	"time"
)

func TestMeasuredUTCDateUsesActualUTCDay(t *testing.T) {
	local := time.Date(
		2026, time.September, 1, 23, 30, 0, 0,
		time.FixedZone("test-offset", -2*60*60),
	)
	if got := measuredUTCDate(local); got != "2026-09-02" {
		t.Fatalf("measured UTC date = %q", got)
	}
}
