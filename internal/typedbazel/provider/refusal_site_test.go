package provider

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// The neutral-preparation harness reports a STOP by printing the error that
// internal/typedsandbox returned. Since leaf-4.107 that error names the first failing
// predicate and, on the two content-dependent predicates, retains the container's captured
// stderr as a bounded hex prefix. Since leaf-4.115 the in-container helper writes a
// `phebs_site=<token>\n` frame to descriptor 2 immediately before each of its eleven
// terminal exit-125 refusals, so the frame arrives inside that already-existing transport.
//
// Nothing turned those hex bytes back into a site name, which meant a STOP handed a human
// 157 reason bytes and a hex string to decode by hand. This file is that reader. It is
// deliberately test-only: exporting anything from internal/typedsandbox for a diagnostic
// would move the pinned production helper identity for no runtime benefit, so the frame
// prefix is repeated here and the vocabulary is NOT. The reader echoes whatever bounded
// token it finds and leaves the writer side as the only authority on which tokens exist.
const (
	refusalSiteFramePrefix = "phebs_site="
	stderrPrefixHexField   = "stderr_prefix_hex="
	stderrLenField         = "stderr_len="
)

// The two suffixes the reader appends to an absence note. They are constants rather than
// inline literals so the no-cause test scans the same strings the reader actually emits.
const (
	refusalStderrLenSuffix    = " stderr_len="
	refusalHexTruncatedSuffix = " hex_truncated"
)

// refusalWirePrefixBytes mirrors internal/typedsandbox's reportWirePrefixBytes, the bound
// boundedWireHex applies before the hex reaches a refusal. It is repeated here for the same
// reason the frame prefix is: importing it would mean exporting it. refusalText below
// applies it so the tests parse a prefix that is truncated the way the real one is, which
// is what makes rule (i) testable at all rather than merely stated.
const refusalWirePrefixBytes = 512

// refusalSiteTokenMax bounds what the reader will echo back into a failure message. The
// writer's real vocabulary tops out at nine bytes, but this reader cannot see that
// vocabulary, so it enforces a generous bound of its own rather than trusting the input:
// a longer run is not a token and is reported as torn instead of copied.
const refusalSiteTokenMax = 32

// refusalSiteNoteMax is the longest note readRefusalSite can return, in bytes, and is
// measured by TestReadRefusalSiteNoteIsBounded rather than assumed. The widest concrete
// note is "site_absent stderr_len=2000000000 hex_truncated" at exactly 47 bytes; "site="
// plus a 32-byte token is 37, so it does not bound.
const refusalSiteNoteMax = 47

// refusalSiteNoteShapes is the CLOSED set of note shapes. Every note must begin with
// exactly one of these, and none of them carries a cause: the reader can say a token was
// found, torn, undecodable, absent or unreported, and it can never say runc, OOM,
// pre-helper or anything else. That restriction is the whole of leaf-4.115's HANDOFF-H2
// rule (iii) — a missing token is inconclusive, because the writer drops the frame on an
// fstat failure, on a setnonblock failure and on every descriptor kind outside
// regular-file/pipe/socket, so absence has at least four causes and naming one from here
// would be inventing evidence.
var refusalSiteNoteShapes = []string{
	"site=",
	"site_torn",
	"site_undecodable",
	"site_unreported",
	"site_absent",
}

// closedNoteShape reports whether note belongs to the closed set and, for the two shapes
// that append numbers, whether those numbers are well formed. It is the classifier the
// boundedness and no-cause tests assert against, and it is exercised in both directions:
// TestReadRefusalSiteNoteShapesCarryNoCause requires it to ACCEPT every note the reader
// actually produces and to REJECT a note that names a cause.
func closedNoteShape(note string) bool {
	for _, shape := range refusalSiteNoteShapes {
		if !strings.HasPrefix(note, shape) {
			continue
		}
		switch shape {
		case "site=":
			return closedToken(note[len(shape):])
		case "site_absent":
			return closedAbsentTail(note[len(shape):])
		default:
			return note == shape
		}
	}
	return false
}

