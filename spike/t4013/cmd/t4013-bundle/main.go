// Command t4013-bundle safely extracts one bounded returned evidence package.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/spike/t4013"
)

func main() {
	packagePath := flag.String("package", "", "absolute returned package path")
	outputRoot := flag.String("output", "", "empty private extraction directory")
	signerFingerprint := flag.String("signer-fingerprint", "", "optional reviewed signer SHA256 fingerprint")
	packageDigest := flag.String("package-digest", "", "optional reviewed sha256 package digest")
	flag.Parse()
	if flag.NArg() != 0 || *packagePath == "" || *outputRoot == "" {
		fail("-package and -output are required")
	}
	if err := t4013.ExtractReturnedBundle(
		*packagePath,
		*outputRoot,
		*signerFingerprint,
		*packageDigest,
	); err != nil {
		fail("extract returned package: %v", err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t4013-bundle: "+format+"\n", args...)
	os.Exit(1)
}
