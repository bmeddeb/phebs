package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/packrelease"
	"github.com/bmeddeb/phebs/internal/resolvermaterialize"
)

// serveExactMode reads the exact-report and exact-read mode switches.
func serveExactMode() (exactReports, exactReads bool, err error) {
	exactReports, err = t4013ExactReportsEnabled()
	if err != nil {
		return false, false, err
	}
	exactReads, err = t421ExactReadsEnabled()
	if err != nil {
		return false, false, err
	}
	return exactReports, exactReads, nil
}

// readServeSemanticLaunch reads the admitted semantic launch request and
// validates the serve selection against the exact modes.
func readServeSemanticLaunch(flags *serveFlags, exactReads, exactReports bool) (*t422SemanticLaunch, error) {
	semanticLaunch, err := readT422SemanticLaunch(dispatchadmission.ProcessContext(), os.Stdin)
	if err != nil {
		return nil, err
	}
	if err := semanticLaunch.validateServeSelection(flags.addr, flags.positional, exactReads, exactReports); err != nil {
		return nil, err
	}
	return semanticLaunch, nil
}

// loadServeConfig loads the server config for the admitted launch and binds
// the synthetic demo fixtures selected by environment.
func loadServeConfig(semanticLaunch *t422SemanticLaunch, flags *serveFlags) (*config.Config, []byte, error) {
	cfg, rawConfig, err := semanticLaunch.loadConfig(flags.configPath, flags.allowInsecurePerms)
	if err != nil {
		return nil, nil, err
	}
	reportT4013Startup("config_loaded")
	if fixture := os.Getenv("PHEBS_T307_NEUTRAL_SERVICE_REPO"); fixture != "" {
		if err := bindT307NeutralServiceDemo(cfg, fixture); err != nil {
			return nil, nil, err
		}
		log.Printf(
			"WARNING: neutral T30.7 focused-service demo enabled from %s; provisional evidence remains validation-gated",
			fixture,
		)
	}
	if catalog := os.Getenv("PHEBS_T335_SERVICE_CATALOG"); catalog != "" {
		fixture := os.Getenv("PHEBS_T307_NEUTRAL_SERVICE_REPO")
		if err := bindT335ServiceDirectoryDemo(cfg, fixture, catalog); err != nil {
			return nil, nil, err
		}
		log.Printf(
			"WARNING: neutral T33.5 multi-service directory enabled from %s; catalog metadata establishes no relationship or accuracy claim",
			catalog,
		)
	}
	if fixture, catalog := os.Getenv("PHEBS_T344_SERVICE_SEARCH_REPO"),
		os.Getenv("PHEBS_T344_SERVICE_SEARCH_CATALOG"); fixture != "" || catalog != "" {
		if err := bindT344ServiceSearchDemo(cfg, fixture, catalog); err != nil {
			return nil, nil, err
		}
		log.Printf(
			"WARNING: neutral T34.4 whole-repository service-search demo enabled; scope receipts establish no evidence, accuracy, or release claim",
		)
	}
	if fixture := os.Getenv("PHEBS_THRIFT_FIELD_DEMO_REPO"); fixture != "" {
		if err := bindSyntheticThriftFieldDemo(cfg, fixture); err != nil {
			return nil, nil, err
		}
		log.Printf(
			"WARNING: synthetic Thrift field-zero repository enabled from %s; not production evidence",
			fixture,
		)
	}
	if flags.addr != "" {
		cfg.Server.Addr = flags.addr
	}
	return cfg, rawConfig, nil
}

// newServeExtractionRegistries wires the extractor set and the resolver,
// caller-leaf, and caller-publication registries. The extractor set is the union
// of the provisional-dark admission (unchanged, gated by the experimental
// switches) and the ordinary/released admission computed solely from verified
// signed PackRelease records. Registration into the released set is never
// obtained by toggling a provisional extraction switch.
func newServeExtractionRegistries(d *serveDeps) error {
	cfg := d.cfg
	dark := evidenceExtractors(
		cfg.Experimental.ProvisionalProtoExtraction,
		cfg.Experimental.ProvisionalThriftExtraction,
		cfg.Experimental.ProvisionalThriftFieldExtraction,
		cfg.Experimental.ProvisionalKafkaExtraction,
	)
	released, err := releasedExtractors(cfg)
	if err != nil {
		return err
	}
	d.exs = mergeExtractors(dark, released)
	resolverRegistry, err := resolvermaterialize.NewRegistry(d.exs)
	if err != nil {
		return err
	}
	d.resolverRegistry = resolverRegistry
	callerRegistry, err := callerexecute.NewRegistry(d.exs)
	if err != nil {
		return err
	}
	d.callerRegistry = callerRegistry
	d.callerPublications = callerpublication.NewRegistry(
		callerexecute.Root(cfg.Server.DataDir),
	)
	return nil
}

