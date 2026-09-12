package t421

import (
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

const executionStartupDiagnostics = "PHEBS_T4013_STARTUP_DIAGNOSTICS=source-free-v1"

// Private role values come from the held launch custodies. These strings alone
// issue no admission; normalize observes the supplied command environment only.
type executionRuntimeEnvironmentBindings struct {
	Home, Temporary, GitDirectory string
	SurrealPath, SurrealSHA256    string
	ZoektPath, ZoektSHA256        string
}

func executionPhebsEnvironment(base []string, binding executionRuntimeEnvironmentBindings) []string {
	return append(base,
		dispatchadmission.ProductionEnvironment+"="+dispatchadmission.ProductionStoreSelector,
		"PHEBS_SURREAL="+binding.SurrealPath, "PHEBS_SURREAL_SHA256="+binding.SurrealSHA256,
		"PHEBS_ZOEKT_GIT_INDEX="+binding.ZoektPath, "PHEBS_ZOEKT_GIT_INDEX_SHA256="+binding.ZoektSHA256,
		"PHEBS_T421_EXACT_READS=source-free-v1", "PHEBS_T4013_EXACT_REPORTS=source-free-v1")
}

func executionServeEnvironment(parent []string) []string {
	server := make([]string, len(parent)+1)
	copy(server, parent)
	server[len(parent)] = executionStartupDiagnostics
	return server
}

// This is the expected V3 runtime recipe, not a measurement or build recipe.
// Legacy profiles retain their original independent arrays.
func frozenV3RuntimeEnvironment() []string {
	return []string{
		"GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=@null-device", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0",
		"GOENV=off", "GOPROXY=off", "GOSUMDB=off", "GOTELEMETRY=off", "GOTOOLCHAIN=local", "GOWORK=off",
		"HOME=@home", "LANG=C", "LC_ALL=C", "PATH=@git-exec",
		"PHEBS_SURREAL=@surreal", "PHEBS_SURREAL_SHA256=@surreal-sha256",
		"PHEBS_T4013_EXACT_REPORTS=source-free-v1", "PHEBS_T421_EXACT_READS=source-free-v1",
		"PHEBS_T422_DISPATCH=parent-bound-store-v1",
		"PHEBS_ZOEKT_GIT_INDEX=@zoekt-git-index", "PHEBS_ZOEKT_GIT_INDEX_SHA256=@zoekt-git-index-sha256",
		"TEMP=@temp", "TMP=@temp", "TMPDIR=@temp", "TZ=UTC",
	}
}

// No file read, executable lookup, hash or caller-authored verified bit is used.
// The future issuer must supply genuine held bindings and hash this ACTUAL
// normalized output, not hash frozenV3RuntimeEnvironment as an observation.
func (binding executionRuntimeEnvironmentBindings) normalize(actual []string, serve bool) ([]string, error) {
	refused := errors.New("execution runtime environment differs from bound recipe")
	for _, path := range []string{binding.Home, binding.Temporary, binding.GitDirectory, binding.SurrealPath, binding.ZoektPath} {
		if !executionGitAbsolutePath(path) {
			return nil, refused
		}
	}
	if !validExecutionSHA256(binding.SurrealSHA256) || !validExecutionSHA256(binding.ZoektSHA256) {
		return nil, refused
	}
	want := frozenV3RuntimeEnvironment()
	if serve {
		want = append(want, executionStartupDiagnostics)
	}
	if len(actual) != len(want) {
		return nil, refused
	}
	bound := map[string][2]string{
		"HOME": {binding.Home, "@home"}, "PATH": {binding.GitDirectory, "@git-exec"},
		"TEMP": {binding.Temporary, "@temp"}, "TMP": {binding.Temporary, "@temp"}, "TMPDIR": {binding.Temporary, "@temp"},
		"GIT_CONFIG_GLOBAL": {os.DevNull, "@null-device"},
		"PHEBS_SURREAL":     {binding.SurrealPath, "@surreal"}, "PHEBS_SURREAL_SHA256": {binding.SurrealSHA256, "@surreal-sha256"},
		"PHEBS_ZOEKT_GIT_INDEX": {binding.ZoektPath, "@zoekt-git-index"}, "PHEBS_ZOEKT_GIT_INDEX_SHA256": {binding.ZoektSHA256, "@zoekt-git-index-sha256"},
	}
	normalized := make([]string, 0, len(actual))
	seen := make(map[string]bool, len(actual))
	for _, entry := range actual {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || seen[key] || strings.ContainsAny(entry, "\x00\r\n") {
			return nil, refused
		}
		seen[key] = true
		if role, ok := bound[key]; ok {
			if value != role[0] {
				return nil, refused
			}
			value = role[1]
		}
		normalized = append(normalized, key+"="+value)
	}
	slices.Sort(normalized)
	slices.Sort(want)
	if !slices.Equal(normalized, want) {
		return nil, refused
	}
	return normalized, nil
}
