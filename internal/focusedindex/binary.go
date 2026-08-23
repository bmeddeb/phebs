package focusedindex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/executableidentity"
)

func FindBinary() (string, error) {
	path, err := findBinary()
	if err != nil {
		return "", err
	}
	if err := executableidentity.Verify(path, os.Getenv("PHEBS_FOCUSED_INDEX_SHA256")); err != nil {
		return "", fmt.Errorf("verify focused-index identity: %w", err)
	}
	return path, nil
}

func findBinary() (string, error) {
	if path := os.Getenv("PHEBS_FOCUSED_INDEX"); path != "" {
		return executablePath(path)
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		for _, candidate := range []string{
			filepath.Join(dir, "phebs-focused-index"),
			filepath.Join(dir, "bin", "phebs-focused-index"),
		} {
			if resolved, err := executablePath(candidate); err == nil {
				return resolved, nil
			}
		}
	}
	path, err := exec.LookPath("phebs-focused-index")
	if err != nil {
		return "", err
	}
	return executablePath(path)
}

func executablePath(candidate string) (string, error) {
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve focused-index path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve focused-index binary: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect focused-index binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("focused-index binary is not an executable regular file")
	}
	return resolved, nil
}
