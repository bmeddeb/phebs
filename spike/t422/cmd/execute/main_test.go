package main

import (
	"bytes"
	"testing"
)

func TestWriteExecutionCommandFailure(t *testing.T) {
	for _, test := range []struct {
		name            string
		alreadyReported bool
		want            string
	}{
		{name: "generic", want: "t422-execute: authenticated execution operation unavailable\n"},
		{name: "preclaim record already reported", alreadyReported: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			writeExecutionCommandFailure(&stderr, test.alreadyReported)
			if stderr.String() != test.want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
		})
	}
}
