//go:build !darwin && !linux

package t4013

import "errors"

func processStartIdentity(int, processSnapshot) (processIdentityObservation, error) {
	return processIdentityObservation{}, errors.New("T40.13 process identity is unsupported")
}
