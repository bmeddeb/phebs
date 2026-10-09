package main

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// The native Settings bridge fixture reports a STOP through typedSettingsNativeSettle.
// That line carries the closed classification (typedServeReason) plus bounded phase
// reports, so a refusal on one of the sandbox's two content-dependent predicates classifies
// only as "failed": the `phebs_site=<token>` frame the in-container helper writes to
// descriptor 2 immediately before each of its terminal exit-125 refusals stays inside the
// retained stderr hex, and nothing turned those bytes back into a site name.
//
// This file is that reader. It is deliberately test-only, for the same reason the
// precedent in internal/typedbazel/provider/refusal_site_test.go is: exporting anything
// from internal/typedsandbox for a diagnostic would move the pinned production helper
// identity for no runtime benefit, so the frame prefix is repeated here and the
// vocabulary is NOT. The reader echoes whatever bounded token it finds and leaves the
// writer side as the only authority on which tokens exist.
//
// It honours the four rules the precedent fixed for any host reader:
//
//	(i)   the absence inference comes from stderr_len, never from an empty hex prefix,
//	      because the retained prefix is bounded and the frame is written LAST — so a
//	      long stderr pushes the frame out of the hex while stderr_len still counts it,
//	      and that case is reported as hex_truncated rather than as a plain absence;
//	(ii)  a token is accepted only when newline-terminated, non-empty, within the bound
//	      and wholly inside the charset, since the writer performs one unretried write
//	      and a partial write must not be parsed as a site;
//	(iii) the note names no cause, so a missing token stays inconclusive;
//	(iv)  a refusal that carries no stderr field at all reports site_unreported rather
//	      than an absence that was never measured.
const (
	typedSettingsNativeRefusalFramePrefix = "phebs_site="
	typedSettingsNativeRefusalHexField    = "stderr_prefix_hex="
	typedSettingsNativeRefusalLenField    = "stderr_len="
)

// The two suffixes the reader appends to an absence note. They are constants rather than
// inline literals so the no-cause test scans the same strings the reader actually emits.
const (
	typedSettingsNativeRefusalLenSuffix    = " stderr_len="
	typedSettingsNativeRefusalTruncatedSfx = " hex_truncated"
)

// typedSettingsNativeRefusalWirePrefixBytes mirrors internal/typedsandbox's
// reportWirePrefixBytes, the bound boundedWireHex applies before the hex reaches a
// refusal. It is repeated here for the same reason the frame prefix is: importing it
// would mean exporting it. typedSettingsNativeRefusalText below applies it so the tests
// parse a prefix that is truncated the way the real one is, which is what makes rule (i)
// testable at all rather than merely stated.
const typedSettingsNativeRefusalWirePrefixBytes = 512

// typedSettingsNativeRefusalTokenMax bounds what the reader will echo back into a failure
// message. The writer's real vocabulary tops out at fourteen bytes, but this reader cannot
// see that vocabulary, so it enforces a generous bound of its own rather than trusting the
// input: a longer run is not a token and is reported as torn instead of copied.
const typedSettingsNativeRefusalTokenMax = 32

// typedSettingsNativeRefusalNoteMax is the longest note the reader can return, in bytes,
// and is measured by TestTypedSettingsNativeRefusalSiteNoteIsBounded rather than assumed.
// The widest concrete note is "site_absent stderr_len=2000000000 hex_truncated" at
// exactly 47 bytes; "site=" plus a 32-byte token is 37, so it does not bound.
const typedSettingsNativeRefusalNoteMax = 47

// typedSettingsNativeRefusalNoteShapes is the CLOSED set of note shapes. Every note must
// begin with exactly one of these, and none of them carries a cause: the reader can say a
// token was found, torn, undecodable, absent or unreported, and it can never say runc,
// OOM, pre-helper or anything else. That restriction is the whole of rule (iii) — a
// missing token is inconclusive, because the writer drops the frame on an fstat failure,
// on a setnonblock failure and on every descriptor kind outside regular-file/pipe/socket,
// so absence has at least four causes and naming one from here would be inventing evidence.
var typedSettingsNativeRefusalNoteShapes = []string{
	"site=",
	"site_torn",
	"site_undecodable",
	"site_unreported",
	"site_absent",
}

