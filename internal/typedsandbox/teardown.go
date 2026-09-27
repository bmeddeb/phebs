package typedsandbox

import (
	"context"
	"sync"
	"time"
)

const teardownLimit = 20 * time.Second

// stopOnlyTransport cannot authorize work. Its single timer starts at the first
// observed execution cancellation or cleanup entry, never once per operation.
// Only attach and exact stop/join/removal use this context. stop joins its watcher.
func stopOnlyTransport(execution context.Context) (context.Context, func(), func()) {
	transport, cancel := context.WithCancel(context.WithoutCancel(execution))
	var begin sync.Once
	var timer *time.Timer
	start := func() { begin.Do(func() { timer = time.AfterFunc(teardownLimit, cancel) }) }
	finish, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-execution.Done():
			start()
		case <-finish:
		}
	}()
	var end sync.Once
	stop := func() {
		end.Do(func() {
			cancel()
			close(finish)
			<-done
			if timer != nil {
				timer.Stop()
			}
		})
	}
	return transport, start, stop
}
