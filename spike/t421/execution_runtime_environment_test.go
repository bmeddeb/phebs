package t421

import (
	"slices"
	"strings"
	"testing"
)

func TestExecutionRuntimeEnvironment(t *testing.T) {
	// Supplied private paths/digests test normalization, not actual custody,
	// host admission, command execution or startup-log delivery.
	binding := executionRuntimeEnvironmentBindings{
		Home: "/private/home", Temporary: "/private/tmp", GitDirectory: "/private/git",
		SurrealPath: "/private/surreal", SurrealSHA256: "sha256:" + strings.Repeat("1", 64),
		ZoektPath: "/private/zoekt", ZoektSHA256: "sha256:" + strings.Repeat("2", 64),
	}
	base := externalToolEnvironment(binding.Temporary)
	for index, entry := range base {
		if strings.HasPrefix(entry, "HOME=") {
			base[index] = "HOME=" + binding.Home
		} else if strings.HasPrefix(entry, "PATH=") {
			base[index] = "PATH=" + binding.GitDirectory
		}
	}
	baseBefore := slices.Clone(base)
	parent := executionPhebsEnvironment(base, binding)
	parentBefore := slices.Clone(parent)
	server := executionServeEnvironment(parent)
	if len(parent) != 28 || len(server) != 29 || !slices.Equal(base, baseBefore) || !slices.Equal(parent, parentBefore) {
		t.Fatal("command environment classes or borrowed slices changed")
	}
	for _, test := range []struct {
		name  string
		serve bool
		edit  func([]string) []string
	}{
		{"recovery", false, nil}, {"serve", true, nil},
		{"missing", true, func(v []string) []string { return v[1:] }},
		{"duplicate", true, func(v []string) []string { v[1] = v[0]; return v }},
		{"unknown", true, func(v []string) []string { v[0] = "UNDECLARED=1"; return v }},
		{"build_only", true, func(v []string) []string { v[0] = "CGO_ENABLED=0"; return v }},
		{"wrong_home", true, func(v []string) []string { v[0] = "HOME=/private/other"; return v }},
		{"wrong_digest", true, func(v []string) []string {
			for i, entry := range v {
				if strings.HasPrefix(entry, "PHEBS_SURREAL_SHA256=") {
					v[i] = "PHEBS_SURREAL_SHA256=sha256:" + strings.Repeat("3", 64)
				}
			}
			return v
		}},
		{"recovery_startup", false, func(v []string) []string { return append(v, executionStartupDiagnostics) }},
		{"serve_without_startup", true, func(v []string) []string { return v[:len(v)-1] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := slices.Clone(parent)
			if test.serve {
				actual = slices.Clone(server)
			}
			if test.edit != nil {
				actual = test.edit(actual)
			}
			before := slices.Clone(actual)
			observed, err := binding.normalize(actual, test.serve)
			if (err == nil) != (test.edit == nil) || !slices.Equal(actual, before) {
				t.Fatal("actual environment normalization", err)
			}
			if err == nil {
				want := frozenExecutionEnvironment(Plan{Schema: PlanV3Schema}, ExecutionProfileAdmissionBinding{}).BaseVariables
				if test.serve {
					want = append(want, executionStartupDiagnostics)
				}
				slices.Sort(want)
				if !slices.Equal(observed, want) {
					t.Fatal("actual builder differs from V3 expected recipe")
				}
			}
		})
	}
	// The serve-only append must not mutate either the recovery or nested-tool
	// environment, including when the caller's slice has spare capacity.
	shared := make([]string, len(parent), len(parent)+2)
	copy(shared, parent)
	served := executionServeEnvironment(shared)
	served[0] = "changed"
	if !slices.Equal(shared, parent) {
		t.Fatal("serve environment aliases parent")
	}
}

func TestExecutionRuntimeEnvironmentVersioning(t *testing.T) {
	for _, schema := range []string{PlanSchema, PlanV2Schema, PlanV3Schema} {
		value := frozenExecutionEnvironment(Plan{Schema: schema}, ExecutionProfileAdmissionBinding{})
		if schema == PlanV3Schema {
			if value.Schema != "t422-closed-execution-environment-v3" || len(value.BaseVariables) != 28 ||
				!slices.Equal(value.ServerVariables, []string{executionStartupDiagnostics}) {
				t.Fatal("V3 runtime recipe", value)
			}
			continue
		}
		wantServer := 5
		if schema == PlanV2Schema {
			wantServer++
		}
		if value.Schema != "t422-closed-execution-environment-v1" || len(value.BaseVariables) != 37 || len(value.ServerVariables) != wantServer ||
			!slices.Contains(value.BaseVariables, "CGO_ENABLED=0") ||
			!slices.Contains(value.BaseVariables, "PHEBS_BUF_SHA256=@buf-sha256") ||
			slices.Contains(value.BaseVariables, "PHEBS_T422_DISPATCH=parent-bound-store-v1") {
			t.Fatal("legacy environment changed", schema, value)
		}
	}
	plan := accountingTestPlan(t)
	if len(plan.ToolPolicy.RequiredTools) != 12 {
		t.Fatal("runtime projection weakened independent tool inventory")
	}
}
