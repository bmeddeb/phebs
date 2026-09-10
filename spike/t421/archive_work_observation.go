package t421

// This is a distinct producer-local coverage profile, not server-owner or
// whole-phase completeness. The caller owns native/transport joins and the
// shared 64 MiB allowance; the parser retains its bounded single work scan.
func observeArchiveWork(output *checkoutCommandOutput, plan Plan, producer uint32, input [32]byte, joined, healthy bool) (out ExecutionAttemptObservation, err error) {
	if !joined || output == nil || producer != 10 && producer != 11 {
		return out, errExecutionAttempts
	}
	out, err = observeExecutionAttempts(output.buffer.Bytes(), plan, producer, input, true)
	if err != nil || !healthy || output.err != nil {
		out.Complete, out.Cache.Complete = false, false
		out.SourceCensus.Complete, out.CatalogCensus.Complete = false, false
		return out, errExecutionAttempts
	}
	return out, nil
}
