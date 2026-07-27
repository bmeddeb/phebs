// Package glossary owns the canonical, versioned user-language contract used
// by contract intelligence and the Change Workbench.
package glossary

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	SchemaVersion = "change-workbench-glossary-v1"

	maxGlossaryBytes = 256 << 10
	maxTerms         = 64
	maxTextBytes     = 4 << 10
	maxArrayRows     = 64
)

var (
	termIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	wireAliasPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

	requiredCapabilityIDs = []string{
		"caller-map-exact-identity",
		"change-workbench",
		"code-navigation",
		"contract-atlas",
		"contract-impact-report",
		"coverage-certificate",
		"history",
		"source-search",
	}
	requiredTermIDs = []string{
		"analysis_scope_and_gaps",
		"could_not_resolve",
		"coverage_certificate",
		"implementation_evidence",
		"matching_static_evidence",
		"name_match_needing_review",
		"resolved_caller",
		"success_criterion",
	}
)

//go:generate go run ./cmd/glossary-gen -write -root ../..

//go:embed glossary.json
var source []byte

// TermID is a stable glossary identity. It is not presentation text.
type TermID string

// Capability is an advertised or planned product capability used to decide
// whether help is available on a given surface.
type Capability string

// Mode is one of the four reviewed Change Workbench ticket journeys.
type Mode string

// Surface is a UI, API-adjacent, agent, or documentation projection.
type Surface string

// Document is the canonical glossary source shape.
type Document struct {
	SchemaVersion string       `json:"schema_version"`
	Capabilities  []Capability `json:"capabilities"`
	Terms         []Term       `json:"terms"`
}

// Term contains both presentation help and the boundaries that qualify it.
type Term struct {
	ID                TermID              `json:"id"`
	Label             string              `json:"label"`
	ShortHelp         string              `json:"short_help"`
	ExpandedHelp      string              `json:"expanded_help"`
	EvidenceBoundary  string              `json:"evidence_boundary"`
	AuthorityBoundary string              `json:"authority_boundary"`
	Modes             []Mode              `json:"modes"`
	Surfaces          []Surface           `json:"surfaces"`
	WireAliases       []string            `json:"wire_aliases"`
	Availability      CapabilityPredicate `json:"availability"`
}

// CapabilityPredicate describes when a term has supporting product evidence.
// RequiresAll and RequiresAny are combined with AND when both are populated.
type CapabilityPredicate struct {
	RequiresAll     []Capability `json:"requires_all"`
	RequiresAny     []Capability `json:"requires_any"`
	UnavailableHelp string       `json:"unavailable_help"`
}

// Source returns an isolated copy of the reviewed canonical input.
func Source() []byte {
	return slices.Clone(source)
}

// LoadEmbedded validates and canonicalizes the checked-in glossary.
func LoadEmbedded() (Document, []byte, string, error) {
	return Load(source)
}

// Load strictly decodes, normalizes, and validates a glossary input. The
// returned digest is domain-separated from every other content digest.
func Load(raw []byte) (Document, []byte, string, error) {
	if len(raw) == 0 || len(raw) > maxGlossaryBytes || !utf8.Valid(raw) {
		return Document{}, nil, "", errors.New(
			"glossary input size or UTF-8 encoding is outside the allowed range",
		)
	}
	var value Document
	if err := strictDecode(raw, &value); err != nil {
		return Document{}, nil, "", fmt.Errorf("decode glossary: %w", err)
	}
	normalize(&value)
	if err := validate(value); err != nil {
		return Document{}, nil, "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return Document{}, nil, "", fmt.Errorf("encode canonical glossary: %w", err)
	}
	return value, canonical, digest(canonical), nil
}

// Available reports whether enabled capabilities satisfy a term's reviewed
// predicate. It does not infer or enable a capability.
func Available(predicate CapabilityPredicate, enabled map[Capability]bool) bool {
	for _, capability := range predicate.RequiresAll {
		if !enabled[capability] {
			return false
		}
	}
	if len(predicate.RequiresAny) == 0 {
		return true
	}
	for _, capability := range predicate.RequiresAny {
		if enabled[capability] {
			return true
		}
	}
	return false
}

func strictDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func normalize(value *Document) {
	slices.Sort(value.Capabilities)
	for index := range value.Terms {
		term := &value.Terms[index]
		slices.Sort(term.Modes)
		slices.Sort(term.Surfaces)
		slices.Sort(term.WireAliases)
		slices.Sort(term.Availability.RequiresAll)
		slices.Sort(term.Availability.RequiresAny)
	}
	slices.SortFunc(value.Terms, func(left, right Term) int {
		return strings.Compare(string(left.ID), string(right.ID))
	})
}

