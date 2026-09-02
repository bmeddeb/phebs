package t4110

import (
	"os"
	"testing"
)

func TestT4110TransitionSmoke(t *testing.T) {
	if os.Getenv("PHEBS_T4110_SMOKE") != "1" {
		t.Skip("set PHEBS_T4110_SMOKE=1 to run the disposable Surreal smoke")
	}
	report, err := RunSmoke(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Revisions != 3 || report.FinalServices != 5 ||
		report.FinalAccepted != 2 || report.ReaddIncarnation != 2 ||
		!report.StoreClosed || !report.CustodyRemoved {
		t.Fatalf("smoke report = %+v", report)
	}
}
