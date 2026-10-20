//go:build linux

package t421

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxGoBuildLifetimeInitialOwnsNonce(t *testing.T) {
	for _, size := range []int{47, 48, 49} {
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() { _ = unix.Close(pair[0]); _ = unix.Close(pair[1]) }()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			sender, receiver := &inputTreeSocket{fd: pair[0]}, &inputTreeSocket{fd: pair[1]}
			original := bytes.Repeat([]byte{0x5a}, size)
			if sender.send(ctx, treeWireInit, original, nil) != nil {
				t.Fatal("initial send")
			}
			initial, err := receiveGoLifetimeInitial(ctx, receiver)
			if size != 48 {
				if err == nil || initial != nil {
					t.Fatal("nonfixed initial frame accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if sender.send(ctx, treeWireEnd, bytes.Repeat([]byte{0xa5}, 64), nil) != nil {
				t.Fatal("next frame")
			}
			if _, _, err := receiver.receive(ctx, treeWireEnd, false); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(initial, original) {
				t.Fatal("later receive overwrote admitted nonce/header")
			}
		}()
	}
}

func TestLinuxGoBuildLifetimeInputShape(t *testing.T) {
	base := inputTreeCensus{rows: []inputTreeEntry{{name: "inputs", mode: 0o700}, {name: "inputs/sentinel", mode: 0o400, size: uint64(len(linuxGoLifetimeSentinel))}}}
	if !goLifetimeInputShape(base) {
		t.Fatal("fixed lifetime sentinel refused")
	}
	for _, test := range []struct {
		name   string
		change func(*inputTreeCensus)
	}{
		{"missing", func(c *inputTreeCensus) { c.rows = c.rows[:1] }},
		{"extra", func(c *inputTreeCensus) { c.rows = append(c.rows, inputTreeEntry{name: "inputs/extra", mode: 0o400}) }},
		{"wrong-root", func(c *inputTreeCensus) { c.rows[0].name = "foreign" }},
		{"executable", func(c *inputTreeCensus) { c.rows[1].mode = 0o500 }},
		{"wrong-size", func(c *inputTreeCensus) { c.rows[1].size++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := base
			copy.rows = slices.Clone(base.rows)
			test.change(&copy)
			if goLifetimeInputShape(copy) {
				t.Fatal("nonfixed lifetime input admitted")
			}
		})
	}
}