func closedToken(tok string) bool {
	if tok == "" || len(tok) > refusalSiteTokenMax {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if !refusalSiteTokenByte(tok[i]) {
			return false
		}
	}
	return true
}

// closedAbsentTail accepts only "", " stderr_len=<digits>" and
// " stderr_len=<digits> hex_truncated". Anything else — a cause, a token, a stray byte —
// is rejected, which is what makes the absence note unable to smuggle content.
func closedAbsentTail(tail string) bool {
	if tail == "" {
		return true
	}
	if !strings.HasPrefix(tail, refusalStderrLenSuffix) {
		return false
	}
	rest := tail[len(refusalStderrLenSuffix):]
	if i := strings.Index(rest, refusalHexTruncatedSuffix); i >= 0 {
		if rest[i+len(refusalHexTruncatedSuffix):] != "" {
			return false
		}
		rest = rest[:i]
	}
	if rest == "" {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false
		}
	}
	return true
}

func refusalSiteTokenByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

func hexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// readRefusalSite turns the bounded stderr hex a supervisor-report refusal retained into
// a short note naming the refusal site, for the harness line that reports a STOP.
//
// It honours the four rules leaf-4.115's HANDOFF-H2 set for any host reader:
//
//	(i)   the absence inference comes from stderr_len, never from an empty hex prefix,
//	      because boundedWireHex truncates at 512 bytes and the frame is written LAST —
//	      so a long stderr pushes the frame out of the hex while stderr_len still counts
//	      it, and that case is reported as hex_truncated rather than as a plain absence;
//	(ii)  a token is accepted only when newline-terminated, non-empty, within the bound
//	      and wholly inside the charset, since the writer performs one unretried write
//	      and a partial write must not be parsed as a site;
//	(iii) the note names no cause, so a missing token stays inconclusive;
//	(iv)  a refusal that carries no stderr field at all — which is what happens when
//	      wire.err short-circuits before supervisorReportRefusal is reached — reports
//	      site_unreported rather than an absence that was never measured.
func readRefusalSite(errText string) string {
	i := strings.Index(errText, stderrPrefixHexField)
	if i < 0 {
		return "site_unreported"
	}
	rest := errText[i+len(stderrPrefixHexField):]
	j := 0
	for j < len(rest) && hexDigit(rest[j]) {
		j++
	}
	raw, err := hex.DecodeString(rest[:j])
	if err != nil {
		// An odd-length run means the field was itself cut by an outer bound, so the
		// frame may or may not have been in it. Say so rather than guessing.
		return "site_undecodable"
	}
	if p := bytes.Index(raw, []byte(refusalSiteFramePrefix)); p >= 0 {
		after := raw[p+len(refusalSiteFramePrefix):]
		nl := bytes.IndexByte(after, '\n')
		if nl <= 0 || nl > refusalSiteTokenMax {
			return "site_torn"
		}
		if !closedToken(string(after[:nl])) {
			return "site_torn"
		}
		return "site=" + string(after[:nl])
	}
	n, ok := refusalStderrLen(errText)
	if !ok {
		return "site_absent"
	}
	note := "site_absent" + refusalStderrLenSuffix + strconv.Itoa(n)
	if n > len(raw) {
		note += refusalHexTruncatedSuffix
	}
	return note
}

func refusalStderrLen(errText string) (int, bool) {
	i := strings.Index(errText, stderrLenField)
	if i < 0 {
		return 0, false
	}
	rest := errText[i+len(stderrLenField):]
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	if j == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:j])
	if err != nil {
		return 0, false
	}
	return n, true
}

