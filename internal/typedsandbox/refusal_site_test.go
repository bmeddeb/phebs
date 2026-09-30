package typedsandbox

import (
	"strings"
	"testing"
)

func refusalSiteVocabulary() []string {
	return []string{
		SiteIdentity, SiteTimer, SiteBootstrap, SiteBootNow, SiteSignal, SiteAllowance,
		SiteProcess, SiteScratch, SiteEncode, SiteSelector, SiteArgv,
	}
}

func TestRefusalSiteVocabularyIsClosedAndDistinct(t *testing.T) {
	vocab := refusalSiteVocabulary()
	if len(vocab) != 11 {
		t.Fatalf("vocabulary has %d tokens, want 11", len(vocab))
	}
	seen := make(map[string]struct{}, len(vocab))
	for _, token := range vocab {
		if token == "" {
			t.Fatal("empty token in vocabulary")
		}
		if len(token) > len(refusalSiteMax) {
			t.Fatalf("token %q is longer than refusalSiteMax %q, so the frame would truncate it", token, refusalSiteMax)
		}
		if strings.ContainsAny(token, "=\n\r/\\ ") {
			t.Fatalf("token %q carries a framing, path or whitespace byte", token)
		}
		if _, dup := seen[token]; dup {
			t.Fatalf("token %q appears twice, so two sites would be indistinguishable on the wire", token)
		}
		seen[token] = struct{}{}
	}
	if len(refusalSiteMax) != maxTokenLen(vocab) {
		t.Fatalf("refusalSiteMax is %q but the longest token is %d bytes", refusalSiteMax, maxTokenLen(vocab))
	}
}

func maxTokenLen(vocab []string) int {
	longest := 0
	for _, token := range vocab {
		if len(token) > longest {
			longest = len(token)
		}
	}
	return longest
}

func TestRefusalSiteFrame(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"longest_token", SiteBootstrap, "phebs_site=bootstrap\n"},
		{"shortest_token", SiteArgv, "phebs_site=argv\n"},
		{"identity", SiteIdentity, "phebs_site=identity\n"},
		{"selector", SiteSelector, "phebs_site=selector\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(refusalSiteFrame(tc.token))
			if got != tc.want {
				t.Fatalf("frame = %q, want %q", got, tc.want)
			}
		})
	}
	for _, token := range refusalSiteVocabulary() {
		frame := refusalSiteFrame(token)
		if len(frame) > refusalSiteFrameBytes {
			t.Fatalf("token %q produced a %d-byte frame, over the %d-byte bound", token, len(frame), refusalSiteFrameBytes)
		}
		if frame[len(frame)-1] != '\n' {
			t.Fatalf("token %q produced a frame not terminated by a newline", token)
		}
		if !strings.HasPrefix(string(frame), refusalSitePrefix) {
			t.Fatalf("token %q produced a frame without the %q prefix", token, refusalSitePrefix)
		}
		if body := strings.TrimSuffix(strings.TrimPrefix(string(frame), refusalSitePrefix), "\n"); body != token {
			t.Fatalf("token %q round-tripped as %q", token, body)
		}
	}
}

func TestRefusalSiteFrameBoundFitsTheHostWirePrefix(t *testing.T) {
	// The host retains at most reportWirePrefixBytes of the captured stderr as
	// hex, so a frame longer than that could be cut mid-token and become
	// ambiguous. This asserts the bound from the host-side constant rather than
	// from a number copied into this test.
	if refusalSiteFrameBytes > reportWirePrefixBytes {
		t.Fatalf("refusalSiteFrameBytes %d exceeds reportWirePrefixBytes %d", refusalSiteFrameBytes, reportWirePrefixBytes)
	}
	if len(refusalSitePrefix) != 11 {
		t.Fatalf("prefix %q is %d bytes, want 11", refusalSitePrefix, len(refusalSitePrefix))
	}
}
