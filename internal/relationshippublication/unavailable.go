package relationshippublication

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/downstreamauthority"
)

const (
	UnavailableSchema = "phebs-relationship-unavailable-v1"
	UnavailableName   = "unavailable.json"
)

type Unavailable struct {
	Schema   string                        `json:"schema"`
	Upstream downstreamauthority.Authority `json:"upstream"`
	Reason   string                        `json:"reason"`
	Prior    *Pointer                      `json:"prior,omitempty"`
	Digest   string                        `json:"digest"`
}

// MarkUnavailable installs an explicit source-free non-authority before
// removing current.json. OpenCurrent checks this marker first, so neither a
// crash between those mutations nor an older valid generation can become a
// silent fallback for failed or prerequisite-unavailable upstream work.
func MarkUnavailable(
	ctx context.Context, root, repository string,
	upstream downstreamauthority.Authority,
) (Unavailable, error) {
	if err := ctx.Err(); err != nil {
		return Unavailable{}, err
	}
	if downstreamauthority.Validate(upstream) != nil ||
		downstreamauthority.RequireUsable(upstream) == nil || upstream.Repository != repository {
		return Unavailable{}, ErrInvalid
	}
	base := repositoryRoot(root, repository)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return Unavailable{}, err
	}
	if err := validateDirectory(base); err != nil {
		return Unavailable{}, err
	}
	value := Unavailable{Schema: UnavailableSchema, Upstream: upstream, Reason: "upstream_unavailable"}
	existing, existingPresent, existingErr := readUnavailable(root, repository)
	if existingErr != nil {
		return Unavailable{}, existingErr
	}
	if raw, err := readRegular(filepath.Join(base, "current.json"), MaxRootBytes); err == nil {
		var pointer Pointer
		if decodeExact(raw, MaxRootBytes, &pointer) != nil || validatePointer(pointer) != nil ||
			pointer.Repository != repository {
			return Unavailable{}, ErrInvalid
		}
		value.Prior = &pointer
	} else if !errors.Is(err, os.ErrNotExist) {
		return Unavailable{}, err
	} else if existingPresent {
		// Reconciliation may observe the same unavailable generation through
		// several domain-settlement callbacks. Preserve the rollback floor once
		// current.json has already been retired.
		value.Prior = existing.Prior
	}
	value.Digest, _ = digestUnavailable(value)
	raw, err := json.Marshal(value)
	if err != nil {
		return Unavailable{}, err
	}
	if err := replaceFile(filepath.Join(base, UnavailableName), raw); err != nil {
		return Unavailable{}, err
	}
	if err := os.Remove(filepath.Join(base, "current.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Unavailable{}, err
	}
	return value, syncDirectory(base)
}

func readUnavailable(root, repository string) (Unavailable, bool, error) {
	raw, err := readRegular(filepath.Join(repositoryRoot(root, repository), UnavailableName), MaxRootBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Unavailable{}, false, nil
	}
	if err != nil {
		return Unavailable{}, false, err
	}
	var value Unavailable
	if decodeExact(raw, MaxRootBytes, &value) != nil || validateUnavailable(value, repository) != nil {
		return Unavailable{}, false, ErrInvalid
	}
	return value, true, nil
}

func validateUnavailable(value Unavailable, repository string) error {
	if value.Schema != UnavailableSchema || value.Reason != "upstream_unavailable" ||
		value.Upstream.Repository != repository || downstreamauthority.Validate(value.Upstream) != nil ||
		downstreamauthority.RequireUsable(value.Upstream) == nil {
		return ErrInvalid
	}
	if value.Prior != nil && (validatePointer(*value.Prior) != nil || value.Prior.Repository != repository) {
		return ErrInvalid
	}
	want, err := digestUnavailable(value)
	if err != nil || want != value.Digest {
		return ErrInvalid
	}
	return nil
}

func digestUnavailable(value Unavailable) (string, error) {
	value.Digest = ""
	return digestValue(value)
}
