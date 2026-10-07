//go:build darwin

package t421

// validateExternalToolNativeImage screens the selected image as a bounded
// native Mach-O executable. The neutral header validator performs the checks;
// the caller separately pins the exact path and digest.
func validateExternalToolNativeImage(path string) error {
	return validateExternalToolImage(path)
}
