package main

import "bytes"

// The lexer yields one transient range at a time. Only allowed principal and
// pattern ranges enter results. Credential/grant ranges are never accumulated.
type aclLexer struct {
	raw            []byte
	p, line, count int
}
type aclLexeme struct {
	a, b, line  int
	punctuation byte
}

func (l *aclLexer) next() (aclLexeme, bool, string) {
	for l.p < len(l.raw) {
		ch := l.raw[l.p]
		if ch == '\n' {
			l.line++
			l.p++
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\r' {
			l.p++
			continue
		}
		break
	}
	if l.p == len(l.raw) {
		return aclLexeme{}, false, ""
	}
	start, line := l.p, l.line
	l.count++
	if l.count > 65536 {
		return aclLexeme{}, false, "resource-limit"
	}
	if l.raw[l.p] == '(' || l.raw[l.p] == ')' {
		l.p++
		return aclLexeme{start, l.p, line, l.raw[start]}, true, ""
	}
	quote := byte(0)
	for l.p < len(l.raw) {
		ch := l.raw[l.p]
		if quote != 0 {
			if ch == '\n' || ch == '\r' {
				return aclLexeme{}, false, "malformed-input"
			}
			if ch == '\\' {
				l.p += 2
				continue
			}
			if ch == quote {
				quote = 0
			}
			l.p++
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			l.p++
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '(' || ch == ')' {
			break
		}
		l.p++
	}
	if quote != 0 || l.p > len(l.raw) {
		return aclLexeme{}, false, "malformed-input"
	}
	if l.p-start > 4096 {
		return aclLexeme{}, false, "resource-limit"
	}
	return aclLexeme{start, l.p, line, 0}, true, ""
}
func parseACL(f inputFile, raw []byte) blobResult {
	b := newResult(f)
	lexer := aclLexer{raw: raw}
	line, position, principals, patterns, depth := -1, 0, 0, 0, 0
	skip := false
	var principal aclTokenFact
	for {
		t, ok, reason := lexer.next()
		if reason != "" {
			return refusal(f, reason)
		}
		if !ok {
			break
		}
		if t.line != line {
			if line >= 0 && !skip && (position < 2 || depth != 0) {
				return refusal(f, "malformed-input")
			}
			line = t.line
			position = 0
			patterns = 0
			depth = 0
			skip = false
			principal = aclTokenFact{}
		}
		v := raw[t.a:t.b]
		if position == 0 {
			position++
			if len(v) > 0 && v[0] == '#' {
				skip = true
				continue
			}
			if !bytes.Equal(v, []byte("user")) {
				return refusal(f, "malformed-input")
			}
			continue
		}
		if skip {
			continue
		}
		if position == 1 {
			position++
			if len(v) == 0 || t.punctuation != 0 || bytes.IndexAny(v, "\"'") >= 0 {
				return refusal(f, "malformed-input")
			}
			principals++
			if principals > 256 {
				return refusal(f, "resource-limit")
			}
			principal = aclTokenFact{Token: append([]byte{}, v...), Span: chosen(raw, t.a, t.b)}
			b.Principals = append(b.Principals, principal)
			continue
		}
		if t.punctuation == '(' {
			depth++
			if depth > 64 {
				return refusal(f, "resource-limit")
			}
			continue
		}
		if t.punctuation == ')' {
			depth--
			if depth < 0 {
				return refusal(f, "malformed-input")
			}
			continue
		}
		if len(v) == 0 {
			continue
		}
		// Discard before considering pattern syntax, including >~secret.
		if v[0] == '>' || v[0] == '<' || v[0] == '#' || v[0] == '!' || v[0] == '+' || v[0] == '-' {
			continue
		}
		intent := ""
		if v[0] == '~' {
			intent = "RW"
		} else if bytes.HasPrefix(v, []byte("%R~")) {
			intent = "R"
		} else if bytes.HasPrefix(v, []byte("%W~")) {
			intent = "W"
		} else if bytes.HasPrefix(v, []byte("%RW~")) {
			intent = "RW"
		}
		if intent == "" {
			continue
		}
		if bytes.IndexAny(v, "\"'") >= 0 {
			return refusal(f, "malformed-input")
		}
		patterns++
		if patterns > 128 {
			return refusal(f, "resource-limit")
		}
		b.ACLPairs = append(b.ACLPairs, aclPair{Principal: principal, Pattern: aclTokenFact{Token: append([]byte{}, v...), Span: chosen(raw, t.a, t.b)}, Intent: intent})
	}
	if line >= 0 && !skip && (position < 2 || depth != 0) {
		return refusal(f, "malformed-input")
	}
	return b
}
