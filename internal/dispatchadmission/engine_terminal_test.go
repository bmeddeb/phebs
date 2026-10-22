//go:build darwin || linux

package dispatchadmission

import (
	"context"
	"errors"
	"testing"
)

func TestTerminalQuiescenceRetiresActualEngineEndpoint(t *testing.T) {
	for _, busy := range []bool{false, true} {
		name := "joined-EOF"
		if busy {
			name = "accepted-HOLD-refuses-retirement"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newTerminalPhaseFixture(t, func(context.Context) error { return nil })
			parent, child := archiveMeasurementTestPipe(t)
			binding := EngineMeasurementBinding{ProducerID: 4, ProducerBinding: [32]byte{4}, InputSHA256: [32]byte{7}}
			server := make(chan error, 1)
			go func() {
				server <- ServeEngineMeasurement(fixture.ctx, parent, binding, 2, func(ctx context.Context, _ uint32, measure func(context.Context, EngineMeasurementIdentity) error) error {
					return measure(ctx, engineMeasurementTestIdentity())
				})
			}()
			engine, err := newEngineMeasurementClient(fixture.ctx, fixture.ctx, child, binding, 2)
			if err != nil {
				t.Fatal(err)
			}
			fixture.lifetime.engineMeasurement = engine
			var released chan struct{}
			var measured chan error
			if busy {
				entered := make(chan struct{})
				released, measured = make(chan struct{}), make(chan error, 1)
				go func() {
					measured <- engine.measure(fixture.ctx, 6, engineMeasurementTestIdentity(), func(ctx context.Context) error {
						close(entered)
						select {
						case <-released:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					})
				}()
				<-entered
			}
			fixture.advance(t)
			err = fixture.control.TerminalQuiesce(fixture.ctx)
			if busy {
				if err == nil {
					t.Error("terminal success retired a transferred measurement owner", err)
				}
				close(released)
				if err := <-measured; err != nil {
					t.Fatal("actual HOLD/RELEASE did not settle", err)
				}
				if err := engine.close(fixture.ctx); err != nil {
					t.Fatal("later joined endpoint cleanup", err)
				}
			} else {
				engine.mu.Lock()
				closed := engine.closed
				engine.mu.Unlock()
				if err != nil || !closed {
					t.Fatal("terminal echo preceded authenticated engine EOF", err)
				}
			}
			if err := <-server; err != nil {
				t.Fatal("engine endpoint did not genuinely join", err)
			}
			if err := engine.close(fixture.ctx); err != nil && !errors.Is(err, ErrBusy) {
				t.Fatal("settled endpoint close changed result", err)
			}
		})
	}
}
