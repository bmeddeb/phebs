// Command author writes the deterministic T41.1 envelope and one source-free
// host measurement receipt. It does not change a production service limit.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t411"
)

func main() {
	repositoryRoot := "."
	var destination string
	switch len(os.Args) {
	case 2:
		destination = filepath.Clean(os.Args[1])
	case 3:
		repositoryRoot = filepath.Clean(os.Args[1])
		destination = filepath.Clean(os.Args[2])
	default:
		fail("usage: author [output-directory] OR author <repository-root> <output-directory>")
	}
	receipt, err := t411.Author(repositoryRoot, destination)
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
	_, _ = fmt.Fprintf(os.Stderr, "t411-author: "+format+"\n", arguments...)
	os.Exit(1)
}
