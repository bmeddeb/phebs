package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func t422TerminalFooterStates() (dispatchadmission.ProductionSemanticSnapshot, dispatchadmission.ProductionSemanticSnapshot) {
	initial := dispatchadmission.ProductionSemanticSnapshot{Mode: dispatchadmission.ProductionSemanticV3, ProducerID: 4, Phase: 6}
	for index := range initial.InputSHA256 {
		initial.InputSHA256[index] = 0xab
	}
	current := initial
	current.Phase = 8
	return initial, current
}

func TestT422TerminalFooterBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*dispatchadmission.ProductionSemanticSnapshot, *dispatchadmission.ProductionSemanticSnapshot)
	}{
		{"exact", nil},
		{"initial_producer", func(a, _ *dispatchadmission.ProductionSemanticSnapshot) { a.ProducerID = 5 }},
		{"current_producer", func(_, b *dispatchadmission.ProductionSemanticSnapshot) { b.ProducerID = 5 }},
		{"initial_phase", func(a, _ *dispatchadmission.ProductionSemanticSnapshot) { a.Phase = 8 }},
		{"current_phase", func(_, b *dispatchadmission.ProductionSemanticSnapshot) { b.Phase = 7 }},
		{"mode", func(a, b *dispatchadmission.ProductionSemanticSnapshot) { a.Mode, b.Mode = "", "" }},
		{"changed_mode", func(_, b *dispatchadmission.ProductionSemanticSnapshot) { b.Mode = "" }},
		{"input", func(_, b *dispatchadmission.ProductionSemanticSnapshot) { b.InputSHA256[0]++ }},
		{"zero_input", func(a, b *dispatchadmission.ProductionSemanticSnapshot) {
			a.InputSHA256, b.InputSHA256 = [32]byte{}, [32]byte{}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			initial, current := t422TerminalFooterStates()
			if test.change != nil {
				test.change(&initial, &current)
			}
			got, err := t422TerminalFooter(initial, current)
			if test.change != nil {
				if err == nil || len(got) != 0 {
					t.Fatal("invalid binding admitted", string(got), err)
				}
				return
			}
			want := "TFE1:4:8:sha256:" + strings.Repeat("ab", 32) + "\n"
			if err != nil || string(got) != want || len(got) != 81 {
				t.Fatal("footer", string(got), len(got), err)
			}
		})
	}
}

type t422FooterTestWriter func([]byte) (int, error)

func (write t422FooterTestWriter) Write(raw []byte) (int, error) { return write(raw) }

func TestT422TerminalFooterWriteFailures(t *testing.T) {
	for _, mode := range []string{"complete", "short", "error", "full_error", "cancel_before", "cancel_during", "nil_context", "nil_writer"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var writes int
			var writer io.Writer = t422FooterTestWriter(func(raw []byte) (int, error) {
				writes++
				switch mode {
				case "short":
					return len(raw) - 1, nil
				case "error":
					return 0, io.ErrClosedPipe
				case "full_error":
					return len(raw), io.ErrClosedPipe
				case "cancel_during":
					cancel()
				}
				return len(raw), nil
			})
			switch mode {
			case "cancel_before":
				cancel()
			case "nil_context":
				ctx = nil
			case "nil_writer":
				writer = nil
			}
			initial, current := t422TerminalFooterStates()
			err := writeT422TerminalFooter(ctx, writer, initial, current)
			if (err == nil) != (mode == "complete") || (err != nil && !errors.Is(err, errT422AttemptReport)) {
				t.Fatal("write outcome", err)
			}
			wantWrites := 1
			if mode == "cancel_before" || mode == "nil_context" || mode == "nil_writer" {
				wantWrites = 0
			}
			if writes != wantWrites {
				t.Fatal("write count", writes)
			}
		})
	}
}

func TestT422TerminalFooterPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	initial, current := t422TerminalFooterStates()
	if err := writeT422TerminalFooter(t.Context(), writer, initial, current); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	want, _ := t422TerminalFooter(initial, current)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("pipe footer", string(got), err)
	}
}

// A synchronous write must finish before its caller can return (and therefore
// before the existing PC callback can echo). This is mechanics, not pump-health
// or native checkpoint evidence; the inherited test covers the actual callback.
func TestT422TerminalFooterJoinsWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		writer := t422FooterTestWriter(func(raw []byte) (int, error) {
			close(entered)
			<-release
			return len(raw), nil
		})
		initial, current := t422TerminalFooterStates()
		go func() { done <- writeT422TerminalFooter(t.Context(), writer, initial, current) }()
		<-entered
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatal("returned before write completed", err)
		default:
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
