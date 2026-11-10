// Package loopbackowner proves which process holds a loopback TCP listener.
//
// Supervised SurrealDB children are started on a port chosen by listening on
// 127.0.0.1:0 and closing the listener before the child binds it. Another
// process can take the port inside that window. The child then fails to bind,
// and a readiness probe on the address is answered by the other process. A
// probe that only asks "is something serving here?" cannot tell the two
// apart; Check can.
package loopbackowner

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrForeign reports that some process other than pid holds a TCP
	// listener on the address's port. Startup lost the port to it.
	ErrForeign = errors.New("loopback listener is held by another process")
	// ErrNotListening reports that no TCP listener exists on the port.
	ErrNotListening = errors.New("no loopback listener on port")
	// ErrExited reports that pid has exited while no listener is left on the
	// port. An exited pid with a listener still on the port is ErrForeign.
	ErrExited = errors.New("process exited without a loopback listener on port")
	// ErrUnsupported reports that this platform cannot attribute listeners
	// to processes; callers keep their unverified behavior.
	ErrUnsupported = errors.New("loopback listener ownership is not verifiable on this platform")
)

// Check returns nil only when pid holds every TCP listener on addr's port and
// at least one exists. addr is a host:port string; only the port is matched,
// because a wildcard listener on the same port also takes loopback traffic.
// An exited pid holds nothing: a listener left on the port is ErrForeign,
// and none at all is ErrExited.
func Check(pid int, addr string) error { return check(pid, addr) }

// awaitInterval paces Await's attribution polls while pid is still starting.
const awaitInterval = 25 * time.Millisecond

// Await polls until pid holds a TCP listener on addr's port, so that a
// readiness probe sent to addr afterwards reaches pid and nothing else. It
// returns nil once pid holds every listener on the port. It returns ErrForeign
// as soon as another process holds one (pid lost the port), ErrUnsupported
// where listeners cannot be attributed (callers keep their unverified
// probe), ErrExited at once when pid exits with no listener left on the
// port, ctx's error when pid never listens in time, and any other
// attribution failure as is.
func Await(ctx context.Context, pid int, addr string) error {
	ticker := time.NewTicker(awaitInterval)
	defer ticker.Stop()
	for {
		if err := Check(pid, addr); !errors.Is(err, ErrNotListening) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
