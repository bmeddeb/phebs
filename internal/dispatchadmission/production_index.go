package dispatchadmission

import "strings"

const IndexOfferEnvironment = "PHEBS_T422_INDEX_OFFERS"

func validProductionToolEnvironment(environment []string, role, semantic string) bool {
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
