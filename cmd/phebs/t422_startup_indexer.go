package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/bmeddeb/phebs/internal/diagnostics"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/indexer"
)

var errT422StartupIndexer = errors.New("selected launch cannot serve without its index child")

// admitStartupIndexer resolves the same-module index children before HTTP
// serving and index-worker startup; other workers may already be running.
// Ordinary serving keeps the historical warning and
// runs without indexing. A dispatch-admitted selected launch fails closed here
// instead: without indexing its cold phase can never converge, so the parent
// would otherwise poll an impossible phase until its own deadline expired.
func admitStartupIndexer(analysisUnits bool) (bin, focused string, err error) {
	bin, err = indexer.FindBinary()
	if err != nil {
		if dispatchadmission.ProductionSemanticSelected() {
			diagnostics.Logf("selected launch refused before serving: zoekt-git-index unavailable: %v", err)
			return "", "", fmt.Errorf("%w: zoekt-git-index: %w", errT422StartupIndexer, err)
		}
		diagnostics.Logf("WARNING: zoekt-git-index unavailable — indexing disabled (make build provides the exact linked module pin; or set PHEBS_ZOEKT_GIT_INDEX): %v", err)
		return "", "", nil
	}
	focused, focusedErr := focusedindex.FindBinary()
	if focusedErr != nil && analysisUnits {
		if dispatchadmission.ProductionSemanticSelected() {
			diagnostics.Logf("selected launch refused before serving: phebs-focused-index unavailable: %v", focusedErr)
			return "", "", fmt.Errorf("%w: phebs-focused-index: %w", errT422StartupIndexer, focusedErr)
		}
		log.Print("WARNING: phebs-focused-index not found — indexing disabled for configured analysis units (make build provides it; or set PHEBS_FOCUSED_INDEX)")
		focused = ""
	}
	return bin, focused, nil
}
