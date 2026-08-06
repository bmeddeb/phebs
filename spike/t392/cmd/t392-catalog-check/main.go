// Command t392-catalog-check validates an explicitly authored service catalog
// and writes its canonical bytes. It never discovers or invents authority.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func main() {
	inputPath := flag.String("input", "", "explicit operator-authored catalog path")
	outputPath := flag.String("output", "", "new canonical catalog path")
	flag.Parse()
	if *inputPath == "" || *outputPath == "" {
		fail("-input and -output are required")
	}
	data, err := os.ReadFile(*inputPath)
	if err != nil {
		fail("read catalog: %v", err)
	}
	catalog, err := servicecatalog.Decode(data)
	if err != nil {
		fail("decode catalog: %v", err)
	}
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		fail("canonicalize catalog: %v", err)
	}
	canonicalBytes := len(canonical)
	canonical = append(canonical, '\n')
	file, err := os.OpenFile(*outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail("create canonical catalog: %v", err)
	}
	if _, err := file.Write(canonical); err != nil {
		_ = file.Close()
		fail("write canonical catalog: %v", err)
	}
	if err := file.Close(); err != nil {
		fail("close canonical catalog: %v", err)
	}
	digest, err := servicecatalog.Digest(catalog)
	if err != nil {
		fail("digest catalog: %v", err)
	}
	fmt.Printf("services=%d memberships=%d unowned=%d canonical_bytes=%d digest=%s\n",
		len(catalog.Services), len(catalog.Memberships), len(catalog.Unowned), canonicalBytes, digest)
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t392-catalog-check: "+format+"\n", args...)
	os.Exit(1)
}
