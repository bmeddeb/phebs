package indexer

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestIndexOfferOutputNativeStream(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		offers    uint64
		healthy   bool
	}{
		{"zero", "ZIB1\nZIE1:0\n", 0, true},
		{"two", "2026 log\nZIB1\nZI1\nZI1\nZIE1:2\n2026 done\n", 2, true},
		{"missing", "ordinary\n", 0, false},
		{"unbound", "ZI1\n", 0, false},
		{"duplicate begin", "ZIB1\nZIB1\n", 0, false},
		{"missing end", "ZIB1\nZI1\n", 1, false},
		{"partial token", "ZIB1\nZI1\nZI", 1, false},
		{"wrong tally", "ZIB1\nZI1\nZIE1:2\n", 1, false},
		{"leading zero", "ZIB1\nZIE1:00\n", 0, false},
		{"overflow tally", "ZIB1\nZIE1:18446744073709551616\n", 0, false},
		{"unknown version", "ZIB1\nZI2\n", 0, false},
		{"after end", "ZIB1\nZIE1:0\nZI1\n", 0, false},
		{"duplicate end", "ZIB1\nZIE1:0\nZIE1:0\n", 0, false},
		{"unsupported status", "ZIB1\nZI1\nZIE1:f:1\n", 1, false},
		{"long diagnostic", strings.Repeat("x", 3*maxVerboseLineBytes) + "\nZIB1\nZI1\nZIE1:1\n", 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, step := range []int{1, 7, len(test.raw)} {
				var diagnostic bytes.Buffer
				var offers, ends uint64
				ctx, err := WithIndexOfferObserver(t.Context(), func(event IndexOfferEvent) error {
					if event.Kind == 'i' {
						offers += event.Count
					}
					if event.Kind == 'e' {
						ends++
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				out, err := selectedIndexOfferOutput(ctx, &diagnostic, false)
				if err != nil {
					t.Fatal(err)
				}
				for offset := 0; offset < len(test.raw); offset += step {
					if _, err = out.Write([]byte(test.raw[offset:min(offset+step, len(test.raw))])); err != nil {
						break
					}
				}
				err = out.finish(err)
				if (err == nil) != test.healthy || offers != test.offers || (ends == 1) != test.healthy {
					t.Fatal(step, offers, ends, err)
				}
				if bytes.Contains(diagnostic.Bytes(), []byte("ZI1")) {
					t.Fatal("native token entered advisory tail")
				}
			}
		})
	}
}

func TestIndexOfferOutputRefusal(t *testing.T) {
	if _, err := selectedIndexOfferOutput(t.Context(), io.Discard, false); err == nil {
		t.Fatal("missing sink admitted")
	}
	for _, name := range []string{"focused", "sink", "panic", "exit", "twice"} {
		t.Run(name, func(t *testing.T) {
			ctx, err := WithIndexOfferObserver(t.Context(), func(IndexOfferEvent) error {
				if name == "panic" {
					panic("refuse")
				}
				if name == "sink" {
					return io.ErrClosedPipe
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			out, err := selectedIndexOfferOutput(ctx, io.Discard, name == "focused")
			if name == "focused" {
				if err == nil {
					t.Fatal("focused admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = out.Write([]byte("ZIB1\nZI1\nZIE1:1\n"))
			if name == "exit" {
				err = errors.New("child exit failure after valid output")
			}
			err = out.finish(err)
			if name == "twice" {
				if err != nil {
					t.Fatal(err)
				}
				err = out.finish(nil)
			}
			if err == nil {
				t.Fatal("failure admitted")
			}
		})
	}
}

func TestIndexOfferOutputCooperativeFailurePreservesRetry(t *testing.T) {
	// A tiny normal process exit supplies a real ExitError/ProcessState; this
	// is not an index build or native workload acceptance test.
	exitErr := exec.CommandContext(t.Context(), "/usr/bin/false").Run()
	if exitErr == nil {
		t.Fatal("expected exit one")
	}
	exitTwo := exec.CommandContext(t.Context(), "/bin/sh", "-c", "exit 2").Run()
	if exitTwo == nil {
		t.Fatal("expected exit two")
	}
	for _, test := range []struct {
		err      error
		accepted bool
	}{
		{exitErr, true},
		{exitTwo, true},
		{errors.Join(exitErr), true},
		{errors.Join(exitErr, io.ErrUnexpectedEOF), false},
	} {
		var failed, terminals uint64
		ctx, err := WithIndexOfferObserver(t.Context(), func(event IndexOfferEvent) error {
			if event.Kind == 'f' {
				failed += event.Count
				terminals++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		out, err := selectedIndexOfferOutput(ctx, io.Discard, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write([]byte("ZIB1\nZI1\nZIE1:1\n")); err != nil {
			t.Fatal(err)
		}
		err = out.finish(test.err)
		if (err == nil) != test.accepted ||
			(test.accepted && (failed != 1 || terminals != 1)) ||
			(!test.accepted && (failed != 0 || terminals != 0)) {
			t.Fatal(failed, terminals, err)
		}
	}
}
