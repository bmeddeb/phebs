package recovery

import (
	"math"
	"testing"
)

func TestArchiveCheckpointMaximum(t *testing.T) {
	if BackupCheckpointMaximum() != 16 {
		t.Fatal("Create failed-prefix checkpoint bound changed")
	}
	for _, tt := range []struct {
		transactions uint64
		want         uint32
		valid        bool
	}{
		{0, 0, false}, {1, 0, false}, {2, 34, true},
		{3, 36, true}, {100000, 200030, true},
		{(math.MaxUint32-34)/2 + 2, math.MaxUint32 - 1, true},
		{(math.MaxUint32-34)/2 + 3, 0, false},
		{math.MaxUint64, 0, false},
	} {
		got, err := RestoreCheckpointMaximum(tt.transactions)
		if (err == nil) != tt.valid || got != tt.want {
			t.Fatalf("transactions=%d got=%d error=%v want=%d", tt.transactions, got, err, tt.want)
		}
	}
}
