package store

import (
	"errors"
	"os"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func selectedSurrealBinaryInfo(path string) (os.FileInfo, bool, error) {
	file, selected, err := dispatchadmission.OpenSelectedProductionToolImage("surreal", path)
	if !selected || err != nil {
		return nil, selected, err
	}
	info, statErr := file.Stat()
	return info, true, errors.Join(statErr, file.Close())
}

func verifySurrealExecutable(path, expected string) error {
	selected, err := dispatchadmission.VerifySelectedProductionToolImage("surreal", path, expected)
	if selected || err != nil {
		return err
	}
	return executableidentity.Verify(path, expected)
}