// packRecipes is the fixed in-tree registry mapping a canonical pack identifier
// to the extractors that implement it. It ships EMPTY on purpose: no pack has
// yet earned a signed released PackRelease, so binding a recipe now would assert
// an authorization that does not exist. Each owning epic binds its recipe here in
// the same PR that records its first passing released record. There is no
// third-party loader and no manifest-selected arbitrary code: a released pack
// resolves only through this exact in-tree registry.
var packRecipes = map[string]func() []extract.Extractor{}

// releasedExtractors computes the ordinary/released admission set from the
// verified signed PackRelease records named by cfg.ReleaseSelection. It is the
// sole path into that set and is read once at this admitted startup boundary,
// never on a request, sync, or per-query path:
//
//   - An empty Path admits nothing, performs no read, and adds no pack work.
//   - A configured Path whose records fail verification refuses startup.
//   - A verified released pack with no fixed in-tree recipe is an unresolved
//     release inconsistency and refuses startup rather than silently admitting
//     nothing, so a released authorization can never outrun its implementation.
func releasedExtractors(cfg *config.Config) ([]extract.Extractor, error) {
	if cfg.ReleaseSelection.Path == "" {
		return nil, nil
	}
	keys, err := releaseKeyRing(cfg.ReleaseSelection.Keys)
	if err != nil {
		return nil, err
	}
	selection, err := packrelease.LoadSelection(
		cfg.ReleaseSelection.Path, packrelease.Options{Keys: keys},
	)
	if err != nil {
		return nil, fmt.Errorf("pack release selection: %w", err)
	}
	if selection.Empty() {
		return nil, nil
	}
	released := make([]extract.Extractor, 0, selection.Count())
	for _, packID := range selection.PackIDs() {
		recipe, ok := packRecipes[packID]
		if !ok {
			return nil, fmt.Errorf(
				"pack release selection: released pack %q has no fixed in-tree recipe; "+
					"refusing to admit an unbound release", packID,
			)
		}
		released = append(released, recipe()...)
	}
	return released, nil
}

// releaseKeyRing builds the operator-controlled ed25519 trust anchor from the
// configured keys. A malformed key refuses startup; the ring is never populated
// from a release record's own embedded key material.
func releaseKeyRing(keys []config.ReleaseKey) (packrelease.KeyRing, error) {
	ring := make(packrelease.KeyRing, len(keys))
	for _, key := range keys {
		public, err := base64.StdEncoding.DecodeString(key.PublicKey)
		if err != nil || len(public) != ed25519.PublicKeySize {
			return nil, fmt.Errorf(
				"pack release key %q: public_key is not a base64 %d-byte ed25519 public key",
				key.ID, ed25519.PublicKeySize,
			)
		}
		ring[key.ID] = ed25519.PublicKey(public)
	}
	return ring, nil
}

// mergeExtractors concatenates the provisional-dark and released admission sets,
// de-duplicating by extractor identity (Domain + Version) so a pack that is both
// dark-flagged and released registers exactly once. Order is dark-then-released
// for deterministic registry construction. With no released extractors it returns
// the dark set unchanged, preserving today's behavior byte for byte.
func mergeExtractors(dark, released []extract.Extractor) []extract.Extractor {
	if len(released) == 0 {
		return dark
	}
	merged := make([]extract.Extractor, 0, len(dark)+len(released))
	seen := make(map[string]struct{}, len(dark)+len(released))
	for _, extractor := range append(append([]extract.Extractor{}, dark...), released...) {
		identity := extractor.Domain() + "@" + extractor.Version()
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		merged = append(merged, extractor)
	}
	return merged
}
