package typedsandbox

import (
	"strings"
	"testing"
)

// refusalSiteVocabulary reads the table rather than restating it, so a token added
// to refusalSiteTokens is measured here without this list being edited to match.
func refusalSiteVocabulary() []string { return refusalSiteTokens[:] }

func maxTokenLen(vocab []string) int {
	longest := 0
	for _, token := range vocab {
		if len(token) > longest {
			longest = len(token)
		}
	}
	return longest
}

func TestRefusalSiteVocabularyIsClosedAndDistinct(t *testing.T) {
	vocab := refusalSiteVocabulary()
	if len(vocab) != int(refusalSiteCount) {
		t.Fatalf("table holds %d tokens but refusalSiteCount is %d", len(vocab), refusalSiteCount)
	}
	if len(vocab) != 15 {
		t.Fatalf("vocabulary has %d tokens, want 15; adding a site is a deliberate act", len(vocab))
	}
	seen := make(map[string]struct{}, len(vocab))
	for site, token := range vocab {
		if token == "" {
			t.Fatalf("site %d has no table entry, so RefuseSite would write an empty token", site)
		}
		if refusalSite(site).token() != token {
			t.Fatalf("site %d token() = %q, want %q", site, refusalSite(site).token(), token)
		}
		if len(token) > len(refusalSiteLongestToken) {
			t.Fatalf("token %q is longer than refusalSiteLongestToken %q, so the frame would truncate it", token, refusalSiteLongestToken)
		}
		if strings.ContainsAny(token, "=\n\r/\\ ") {
			t.Fatalf("token %q carries a framing, path or whitespace byte", token)
		}
		if _, dup := seen[token]; dup {
			t.Fatalf("token %q appears twice, so two sites would be indistinguishable on the wire", token)
		}
		seen[token] = struct{}{}
	}
	if len(refusalSiteLongestToken) != maxTokenLen(vocab) {
		t.Fatalf("refusalSiteLongestToken is %q but the longest token is %d bytes",
			refusalSiteLongestToken, maxTokenLen(vocab))
	}
	if refusalSiteMaxToken() != refusalSiteLongestToken {
		t.Fatalf("refusalSiteLongestToken %q is not the table's longest member %q",
			refusalSiteLongestToken, refusalSiteMaxToken())
	}
}

func refusalSiteMaxToken() string {
	longest := ""
	for _, token := range refusalSiteVocabulary() {
		if len(token) > len(longest) {
			longest = token
		}
	}
	return longest
}

// TestRefusalSiteNoTokenIsAPrefixOfAnother pins the property that makes a complete
// frame unambiguous. It does not make a TORN frame unambiguous — a host reader must
// still require the trailing newline before accepting a token, and that requirement
// is recorded in the ledger rather than enforced here.
func TestRefusalSiteNoTokenIsAPrefixOfAnother(t *testing.T) {
	vocab := refusalSiteVocabulary()
	for i, a := range vocab {
		for j, b := range vocab {
			if i != j && strings.HasPrefix(b, a) {
				t.Fatalf("token %q is a prefix of %q, so a truncated frame would be ambiguous", a, b)
			}
		}
	}
}

// TestRefuseSiteRejectsAnIndexOutsideTheTable is the runtime half of the closure
// argument. The unexported index type stops a caller in another package from
// forging a site at compile time; this proves that an out-of-range value that does
// reach RefuseSite writes nothing rather than an empty or partial token.
func TestRefuseSiteRejectsAnIndexOutsideTheTable(t *testing.T) {
	for _, site := range []refusalSite{-1, refusalSiteCount, refusalSiteCount + 1, 1 << 20} {
		if got := site.token(); got != "" {
			t.Fatalf("site %d token() = %q, want empty", site, got)
		}
		if frame := refusalSiteFrame(site); frame != nil {
			t.Fatalf("site %d produced frame %q, want nil", site, frame)
		}
	}
}

func TestRefusalSiteFrame(t *testing.T) {
	cases := []struct {
		name string
		site refusalSite
		want string
	}{
		{"longest_token", SiteBootstrap, "phebs_site=bootstrap\n"},
		{"shortest_token", SiteArgv, "phebs_site=argv\n"},
		{"identity", SiteIdentity, "phebs_site=identity\n"},
		{"selector", SiteSelector, "phebs_site=selector\n"},
		{"allow_live", SiteAllowanceLive, "phebs_site=allow_live\n"},
		{"allow_controls", SiteAllowanceControls, "phebs_site=allow_controls\n"},
		{"allow_seal", SiteAllowanceSeal, "phebs_site=allow_seal\n"},
		{"allow_digest", SiteAllowanceDigest, "phebs_site=allow_digest\n"},
		{"allow_binding", SiteAllowanceBinding, "phebs_site=allow_binding\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(refusalSiteFrame(tc.site))
			if got != tc.want {
				t.Fatalf("frame = %q, want %q", got, tc.want)
			}
		})
	}
	for site, token := range refusalSiteVocabulary() {
		frame := refusalSiteFrame(refusalSite(site))
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
	// Both figures are derived, not restated: the bound must be exactly the prefix
	// plus the longest declared token plus the newline, and the prefix must not
	// itself contain a byte that could terminate a frame early.
	if want := len(refusalSitePrefix) + len(refusalSiteLongestToken) + 1; refusalSiteFrameBytes != want {
		t.Fatalf("refusalSiteFrameBytes is %d, want the derived %d", refusalSiteFrameBytes, want)
	}
	if strings.ContainsAny(refusalSitePrefix, "\n\r") {
		t.Fatalf("prefix %q contains a line terminator", refusalSitePrefix)
	}
	if !strings.HasSuffix(refusalSitePrefix, "=") {
		t.Fatalf("prefix %q does not end in the separator a host reader splits on", refusalSitePrefix)
	}
}
