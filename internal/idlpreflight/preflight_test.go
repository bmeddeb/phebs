package idlpreflight

import (
	"context"
	"strings"
	"testing"
)

func TestThriftQualifiedIdentifiersKeepFrozenTokenAccounting(t *testing.T) {
	// With the shipped Thrift lexer each dotted identifier is one token. A
	// shared protobuf word predicate would count this population as 500,001
	// tokens and reject an input that previously passed.
	source := strings.Repeat("ns.Type ", MaxTokens/3+1)
	if err := Validate(context.Background(), Thrift, source); err != nil {
		t.Fatalf("qualified Thrift identifiers changed token accounting: %v", err)
	}
}
