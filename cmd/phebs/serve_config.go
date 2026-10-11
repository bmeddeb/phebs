package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/extractors/protodecl"
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
	released, err := releasedExtractors(d.ctx, cfg)
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

// releaseLoadBindings supplies what a released record must match before it may
// load: the artifact resolver, the running implementation identity and the
// present referenced-artifacts root. Production derives all three from present
// fact (release_identity.go): the running binary, toolchain and
// pack-implementation identity are machine-derived, and the
// referenced-artifacts root and resolver come from one census of the configured
// artifact directory. It stays a variable only so tests can substitute a
// fixture build's bindings. With no artifacts directory configured it returns
// unbound options, so a governing released record refuses startup
// (unresolved_reference) instead of loading without a binding.
var releaseLoadBindings = deriveReleaseLoadBindings

// releasedExtractors computes the ordinary/released admission set from the
// verified signed PackRelease records named by cfg.ReleaseSelection. It is the
// sole path into that set and is read once at this admitted startup boundary,
// never on a request, sync, or per-query path:
//
//   - An empty Path admits nothing, performs no read, and adds no pack work.
//   - A configured Path whose records fail verification refuses startup, and a
//     governing released record refuses it unless releaseLoadBindings actually
//     binds it to this binary and its artifacts.
//   - A pack whose governing record is suspended, retired, design, shadow or
//     experimental-dark, has expired or is not yet approved, or whose
//     release_id the operator revoked, is withdrawn rather than refused: it is
//     not admitted and no older record replaces it.
//     Each withdrawal is named in one bounded startup log line, so suspension
//     and rollback are observable instead of only an absent pack.
//   - A verified released pack with no fixed in-tree recipe is an unresolved
//     release inconsistency and refuses startup rather than silently admitting
//     nothing, so a released authorization can never outrun its implementation.
func releasedExtractors(ctx context.Context, cfg *config.Config) ([]extract.Extractor, error) {
	if cfg.ReleaseSelection.Path == "" {
		return nil, nil
	}
	keys, err := releaseKeyRing(cfg.ReleaseSelection.Keys)
	if err != nil {
		return nil, err
	}
	opts := packrelease.Options{Keys: keys, Revoked: revokedReleaseIDs(cfg.ReleaseSelection.Revoked)}
	selection, err := packrelease.LoadSelectionWithBindings(ctx, cfg.ReleaseSelection.Path, opts,
		func(ctx context.Context) (packrelease.Options, error) {
			return releaseLoadBindings(ctx, cfg.ReleaseSelection.ArtifactsPath)
		})
	if err != nil {
		return nil, fmt.Errorf("pack release selection: %w", err)
	}
	// Named before the empty-selection return: a directory holding only
	// suspended or revoked records admits nothing, which is exactly when the
	// operator needs to see that the withdrawal took effect.
	logWithdrawnSelection(selection.Withdrawn())
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
		public, err := packrelease.ParsePublicKey(key.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("pack release key %q: %w", key.ID, err)
		}
		ring[key.ID] = public
	}
	return ring, nil
}

// revokedReleaseIDs reshapes the operator's configured revocation list into the
// lookup the selection judges a governing record against. Configuration parsing
// already proved each entry unique and within a release_id's grammar, so this
// only changes the shape. An empty list yields nil, which keeps the dark default
// allocation-free and the selection's behavior unchanged.
func revokedReleaseIDs(ids []string) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}
	revoked := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		revoked[id] = struct{}{}
	}
	return revoked
}

// maxLoggedWithdrawals bounds how much withdrawal detail one startup logs. The
// selection already bounds the governing-record count, so this caps only the
// diagnostic's own length and never the selection's contents.
const maxLoggedWithdrawals = 16

// logWithdrawnSelection names the packs a configured selection directory holds
// that are not admitted, so a suspension or a revocation has an observable
// startup effect instead of only an absent pack. It discloses a pack identifier
// taken from an authenticated record (already within the identifier grammar)
// and a bounded cause, never a record's contents, signature, artifacts or any
// repository data.
func logWithdrawnSelection(withdrawn []packrelease.Withdrawal) {
	if len(withdrawn) == 0 {
		return
	}
	detailed := withdrawn
	if len(detailed) > maxLoggedWithdrawals {
		detailed = detailed[:maxLoggedWithdrawals]
	}
	pairs := make([]string, 0, len(detailed))
	for _, entry := range detailed {
		pairs = append(pairs, entry.PackID+"="+entry.Cause)
	}
	suffix := ""
	if omitted := len(withdrawn) - len(detailed); omitted > 0 {
		suffix = fmt.Sprintf(", %d more omitted", omitted)
	}
	log.Printf("pack release selection: %d configured pack(s) not admitted: %s%s",
		len(withdrawn), strings.Join(pairs, ", "), suffix)
}

// mergeExtractors concatenates the provisional-dark and released admission sets
// so each domain registers exactly once. A released recipe governs its domain:
// a dark extractor for the same domain, at any version, is dropped rather than
// registered beside it or preferred over it. Order is dark-then-released for
// deterministic registry construction. With no released extractors it returns
// the dark set unchanged, preserving today's behavior byte for byte.
func mergeExtractors(dark, released []extract.Extractor) []extract.Extractor {
	if len(released) == 0 {
		return dark
	}
	releasedDomains := make(map[string]struct{}, len(released))
	for _, extractor := range released {
		releasedDomains[extractor.Domain()] = struct{}{}
	}
	merged := make([]extract.Extractor, 0, len(dark)+len(released))
	for _, extractor := range dark {
		if _, governed := releasedDomains[extractor.Domain()]; !governed {
			merged = append(merged, extractor)
		}
	}
	seen := make(map[string]struct{}, len(released))
	for _, extractor := range released {
		if _, duplicate := seen[extractor.Domain()]; duplicate {
			continue
		}
		seen[extractor.Domain()] = struct{}{}
		merged = append(merged, extractor)
	}
	return merged
}

// callerMapPackID is the stable pack identity of the Caller Map recipe frozen
// by T47.2a: `phebs.grpc.caller.go` in docs/PROTO_GRPC_PACK_CARDS.md ("Exact
// Go gRPC callers"), grpc-caller 1.5.0, schema t20-caller-v1. T47.5 signs the
// first caller-specific released record against exactly this identity and, in
// the same PR, binds callerMapRecipe into packRecipes above.
const callerMapPackID = "phebs.grpc.caller.go"

// callerMapRecipe is the fixed in-tree extractor set implementing the T47.2a
// frozen Go/gRPC-Protobuf recipe. The declaration extractor travels with the
// caller extractor on purpose: resolved callers are attributed through
// repository-committed declaration lineage, so the recipe names every
// extractor the product reads. A released Caller Map therefore admits its
// necessary declaration discovery because this recipe carries it, never
// because the experimental protobuf umbrella happened to be switched on.
//
// The recipe is deliberately NOT registered in packRecipes in this build:
// binding it would assert a released authorization that does not exist until a
// signed record for callerMapPackID passes verification (T47.5's promotion).
// Until then a configured selection naming this pack refuses startup as an
// unbound release, so Caller Map cannot reach ordinary activation without its
// release record and its binding landing together.
func callerMapRecipe() []extract.Extractor {
	return []extract.Extractor{
		protodecl.New(),
		gocaller.NewGRPC(),
	}
}
