//go:build !linux && !darwin

package typedsandbox

import "os"

// writeRefusalSite keeps the diagnostic honest on platforms where the raw
// descriptor write is unavailable. The supervisor role never runs here, so the
// only reachable callers are the dispatcher's two terminal refusals.
func writeRefusalSite(frame []byte) { _, _ = os.Stderr.Write(frame) }
