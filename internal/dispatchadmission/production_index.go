package dispatchadmission

import (
	"runtime"
	"strings"
)

const IndexOfferEnvironment = "PHEBS_T422_INDEX_OFFERS"

func validProductionToolEnvironment(environment []string, role, semantic string) bool {
	if foregroundGitSelected(environment) {
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || semantic != ProductionSemanticV3 || (role != "git" && role != "zoekt-git-index") {
			return false
		}
		var ok bool
		environment, ok = legacyForegroundGitEnvironment(environment)
		if !ok {
			return false
		}
	}
	var selected []string
	var mask uint8
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case IndexOfferEnvironment, "ZOEKT_DISABLE_CATFILE_BATCH":
			if semantic != ProductionSemanticV3 || role != "zoekt-git-index" {
				return false
			}
			bit, want := uint8(1), "v1"
			if key == "ZOEKT_DISABLE_CATFILE_BATCH" {
				bit, want = 2, "true"
			}
			if value != want || mask&bit != 0 {
				return false
			}
			mask |= bit
		}
	}
	if mask == 0 {
		return validProductionEnvironment(environment, role != "surreal")
	}
	if mask != 3 || len(environment) > 64 {
		return false
	}
	for _, entry := range environment {
		if !strings.HasPrefix(entry, IndexOfferEnvironment+"=") && !strings.HasPrefix(entry, "ZOEKT_DISABLE_CATFILE_BATCH=") {
			selected = append(selected, entry)
		}
	}
	return validProductionEnvironment(selected, true)
}

func foregroundGitSelected(environment []string) bool {
	for _, entry := range environment {
		if entry == "GIT_CONFIG_COUNT=5" || strings.HasPrefix(entry, "GIT_CONFIG_KEY_3=") || strings.HasPrefix(entry, "GIT_CONFIG_KEY_4=") || strings.HasPrefix(entry, "GIT_CONFIG_VALUE_3=") || strings.HasPrefix(entry, "GIT_CONFIG_VALUE_4=") {
			return true
		}
	}
	return false
}

// Normalize only the closed foreground recipe for the unchanged legacy checks.
// Automatic maintenance stays enabled; its original parent must retain its Wait.
func legacyForegroundGitEnvironment(environment []string) ([]string, bool) {
	if len(environment) > 64 {
		return nil, false
	}
	selected := make([]string, 0, len(environment))
	var mask uint8
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		var bit uint8
		switch key {
		case "GIT_CONFIG_COUNT":
			bit = 1
			if value != "5" {
				return nil, false
			}
			selected = append(selected, "GIT_CONFIG_COUNT=3")
		case "GIT_CONFIG_KEY_3":
			bit = 2
			if value != "gc.autoDetach" {
				return nil, false
			}
		case "GIT_CONFIG_VALUE_3":
			bit = 4
			if value != "false" {
				return nil, false
			}
		case "GIT_CONFIG_KEY_4":
			bit = 8
			if value != "maintenance.autoDetach" {
				return nil, false
			}
		case "GIT_CONFIG_VALUE_4":
			bit = 16
			if value != "false" {
				return nil, false
			}
		default:
			selected = append(selected, entry)
		}
		if bit&mask != 0 {
			return nil, false
		}
		mask |= bit
	}
	return selected, mask == 31
}

func hasProductionIndexMode(environment []string) bool {
	var mode, goGit bool
	for _, entry := range environment {
		mode = mode || entry == IndexOfferEnvironment+"=v1"
		goGit = goGit || entry == "ZOEKT_DISABLE_CATFILE_BATCH=true"
	}
	return mode && goGit
}

// FailProductionIndexObservation makes unavailable selected native coverage
// sticky, even if the ordinary index worker classifies the returned error.
func FailProductionIndexObservation() error {
	if lifetime := productionRuntime.Load(); lifetime != nil && lifetime.client != nil {
		return lifetime.client.fail(ErrProtocol)
	}
	return ErrProductionBootstrap
}
