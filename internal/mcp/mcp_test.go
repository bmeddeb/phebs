package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// sliceLines is pure — the range/cap arithmetic is the fiddly part of
// read_file, so it gets its own table.
func TestSliceLines(t *testing.T) {
	content := "one\ntwo\nthree\nfour\nfive\n"
	tests := []struct {
		name       string
		start, end int
		want       string
		wantStart  int
		wantEnd    int
	}{
		{"whole file", 0, 0, "one\ntwo\nthree\nfour\nfive", 1, 5},
		{"range", 2, 4, "two\nthree\nfour", 2, 4},
		{"single line", 3, 3, "three", 3, 3},
		{"start beyond EOF clamps", 99, 0, "five", 5, 5},
		{"end beyond EOF clamps", 4, 99, "four\nfive", 4, 5},
		{"end before start clamps", 4, 2, "four\nfive", 4, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sliceLines(content, tt.start, tt.end)
			if got.Content != tt.want || got.StartLine != tt.wantStart || got.EndLine != tt.wantEnd || got.TotalLine != 5 {
				t.Errorf("sliceLines(%d,%d) = %+v, want content %q lines %d-%d/5",
					tt.start, tt.end, got, tt.want, tt.wantStart, tt.wantEnd)
			}
			if got.Truncated {
				t.Error("unexpectedly truncated")
			}
		})
	}
}

func TestSliceLinesCap(t *testing.T) {
	long := strings.Repeat(strings.Repeat("x", 999)+"\n", 300) // 300 KB of 1 KB lines
	got := sliceLines(long, 0, 0)
	if !got.Truncated {
		t.Fatal("300KB file not truncated at the 200KB cap")
	}
	if len(got.Content) > maxFileBytes {
		t.Errorf("content %d bytes exceeds cap %d", len(got.Content), maxFileBytes)
	}
	if got.EndLine >= got.TotalLine {
		t.Errorf("EndLine %d should stop short of TotalLine %d", got.EndLine, got.TotalLine)
	}
	// the delivered range must be re-requestable
	if got.StartLine != 1 || got.EndLine < 1 {
		t.Errorf("bad delivered range %d-%d", got.StartLine, got.EndLine)
	}
}

// TestToolSchemas: the server exposes exactly the three tools with object
// schemas (the SDK enforces the rest of the contract).
func TestToolSchemas(t *testing.T) {
	ctx := t.Context()
	s := NewServer(Options{Version: "test"})
	st, ct := sdk.NewInMemoryTransports()
	go func() { _, _ = s.Connect(ctx, st, nil) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range tools.Tools {
		got[tool.Name] = true
		js, _ := json.Marshal(tool.InputSchema)
		if !strings.Contains(string(js), `"object"`) {
			t.Errorf("%s input schema not an object: %s", tool.Name, js)
		}
	}
	for _, want := range []string{"search_code", "read_file", "list_repos"} {
		if !got[want] {
			t.Errorf("tool %s missing (have %v)", want, got)
		}
	}
}
