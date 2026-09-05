//go:build darwin || linux

package dispatchadmission

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"
)

func TestAuthorCheckpointWaitsForCompleteControlACK(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "delayed"
		if lost {
			name = "lost"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			controller, client, server := paired(t, testConfig())
			if err := client.Run(ctx, 1, exec.CommandContext(ctx, "/usr/bin/true")); err != nil {
				t.Fatal(err)
			}
			old := productionRuntime.Load()
			productionRuntime.Store(&ProductionLifetime{program: ProgramCorpusAuthor, client: client, inputSHA256: [32]byte{9}})
			defer productionRuntime.Store(old)
			parentFile, childFile, err := NewPipe()
			if err != nil {
				t.Fatal(err)
			}
			parent, err := adopt(parentFile)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close() }()
			child, err := adopt(childFile)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Close() }()
			config := PhaseControlConfig{Phases: []uint32{1, 2}, InitialPhase: 1, MaximumPhases: 2,
				MaximumWireBytes: 4096, Timeout: time.Second}
			controlDone := make(chan error, 1)
			go func() { controlDone <- servePhaseControl(ctx, child, client, config, 0, client.binding) }()
			pause := phaseControlFrame{op: phasePause, phase: 1, sequence: 1, binding: client.binding}.encode()
			if _, err := parent.Write(pause[:]); err != nil {
				t.Fatal(err)
			}
			var ack [FrameBytes]byte
			if _, err := io.ReadFull(parent, ack[:]); err != nil || ack != pause {
				t.Fatal("pause ACK failed")
			}
			if err := controller.Fence(); err != nil {
				t.Fatal(err)
			}
			// Fill the actual socket's outbound buffer before the checkpoint.
			// Its 64-byte ACK then cannot complete until this parent drains it.
			if child.SetWriteBuffer(1024) != nil || child.SetWriteDeadline(time.Now().Add(20*time.Millisecond)) != nil {
				t.Fatal("cannot bound ACK-delay fixture")
			}
			filled := 0
			for filled < 1<<20 {
				n, err := child.Write(make([]byte, 4096))
				filled += n
				if err != nil {
					break
				}
			}
			if filled == 0 || filled >= 1<<20 || child.SetWriteDeadline(time.Time{}) != nil {
				t.Fatal("ACK fixture did not establish bounded backpressure")
			}
			checkpoint := phaseControlFrame{op: phaseCheckpoint, phase: 1, sequence: 2, binding: client.binding}.encode()
			if _, err := parent.Write(checkpoint[:]); err != nil {
				t.Fatal(err)
			}
			for {
				client.mu.Lock()
				checkpointed, acknowledged, changed := client.checkpoint, client.controlCheckpointAcknowledged, client.changed
				client.mu.Unlock()
				if checkpointed {
					if acknowledged {
						t.Fatal("local checkpoint was mislabeled as completed control ACK")
					}
					break
				}
				select {
				case <-changed:
				case <-ctx.Done():
					t.Fatal("local checkpoint did not arrive")
				}
			}
			waited := make(chan error, 1)
			go func() { waited <- WaitAuthorCheckpoint(ctx) }()
			select {
			case err := <-waited:
				t.Fatalf("checkpoint wait returned before blocked ACK: %v", err)
			default:
			}
			if lost {
				_ = parent.Close()
				if err := <-waited; err == nil {
					t.Fatal("lost checkpoint ACK authorized Close")
				}
				if err := <-controlDone; err == nil {
					t.Fatal("lost checkpoint ACK did not fail control")
				}
				<-server
				snapshot, err := controller.Snapshot()
				if snapshot.Attempts != 1 || err == nil && snapshot.Complete {
					t.Fatal("lost ACK erased prefix or claimed complete closure")
				}
				return
			}
			if _, err := io.CopyN(io.Discard, parent, int64(filled)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(parent, ack[:]); err != nil || !bytes.Equal(ack[:], checkpoint[:]) {
				t.Fatal("complete checkpoint echo ACK missing")
			}
			if err := <-waited; err != nil {
				t.Fatal(err)
			}
			if err := client.Close(ctx); err != nil {
				t.Fatal(err)
			}
			_ = child.Close()
			if err := <-controlDone; err != nil {
				t.Fatal(err)
			}
			if err := <-server; err != nil {
				t.Fatal(err)
			}
			if err := WaitAuthorCheckpoint(ctx); !errors.Is(err, ErrProductionBootstrap) {
				t.Fatal("closed author retained checkpoint permission")
			}
		})
	}
}

func TestAuthorInputDigestChangesOnlyBoundedBootstrapRecord(t *testing.T) {
	record := productionTestRecord()
	record.Program, record.Producer.Sites, record.Tools = ProgramCorpusAuthor, AuthorSites(), record.Tools[:1]
	if record.validate() == nil {
		t.Fatal("author accepted no request binding")
	}
	record.InputSHA256 = [32]byte{7}
	if record.validate() != nil {
		t.Fatal("closed author digest record refused")
	}
	if FrameBytes != 64 || productionBootstrapHeaderBytes != 72 {
		t.Fatal("author input binding changed frame/header sizes")
	}
}
