// Package callerpublication owns complete caller-generation visibility. Leaf
// artifacts remain independently durable and invisible until named by one
// validated complete manifest and matching store pointer.
package callerpublication

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/candidate"
)

const (
	ManifestSchema = "phebs-caller-generation-publication-manifest-v1"
	StateSchema    = "phebs-caller-generation-publication-state-v1"
	MarkerSchema   = "phebs-caller-generation-publication-marker-v1"

	MaxManifestBytes = 32 << 20
	maxMarkerBytes   = 4 << 10
)

var (
	ErrInvalidManifest  = errors.New("invalid caller publication manifest")
	ErrPublishing       = errors.New("caller publication is in progress")
	ErrPublicationIO    = errors.New("caller publication I/O failure")
	ErrRegistryConflict = errors.New("caller publication registry has another current generation")
)

// PairReceipt is the exact successful result artifact for one ordered caller
// domain/leaf pair. A complete manifest has no failure disposition: any
// missing or terminal pair prevents the manifest from being built.
type PairReceipt struct {
	Pair    callerleaf.PairIdentity `json:"pair"`
	Receipt callerleaf.Receipt      `json:"receipt"`
}

type Manifest struct {
	Schema        string                        `json:"schema"`
	Generation    callerleaf.GenerationIdentity `json:"generation"`
	PairSetDigest string                        `json:"pair_set_digest"`
	Pairs         []PairReceipt                 `json:"pairs"`
	Aggregate     callerleaf.AggregateReceipt   `json:"aggregate"`
	Digest        string                        `json:"digest"`
}

// State is the primitive store-pointer projection of a complete filesystem
// publication. It carries the full semantic generation rather than relying on
// a mutable control row to reconstruct visibility after the fact.
type State struct {
	Schema         string                        `json:"schema"`
	Generation     callerleaf.GenerationIdentity `json:"generation"`
	PairSetDigest  string                        `json:"pair_set_digest"`
	Aggregate      callerleaf.AggregateReceipt   `json:"aggregate"`
	ManifestDigest string                        `json:"manifest_digest"`
	Manifest       string                        `json:"manifest"`
}

type marker struct {
	Schema           string `json:"schema"`
	Repository       string `json:"repository"`
	GenerationDigest string `json:"generation_digest"`
	ManifestDigest   string `json:"manifest_digest"`
	Manifest         string `json:"manifest"`
}

// BuildManifest recomputes every semantic digest and aggregate from the exact
// ordered successful pair projection. Caller-supplied aggregate or digest
// values are never persisted.
func BuildManifest(
	generation callerleaf.GenerationIdentity,
	pairs []PairReceipt,
) (Manifest, error) {
	generation.Extractors = slices.Clone(generation.Extractors)
	if err := callerleaf.ValidateGenerationIdentity(generation); err != nil {
		return Manifest{}, fmt.Errorf("%w: generation: %v", ErrInvalidManifest, err)
	}
	if pairs == nil || len(pairs) > callerleaf.MaxExpectedPairs {
		return Manifest{}, fmt.Errorf("%w: pair set is nil or unbounded", ErrInvalidManifest)
	}
	identities := make([]callerleaf.PairIdentity, len(pairs))
	aggregate := callerleaf.AggregateReceipt{}
	owned := slices.Clone(pairs)
	for index := range owned {
		pair := owned[index]
		if err := callerleaf.ValidatePairIdentity(generation, pair.Pair); err != nil {
			return Manifest{}, fmt.Errorf("%w: pair %d: %v", ErrInvalidManifest, index, err)
		}
		if err := callerleaf.ValidateReceipt(generation, pair.Pair, pair.Receipt); err != nil {
			return Manifest{}, fmt.Errorf("%w: receipt %d: %v", ErrInvalidManifest, index, err)
		}
		if err := aggregate.Add(pair.Receipt); err != nil {
			return Manifest{}, fmt.Errorf("%w: aggregate: %v", ErrInvalidManifest, err)
		}
		identities[index] = pair.Pair
	}
	aggregate.PeakOpenFiles = callerleaf.MaxOpenFiles
	pairSetDigest, err := callerleaf.PairSetDigest(generation, identities)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: pair set: %v", ErrInvalidManifest, err)
	}
	manifest := Manifest{
		Schema: ManifestSchema, Generation: generation,
		PairSetDigest: pairSetDigest, Pairs: owned, Aggregate: aggregate,
	}
	digest, raw, err := manifestDigest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if len(raw)+1 > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds byte limit", ErrInvalidManifest)
	}
	manifest.Digest = digest
	if len(canonicalManifest(manifest)) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: manifest exceeds byte limit", ErrInvalidManifest)
	}
	return manifest, nil
}

