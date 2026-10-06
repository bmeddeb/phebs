package planner

const (
	NativeDriverSHA256Arm64 = "f49a0ff4339e32cc699c6fbb5a80b9d8f936b3bfe6b08a924fa19495e35b2bfd"
	NativeDriverSHA256Amd64 = "2b58a9c9a294fc8d9c899bd66f881f7236ed4422a998a4cebab07662ec373bb8"
)

// NativeGoProfile binds source constraints and native driver build identity to
// the same fixed Linux Go 1.25.0 architecture variant.
type NativeGoProfile struct {
	DriverSHA256, VariantKey, Variant, ToolTag string
}

var nativeGoProfiles = map[string]NativeGoProfile{
	"arm64": {NativeDriverSHA256Arm64, "GOARM64", "v8.0", "arm64.v8.0"},
	"amd64": {NativeDriverSHA256Amd64, "GOAMD64", "v1", "amd64.v1"},
}

// NativeProfile returns only the closed native architecture profiles.
func NativeProfile(arch string) (NativeGoProfile, bool) {
	profile, ok := nativeGoProfiles[arch]
	return profile, ok
}

// SupportedGoMode rejects modes outside the fixed native Linux profiles.
func SupportedGoMode(mode GoMode) bool {
	_, ok := NativeProfile(mode.GOARCH)
	return mode.GOOS == "linux" && ok && len(mode.Tags) <= 64
}
