// Package repopath owns the canonical repository-relative Git path accepted
// by config, candidate manifests, and extraction reads.
package repopath

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid repository-relative path")

const MaxBytes = 4096

// Validate rejects paths that can be interpreted as options, escape or alias
// another path, or cannot be passed consistently through every Git boundary.
func Validate(value string) error {
	if value == "" || len(value) > MaxBytes ||
		value == "." || strings.HasPrefix(value, "-") ||
		path.IsAbs(value) || path.Clean(value) != value ||
		strings.Contains(value, `\`) || !utf8.ValidString(value) {
		return ErrInvalid
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return ErrInvalid
		}
	}
	for _, current := range value {
		if current < 0x20 || current == 0x7f {
			return ErrInvalid
		}
	}
	return nil
}
