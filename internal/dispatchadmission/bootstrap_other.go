//go:build !darwin && !linux

package dispatchadmission

func inheritedProductionSocket(_ int) bool { return false }