func validate(value Document) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported glossary schema %q", value.SchemaVersion)
	}
	if len(value.Capabilities) == 0 || len(value.Capabilities) > maxArrayRows {
		return errors.New("glossary capabilities are outside the allowed range")
	}
	capabilityIDs := make([]string, len(value.Capabilities))
	capabilities := make(map[Capability]bool, len(value.Capabilities))
	for index, capability := range value.Capabilities {
		if !capabilityPattern.MatchString(string(capability)) {
			return fmt.Errorf("invalid glossary capability %q", capability)
		}
		if capabilities[capability] {
			return fmt.Errorf("duplicate glossary capability %q", capability)
		}
		capabilities[capability] = true
		capabilityIDs[index] = string(capability)
	}
	if !slices.Equal(capabilityIDs, requiredCapabilityIDs) {
		return errors.New("glossary capabilities contain an unknown or stale id")
	}
	if len(value.Terms) == 0 || len(value.Terms) > maxTerms {
		return errors.New("glossary terms are outside the allowed range")
	}
	termIDs := make([]string, len(value.Terms))
	labels := make(map[string]bool, len(value.Terms))
	aliases := make(map[string]TermID)
	coveredSurfaces := make(map[Surface]bool)
	for index, term := range value.Terms {
		if !termIDPattern.MatchString(string(term.ID)) {
			return fmt.Errorf("invalid glossary term id %q", term.ID)
		}
		termIDs[index] = string(term.ID)
		labelKey := strings.ToLower(term.Label)
		if labels[labelKey] {
			return fmt.Errorf("duplicate glossary label %q", term.Label)
		}
		labels[labelKey] = true
		for name, text := range map[string]string{
			"label":              term.Label,
			"short_help":         term.ShortHelp,
			"expanded_help":      term.ExpandedHelp,
			"evidence_boundary":  term.EvidenceBoundary,
			"authority_boundary": term.AuthorityBoundary,
			"unavailable_help":   term.Availability.UnavailableHelp,
		} {
			if err := validateText(string(term.ID)+"."+name, text, maxTextBytes); err != nil {
				return err
			}
		}
		if len(term.Label) > 128 {
			return fmt.Errorf("glossary term %s label exceeds 128 bytes", term.ID)
		}
		if err := validateModes(term); err != nil {
			return err
		}
		if err := validateSurfaces(term); err != nil {
			return err
		}
		for _, surface := range term.Surfaces {
			coveredSurfaces[surface] = true
		}
		if term.WireAliases == nil || len(term.WireAliases) > maxArrayRows {
			return fmt.Errorf("glossary term %s wire aliases are invalid", term.ID)
		}
		for aliasIndex, alias := range term.WireAliases {
			if aliasIndex > 0 && alias == term.WireAliases[aliasIndex-1] {
				return fmt.Errorf("glossary term %s repeats wire alias %q", term.ID, alias)
			}
			if !wireAliasPattern.MatchString(alias) {
				return fmt.Errorf("glossary term %s has invalid wire alias %q", term.ID, alias)
			}
			if prior, ok := aliases[alias]; ok {
				return fmt.Errorf("glossary wire alias %q is shared by %s and %s", alias, prior, term.ID)
			}
			aliases[alias] = term.ID
		}
		if err := validatePredicate(term, capabilities); err != nil {
			return err
		}
	}
	if !slices.Equal(termIDs, requiredTermIDs) {
		return errors.New("glossary terms contain a duplicate, unknown, or stale id")
	}
	for _, surface := range []Surface{"atlas", "caller_map", "impact", "manual", "mcp", "workbench"} {
		if !coveredSurfaces[surface] {
			return fmt.Errorf("glossary has no registered term for %s", surface)
		}
	}
	return nil
}

func validateModes(term Term) error {
	allowed := map[Mode]bool{"add": true, "migrate": true, "modify": true, "retire": true}
	if len(term.Modes) == 0 || len(term.Modes) > 4 {
		return fmt.Errorf("glossary term %s modes are outside the allowed range", term.ID)
	}
	for index, mode := range term.Modes {
		if !allowed[mode] {
			return fmt.Errorf("glossary term %s has invalid mode %q", term.ID, mode)
		}
		if index > 0 && mode == term.Modes[index-1] {
			return fmt.Errorf("glossary term %s repeats mode %q", term.ID, mode)
		}
	}
	return nil
}

func validateSurfaces(term Term) error {
	allowed := map[Surface]bool{
		"atlas": true, "caller_map": true, "impact": true,
		"manual": true, "mcp": true, "workbench": true,
	}
	if len(term.Surfaces) == 0 || len(term.Surfaces) > len(allowed) {
		return fmt.Errorf("glossary term %s surfaces are outside the allowed range", term.ID)
	}
	for index, surface := range term.Surfaces {
		if !allowed[surface] {
			return fmt.Errorf("glossary term %s has invalid surface %q", term.ID, surface)
		}
		if index > 0 && surface == term.Surfaces[index-1] {
			return fmt.Errorf("glossary term %s repeats surface %q", term.ID, surface)
		}
	}
	if !slices.Contains(term.Surfaces, Surface("manual")) ||
		!slices.Contains(term.Surfaces, Surface("mcp")) {
		return fmt.Errorf("glossary term %s lacks required MANUAL or MCP registration", term.ID)
	}
	return nil
}

func validatePredicate(term Term, capabilities map[Capability]bool) error {
	predicate := term.Availability
	if predicate.RequiresAll == nil || predicate.RequiresAny == nil ||
		len(predicate.RequiresAll)+len(predicate.RequiresAny) == 0 {
		return fmt.Errorf("glossary term %s has no capability predicate", term.ID)
	}
	seen := make(map[Capability]bool)
	for _, capability := range append(slices.Clone(predicate.RequiresAll), predicate.RequiresAny...) {
		if !capabilities[capability] {
			return fmt.Errorf("glossary term %s references unknown capability %q", term.ID, capability)
		}
		if seen[capability] {
			return fmt.Errorf("glossary term %s repeats capability %q", term.ID, capability)
		}
		seen[capability] = true
	}
	return nil
}

func validateText(name, value string, limit int) error {
	if value == "" || len(value) > limit || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is empty, oversized, non-UTF-8, or not trimmed", name)
	}
	if strings.ContainsAny(value, "*_`[]<>#|\\") {
		return fmt.Errorf("%s contains markup syntax", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}

func digest(canonical []byte) string {
	hash := sha256.New()
	hash.Write([]byte("phebs-change-workbench-glossary-v1"))
	hash.Write([]byte{0})
	hash.Write(canonical)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
