// Package authorityvalidate owns dependency-low canonical validation for the
// downstream authority envelope. Both the typed authority package and artifact
// packages use this exact implementation.
package authorityvalidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/reponame"
)

const (
	Schema               = "phebs-downstream-upstream-authority-v1"
	ObservationVersionV2 = "observation-v2"
)

var ErrInvalid = errors.New("invalid downstream upstream authority")

type Result struct {
	Repository string
	Digest     string
	Usable     bool
}

type authority struct {
	Schema      string                                `json:"schema"`
	Repository  string                                `json:"repository"`
	Observation observation                           `json:"observation"`
	Required    []domainIdentity                      `json:"required"`
	Domains     []candidate.DownstreamDomainAuthority `json:"domains"`
	Digest      string                                `json:"digest"`
}

type observation struct {
	Version                     string `json:"version"`
	Repository                  string `json:"repository"`
	SourceGenerationDigest      string `json:"source_generation_digest"`
	SourceRootDigest            string `json:"source_root_digest"`
	ObservationGenerationDigest string `json:"observation_generation_digest"`
	ObservationRootDigest       string `json:"observation_root_digest"`
	PartitionPolicyDigest       string `json:"partition_policy_digest"`
	ObservationPolicyDigest     string `json:"observation_policy_digest"`
	InventoryPolicyDigest       string `json:"inventory_policy_digest,omitempty"`
	RecordCount                 int    `json:"record_count"`
	ObservedCount               int    `json:"observed_count"`
}

type domainIdentity struct {
	Domain  string `json:"domain"`
	Version string `json:"version"`
}

func Canonical(raw []byte) (Result, error) {
	var value authority
	if !json.Valid(raw) || json.Unmarshal(raw, &value) != nil {
		return Result{}, ErrInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Result{}, ErrInvalid
	}
	observation := value.Observation
	if value.Schema != Schema || reponame.Validate(value.Repository) != nil ||
		observation.Version != ObservationVersionV2 || observation.Repository != value.Repository ||
		!validDigest(observation.SourceGenerationDigest) || !validDigest(observation.SourceRootDigest) ||
		!validDigest(observation.ObservationGenerationDigest) || !validDigest(observation.ObservationRootDigest) ||
		!validDigest(observation.PartitionPolicyDigest) || !validDigest(observation.ObservationPolicyDigest) ||
		!validDigest(observation.InventoryPolicyDigest) || observation.RecordCount < 0 ||
		observation.ObservedCount < 0 || observation.ObservedCount > observation.RecordCount ||
		len(value.Required) > 64 || len(value.Domains) > len(value.Required) || !validDigest(value.Digest) {
		return Result{}, ErrInvalid
	}
	prior := ""
	required := make(map[string]struct{}, len(value.Required))
	for _, domain := range value.Required {
		key := domain.Domain + "\x00" + domain.Version
		if key <= prior || !validToken(domain.Domain, 128) || !validToken(domain.Version, 128) {
			return Result{}, ErrInvalid
		}
		required[key] = struct{}{}
		prior = key
	}
	prior = ""
	for _, domain := range value.Domains {
		key := domain.Domain + "\x00" + domain.Version
		_, isRequired := required[key]
		if key <= prior || candidate.ValidateDownstreamDomainAuthority(domain) != nil || !isRequired ||
			domain.SourceGenerationDigest != observation.SourceGenerationDigest ||
			domain.ObservationGenerationDigest != observation.ObservationGenerationDigest {
			return Result{}, ErrInvalid
		}
		prior = key
	}
	want := value
	want.Digest = ""
	wantRaw, err := json.Marshal(want)
	if err != nil {
		return Result{}, ErrInvalid
	}
	sum := sha256.Sum256(append([]byte(Schema+"\x00"), wantRaw...))
	if "sha256:"+hex.EncodeToString(sum[:]) != value.Digest {
		return Result{}, ErrInvalid
	}
	usable := len(value.Domains) == len(value.Required)
	for index, domain := range value.Domains {
		usable = usable && domain.Domain == value.Required[index].Domain &&
			domain.Version == value.Required[index].Version &&
			(domain.Disposition == candidate.PartitionResultSuccess ||
				domain.Disposition == candidate.PartitionResultEmpty)
	}
	return Result{Repository: value.Repository, Digest: value.Digest, Usable: usable}, nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	encoded := strings.TrimPrefix(value, "sha256:")
	decoded, err := hex.DecodeString(encoded)
	return err == nil && hex.EncodeToString(decoded) == encoded
}

func validToken(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}
