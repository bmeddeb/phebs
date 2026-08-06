// Command t392-freeze performs the one-time administrative freeze after an
// approver has accepted the substantive private T39.2 candidate.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/bmeddeb/phebs/spike/t392"
)

func main() {
	candidatePath := flag.String("candidate", "", "approved private candidate JSON path")
	outputPath := flag.String("output", "", "new frozen private plan path")
	approver := flag.String("approver", "", "explicit approver identity")
	validFor := flag.Duration("valid-for", 24*time.Hour, "authorization lifetime")
	flag.Parse()
	if *candidatePath == "" || *outputPath == "" || *approver == "" || *validFor <= 0 {
		fail("-candidate, -output, -approver, and a positive -valid-for are required")
	}
	candidate, err := os.ReadFile(*candidatePath)
	if err != nil {
		fail("read candidate: %v", err)
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		fail("generate commitment nonce: %v", err)
	}
	approved := time.Now().UTC().Truncate(time.Second)
	plan, err := t392.FinalizeCandidate(
		candidate, hex.EncodeToString(nonceBytes), approved.Format(time.RFC3339),
		approved.Add(*validFor).Format(time.RFC3339), *approver,
	)
	if err != nil {
		fail("finalize candidate: %v", err)
	}
	file, err := os.OpenFile(*outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail("create frozen plan: %v", err)
	}
	if _, err := file.Write(plan); err != nil {
		_ = file.Close()
		fail("write frozen plan: %v", err)
	}
	if err := file.Close(); err != nil {
		fail("close frozen plan: %v", err)
	}
	fmt.Printf("plan_digest=%s\n", t392.PlanDigest(plan))
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "t392-freeze: "+format+"\n", args...)
	os.Exit(1)
}
