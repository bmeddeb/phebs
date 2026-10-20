//go:build !linux

package dispatchadmission

import "os"

func openSelectedProductionToolImage(string, string) (*os.File, bool, error) {
	return nil, false, nil
}
