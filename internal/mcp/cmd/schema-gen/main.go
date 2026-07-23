// Command schema-gen writes the canonical MCP envelope and payload schemas.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/investigation"
)

func main() {
	out := flag.String("out", "", "directory for generated schemas")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create schema directory: %v\n", err)
		os.Exit(1)
	}
	documents := investigation.SchemaDocuments()
	for _, name := range investigation.SchemaFilenames() {
		document, ok := documents[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "schema %q is not defined\n", name)
			os.Exit(1)
		}
		content, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal %s: %v\n", name, err)
			os.Exit(1)
		}
		content = append(content, '\n')
		if err := os.WriteFile(filepath.Join(*out, name), content, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", name, err)
			os.Exit(1)
		}
	}
}
