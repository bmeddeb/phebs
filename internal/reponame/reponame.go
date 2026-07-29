// Package reponame owns the canonical repository identity accepted at every
// config, state, artifact, and publication boundary.
package reponame

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

const MaxBytes = 1024

var ErrInvalid = errors.New("invalid repository name")

// Validate rejects names that can escape, alias, or nest another mirror in
// the legacy host/path.git disk layout. Interior Unicode whitespace remains
// valid, but leading or trailing whitespace is never canonical.
func Validate(name string) error {
	if name == "" || len(name) > MaxBytes || !utf8.ValidString(name) ||
		strings.TrimSpace(name) != name || strings.Contains(name, `\`) ||
		path.IsAbs(name) || path.Clean(name) != name || name == "." {
		return ErrInvalid
	}
	components := strings.Split(name, "/")
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			return ErrInvalid
		}
		for _, current := range component {
			if current < 0x20 || current == 0x7f {
				return ErrInvalid
			}
		}
		if index < len(components)-1 &&
			strings.HasSuffix(strings.ToLower(component), ".git") {
			return ErrInvalid
		}
	}
	return nil
}
