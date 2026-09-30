//go:build !unix

package typedsandbox

// writeRefusalSite is deliberately a no-op off unix. The raw-descriptor discipline
// in refusal_site_unix.go has no portable equivalent here: os.Stderr.Write can
// block, and a terminal refusal that blocks on a full stderr pipe hangs instead of
// exiting — the exact defect this instrumentation must not reintroduce. Nothing is
// lost by writing nothing, because no reachable caller exists on these platforms:
// the supervisor role is linux-only and cmd/phebs does not compile for them at all.
// Missing evidence stays missing rather than risking a hung exit.
func writeRefusalSite(frame []byte) {}
