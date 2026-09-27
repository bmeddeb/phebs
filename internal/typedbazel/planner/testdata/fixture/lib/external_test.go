package lib_test

import (
	"example.test/neutral/lib"
	"testing"
)

func TestExternal(t *testing.T) {
	if lib.Value() < 1 {
		t.Fatal("value")
	}
}
