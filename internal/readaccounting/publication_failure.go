package readaccounting

import "context"

// PublicationFailureStage is private diagnostic location, never authority.
type PublicationFailureStage uint8

const (
	PublicationFailureAssembly PublicationFailureStage = iota + 1
	PublicationFailureFence
	PublicationFailureObservation
	PublicationFailureAuthority
	PublicationFailureEncoding
	PublicationFailureStoreInput
	PublicationFailureStoreQuery
	PublicationFailureStoreCount
	PublicationFailureStoreIdentity
	PublicationFailurePublisher
	PublicationFailurePointer
	PublicationFailureSettlement
	PublicationFailureHandler
)

func (stage PublicationFailureStage) String() string {
	names := [...]string{"unknown", "assembly", "fence", "publication_observer", "publisher_authority", "publisher_encoding", "store_input", "store_query", "store_result_count", "store_result_identity", "publisher_return", "pointer_write", "settlement", "handler_return"}
	if int(stage) >= len(names) {
		return names[0]
	}
	return names[stage]
}

type PublicationFailureFlags struct{ ResultsPresent, SingleResult bool }
type PublicationFailureObserver func(PublicationFailureStage, error, PublicationFailureFlags)
type publicationFailureKey struct{}

// Optional observer carries only the original error to its synchronous sink;
// no error is retained here, no ledger charged, and no acceptance is issued.
// Nil/nested binding is a diagnostic no-op, not a new operational refusal.
func WithPublicationFailureObserver(ctx context.Context, observe PublicationFailureObserver) context.Context {
	if ctx == nil || observe == nil || ctx.Value(publicationFailureKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, publicationFailureKey{}, observe)
}
func NotePublicationFailure(ctx context.Context, stage PublicationFailureStage, err error, flags PublicationFailureFlags) {
	if ctx == nil || err == nil {
		return
	}
	observe, _ := ctx.Value(publicationFailureKey{}).(PublicationFailureObserver)
	if observe == nil {
		return
	}
	defer func() { _ = recover() }()
	observe(stage, err, flags)
}
