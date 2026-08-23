//go:build !darwin && !linux

package t4013

import "errors"

var errCustodyUnsupported = errors.New("T40.13 custody supervision requires Linux or macOS")

func beginPrepareCustody(string, string, string) (*custodySupervision, error) {
	return nil, errCustodyUnsupported
}

func beginExecuteCustody(string, string, string) (*custodySupervision, error) {
	return nil, errCustodyUnsupported
}

func inspectCustodySupervision(
	string, string, string, custodyOperation, string,
) (custodyStatus, *custodySupervision, error) {
	return custodyStatusIndeterminate, nil, errCustodyUnsupported
}

func (*custodySupervision) Drain(string) error             { return errCustodyUnsupported }
func (*custodySupervision) AbortPrepareAdmission() error   { return errCustodyUnsupported }
func (*custodySupervision) AbortExecuteAdmission() error   { return errCustodyUnsupported }
func (*custodySupervision) BeginFinalization(string) error { return errCustodyUnsupported }
func (*custodySupervision) DrainTerminal() error           { return errCustodyUnsupported }
func (*custodySupervision) Retire() error                  { return errCustodyUnsupported }
func (*custodySupervision) Close() error                   { return nil }

func confirmCustodyRetirement(string, custodyControlState) error { return errCustodyUnsupported }
