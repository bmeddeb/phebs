// Command author deterministically recreates the retained T32.3 corpus,
// independent oracle, and 1,000/5,000-service load profiles.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t323"
)

func main() {
	root := filepath.Join("spike", "t323")
	if len(os.Args) == 2 {
		root = filepath.Clean(os.Args[1])
	} else if len(os.Args) > 2 {
		fail("usage: author [output-directory]")
	}
	receipt, err := t323.Author(root)
	if err != nil {
		fail("author: %v", err)
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		fail("encode receipt: %v", err)
	}
	fmt.Println(string(encoded))
}

func fail(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t323-author: "+format+"\n", arguments...)
	os.Exit(1)
}