// typedSettingsNativeRefusalNoteShape reports whether note belongs to the closed set and,
// for the two shapes that append numbers, whether those numbers are well formed. It is the
// classifier the boundedness and no-cause tests assert against, and it is exercised in
// both directions: the no-cause test requires it to ACCEPT every note the reader actually
// produces and to REJECT a note that names a cause.
func typedSettingsNativeRefusalNoteShape(note string) bool {
	for _, shape := range typedSettingsNativeRefusalNoteShapes {
		if !strings.HasPrefix(note, shape) {
			continue
		}
		switch shape {
		case "site=":
			return typedSettingsNativeRefusalToken(note[len(shape):])
		case "site_absent":
			return typedSettingsNativeAbsentTail(note[len(shape):])
		default:
			return note == shape
		}
	}
	return false
}

func typedSettingsNativeRefusalToken(tok string) bool {
	if tok == "" || len(tok) > typedSettingsNativeRefusalTokenMax {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if !typedSettingsNativeRefusalTokenByte(tok[i]) {
			return false
		}
	}
	return true
}

// typedSettingsNativeAbsentTail accepts only "", " stderr_len=<digits>" and
// " stderr_len=<digits> hex_truncated". Anything else — a cause, a token, a stray byte —
// is rejected, which is what makes the absence note unable to smuggle content.
func typedSettingsNativeAbsentTail(tail string) bool {
	if tail == "" {
		return true
	}
	if !strings.HasPrefix(tail, typedSettingsNativeRefusalLenSuffix) {
		return false
	}
	rest := tail[len(typedSettingsNativeRefusalLenSuffix):]
	if i := strings.Index(rest, typedSettingsNativeRefusalTruncatedSfx); i >= 0 {
		if rest[i+len(typedSettingsNativeRefusalTruncatedSfx):] != "" {
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

func typedSettingsNativeRefusalTokenByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

func typedSettingsNativeHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// typedSettingsNativeRefusalSite turns the bounded stderr hex a supervisor-report refusal
// retained into a short note naming the refusal site, for the fixture line that reports a
// STOP. The input is the raw joined error text; the output is one of the closed note
// shapes above and never any other byte of the error.
func typedSettingsNativeRefusalSite(errText string) string {
	i := strings.Index(errText, typedSettingsNativeRefusalHexField)
	if i < 0 {
		return "site_unreported"
	}
	rest := errText[i+len(typedSettingsNativeRefusalHexField):]
	j := 0
	for j < len(rest) && typedSettingsNativeHexDigit(rest[j]) {
		j++
	}
	raw, err := hex.DecodeString(rest[:j])
	if err != nil {
		// An odd-length run means the field was itself cut by an outer bound, so the
		// frame may or may not have been in it. Say so rather than guessing.
		return "site_undecodable"
	}
	if p := bytes.Index(raw, []byte(typedSettingsNativeRefusalFramePrefix)); p >= 0 {
		after := raw[p+len(typedSettingsNativeRefusalFramePrefix):]
		nl := bytes.IndexByte(after, '\n')
		if nl <= 0 || nl > typedSettingsNativeRefusalTokenMax {
			return "site_torn"
		}
		if !typedSettingsNativeRefusalToken(string(after[:nl])) {
			return "site_torn"
		}
		return "site=" + string(after[:nl])
	}
	n, ok := typedSettingsNativeStderrLen(errText)
	if !ok {
		return "site_absent"
	}
	note := "site_absent" + typedSettingsNativeRefusalLenSuffix + strconv.Itoa(n)
	if n > len(raw) {
		note += typedSettingsNativeRefusalTruncatedSfx
	}
	return note
}

func typedSettingsNativeStderrLen(errText string) (int, bool) {
	i := strings.Index(errText, typedSettingsNativeRefusalLenField)
	if i < 0 {
		return 0, false
	}
	rest := errText[i+len(typedSettingsNativeRefusalLenField):]
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

// typedSettingsNativeRefusalText reproduces the exact shape of the two content-dependent
// refusals internal/typedsandbox emits, so these tests parse the real format rather than a
// paraphrase of it — including the 512-byte prefix bound. predicate selects between the
// undecodable-envelope form, which carries both streams, and the non-empty-stderr form,
// which carries only stderr.
func typedSettingsNativeRefusalText(predicate string, stdout, stderr []byte) string {
	// The real refusal reports the FULL length of each stream but hex-encodes only the
	// bounded prefix, and that asymmetry is the whole of rule (i). Truncating before taking
	// the length would make the two agree and the rule untestable.
	stderrLen := len(stderr)
	stderrHex := stderr
	if len(stderrHex) > typedSettingsNativeRefusalWirePrefixBytes {
		stderrHex = stderrHex[:typedSettingsNativeRefusalWirePrefixBytes]
	}
	if predicate == "report_decode" {
		stdoutLen := len(stdout)
		stdoutHex := stdout
		if len(stdoutHex) > typedSettingsNativeRefusalWirePrefixBytes {
			stdoutHex = stdoutHex[:typedSettingsNativeRefusalWirePrefixBytes]
		}
		return "supervisor_report_refusal predicate=report_decode decode_error=\"unexpected end of JSON input\"" +
			" stdout_len=" + strconv.Itoa(stdoutLen) + " stdout_prefix_hex=" + hex.EncodeToString(stdoutHex) +
			" stderr_len=" + strconv.Itoa(stderrLen) + " stderr_prefix_hex=" + hex.EncodeToString(stderrHex)
	}
	return "supervisor_report_refusal predicate=nonempty_stderr stderr_len=" +
		strconv.Itoa(stderrLen) + " stderr_prefix_hex=" + hex.EncodeToString(stderrHex)
}

// typedSettingsNativeRefusalTextWithLen builds a refusal whose reported stderr_len
// disagrees with the prefix it carries, which is exactly what a truncated real refusal
// looks like. It lets the bound test drive a nine-digit length without allocating nine
// hundred megabytes.
func typedSettingsNativeRefusalTextWithLen(n int) string {
	return "supervisor_report_refusal predicate=report_decode stderr_len=" + strconv.Itoa(n) + " stderr_prefix_hex="
}

func typedSettingsNativeRefusalFrame(tok string) []byte { return []byte("phebs_site=" + tok + "\n") }

func TestTypedSettingsNativeRefusalSite(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"complete frame alone", typedSettingsNativeRefusalText("report_decode", nil, typedSettingsNativeRefusalFrame("selector")), "site=selector"},
		{"frame after earlier stderr", typedSettingsNativeRefusalText("nonempty_stderr", nil, append([]byte("runtime: warning\n"), typedSettingsNativeRefusalFrame("encode")...)), "site=encode"},
		{"nonempty stderr predicate", typedSettingsNativeRefusalText("nonempty_stderr", nil, typedSettingsNativeRefusalFrame("scratch")), "site=scratch"},
		{"first of two frames wins", typedSettingsNativeRefusalText("nonempty_stderr", nil, append(typedSettingsNativeRefusalFrame("identity"), typedSettingsNativeRefusalFrame("argv")...)), "site=identity"},
		{"longest real token", typedSettingsNativeRefusalText("report_decode", nil, typedSettingsNativeRefusalFrame("allow_bootread")), "site=allow_bootread"},
		{"shortest real token", typedSettingsNativeRefusalText("report_decode", nil, typedSettingsNativeRefusalFrame("argv")), "site=argv"},
		{"token at the bound", typedSettingsNativeRefusalText("report_decode", nil, typedSettingsNativeRefusalFrame(strings.Repeat("a", typedSettingsNativeRefusalTokenMax))), "site=" + strings.Repeat("a", typedSettingsNativeRefusalTokenMax)},
		{"no newline", typedSettingsNativeRefusalText("report_decode", nil, []byte("phebs_site=bootstrap")), "site_torn"},
		{"prefix with nothing after it", typedSettingsNativeRefusalText("report_decode", nil, []byte("phebs_site=")), "site_torn"},
		{"empty token", typedSettingsNativeRefusalText("report_decode", nil, []byte("phebs_site=\n")), "site_torn"},
		{"token over the bound", typedSettingsNativeRefusalText("report_decode", nil, typedSettingsNativeRefusalFrame(strings.Repeat("a", typedSettingsNativeRefusalTokenMax+1))), "site_torn"},
		{"space in the token", typedSettingsNativeRefusalText("report_decode", nil, []byte("phebs_site=boot strap\n")), "site_torn"},
		{"uppercase token", typedSettingsNativeRefusalText("report_decode", nil, []byte("phebs_site=BOOTSTRAP\n")), "site_torn"},
		{"path in the token", typedSettingsNativeRefusalText("report_decode", nil, []byte("phebs_site=/var/lib/secret\n")), "site_torn"},
		{"empty stderr", typedSettingsNativeRefusalText("report_decode", nil, nil), "site_absent stderr_len=0"},
		{"short stderr with no frame", typedSettingsNativeRefusalText("report_decode", nil, []byte("hello!!")), "site_absent stderr_len=7"},
		{"long stderr with no frame", typedSettingsNativeRefusalText("report_decode", nil, bytes.Repeat([]byte("y"), 600)), "site_absent stderr_len=600 hex_truncated"},
		{"no stderr field at all", "execution refused by the typed sandbox", "site_unreported"},
		{"odd length hex run", "supervisor_report_refusal predicate=report_decode stderr_len=3 stderr_prefix_hex=616", "site_undecodable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := typedSettingsNativeRefusalSite(tc.text); got != tc.want {
				t.Fatalf("typedSettingsNativeRefusalSite = %q, want %q", got, tc.want)
			}
		})
	}
	if len(cases) < 15 {
		t.Fatalf("the table carries %d cases; the ledger requires at least 15", len(cases))
	}
}