// refusalText reproduces the exact shape of the two content-dependent refusals
// internal/typedsandbox emits, so these tests parse the real format rather than a
// paraphrase of it — including the 512-byte prefix bound. predicate selects between the
// undecodable-envelope form, which carries both streams, and the non-empty-stderr form,
// which carries only stderr.
func refusalText(predicate string, stdout, stderr []byte) string {
	// The real refusal reports the FULL length of each stream but hex-encodes only the
	// bounded prefix, and that asymmetry is the whole of rule (i). Truncating before taking
	// the length would make the two agree and the rule untestable.
	stderrLen := len(stderr)
	stderrHex := stderr
	if len(stderrHex) > refusalWirePrefixBytes {
		stderrHex = stderrHex[:refusalWirePrefixBytes]
	}
	if predicate == "report_decode" {
		stdoutLen := len(stdout)
		stdoutHex := stdout
		if len(stdoutHex) > refusalWirePrefixBytes {
			stdoutHex = stdoutHex[:refusalWirePrefixBytes]
		}
		return "supervisor_report_refusal predicate=report_decode decode_error=\"unexpected end of JSON input\"" +
			" stdout_len=" + strconv.Itoa(stdoutLen) + " stdout_prefix_hex=" + hex.EncodeToString(stdoutHex) +
			" stderr_len=" + strconv.Itoa(stderrLen) + " stderr_prefix_hex=" + hex.EncodeToString(stderrHex)
	}
	return "supervisor_report_refusal predicate=nonempty_stderr stderr_len=" +
		strconv.Itoa(stderrLen) + " stderr_prefix_hex=" + hex.EncodeToString(stderrHex)
}

// refusalTextWithLen builds a refusal whose reported stderr_len disagrees with the prefix
// it carries, which is exactly what a truncated real refusal looks like. It lets the bound
// test drive a nine-digit length without allocating nine hundred megabytes.
func refusalTextWithLen(n int) string {
	return "supervisor_report_refusal predicate=report_decode stderr_len=" + strconv.Itoa(n) + " stderr_prefix_hex="
}

func frameBytes(tok string) []byte { return []byte("phebs_site=" + tok + "\n") }

func TestReadRefusalSite(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"complete frame alone", refusalText("report_decode", nil, frameBytes("selector")), "site=selector"},
		{"frame after earlier stderr", refusalText("nonempty_stderr", nil, append([]byte("runtime: warning\n"), frameBytes("encode")...)), "site=encode"},
		{"nonempty stderr predicate", refusalText("nonempty_stderr", nil, frameBytes("scratch")), "site=scratch"},
		{"first of two frames wins", refusalText("nonempty_stderr", nil, append(frameBytes("identity"), frameBytes("argv")...)), "site=identity"},
		{"longest real token", refusalText("report_decode", nil, frameBytes("bootstrap")), "site=bootstrap"},
		{"shortest real token", refusalText("report_decode", nil, frameBytes("argv")), "site=argv"},
		{"token at the bound", refusalText("report_decode", nil, frameBytes(strings.Repeat("a", refusalSiteTokenMax))), "site=" + strings.Repeat("a", refusalSiteTokenMax)},
		{"no newline", refusalText("report_decode", nil, []byte("phebs_site=bootstrap")), "site_torn"},
		{"prefix with nothing after it", refusalText("report_decode", nil, []byte("phebs_site=")), "site_torn"},
		{"empty token", refusalText("report_decode", nil, []byte("phebs_site=\n")), "site_torn"},
		{"token over the bound", refusalText("report_decode", nil, frameBytes(strings.Repeat("a", refusalSiteTokenMax+1))), "site_torn"},
		{"space in the token", refusalText("report_decode", nil, []byte("phebs_site=boot strap\n")), "site_torn"},
		{"uppercase token", refusalText("report_decode", nil, []byte("phebs_site=BOOTSTRAP\n")), "site_torn"},
		{"path in the token", refusalText("report_decode", nil, []byte("phebs_site=/var/lib/secret\n")), "site_torn"},
		{"empty stderr", refusalText("report_decode", nil, nil), "site_absent stderr_len=0"},
		{"short stderr with no frame", refusalText("report_decode", nil, []byte("hello!!")), "site_absent stderr_len=7"},
		{"long stderr with no frame", refusalText("report_decode", nil, bytes.Repeat([]byte("y"), 600)), "site_absent stderr_len=600 hex_truncated"},
		{"no stderr field at all", "execution refused by the typed sandbox", "site_unreported"},
		{"odd length hex run", "supervisor_report_refusal predicate=report_decode stderr_len=3 stderr_prefix_hex=616", "site_undecodable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readRefusalSite(tc.text); got != tc.want {
				t.Fatalf("readRefusalSite = %q, want %q", got, tc.want)
			}
		})
	}
	if len(cases) < 15 {
		t.Fatalf("the table carries %d cases; the ledger requires at least 15", len(cases))
	}
}

