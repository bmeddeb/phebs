package t421

import (
	"context"
	"errors"
	"io"
	"testing"
)

// A test-only observer proves routing through source reconstruction. Actual
// Linux custody is qualified separately with the real H2p transport.
type checkoutTransportFixture struct {
	legacy  executionCheckoutInspector
	roots   map[string]int
	refused bool
}

func (transport *checkoutTransportFixture) checkGit(ctx context.Context, digest string) error {
	if transport.refused || digest != transport.legacy.digest {
		return ErrExecutionGitCustody
	}
	return transport.legacy.checkGit(ctx)
}

func (transport *checkoutTransportFixture) runInput(ctx context.Context, root string, input io.Reader, limit int64, args ...string) ([]byte, error) {
	transport.roots[root]++
	inspection := transport.legacy
	inspection.root = root
	return inspection.runInput(ctx, input, limit, args...)
}

func TestExecutionReferenceSourceRetainsProtectedCheckoutTransport(t *testing.T) {
	fixture := newExecutionCheckoutFixture(t)
	legacy := referenceFixtureInspector(t, fixture)
	transport := &checkoutTransportFixture{legacy: legacy, roots: make(map[string]int)}
	origin := executionCheckoutInspector{root: fixture.root, digest: legacy.digest, transport: transport}
	if _, err := inspectExecutionCheckout(t.Context(), origin, fixture.plan, fixture.integration, fixture.source); err != nil {
		t.Fatal(err)
	}
	destination := newReferenceFixtureDestination(t)
	reference, err := createExecutionReferenceSource(t.Context(), origin, fixture.source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if reference.root.transport != transport || reference.root.git != "" || reference.root.custody != nil || transport.roots[fixture.root] == 0 || transport.roots[destination] == 0 {
		t.Fatal("source reconstruction dropped transport or mixed executable authority")
	}
	if err := reference.verify(t.Context()); err != nil {
		t.Fatal(err)
	}
	transport.refused = true
	if err := reference.verify(t.Context()); err == nil {
		t.Fatal("source verification fell back after protected transport refused")
	}
}

func TestExecutionCheckoutTransportRejectsMixedOrWrongAuthority(t *testing.T) {
	transport := &checkoutTransportFixture{roots: make(map[string]int)}
	for _, inspection := range []executionCheckoutInspector{
		{root: "/", git: "/must-not-execute", transport: transport},
		{root: "/", custody: &ExecutionGitCustody{}, transport: transport},
		{root: "/", digest: "wrong", transport: transport},
	} {
		if _, err := inspection.run(t.Context(), 1, "version"); err == nil {
			t.Fatal("ambiguous transport authority admitted")
		}
	}
	if len(transport.roots) != 0 {
		t.Fatal("refused inspection reached any command transport")
	}
	if !errors.Is((&executionCheckoutInspector{transport: transport, git: "mixed"}).checkGit(t.Context()), ErrExecutionGitCustody) {
		t.Fatal("wrong authority error")
	}
}