// TestTypedSettingsNativeRefusalSiteTakesAbsenceFromTheLength is rule (i). The frame is
// written LAST and the retained prefix is bounded, so a stderr longer than the prefix can
// hide a frame that was genuinely written. A reader that reported a plain absence there
// would be reporting its own truncation as evidence about the container. The two halves of
// the rule are asserted together: a long stderr must say hex_truncated, and a short one
// that fits entirely must not.
func TestTypedSettingsNativeRefusalSiteTakesAbsenceFromTheLength(t *testing.T) {
	long := typedSettingsNativeRefusalText("report_decode", nil, bytes.Repeat([]byte("y"), 900))
	if got := typedSettingsNativeRefusalSite(long); got != "site_absent stderr_len=900 hex_truncated" {
		t.Fatalf("a truncated prefix reported %q; it must not masquerade as a measured absence", got)
	}
	short := typedSettingsNativeRefusalText("report_decode", nil, bytes.Repeat([]byte("y"), 40))
	if got := typedSettingsNativeRefusalSite(short); got != "site_absent stderr_len=40" {
		t.Fatalf("a wholly-present prefix reported %q, want site_absent stderr_len=40", got)
	}
	zero := typedSettingsNativeRefusalText("report_decode", nil, nil)
	if got := typedSettingsNativeRefusalSite(zero); got != "site_absent stderr_len=0" {
		t.Fatalf("the only strong absence reported %q, want site_absent stderr_len=0", got)
	}
	// A frame that survives inside the prefix is still found even when the stderr is long,
	// so hex_truncated is a statement about absence and never suppresses a token. The frame
	// is placed first here, which the real writer never does, precisely to separate the two
	// questions: truncation must not blind the reader to a token it can still see.
	survives := typedSettingsNativeRefusalText("report_decode", nil, append(typedSettingsNativeRefusalFrame("timer"), bytes.Repeat([]byte("y"), 900)...))
	if got := typedSettingsNativeRefusalSite(survives); got != "site=timer" {
		t.Fatalf("a frame inside the prefix reported %q, want site=timer", got)
	}
}