// TestReadRefusalSiteTakesAbsenceFromTheLengthNotFromTheHexPrefix is HANDOFF-H2 rule (i).
// The frame is written LAST and boundedWireHex truncates at 512 bytes, so a stderr longer
// than the prefix can hide a frame that was genuinely written. A reader that reported a
// plain absence there would be reporting its own truncation as evidence about the
// container. The two halves of the rule are asserted together: a long stderr must say
// hex_truncated, and a short one that fits entirely must not.
func TestReadRefusalSiteTakesAbsenceFromTheLengthNotFromTheHexPrefix(t *testing.T) {
	long := refusalText("report_decode", nil, bytes.Repeat([]byte("y"), 900))
	if got := readRefusalSite(long); got != "site_absent stderr_len=900 hex_truncated" {
		t.Fatalf("a truncated prefix reported %q; it must not masquerade as a measured absence", got)
	}
	short := refusalText("report_decode", nil, bytes.Repeat([]byte("y"), 40))
	if got := readRefusalSite(short); got != "site_absent stderr_len=40" {
		t.Fatalf("a wholly-present prefix reported %q, want site_absent stderr_len=40", got)
	}
	zero := refusalText("report_decode", nil, nil)
	if got := readRefusalSite(zero); got != "site_absent stderr_len=0" {
		t.Fatalf("the only strong absence reported %q, want site_absent stderr_len=0", got)
	}
	// A frame that survives inside the prefix is still found even when the stderr is long,
	// so hex_truncated is a statement about absence and never suppresses a token. The frame
	// is placed first here, which the real writer never does, precisely to separate the two
	// questions: truncation must not blind the reader to a token it can still see.
	survives := refusalText("report_decode", nil, append(frameBytes("timer"), bytes.Repeat([]byte("y"), 900)...))
	if got := readRefusalSite(survives); got != "site=timer" {
		t.Fatalf("a frame inside the prefix reported %q, want site=timer", got)
	}
}

// TestReadRefusalSiteRejectsATornFrame is HANDOFF-H2 rule (ii). The writer performs one
// unretried write, so a partial frame is possible and must never be reported as a site:
// a truncated token would name a site that may not have been the one reached.
func TestReadRefusalSiteRejectsATornFrame(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("phebs_site=boot"),
		[]byte("phebs_site="),
		[]byte("phebs_site=\n"),
		[]byte("phebs_site=" + strings.Repeat("z", refusalSiteTokenMax+1) + "\n"),
	} {
		text := refusalText("report_decode", nil, raw)
		if got := readRefusalSite(text); got != "site_torn" {
			t.Fatalf("stderr %q reported %q, want site_torn", raw, got)
		}
	}
	// Negative control: the same reader does accept the completed form of the first
	// case, so the rejections above cannot be explained by the prefix never matching.
	if got := readRefusalSite(refusalText("report_decode", nil, []byte("phebs_site=boot\n"))); got != "site=boot" {
		t.Fatalf("the completed frame reported %q, want site=boot", got)
	}
	// A complete frame followed by later stderr is NOT torn. The writer emits exactly one
	// frame, so whatever follows it belongs to someone else and must not invalidate it.
	later := append([]byte("phebs_site=boot\n"), []byte("strap\n")...)
	if got := readRefusalSite(refusalText("report_decode", nil, later)); got != "site=boot" {
		t.Fatalf("a complete frame with trailing stderr reported %q, want site=boot", got)
	}
}

