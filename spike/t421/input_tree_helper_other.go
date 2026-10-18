//go:build !linux

package t421

// The fixed namespace helper never runs or substitutes weaker protection here.
func LinuxInputTreeHelperMain() int { return 125 }
