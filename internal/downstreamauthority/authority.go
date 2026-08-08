// Package downstreamauthority binds T40.4 observation and T40.9 extraction
// roots into one small, source-free identity for derived component planning.
package downstreamauthority

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority/authorityvalidate"
	"github.com/bmeddeb/phebs/internal/observationpublication"
)

const Schema = authorityvalidate.Schema

var (
	ErrInvalid     = errors.New("invalid downstream upstream authority")
	ErrUnavailable = errors.New("downstream upstream authority is unavailable")
)

type Authority struct {
	Schema      string                                     `json:"schema"`
	Repository  string                                     `json:"repository"`
	Observation observationpublication.DownstreamAuthority `json:"observation"`
	Required    []DomainIdentity                           `json:"required"`
	Domains     []candidate.DownstreamDomainAuthority      `json:"domains"`
	Digest      string                                     `json:"digest"`
}

type DomainIdentity struct {
	Domain  string `json:"domain"`
	Version string `json:"version"`
}

func Build(
	observation observationpublication.DownstreamAuthority,
	domains []candidate.DownstreamDomainAuthority,
) (Authority, error) {
	required := make([]DomainIdentity, len(domains))
	for index, domain := range domains {
		required[index] = DomainIdentity{Domain: domain.Domain, Version: domain.Version}
	}
	return BuildRequired(observation, required, domains)
}

func BuildRequired(
	observation observationpublication.DownstreamAuthority,
	required []DomainIdentity,
	domains []candidate.DownstreamDomainAuthority,
) (Authority, error) {
	value := Authority{
		Schema: Schema, Repository: observation.Repository,
		Observation: observation, Required: slices.Clone(required), Domains: slices.Clone(domains),
	}
	slices.SortFunc(value.Required, func(left, right DomainIdentity) int {
		return strings.Compare(left.Domain+"\x00"+left.Version, right.Domain+"\x00"+right.Version)
	})
	slices.SortFunc(value.Domains, func(left, right candidate.DownstreamDomainAuthority) int {
		return strings.Compare(left.Domain+"\x00"+left.Version, right.Domain+"\x00"+right.Version)
	})
	value.Digest = digest(value)
	if err := Validate(value); err != nil {
		return Authority{}, err
	}
	return value, nil
}

func Validate(value Authority) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return ErrInvalid
	}
	if _, err := authorityvalidate.Canonical(raw); err != nil {
		return ErrInvalid
	}
	return nil
}

// RequireUsable rejects both failed roots and prerequisite-unavailable roots.
// A caller may still retain and report the Authority, but it may not leave an
// older derived generation current as a silent fallback.
func RequireUsable(value Authority) error {
	if err := Validate(value); err != nil {
		return err
	}
	if len(value.Domains) != len(value.Required) {
		return fmt.Errorf("%w: required domain root is absent", ErrUnavailable)
	}
	for index, domain := range value.Domains {
		if domain.Domain != value.Required[index].Domain || domain.Version != value.Required[index].Version {
			return fmt.Errorf("%w: required domain root is absent", ErrUnavailable)
		}
		if domain.Disposition != candidate.PartitionResultSuccess &&
			domain.Disposition != candidate.PartitionResultEmpty {
			return fmt.Errorf("%w: %s/%s is %s", ErrUnavailable,
				domain.Domain, domain.Version, domain.Disposition)
		}
	}
	return nil
}

func digest(value Authority) string {
	value.Digest = ""
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(append([]byte("phebs-downstream-upstream-authority-v1\x00"), raw...))
	return "sha256:" + hex.EncodeToString(sum[:])
}
