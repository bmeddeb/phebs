package recovery

import "testing"

func TestCLIEndpointUsesHTTPTransport(t *testing.T) {
	if got, want := cliEndpoint("ws://127.0.0.1:32123"), "http://127.0.0.1:32123"; got != want {
		t.Fatalf("cliEndpoint = %q, want %q", got, want)
	}
}
