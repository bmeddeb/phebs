//go:build linux

package t421

import (
	"errors"
	"math/bits"
	"strconv"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/lifecycle"
)

// MED-498 focused pressure/restore composition: pure sizing and parsing
// helpers shared by the root-tagged native test. They grant no authority; the
// native body observes the actual filesystem after every mutation.

const (
	pressureRestoreBallastLimit = 80 << 30 // validExecutionPressureAllocation bound
	pressureRestoreResultPrefix = "MED498-DRIVER-RESULT "
)

var errPressureRestorePlan = errors.New("pressure restore plan refused")

// pressureRestorePercent mirrors lifecycle's ceiling percentage so the driver
// aims at the same used-percent value the product gate will compute.
func pressureRestorePercent(used, total uint64) int {
	if used == 0 || total == 0 {
		return 0
	}
	high, low := bits.Mul64(used, 100)
	percent, remainder := bits.Div64(high, low, total)
	if remainder > 0 {
		percent++
	}
	if percent > 100 {
		return 100
	}
	return int(percent)
}

// pressureRestoreBallastSize returns the 4-KiB-aligned ballast size that moves
// the filesystem's used bytes to targetPercent of total, given the current
// ballast size. It assumes allocation is charged byte for byte; the caller
// re-observes statfs and accepts only a result inside its band.
func pressureRestoreBallastSize(total, available, ballast uint64, targetPercent int) (uint64, error) {
	if total == 0 || available > total || targetPercent < 0 || targetPercent > 99 || ballast%4096 != 0 {
		return 0, errPressureRestorePlan
	}
	used := total - available
	if ballast > used {
		return 0, errPressureRestorePlan
	}
	others := used - ballast
	hi, lo := bits.Mul64(total, uint64(targetPercent))
	if hi != 0 {
		return 0, errPressureRestorePlan
	}
	want := lo / 100
	if want <= others {
		return 0, nil
	}
	size := (want - others) &^ 4095
	if size > pressureRestoreBallastLimit {
		return 0, errPressureRestorePlan
	}
	return size, nil
}

// pressureRestoreBand is an inclusive used-percent band for one phase.
type pressureRestoreBand struct {
	Name     string
	Target   int
	Low      int
	High     int
	Pressure lifecycle.Pressure
}

func (band pressureRestoreBand) contains(percent int) bool {
	return percent >= band.Low && percent <= band.High
}

// The fixed MED-498 phases against the product's 80/90/75 watermarks. Each
// band keeps a margin from the next watermark so the product's projected
// admission bytes cannot move an observation across it.
func pressureRestoreBands() [4]pressureRestoreBand {
	return [4]pressureRestoreBand{
		{Name: "collect", Target: 84, Low: 81, High: 87, Pressure: lifecycle.PressureCollect},
		{Name: "refuse", Target: 93, Low: 91, High: 96, Pressure: lifecycle.PressureRefuse},
		{Name: "latched", Target: 78, Low: 76, High: 79, Pressure: lifecycle.PressureRefuse},
		{Name: "resumed", Target: 0, Low: 0, High: 60, Pressure: lifecycle.PressureNormal},
	}
}

func validPressureRestoreBands(bands [4]pressureRestoreBand) bool {
	soft, hard, resume := lifecycle.SoftWatermarkPercent, lifecycle.HardWatermarkPercent, lifecycle.ResumeWatermarkPercent
	collect, refuse, latched, resumed := bands[0], bands[1], bands[2], bands[3]
	return collect.Low > soft && collect.High < hard && collect.contains(collect.Target) &&
		refuse.Low > hard && refuse.High < 100 && refuse.contains(refuse.Target) &&
		latched.Low > resume && latched.High < soft && latched.contains(latched.Target) &&
		resumed.High < resume && resumed.contains(resumed.Target)
}

// pressureRestoreResultLine extracts the single driver result line from the
// bounded child log. More than one line or none refuses.
func pressureRestoreResultLine(log string) (string, error) {
	found := ""
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, pressureRestoreResultPrefix) {
			continue
		}
		if found != "" {
			return "", errPressureRestorePlan
		}
		found = strings.TrimPrefix(line, pressureRestoreResultPrefix)
	}
	if found == "" {
		return "", errPressureRestorePlan
	}
	return found, nil
}

