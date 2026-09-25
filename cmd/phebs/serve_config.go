package main

import (
	"log"
	"os"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
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
// caller-leaf, and caller-publication registries.
func newServeExtractionRegistries(d *serveDeps) error {
	cfg := d.cfg
	d.exs = evidenceExtractors(
		cfg.Experimental.ProvisionalProtoExtraction,
		cfg.Experimental.ProvisionalThriftExtraction,
		cfg.Experimental.ProvisionalThriftFieldExtraction,
		cfg.Experimental.ProvisionalKafkaExtraction,
	)
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
