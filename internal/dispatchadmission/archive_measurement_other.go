//go:build !darwin && !linux

package dispatchadmission

type archiveMeasurementIdentity struct{}

func captureInheritedArchiveMeasurement() *archiveMeasurementIdentity { return nil }
