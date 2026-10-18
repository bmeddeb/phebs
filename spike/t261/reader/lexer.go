// Package reader is an isolated experimental committed-SQL reader. It has no
// production imports and no ability to execute SQL or load evaluator labels.
package reader

import (
	"strings"
	"unicode/utf8"
)

const (
	MaxInput      = 262144
	MaxTokens     = 65536
	MaxDepth      = 64
	MaxStatements = 4096
	MaxFacts      = 8192
	MaxOutput     = 4 << 20
)

type token struct {
	Value, Kind string
	Start, End  int
	Quoted      bool
}

func (t token) is(s string) bool {
	return t.Kind == "word" && !t.Quoted && strings.EqualFold(t.Value, s) || t.Kind == "symbol" && t.Value == s
}
func (t token) identifier() bool { return t.Kind == "word" }

type statement struct {
	Tokens     []token
	Start, End int
}

type failure string

func (f failure) Error() string { return string(f) }

// lex preserves byte offsets even inside executable version comments. All
// strings/dollar bodies and ordinary comments remain inert token interiors.
func lex(data []byte, dialect string) ([]statement, error) {
	if len(data) > MaxInput {
		return nil, failure("resource-limit")
	}
	if !utf8.Valid(data) {
		return nil, failure("malformed-input")
	}
	var tokens []token
	depth := 0
	var scan func(int, int) error
	scan = func(begin, end int) error {
		for i := begin; i < end; {
			if len(tokens) >= MaxTokens {
				return failure("resource-limit")
			}
			c := data[i]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' {
				i++
				continue
			}
			if i+1 < end && c == '-' && data[i+1] == '-' || dialect == "mysql" && c == '#' {
				for i < end && data[i] != '\n' {
					i++
				}
				continue
			}
			if i+1 < end && c == '/' && data[i+1] == '*' {
				start := i
				i += 2
				nest := 1
				for i < end && nest > 0 {
					if i+1 < end && data[i] == '/' && data[i+1] == '*' {
						nest++
						if nest > MaxDepth {
							return failure("resource-limit")
						}
						i += 2
					} else if i+1 < end && data[i] == '*' && data[i+1] == '/' {
						nest--
						i += 2
					} else {
						i++
					}
				}
				if nest != 0 {
					return failure("malformed-input")
				}
				body := start + 2
				if dialect == "mysql" && body < i-2 && (data[body] == '!' || body+1 < i-2 && data[body] == 'M' && data[body+1] == '!') {
					if data[body] == 'M' {
						body++
					}
					body++
					for body < i-2 && data[body] >= '0' && data[body] <= '9' {
						body++
					}
					if err := scan(body, i-2); err != nil {
						return err
					}
				}
				continue
			}
			start := i
			if c == '\\' {
				for i < end && data[i] != '\n' {
					i++
				}
				tokens = append(tokens, token{Value: string(data[start:i]), Kind: "meta", Start: start, End: i})
				continue
			}
			if c == '\'' || c == '"' || c == '`' {
				quote := c
				i++
				var value strings.Builder
				closed := false
				for i < end {
					if data[i] == quote {
						if i+1 < end && data[i+1] == quote {
							value.WriteByte(quote)
							i += 2
							continue
						}
						i++
						closed = true
						break
					}
					if data[i] == '\\' && (dialect == "mysql" || quote == '\'') && i+1 < end {
						value.WriteByte(data[i])
						i++
						value.WriteByte(data[i])
						i++
						continue
					}
					value.WriteByte(data[i])
					i++
				}
				if !closed {
					return failure("malformed-input")
				}
				kind := "string"
				if quote == '`' || quote == '"' && dialect == "postgresql" {
					kind = "word"
					if value.Len() > 1024 {
						return failure("resource-limit")
					}
				}
				tokens = append(tokens, token{Value: value.String(), Kind: kind, Quoted: kind == "word", Start: start, End: i})
				continue
			}
			if c == '$' && dialect == "postgresql" {
				j := i + 1
				for j < end && (asciiName(data[j]) || data[j] > 127) {
					j++
				}
				if j < end && data[j] == '$' {
					tag := string(data[i : j+1])
					at := strings.Index(string(data[j+1:end]), tag)
					if at < 0 {
						return failure("malformed-input")
					}
					i = j + 1 + at + len(tag)
					tokens = append(tokens, token{Kind: "string", Value: "<dollar-body>", Start: start, End: i})
					continue
				}
			}
			if asciiName(c) || c >= 128 {
				i++
				for i < end && (asciiName(data[i]) || data[i] > 127 || data[i] >= '0' && data[i] <= '9' || data[i] == '$') {
					i++
				}
				if i-start > 1024 {
					return failure("resource-limit")
				}
				tokens = append(tokens, token{Value: string(data[start:i]), Kind: "word", Start: start, End: i})
				continue
			}
			if c >= '0' && c <= '9' {
				i++
				for i < end && (data[i] >= '0' && data[i] <= '9' || data[i] == '.') {
					i++
				}
				tokens = append(tokens, token{Value: string(data[start:i]), Kind: "number", Start: start, End: i})
				continue
			}
			i++
			if c == '(' {
				depth++
				if depth > MaxDepth {
					return failure("resource-limit")
				}
			}
			if c == ')' {
				depth--
				if depth < 0 {
					return failure("malformed-input")
				}
			}
			tokens = append(tokens, token{Value: string(c), Kind: "symbol", Start: start, End: i})
		}
		return nil
	}
	if err := scan(0, len(data)); err != nil {
		return nil, err
	}
	if depth != 0 {
		return nil, failure("malformed-input")
	}
	var result []statement
	begin := 0
	depth = 0
	appendStmt := func(end int) {
		if end > begin {
			ts := tokens[begin:end]
			result = append(result, statement{Tokens: ts, Start: ts[0].Start, End: ts[len(ts)-1].End})
		}
		begin = end
	}
	for i, t := range tokens {
		if t.Kind == "meta" {
			if i > begin {
				appendStmt(i)
			}
			appendStmt(i + 1)
			continue
		}
		if t.is("(") {
			depth++
		}
		if t.is(")") {
			depth--
		}
		if t.is(";") && depth == 0 {
			appendStmt(i + 1)
		}
	}
	appendStmt(len(tokens))
	if len(result) > MaxStatements {
		return nil, failure("resource-limit")
	}
	return result, nil
}
func asciiName(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }
func stripSemi(ts []token) []token {
	if len(ts) > 0 && ts[len(ts)-1].is(";") {
		return ts[:len(ts)-1]
	}
	return ts
}
func matching(ts []token, start int) int {
	if start >= len(ts) || !ts[start].is("(") {
		return -1
	}
	d := 0
	for i := start; i < len(ts); i++ {
		if ts[i].is("(") {
			d++
		}
		if ts[i].is(")") {
			d--
			if d == 0 {
				return i
			}
		}
	}
	return -1
}
func split(ts []token) [][]token {
	var out [][]token
	start, d := 0, 0
	for i, t := range ts {
		if t.is("(") {
			d++
		}
		if t.is(")") {
			d--
		}
		if t.is(",") && d == 0 {
			out = append(out, ts[start:i])
			start = i + 1
		}
	}
	if start < len(ts) {
		out = append(out, ts[start:])
	}
	return out
}
func index(ts []token, s string) int {
	for i, t := range ts {
		if t.is(s) {
			return i
		}
	}
	return -1
}
func after(ts []token, s string) []token {
	i := index(ts, s)
	if i < 0 {
		return nil
	}
	return ts[i+1:]
}
func normalized(t token, dialect string) string {
	if dialect == "postgresql" && !t.Quoted {
		return asciiLower(t.Value)
	}
	return t.Value
}
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
