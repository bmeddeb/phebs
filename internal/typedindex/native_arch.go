package typedindex

import "runtime"

// AdmittedNativeArch is the ceremony host set. arm64 remains valid. amd64 is
// the same closed Linux contract on a host that is not arm64.
func AdmittedNativeArch(arch string) bool {
	return arch == "arm64" || arch == "amd64"
}

// AdmittedNativeVariant is the pinned Go 1.25 microarchitecture for that host.
func AdmittedNativeVariant(arch, goarm64, goamd64 string) bool {
	switch arch {
	case "arm64":
		return goarm64 == "v8.0"
	case "amd64":
		return goamd64 == "v1"
	default:
		return false
	}
}

// AdmittedNativeToolTag is the build-constraint tag for that pinned variant.
func AdmittedNativeToolTag(arch string) (string, bool) {
	switch arch {
	case "arm64":
		return "arm64.v8.0", true
	case "amd64":
		return "amd64.v1", true
	default:
		return "", false
	}
}

// AdmittedNativeWorker is the ceremony process identity. UID and PID checks
// stay at each call site.
func AdmittedNativeWorker() bool {
	return runtime.GOOS == "linux" && AdmittedNativeArch(runtime.GOARCH)
}
