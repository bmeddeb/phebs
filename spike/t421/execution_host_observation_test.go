package t421

import "testing"

func TestExecutionFSIDIdentityEncoding(t *testing.T) {
	for _, test := range []struct {
		name    string
		fsid    [2]int32
		encoded [8]byte
	}{
		{"zero", [2]int32{}, [8]byte{}},
		{"ordered", [2]int32{1, 2}, [8]byte{0, 0, 0, 1, 0, 0, 0, 2}},
		{"negative", [2]int32{-1, -2147483648}, [8]byte{255, 255, 255, 255, 128, 0, 0, 0}},
		{"maximum", [2]int32{2147483647, -2}, [8]byte{127, 255, 255, 255, 255, 255, 255, 254}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := executionFSIDIdentity(test.fsid); got != SHA256(test.encoded[:]) {
				t.Fatal("FSID bytes are not ordered BE32 bit patterns", got)
			}
		})
	}
	if executionFSIDIdentity([2]int32{1, 2}) == executionFSIDIdentity([2]int32{2, 1}) {
		t.Fatal("FSID order discarded")
	}
}
