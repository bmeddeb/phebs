package typedindex

// T45.7 closed additional-input provider discriminants. These identifiers and
// their ordering are the only thing this file introduces: neither provider is
// implemented, registered, or available, and naming one grants no capability and
// implies no authority. The frozen Bazel provider (ProviderID) remains the sole
// execution authority. Selection definitions, discovery, workers, dispatch and
// registration are separate later leaves that must keep every v1/v2 canonical
// byte, identity and digest exact.
const (
	ModuleProviderID = "go-module-scip-v1"
	ImportProviderID = "imported-artifact-scip-v1"
)

// providerOrder is the fixed public descriptor sequence: Bazel first even when it
// is not execution-available, then the Go module provider, then the artifact
// import provider. It never reorders and never grows from caller input.
var providerOrder = [...]string{ProviderID, ModuleProviderID, ImportProviderID}

// KnownProvider reports whether id is exactly one of the three closed provider
// discriminants. It is total and byte-exact: empty, unknown, case-aliased,
// version-shifted and whitespace-padded values are all rejected. It asserts no
// availability, registration, support or authority for the matched provider.
func KnownProvider(id string) bool {
	for _, p := range providerOrder {
		if id == p {
			return true
		}
	}
	return false
}

// ProviderOrder returns the stable provider descriptor order as a fresh slice, so
// a caller cannot mutate the package-internal sequence.
func ProviderOrder() []string {
	out := make([]string, len(providerOrder))
	copy(out, providerOrder[:])
	return out
}

// ProviderDescriptors returns one capability record per known provider in the
// stable order. Only Bazel currently carries a planning contract, identical to
// Describe(); the unimplemented T45.7 module and import providers report no
// planning contract and no execution availability. Describing a provider is not
// registering it, and an unavailable Bazel card stays unavailable.
func ProviderDescriptors() []Capabilities {
	out := make([]Capabilities, 0, len(providerOrder))
	for _, id := range providerOrder {
		if id == ProviderID {
			out = append(out, Describe())
			continue
		}
		out = append(out, Capabilities{Provider: id})
	}
	return out
}
