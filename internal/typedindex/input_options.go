package typedindex

// T45.7 closed additional-input provider discriminants. Describing an input
// contract grants no runtime registration or execution availability.
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

// ProviderDescriptors returns the implemented planning contracts in stable
// order. Runtime registration is separate; every descriptor stays unavailable.
func ProviderDescriptors() []Capabilities {
	out := make([]Capabilities, 0, len(providerOrder))
	for _, id := range providerOrder {
		if id == ProviderID {
			out = append(out, Describe())
			continue
		}
		out = append(out, Capabilities{Provider: id, PlanningContract: true})
	}
	return out
}