// TestReadRefusalSiteNoteShapesCarryNoCause is HANDOFF-H2 rule (iii), and it asserts the
// property the reader can actually guarantee. The reader cannot know the writer's
// vocabulary, so it cannot reject a token that happens to spell a cause; what it CAN
// guarantee is that its own grammar is cause-free and that a note can only ever be one of
// the closed forms. Both halves are checked: the classifier is exercised in both
// directions against malformed notes, and every fixed part of the grammar is scanned for
// causal vocabulary.
func TestReadRefusalSiteNoteShapesCarryNoCause(t *testing.T) {
	accepted := []string{
		"site=bootstrap", "site=argv", "site_torn", "site_undecodable", "site_unreported",
		"site_absent", "site_absent stderr_len=0", "site_absent stderr_len=600 hex_truncated",
	}
	for _, note := range accepted {
		if !closedNoteShape(note) {
			t.Fatalf("closedNoteShape rejected %q, which the reader can produce", note)
		}
	}
	rejected := []string{
		"site=BOOTSTRAP", "site=", "site=boot strap",
		"site_absent stderr_len=", "site_absent stderr_len=x",
		"site_absent stderr_len=0 pre-helper", "site_absent oom_killed=true",
		"site_absent stderr_len=0 hex_truncated extra",
		"site_unknown", "site_torn because runc reserved 125", "/var/lib/phebs", "",
	}
	for _, note := range rejected {
		if closedNoteShape(note) {
			t.Fatalf("closedNoteShape accepted %q, which is malformed or smuggles content", note)
		}
	}
	// The fixed vocabulary the reader contributes to a note — as distinct from a token it
	// merely echoes — must not name a cause. Absence has at least four causes on the writer
	// side (an fstat failure, a setnonblock failure, a dropped descriptor kind and a
	// saturated transport) plus the pre-helper class runc reserves 125 for, so a reader
	// that asserted one would be inventing evidence.
	causes := []string{
		"runc", "oom", "kill", "panic", "memory", "pre-helper", "prehelper", "exit",
		"signal", "timeout", "capacity", "custody", "cause", "because",
	}
	fixed := append([]string{refusalStderrLenSuffix, refusalHexTruncatedSuffix}, refusalSiteNoteShapes...)
	for _, part := range fixed {
		for _, cause := range causes {
			if containsWord(part, cause) {
				t.Fatalf("the note grammar part %q names a cause (%q)", part, cause)
			}
		}
	}
	// Negative control for the control: the scanner is a whole-word matcher, so it must
	// still find a cause word that really is standing alone. Without this the scan above
	// could pass on a matcher that finds nothing at all — which is exactly how it behaved
	// on the first draft, where a plain substring scan reported " hex_truncated" as naming
	// runc because "truncated" contains those four letters.
	if !containsWord("site_absent because runc reserved 125", "runc") {
		t.Fatal("containsWord missed a standalone cause word, so the grammar scan proves nothing")
	}
	if containsWord(" hex_truncated", "runc") {
		t.Fatal("containsWord matched runc inside truncated, the false positive this test exists to avoid")
	}
}

// containsWord reports whether word occurs in s bounded by non-letters. A plain substring
// scan is wrong here: " hex_truncated" contains the four letters of "runc", and reporting
// that as a cause would be a false positive. Both s and word must already be lowercase.
func containsWord(s, word string) bool {
	isLetter := func(c byte) bool { return c >= 'a' && c <= 'z' }
	for i := 0; i+len(word) <= len(s); i++ {
		if s[i:i+len(word)] != word {
			continue
		}
		if i > 0 && isLetter(s[i-1]) {
			continue
		}
		if j := i + len(word); j < len(s) && isLetter(s[j]) {
			continue
		}
		return true
	}
	return false
}