// TestTypedSettingsNativeRefusalSiteRejectsATornFrame is rule (ii). The writer performs
// one unretried write, so a partial frame is possible and must never be reported as a
// site: a truncated token would name a site that may not have been the one reached.
func TestTypedSettingsNativeRefusalSiteRejectsATornFrame(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("phebs_site=boot"),
		[]byte("phebs_site="),
		[]byte("phebs_site=\n"),
		[]byte("phebs_site=" + strings.Repeat("z", typedSettingsNativeRefusalTokenMax+1) + "\n"),
	} {
		text := typedSettingsNativeRefusalText("report_decode", nil, raw)
		if got := typedSettingsNativeRefusalSite(text); got != "site_torn" {
			t.Fatalf("stderr %q reported %q, want site_torn", raw, got)
		}
	}
	// Negative control: the same reader does accept the completed form of the first
	// case, so the rejections above cannot be explained by the prefix never matching.
	if got := typedSettingsNativeRefusalSite(typedSettingsNativeRefusalText("report_decode", nil, []byte("phebs_site=boot\n"))); got != "site=boot" {
		t.Fatalf("the completed frame reported %q, want site=boot", got)
	}
	// A complete frame followed by later stderr is NOT torn. The writer emits exactly one
	// frame, so whatever follows it belongs to someone else and must not invalidate it.
	later := append([]byte("phebs_site=boot\n"), []byte("strap\n")...)
	if got := typedSettingsNativeRefusalSite(typedSettingsNativeRefusalText("report_decode", nil, later)); got != "site=boot" {
		t.Fatalf("a complete frame with trailing stderr reported %q, want site=boot", got)
	}
}

