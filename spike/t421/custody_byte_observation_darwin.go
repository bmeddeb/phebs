//go:build darwin

package t421

import (
	"context"
	"github.com/bmeddeb/phebs/internal/custodybytes"
)

var errCustodyByteObservation = custodybytes.ErrUnavailable

type custodyByteSample = custodybytes.Sample
type custodyBytePhase = custodybytes.Phase
type custodyByteSnapshot = custodybytes.Snapshot
type custodyByteObservation struct{ *custodybytes.Observer }

func newCustodyByteObservation(owner productionRoot) *custodyByteObservation {
	return &custodyByteObservation{custodybytes.NewBorrowed(owner.file, owner.path, owner.info, owner.volume)}
}
func (g *custodyByteObservation) Snapshot() custodyByteSnapshot {
	if g == nil {
		return custodyByteSnapshot{Unavailable: true}
	}
	return g.Observer.Snapshot()
}
func (g *custodyByteObservation) fail() error { return g.Fail() }
func (g *custodyByteObservation) sample(ctx context.Context, phase uint32, confirm func() bool) (custodyByteSample, error) {
	return g.SampleConfirmed(ctx, phase, confirm)
}
func walkCustodyBytes(ctx context.Context, owner productionRoot) (custodyByteSample, error) {
	return newCustodyByteObservation(owner).Walk(ctx)
}