// pressureRestoreStatusUIDs parses the real and effective UIDs from one
// /proc/<pid>/status record.
func pressureRestoreStatusUIDs(raw []byte) (uint32, uint32, bool) {
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 4 {
			return 0, 0, false
		}
		real, realErr := strconv.ParseUint(fields[0], 10, 32)
		effective, effectiveErr := strconv.ParseUint(fields[1], 10, 32)
		if realErr != nil || effectiveErr != nil {
			return 0, 0, false
		}
		return uint32(real), uint32(effective), true
	}
	return 0, 0, false
}

func TestPressureRestorePercentMatchesLifecycleGate(t *testing.T) {
	gate := lifecycle.NewGateWithProbe("/unused", nil)
	for _, test := range []struct{ used, total uint64 }{
		{0, 4096}, {1, 4096}, {4096, 4096}, {80, 100}, {801, 1000}, {899, 1000}, {900, 1000}, {1 << 40, 3 << 40},
	} {
		capacity, _ := gate.CheckObserved(t.Context(), lifecycle.Capacity{
			TotalBytes: int64(test.total), AvailableBytes: int64(test.total - test.used), UsedBytes: int64(test.used),
		}, 0)
		if got := pressureRestorePercent(test.used, test.total); got != capacity.UsedPercent {
			t.Fatalf("percent(%d,%d) = %d, lifecycle = %d", test.used, test.total, got, capacity.UsedPercent)
		}
		gate = lifecycle.NewGateWithProbe("/unused", nil) // never carry the hard latch between cases
	}
}

func TestPressureRestoreBallastSizeReachesTarget(t *testing.T) {
	const total = 4 << 30
	for _, test := range []struct {
		available, ballast uint64
		target             int
	}{
		{total - 100<<20, 0, 84}, {total - 3<<30, 1 << 30, 93}, {total - 3<<30, 2 << 30, 78}, {total - 2<<30, 1 << 30, 0},
	} {
		size, err := pressureRestoreBallastSize(total, test.available, test.ballast, test.target)
		if err != nil || size%4096 != 0 {
			t.Fatal(test, size, err)
		}
		used := total - test.available - test.ballast + size
		if test.target == 0 {
			if size != 0 {
				t.Fatal("zero target keeps no ballast", size)
			}
			continue
		}
		if got := pressureRestorePercent(used, total); got != test.target {
			t.Fatalf("target %d reached %d (size %d)", test.target, got, size)
		}
	}
	for _, refused := range []struct {
		total, available, ballast uint64
		target                    int
	}{
		{0, 0, 0, 50}, {10, 11, 0, 50}, {4096, 0, 4096 + 1, 50}, {1 << 20, 0, 1 << 21, 50}, {1 << 20, 0, 0, 100}, {1 << 20, 0, 0, -1},
		{1 << 40, 1 << 40, 0, 50},
	} {
		if _, err := pressureRestoreBallastSize(refused.total, refused.available, refused.ballast, refused.target); err == nil {
			t.Fatal("accepted", refused)
		}
	}
}

func TestPressureRestoreBandsStraddleNoWatermark(t *testing.T) {
	bands := pressureRestoreBands()
	if !validPressureRestoreBands(bands) {
		t.Fatal("fixed bands cross a product watermark", bands)
	}
	broken := bands
	broken[2].High = lifecycle.SoftWatermarkPercent
	if validPressureRestoreBands(broken) {
		t.Fatal("latched band reaching the soft watermark accepted")
	}
	broken = bands
	broken[1].Low = lifecycle.HardWatermarkPercent
	if validPressureRestoreBands(broken) {
		t.Fatal("refuse band touching the hard watermark accepted")
	}
}

func TestPressureRestoreResultLineAndStatusUIDs(t *testing.T) {
	line, err := pressureRestoreResultLine("x\n" + pressureRestoreResultPrefix + `{"ok":true}` + "\ny\n")
	if err != nil || line != `{"ok":true}` {
		t.Fatal(line, err)
	}
	for _, refused := range []string{"", "none", pressureRestoreResultPrefix + "a\n" + pressureRestoreResultPrefix + "b"} {
		if _, err := pressureRestoreResultLine(refused); err == nil {
			t.Fatal("accepted", refused)
		}
	}
	real, effective, ok := pressureRestoreStatusUIDs([]byte("Name:\tx\nUid:\t1001\t1002\t1001\t1001\nGid:\t5\t5\t5\t5\n"))
	if !ok || real != 1001 || effective != 1002 {
		t.Fatal(real, effective, ok)
	}
	for _, refused := range []string{"Name:\tx\n", "Uid:\t1\t2\n", "Uid:\ta\t1\t1\t1\n"} {
		if _, _, ok := pressureRestoreStatusUIDs([]byte(refused)); ok {
			t.Fatal("accepted", refused)
		}
	}
}
