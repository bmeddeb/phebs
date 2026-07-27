// Package idlpreflight provides the shared bounded lexical preflight used
// before in-process protobuf and Thrift parsers.
package idlpreflight

import (
	"context"
	"errors"
	"fmt"
)

const (
	MaxFileBytes       = 4 << 20
	MaxTokens          = 500_000
	MaxStructuralDepth = 128
)

type Protocol string

const (
	Protobuf Protocol = "protobuf"
	Thrift   Protocol = "thrift"
)

// Validate rejects oversized or excessively complex source before any
// context-free parser receives it.
func Validate(ctx context.Context, protocol Protocol, source string) error {
	if len(source) > MaxFileBytes {
		return fmt.Errorf(
			"source exceeds %d-byte %s parser limit",
			MaxFileBytes,
			protocolName(protocol),
		)
	}
	switch protocol {
	case Protobuf:
		return validateProtobuf(ctx, source)
	case Thrift:
		return validateThrift(ctx, source)
	default:
		return fmt.Errorf("unsupported IDL protocol %q", protocol)
	}
}

func protocolName(protocol Protocol) string {
	if protocol == Protobuf {
		return "proto"
	}
	return string(protocol)
}

func validateProtobuf(ctx context.Context, source string) error {
	depth, tokens := 0, 0
	var expectedClosers [MaxStructuralDepth]byte
	addToken := func() error {
		tokens++
		if tokens > MaxTokens {
			return fmt.Errorf(
				"source exceeds %d-token proto parser limit",
				MaxTokens,
			)
		}
		return nil
	}
	for index := 0; index < len(source); {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		current := source[index]
		switch {
		case isProtoSpace(current):
			index++
		case current == '/' && index+1 < len(source) &&
			source[index+1] == '/':
			index += 2
			for index < len(source) && source[index] != '\n' {
				index++
			}
		case current == '/' && index+1 < len(source) &&
			source[index+1] == '*':
			index += 2
			closed := false
			for index+1 < len(source) {
				if index&4095 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if source[index] == '*' && source[index+1] == '/' {
					index += 2
					closed = true
					break
				}
				index++
			}
			if !closed {
				return errors.New("unterminated proto block comment")
			}
		case current == '\'' || current == '"':
			if err := addToken(); err != nil {
				return err
			}
			quote := current
			index++
			closed := false
			for index < len(source) {
				if index&4095 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				switch source[index] {
				case '\\':
					index += 2
				case quote:
					index++
					closed = true
				default:
					index++
				}
				if closed {
					break
				}
			}
			if !closed {
				return errors.New("unterminated proto string literal")
			}
		case isWordByte(current):
			if err := addToken(); err != nil {
				return err
			}
			index++
			for index < len(source) && isWordByte(source[index]) {
				index++
			}
		default:
			if err := addToken(); err != nil {
				return err
			}
			switch current {
			case '{', '[', '(', '<':
				if depth >= MaxStructuralDepth {
					return fmt.Errorf(
						"source exceeds %d-level proto structural-depth limit",
						MaxStructuralDepth,
					)
				}
				switch current {
				case '{':
					expectedClosers[depth] = '}'
				case '[':
					expectedClosers[depth] = ']'
				case '(':
					expectedClosers[depth] = ')'
				case '<':
					expectedClosers[depth] = '>'
				}
				depth++
			case '}', ']', ')', '>':
				if depth > 0 && expectedClosers[depth-1] == current {
					depth--
				}
			}
			index++
		}
	}
	return nil
}

func validateThrift(ctx context.Context, source string) error {
	depth, tokens := 0, 0
	addToken := func() error {
		tokens++
		if tokens > MaxTokens {
			return fmt.Errorf(
				"source exceeds %d-token thrift parser limit",
				MaxTokens,
			)
		}
		return nil
	}
	for index := 0; index < len(source); {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		current := source[index]
		switch {
		case current == ' ' || current == '\t' ||
			current == '\r' || current == '\n':
			index++
		case current == '#':
			for index < len(source) && source[index] != '\n' {
				index++
			}
		case current == '/' && index+1 < len(source) &&
			source[index+1] == '/':
			index += 2
			for index < len(source) && source[index] != '\n' {
				index++
			}
		case current == '/' && index+1 < len(source) &&
			source[index+1] == '*':
			index += 2
			closed := false
			for index+1 < len(source) {
				if index&4095 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				if source[index] == '*' && source[index+1] == '/' {
					index += 2
					closed = true
					break
				}
				index++
			}
			if !closed {
				return errors.New("unterminated thrift block comment")
			}
		case current == '\'' || current == '"':
			if err := addToken(); err != nil {
				return err
			}
			quote := current
			index++
			closed := false
			for index < len(source) {
				if index&4095 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				switch source[index] {
				case '\\':
					index += 2
				case quote:
					index++
					closed = true
				default:
					index++
				}
				if closed {
					break
				}
			}
			if !closed {
				return errors.New("unterminated thrift string literal")
			}
		case current == '{' || current == '<':
			if err := addToken(); err != nil {
				return err
			}
			depth++
			if depth > MaxStructuralDepth {
				return fmt.Errorf(
					"source exceeds %d-level thrift nesting limit",
					MaxStructuralDepth,
				)
			}
			index++
		case current == '}' || current == '>':
			if err := addToken(); err != nil {
				return err
			}
			if depth > 0 {
				depth--
			}
			index++
		case isWordByte(current):
			if err := addToken(); err != nil {
				return err
			}
			for index < len(source) && isWordByte(source[index]) {
				index++
			}
		default:
			if err := addToken(); err != nil {
				return err
			}
			index++
		}
	}
	return nil
}

func isProtoSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' ||
		value == '\n' || value == '\f'
}

func isWordByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