func ValidateManifest(manifest Manifest) error {
	wanted, err := BuildManifest(manifest.Generation, manifest.Pairs)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(wanted, manifest) {
		return fmt.Errorf("%w: derived fields differ", ErrInvalidManifest)
	}
	return nil
}

func (manifest Manifest) State() State {
	return State{
		Schema: StateSchema, Generation: cloneGeneration(manifest.Generation),
		PairSetDigest: manifest.PairSetDigest, Aggregate: manifest.Aggregate,
		ManifestDigest: manifest.Digest,
		Manifest: callerpublicationid.ManifestName(
			manifest.Generation.Digest, manifest.Digest,
		),
	}
}

func cloneGeneration(
	generation callerleaf.GenerationIdentity,
) callerleaf.GenerationIdentity {
	generation.Extractors = slices.Clone(generation.Extractors)
	generation.Upstream = slices.Clone(generation.Upstream)
	return generation
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.Generation = cloneGeneration(manifest.Generation)
	manifest.Pairs = slices.Clone(manifest.Pairs)
	return manifest
}

func cloneState(state State) State {
	state.Generation = cloneGeneration(state.Generation)
	return state
}

func ValidateState(state State) error {
	if state.Schema != StateSchema {
		return errors.New("unsupported caller publication state schema")
	}
	if err := callerleaf.ValidateGenerationIdentity(state.Generation); err != nil {
		return err
	}
	if state.Manifest != callerpublicationid.ManifestName(
		state.Generation.Digest, state.ManifestDigest,
	) || !validDigest(state.ManifestDigest) || !validDigest(state.PairSetDigest) {
		return errors.New("caller publication state manifest identity is invalid")
	}
	if state.Aggregate.PairCount < 0 ||
		state.Aggregate.PairCount > callerleaf.MaxExpectedPairs ||
		state.Aggregate.ArtifactCount != state.Aggregate.PairCount ||
		state.Aggregate.ResultCount < 0 ||
		state.Aggregate.ResultCount > callerleaf.MaxAggregateResultRecords ||
		state.Aggregate.AbstentionCount < 0 ||
		state.Aggregate.AbstentionCount > callerleaf.MaxAggregateAbstentionRecords ||
		state.Aggregate.CoverageRecordCount < 0 ||
		state.Aggregate.CoverageRecordCount > callerleaf.MaxAggregateCoverageRecords ||
		state.Aggregate.CoverageRecordCount > state.Aggregate.PairCount ||
		state.Aggregate.CoveredCandidateCount < 0 ||
		state.Aggregate.CoveredCandidateCount > callerleaf.MaxAggregateCoveredCandidates ||
		state.Aggregate.CoveredCandidateCount >
			state.Aggregate.PairCount*candidate.MaxRecordsPerArtifact ||
		(state.Aggregate.CoverageRecordCount == 0) !=
			(state.Aggregate.CoveredCandidateCount == 0) ||
		state.Generation.CallerPolicy.LeafAdapter != callerleaf.LeafAdapterV2 &&
			state.Aggregate.CoverageRecordCount != 0 ||
		state.Aggregate.CanonicalBytes < 0 ||
		state.Aggregate.CanonicalBytes > callerleaf.MaxAggregateCanonicalBytes ||
		state.Aggregate.StagingBytes != state.Aggregate.CanonicalBytes ||
		state.Aggregate.PeakOpenFiles != callerleaf.MaxOpenFiles {
		return errors.New("caller publication state aggregate is invalid")
	}
	return nil
}

func validateExpected(
	generation callerleaf.GenerationIdentity,
	extractors []callerleaf.ExtractorIdentity,
) bool {
	if extractors == nil {
		return false
	}
	input := generation
	input.Extractors = slices.Clone(extractors)
	wanted, err := callerleaf.NewGenerationIdentity(input)
	return err == nil && reflect.DeepEqual(wanted, generation)
}

func manifestDigest(manifest Manifest) (string, []byte, error) {
	manifest.Digest = ""
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", nil, fmt.Errorf("encode caller publication manifest: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("phebs-caller-generation-publication-manifest-v1\x00"))
	_, _ = hash.Write(raw)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), raw, nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && hex.EncodeToString(decoded) == value[len("sha256:"):]
}
