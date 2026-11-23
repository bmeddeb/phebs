package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const implementationRevisionSchema = "phebs-extraction-implementation-revisions-v1"

// ImplementationRevisionDigest names the positive implementation revisions of
// the admitted extractors (PHEBS-37), or returns "" when none declares one so
// existing extraction identities stay byte-exact. Rows are
// "domain@version revision", sorted; one domain@version must not declare two
// revisions.
func ImplementationRevisionDigest(extractors []Extractor) (string, error) {
	revisions := make(map[string]uint32)
	for _, extractor := range extractors {
		if extractor == nil {
			return "", fmt.Errorf("implementation revision: nil extractor")
		}
		revisioned, ok := extractor.(sdk.ImplementationRevisioner)
		if !ok {
			continue
		}
		revision := revisioned.ImplementationRevision()
		if revision == 0 {
			continue
		}
		key := extractor.Domain() + "@" + extractor.Version()
		if prior, seen := revisions[key]; seen && prior != revision {
			return "", fmt.Errorf("implementation revision: %s declares %d and %d", key, prior, revision)
		}
		revisions[key] = revision
	}
	if len(revisions) == 0 {
		return "", nil
	}
	rows := make([]string, 0, len(revisions))
	for key, revision := range revisions {
		rows = append(rows, key+" "+strconv.FormatUint(uint64(revision), 10))
	}
	sort.Strings(rows)
	hash := sha256.New()
	_, _ = hash.Write([]byte(implementationRevisionSchema + "\n"))
	for _, row := range rows {
		_, _ = hash.Write([]byte(row + "\n"))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
