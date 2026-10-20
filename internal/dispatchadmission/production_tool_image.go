package dispatchadmission

import (
	"errors"
	"os"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

// OpenSelectedProductionToolImage recognizes only a live authenticated Linux amd64
// Phebs launch's exact selected anonymous image route. Other paths retain their
// ordinary resolver. Numeric FD names alone establish no custody/provenance;
// the parent must retain the genuine selected object through actual child join.
func OpenSelectedProductionToolImage(role, path string) (*os.File, bool, error) {
	return openSelectedProductionToolImage(role, path)
}

// VerifySelectedProductionToolImage preserves mandatory complete digest checks
// for that selected route. false means the caller must use its ordinary strict
// verifier; true with an error must never fall back to a different path.
func VerifySelectedProductionToolImage(role, path, expected string) (bool, error) {
	file, selected, err := openSelectedProductionToolImage(role, path)
	if !selected || err != nil {
		return selected, err
	}
	verifyErr := executableidentity.VerifyFile(file, expected)
	return true, errors.Join(verifyErr, file.Close())
}
