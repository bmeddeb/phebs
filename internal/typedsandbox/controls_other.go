//go:build !linux && !darwin

package typedsandbox

import (
	"context"
	"os"
)

func openControlDirectory(string) (*os.File, error)           { return nil, ErrRefused }
func verifyControls(context.Context, Options, *os.File) error { return ErrRefused }

func readJournalBytes(string) ([]byte, error) { return nil, ErrCustody }
