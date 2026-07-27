package idlpreview

import (
	"context"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/idlpreflight"
)

func TestValidateParsesBoundedProtobufAndThrift(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol string
		source   Source
	}{
		{
			name: "protobuf", protocol: "protobuf",
			source: Source{
				Path: "shop/cart.proto",
				Content: "syntax = \"proto3\";\n" +
					"package shop;\nservice Cart { rpc Get(GetRequest) returns (GetResponse); }\n" +
					"message GetRequest {}\nmessage GetResponse {}\n",
			},
		},
		{
			name: "thrift", protocol: "thrift",
			source: Source{
				Path: "shop/cart.thrift",
				Content: "namespace go shop\n" +
					"service Cart { string get(1: string id) }\n",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(
				context.Background(),
				test.protocol,
				[]Source{test.source},
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateRejectsEveryBoundBeforeParser(t *testing.T) {
	deepProto := strings.Repeat("{", idlpreflight.MaxStructuralDepth+1)
	deepThrift := strings.Repeat("{", idlpreflight.MaxStructuralDepth+1)
	tooManyTokens := strings.Repeat("x ", idlpreflight.MaxTokens+1)
	tooManyFiles := make([]Source, MaxFiles+1)
	for index := range tooManyFiles {
		tooManyFiles[index] = Source{
			Path:    "file" + strings.Repeat("x", index%3) + ".proto",
			Content: "syntax = \"proto3\";",
		}
	}
	oversized := strings.Repeat("x", idlpreflight.MaxFileBytes+1)
	aggregateContent := strings.Repeat(
		"// bounded proposal input\n",
		idlpreflight.MaxFileBytes/26,
	)
	aggregate := make([]Source, 9)
	for index := range aggregate {
		aggregate[index] = Source{
			Path:    string(rune('a'+index)) + ".proto",
			Content: aggregateContent,
		}
	}
	for _, test := range []struct {
		name     string
		protocol string
		sources  []Source
		want     string
	}{
		{
			name: "protobuf bytes", protocol: "protobuf",
			sources: []Source{{Path: "x.proto", Content: oversized}},
			want:    "4194304-byte",
		},
		{
			name: "thrift bytes", protocol: "thrift",
			sources: []Source{{Path: "x.thrift", Content: oversized}},
			want:    "4194304-byte",
		},
		{
			name: "protobuf tokens", protocol: "protobuf",
			sources: []Source{{Path: "x.proto", Content: tooManyTokens}},
			want:    "500000-token",
		},
		{
			name: "protobuf depth", protocol: "protobuf",
			sources: []Source{{Path: "x.proto", Content: deepProto}},
			want:    "128-level",
		},
		{
			name: "thrift tokens", protocol: "thrift",
			sources: []Source{{Path: "x.thrift", Content: tooManyTokens}},
			want:    "500000-token",
		},
		{
			name: "thrift depth", protocol: "thrift",
			sources: []Source{{Path: "x.thrift", Content: deepThrift}},
			want:    "128-level",
		},
		{
			name: "file count", protocol: "protobuf",
			sources: tooManyFiles, want: "1..256",
		},
		{
			name: "aggregate", protocol: "protobuf",
			sources: aggregate, want: "33554432 bytes",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(context.Background(), test.protocol, test.sources)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