// TestReadRefusalSiteReportsUnreportedWhenNoStderrFieldExists is HANDOFF-H2 rule (iv).
// When wire.err is set the sandbox returns before supervisorReportRefusal is reached, so
// no host-side diagnostic exists at all. Reporting site_absent there would claim a
// measurement that never happened.
func TestReadRefusalSiteReportsUnreportedWhenNoStderrFieldExists(t *testing.T) {
	for _, text := range []string{
		"execution refused by the typed sandbox",
		"typed sandbox: context deadline exceeded",
		"supervisor_report_refusal predicate=schema_mismatch",
	} {
		if got := readRefusalSite(text); got != "site_unreported" {
			t.Fatalf("%q reported %q, want site_unreported", text, got)
		}
	}
	// Negative control: adding the field turns the same refusal into a measured absence.
	if got := readRefusalSite("supervisor_report_refusal predicate=schema_mismatch stderr_len=0 stderr_prefix_hex="); got != "site_absent stderr_len=0" {
		t.Fatalf("the field-bearing form reported %q, want site_absent stderr_len=0", got)
	}
}

// TestReadRefusalSiteNoteIsBounded measures refusalSiteNoteMax rather than trusting it, by
// driving every branch with the widest input that branch accepts.
func TestReadRefusalSiteNoteIsBounded(t *testing.T) {
	widest := []string{
		refusalText("report_decode", nil, frameBytes(strings.Repeat("a", refusalSiteTokenMax))),
		refusalTextWithLen(2_000_000_000),
		refusalText("report_decode", nil, []byte("phebs_site=")),
		"no fields at all",
		"stderr_prefix_hex=abc",
	}
	for _, text := range widest {
		note := readRefusalSite(text)
		if len(note) > refusalSiteNoteMax {
			t.Fatalf("note %q is %d bytes, over the %d-byte bound", note, len(note), refusalSiteNoteMax)
		}
		if !closedNoteShape(note) {
			t.Fatalf("note %q is outside the closed shape set", note)
		}
	}
	// The bound is derived from the widest reachable note, not asserted beside it: the
	// truncated-absence form with a nine-digit length is the longest string the reader can
	// build, and it must fit.
	if longest := "site_absent stderr_len=2000000000 hex_truncated"; len(longest) > refusalSiteNoteMax {
		t.Fatalf("refusalSiteNoteMax=%d cannot hold %q (%d bytes)", refusalSiteNoteMax, longest, len(longest))
	}
	if got := readRefusalSite(refusalTextWithLen(2_000_000_000)); got != "site_absent stderr_len=2000000000 hex_truncated" {
		t.Fatalf("the widest note is %q (%d bytes), so refusalSiteNoteMax=%d is not the real bound", got, len(got), refusalSiteNoteMax)
	}
}

// TestReadRefusalSiteNeverEchoesADecodedByte feeds the reader stderr whose only frame-like
// content is invalid, and requires the note to carry none of it. This is the privacy
// property stated negatively: the decoded container stream may contain anything, and the
// only part of it that may reach a failure message is a validated token.
func TestReadRefusalSiteNeverEchoesADecodedByte(t *testing.T) {
	secret := "phebs_site=/var/lib/phebs-typed-index/SECRET phebs_site=../../etc/passwd\n"
	note := readRefusalSite(refusalText("report_decode", nil, []byte(secret)))
	if note != "site_torn" {
		t.Fatalf("reported %q, want site_torn", note)
	}
	for _, fragment := range []string{"SECRET", "passwd", "var", "lib", "etc", "/"} {
		if strings.Contains(note, fragment) {
			t.Fatalf("note %q echoes %q from the decoded stream", note, fragment)
		}
	}
}