// TestTypedSettingsNativeRefusalSiteNoteShapesCarryNoCause is rule (iii), and it asserts
// the property the reader can actually guarantee. The reader cannot know the writer's
// vocabulary, so it cannot reject a token that happens to spell a cause; what it CAN
// guarantee is that its own grammar is cause-free and that a note can only ever be one of
// the closed forms. Both halves are checked: the classifier is exercised in both
// directions against malformed notes, and every fixed part of the grammar is scanned for
// causal vocabulary.
func TestTypedSettingsNativeRefusalSiteNoteShapesCarryNoCause(t *testing.T) {
	accepted := []string{
		"site=bootstrap", "site=argv", "site_torn", "site_undecodable", "site_unreported",
		"site_absent", "site_absent stderr_len=0", "site_absent stderr_len=600 hex_truncated",
	}
	for _, note := range accepted {
		if !typedSettingsNativeRefusalNoteShape(note) {
			t.Fatalf("typedSettingsNativeRefusalNoteShape rejected %q, which the reader can produce", note)
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
		if typedSettingsNativeRefusalNoteShape(note) {
			t.Fatalf("typedSettingsNativeRefusalNoteShape accepted %q, which is malformed or smuggles content", note)
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
	fixed := append([]string{typedSettingsNativeRefusalLenSuffix, typedSettingsNativeRefusalTruncatedSfx}, typedSettingsNativeRefusalNoteShapes...)
	for _, part := range fixed {
		for _, cause := range causes {
			if typedSettingsNativeContainsWord(part, cause) {
				t.Fatalf("the note grammar part %q names a cause (%q)", part, cause)
			}
		}
	}
	// Negative control for the control: the scanner is a whole-word matcher, so it must
	// still find a cause word that really is standing alone. Without this the scan above
	// could pass on a matcher that finds nothing at all, where a plain substring scan would
	// report " hex_truncated" as naming runc because "truncated" contains those four letters.
	if !typedSettingsNativeContainsWord("site_absent because runc reserved 125", "runc") {
		t.Fatal("the scanner missed a standalone cause word, so the grammar scan proves nothing")
	}
	if typedSettingsNativeContainsWord(" hex_truncated", "runc") {
		t.Fatal("the scanner matched runc inside truncated, the false positive this test exists to avoid")
	}
}

// typedSettingsNativeContainsWord reports whether word occurs in s bounded by
// non-letters. A plain substring scan is wrong here: " hex_truncated" contains the four
// letters of "runc", and reporting that as a cause would be a false positive. Both s and
// word must already be lowercase.
func typedSettingsNativeContainsWord(s, word string) bool {
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

// TestTypedSettingsNativeRefusalSiteReportsUnreported is rule (iv). When wire.err is set
// the sandbox returns before supervisorReportRefusal is reached, so no host-side
// diagnostic exists at all. Reporting site_absent there would claim a measurement that
// never happened.
func TestTypedSettingsNativeRefusalSiteReportsUnreported(t *testing.T) {
	for _, text := range []string{
		"execution refused by the typed sandbox",
		"typed sandbox: context deadline exceeded",
		"supervisor_report_refusal predicate=schema_mismatch",
	} {
		if got := typedSettingsNativeRefusalSite(text); got != "site_unreported" {
			t.Fatalf("%q reported %q, want site_unreported", text, got)
		}
	}
	// Negative control: adding the field turns the same refusal into a measured absence.
	if got := typedSettingsNativeRefusalSite("supervisor_report_refusal predicate=schema_mismatch stderr_len=0 stderr_prefix_hex="); got != "site_absent stderr_len=0" {
		t.Fatalf("the field-bearing form reported %q, want site_absent stderr_len=0", got)
	}
}

// TestTypedSettingsNativeRefusalSiteNoteIsBounded measures typedSettingsNativeRefusalNoteMax
// rather than trusting it, by driving every branch with the widest input that branch accepts.
func TestTypedSettingsNativeRefusalSiteNoteIsBounded(t *testing.T) {
	widest := []string{
		typedSettingsNativeRefusalText("report_decode", nil, typedSettingsNativeRefusalFrame(strings.Repeat("a", typedSettingsNativeRefusalTokenMax))),
		typedSettingsNativeRefusalTextWithLen(2_000_000_000),
		typedSettingsNativeRefusalText("report_decode", nil, []byte("phebs_site=")),
		"no fields at all",
		"stderr_prefix_hex=abc",
	}
	for _, text := range widest {
		note := typedSettingsNativeRefusalSite(text)
		if len(note) > typedSettingsNativeRefusalNoteMax {
			t.Fatalf("note %q is %d bytes, over the %d-byte bound", note, len(note), typedSettingsNativeRefusalNoteMax)
		}
		if !typedSettingsNativeRefusalNoteShape(note) {
			t.Fatalf("note %q is outside the closed shape set", note)
		}
	}
	// The bound is derived from the widest reachable note, not asserted beside it: the
	// truncated-absence form with a nine-digit length is the longest string the reader can
	// build, and it must fit.
	if longest := "site_absent stderr_len=2000000000 hex_truncated"; len(longest) > typedSettingsNativeRefusalNoteMax {
		t.Fatalf("typedSettingsNativeRefusalNoteMax=%d cannot hold %q (%d bytes)", typedSettingsNativeRefusalNoteMax, longest, len(longest))
	}
	if got := typedSettingsNativeRefusalSite(typedSettingsNativeRefusalTextWithLen(2_000_000_000)); got != "site_absent stderr_len=2000000000 hex_truncated" {
		t.Fatalf("the widest note is %q (%d bytes), so typedSettingsNativeRefusalNoteMax=%d is not the real bound", got, len(got), typedSettingsNativeRefusalNoteMax)
	}
}

// TestTypedSettingsNativeRefusalSiteNeverEchoesADecodedByte feeds the reader stderr whose
// only frame-like content is invalid, and requires the note to carry none of it. This is
// the privacy property stated negatively: the decoded container stream may contain
// anything, and the only part of it that may reach a failure message is a validated token.
func TestTypedSettingsNativeRefusalSiteNeverEchoesADecodedByte(t *testing.T) {
	secret := "phebs_site=/var/lib/phebs-typed-index/SECRET phebs_site=../../etc/passwd\n"
	note := typedSettingsNativeRefusalSite(typedSettingsNativeRefusalText("report_decode", nil, []byte(secret)))
	if note != "site_torn" {
		t.Fatalf("reported %q, want site_torn", note)
	}
	for _, fragment := range []string{"SECRET", "passwd", "var", "lib", "etc", "/"} {
		if strings.Contains(note, fragment) {
			t.Fatalf("note %q echoes %q from the decoded stream", note, fragment)
		}
	}
}
