package relationshippublication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/internal/candidate"
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
	raw, present, err := readOptionalControl(
		filepath.Join(repositoryRoot(root, repository), UnavailableName),
	)
	if err != nil {
		return Unavailable{}, false, err
	}
	if !present {
		return Unavailable{}, false, nil
	}
	value, err := decodeUnavailableControl(raw, repository)
	return value, true, err
}

func decodeUnavailableControl(raw []byte, repository string) (Unavailable, error) {
	var value Unavailable
	if decodeExact(raw, MaxRootBytes, &value) != nil || validateUnavailable(value, repository) != nil {
		return Unavailable{}, ErrInvalid
	}
	return value, nil
}

func readOptionalControl(path string) ([]byte, bool, error) {
	raw, err := readRegular(path, MaxRootBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

// readRelationshipControls returns one stable current/unavailable control
// snapshot. Both files are read in current-then-unavailable order twice, so a
// transition that installs or removes either control cannot be mistaken for
// the prior state. Identical A->B->A bytes remain the same exact authority.
func readRelationshipControls(
	root, repository string,
) ([]byte, bool, Unavailable, bool, error) {
	base := repositoryRoot(root, repository)
	currentPath := filepath.Join(base, "current.json")
	unavailablePath := filepath.Join(base, UnavailableName)
	currentOne, currentOnePresent, err := readOptionalControl(currentPath)
	if err != nil {
		return nil, false, Unavailable{}, false, err
	}
	unavailableOne, unavailableOnePresent, err := readOptionalControl(unavailablePath)
	if err != nil {
		return nil, false, Unavailable{}, false, err
	}
	currentTwo, currentTwoPresent, err := readOptionalControl(currentPath)
	if err != nil {
		return nil, false, Unavailable{}, false, err
	}
	unavailableTwo, unavailableTwoPresent, err := readOptionalControl(unavailablePath)
	if err != nil {
		return nil, false, Unavailable{}, false, err
	}
	if currentOnePresent != currentTwoPresent || unavailableOnePresent != unavailableTwoPresent ||
		!bytes.Equal(currentOne, currentTwo) || !bytes.Equal(unavailableOne, unavailableTwo) {
		return nil, false, Unavailable{}, false, ErrPublishing
	}
	if !unavailableTwoPresent {
		return currentTwo, currentTwoPresent, Unavailable{}, false, nil
	}
	unavailable, err := decodeUnavailableControl(unavailableTwo, repository)
	if err != nil {
		return nil, false, Unavailable{}, false, err
	}
	return currentTwo, currentTwoPresent, unavailable, true, nil
}

// ReadUnavailable returns the exact source-free unavailable marker selected by
// a product reader after repository authorization. The result is safe for
// projection into a receipt and contains no member, observation, extraction,
// repository, or Git content reads.
func ReadUnavailable(
	ctx context.Context, root, repository string,
) (Unavailable, bool, error) {
	if err := ctx.Err(); err != nil {
		return Unavailable{}, false, err
	}
	value, present, err := readUnavailable(root, repository)
	if err != nil || !present {
		return value, present, err
	}
	value.Upstream.Required = append([]downstreamauthority.DomainIdentity(nil), value.Upstream.Required...)
	value.Upstream.Domains = append([]candidate.DownstreamDomainAuthority(nil), value.Upstream.Domains...)
	if value.Prior != nil {
		prior := *value.Prior
		value.Prior = &prior
	}
	return value, true, nil
}

// ConfirmUnavailable is the result-time fence for a current-reader gap. A nil
// expected marker proves that the repository remains truly absent; a non-nil
// marker proves the exact unavailable authority and digest remain selected.
func ConfirmUnavailable(
	ctx context.Context, root, repository string, expected *Unavailable,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, currentPresent, current, present, err := readRelationshipControls(root, repository)
	if err != nil {
		return err
	}
	if expected == nil {
		if present || currentPresent {
			return ErrPublishing
		}
		return nil
	}
	if !present || current.Digest != expected.Digest {
		return ErrPublishing
	}
	return nil
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
