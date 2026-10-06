package launcher

import "testing"

func TestNativeCompatDriverPinsStayArchSpecific(t *testing.T) {
	if NativeDriverSHA256 != "f49a0ff4339e32cc699c6fbb5a80b9d8f936b3bfe6b08a924fa19495e35b2bfd" {
		t.Fatal("arm64 driver pin changed")
	}
	if NativeDriverSHA256Amd64 != "2b58a9c9a294fc8d9c899bd66f881f7236ed4422a998a4cebab07662ec373bb8" {
		t.Fatal("amd64 driver pin changed")
	}
	if !AdmittedNativeCompatArch("amd64") || !AdmittedNativeCompatArch("arm64") || AdmittedNativeCompatArch("386") {
		t.Fatal("compatibility architecture set changed")
	}
	if NativeCompatDriverDigest("arm64") != NativeDriverSHA256 || NativeCompatDriverDigest("amd64") != NativeDriverSHA256Amd64 || NativeCompatDriverDigest("386") != "" {
		t.Fatal("compatibility driver digest is ambiguous")
	}
}
