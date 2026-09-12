package repositoryindex

import (
	"context"
	"encoding/hex"
	"math"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/readaccounting"
)

// Only selected (or explicitly test-observed) invocations retain uniqueness.
// The leading byte distinguishes SHA-1 from SHA-256 without retaining paths.
type sourceCensusObservation struct {
	phase           uint32
	seen            map[[33]byte]int64
	logical, unique uint64
	failed          bool
	invalid         bool
	succeeded       bool
	regularOwners   uint64
}

func startSourceCensusObservation(ctx context.Context) (*sourceCensusObservation, error) {
	phase, err := dispatchadmission.ObserveProductionSourceCensus(ctx, readaccounting.SourceCensusBegin, 0, 0, 0)
	if err != nil || phase == 0 {
		return nil, err
	}
	return &sourceCensusObservation{phase: phase, seen: make(map[[33]byte]int64)}, nil
}

func (observation *sourceCensusObservation) add(record SourceRecord) (err error) {
	if observation == nil {
		return nil
	}
	defer func() {
		if err != nil {
			observation.invalid = true
		}
	}()
	if record.Kind != "regular" || record.DeclaredBytes < 0 {
		return invalidf("invalid regular census observation")
	}
	size := uint64(record.DeclaredBytes)
	if size > math.MaxUint64-observation.logical {
		return invalidf("source census byte overflow")
	}
	var key [33]byte
	key[0] = byte(len(record.ObjectID) / 2)
	if key[0] != 20 && key[0] != 32 {
		return invalidf("invalid source census object ID")
	}
	if _, err := hex.Decode(key[1:1+int(key[0])], []byte(record.ObjectID)); err != nil {
		return invalidf("invalid source census object ID")
	}
	if prior, exists := observation.seen[key]; exists {
		if prior != record.DeclaredBytes {
			return invalidf("source census object size changed")
		}
		observation.logical += size
		return nil
	}
	if len(observation.seen) >= MaxOwners || size > math.MaxUint64-observation.unique {
		return invalidf("source census unique bytes overflow")
	}
	observation.seen[key] = record.DeclaredBytes
	observation.logical += size
	observation.unique += size
	return nil
}

func (observation *sourceCensusObservation) flush(ctx context.Context) error {
	if observation == nil {
		return nil
	}
	if observation.failed {
		return readaccounting.ErrEvent
	}
	if observation.logical == 0 && observation.unique == 0 {
		return nil
	}
	_, err := dispatchadmission.ObserveProductionSourceCensus(ctx, readaccounting.SourceCensusBatch, observation.phase, observation.logical, observation.unique)
	if err != nil {
		observation.failed = true
		return err
	}
	observation.logical, observation.unique = 0, 0
	return nil
}

func (observation *sourceCensusObservation) finish(ctx context.Context) error {
	if observation == nil {
		return nil
	}
	if err := observation.flush(ctx); err != nil {
		return err
	}
	if observation.invalid {
		// Zero is deliberately invalid: the closed validator refuses before
		// calling the sink, so this is not a wire event. Use
		// the production boundary so an incomplete selected meter latches
		// even when the index worker ordinarily handles source errors.
		_, err := dispatchadmission.ObserveProductionSourceCensus(ctx, 0, observation.phase, 0, 0)
		return err
	}
	event, owners := readaccounting.SourceCensusEnd, uint64(0)
	if observation.succeeded {
		event, owners = readaccounting.SourceCensusComplete, observation.regularOwners
	}
	_, err := dispatchadmission.ObserveProductionSourceCensus(ctx, event, observation.phase, owners, 0)
	return err
}
