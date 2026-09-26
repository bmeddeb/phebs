package lib

import "testing"

func TestInternal(t *testing.T) {
	if Value() < 1 {
		t.Fatal("value")
	}
}
