//go:build linux

package t421

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func goReferencePlanFixture() linuxGoReferencePlan {
	return linuxGoReferencePlan{Role: "phebs", Source: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40),
		ConfigSHA256: SHA256([]byte("config")), CandidateSHA256: SHA256([]byte("candidate")),
		OriginalSDK: "/selected/sdk", OriginalModules: "/selected/modules", OriginalSource: "/selected/source"}
}

func TestLinuxGoReferencePlanClosedJSON(t *testing.T) {
	plan := goReferencePlanFixture()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := decodeGoReferencePlan(raw); err != nil || actual != plan {
		t.Fatal("closed plan refused", err)
	}
	for _, raw := range [][]byte{nil, append(bytes.Clone(raw), '\n'), append(bytes.Clone(raw), []byte("{}")...),
		bytes.Replace(raw, []byte(`"role":"phebs"`), []byte(`"role":"phebs","role":"phebs"`), 1),
		bytes.Replace(raw, []byte(`"role"`), []byte(`"unknown"`), 1), bytes.Repeat([]byte{'x'}, maxGoReferencePlanBytes+1)} {
		if _, err := decodeGoReferencePlan(raw); err == nil {
			t.Fatal("malformed/ambiguous plan admitted")
		}
	}
	for _, change := range []func(*linuxGoReferencePlan){
		func(p *linuxGoReferencePlan) { p.Role = linuxGoReferenceRole }, func(p *linuxGoReferencePlan) { p.Role = linuxGoBuildRole },
		func(p *linuxGoReferencePlan) { p.Schema = PlanV2Schema }, func(p *linuxGoReferencePlan) { p.Source = "invalid" },
		func(p *linuxGoReferencePlan) { p.Tree = "invalid" }, func(p *linuxGoReferencePlan) { p.ConfigSHA256 = "unbound" },
		func(p *linuxGoReferencePlan) { p.CandidateSHA256 = "unbound" }, func(p *linuxGoReferencePlan) { p.OriginalSDK = "/" },
	} {
		changed := plan
		change(&changed)
		encoded, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeGoReferencePlan(encoded); err == nil {
			t.Fatal("invalid plan authority admitted")
		}
	}
	plan.Schema = PlanV3Schema
	if !plan.valid() {
		t.Fatal("explicit V3 recipe refused")
	}
}

func TestLinuxGoReferencePlanPreservesWireOrdinalsAndOwnedFields(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(pair[0]); _ = unix.Close(pair[1]) }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sender, receiver := &inputTreeSocket{fd: pair[0]}, &inputTreeSocket{fd: pair[1]}
	initial := bytes.Repeat([]byte{0x5a}, 48)
	if sender.send(ctx, treeWireInit, initial, nil) != nil {
		t.Fatal("initial send")
	}
	if _, _, err := receiver.receive(ctx, treeWireInit, false); err != nil {
		t.Fatal(err)
	}
	plan := goReferencePlanFixture()
	if sendGoReferencePlan(ctx, sender, plan) != nil {
		t.Fatal("plan send")
	}
	actual, err := receiveGoReferencePlan(ctx, receiver)
	if err != nil || actual != plan || receiver.received != 2 || sender.sent != 2 {
		t.Fatal("second INIT did not retain ordinal/plan", err)
	}
	if sender.send(ctx, treeWireEnd, bytes.Repeat([]byte{0xa5}, 64), nil) != nil {
		t.Fatal("later frame")
	}
	if _, _, err := receiver.receive(ctx, treeWireEnd, false); err != nil || actual != plan {
		t.Fatal("wire reuse changed owned plan fields", err)
	}
}
