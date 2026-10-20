package indexer

import (
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func selectedIndexExecutable(path string) (bool, error) {
	file, selected, err := dispatchadmission.OpenSelectedProductionToolImage("zoekt-git-index", path)
	if !selected || err != nil {
		return selected, err
	}
	return true, file.Close()
}

func verifyIndexExecutable(path, expected string) error {
	selected, err := dispatchadmission.VerifySelectedProductionToolImage("zoekt-git-index", path, expected)
	if selected || err != nil {
		return err
	}
	return executableidentity.Verify(path, expected)
}
