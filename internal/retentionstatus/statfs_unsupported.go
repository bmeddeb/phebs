//go:build !(aix || android || darwin || dragonfly || freebsd || ios || linux)

package retentionstatus

import (
	"errors"
	"os"
)

func platformStatFS(*os.File) (statFSResult, error) {
	return statFSResult{}, errors.New("filesystem capacity metrics are unsupported on this platform")
}
