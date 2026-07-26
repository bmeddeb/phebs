// Package t211 defines the executable, non-production T21.1 inventory and
// vocabulary contract. Production packages must not import it.
package t211

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	glossarySchema = "change-workbench-glossary-v1"
	scenarioSchema = "change-workbench-scenario-contract-v1"

	maxGlossaryBytes = 256 << 10
	maxScenarioBytes = 512 << 10
	maxTerms         = 64
	maxTextBytes     = 4 << 10
	maxArrayRows     = 64
)

var (
	glossaryIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	capabilityIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	ticketPattern       = regexp.MustCompile(`^T21\.([1-9]|1[0-4])$`)
)

type Glossary struct {
	SchemaVersion string         `json:"schema_version"`
	Capabilities  []string       `json:"capabilities"`
	Terms         []GlossaryTerm `json:"terms"`
}

type GlossaryTerm struct {
	ID                string              `json:"id"`
	Label             string              `json:"label"`
	ShortHelp         string              `json:"short_help"`
	ExpandedHelp      string              `json:"expanded_help"`
	EvidenceBoundary  string              `json:"evidence_boundary"`
	AuthorityBoundary string              `json:"authority_boundary"`
	Modes             []string            `json:"modes"`
	Surfaces          []string            `json:"surfaces"`
	WireAliases       []string            `json:"wire_aliases"`
	Availability      CapabilityPredicate `json:"availability"`
}

type CapabilityPredicate struct {
	RequiresAll     []string `json:"requires_all"`
	RequiresAny     []string `json:"requires_any"`
	UnavailableHelp string   `json:"unavailable_help"`
}

type ScenarioContract struct {
	SchemaVersion     string             `json:"schema_version"`
	SequenceDecision  SequenceDecision   `json:"sequence_decision"`
	NeutralFixture    NeutralFixture     `json:"neutral_fixture"`
	Services          []ServiceInventory `json:"services"`
	Scenarios         []Scenario         `json:"scenarios"`
	TerminologyTraces []TerminologyTrace `json:"terminology_traces"`
	TicketGates       []TicketGate       `json:"ticket_gates"`
}

type SequenceDecision struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type NeutralFixture struct {
	TicketReference      string   `json:"ticket_reference"`
	Repository           string   `json:"repository"`
	Revision             string   `json:"revision"`
	CurrentOperation     string   `json:"current_operation"`
	ReplacementOperation string   `json:"replacement_operation"`
	ProposedOperation    string   `json:"proposed_operation"`
	SourcePaths          []string `json:"source_paths"`
}

type ServiceInventory struct {
	ID            string         `json:"id"`
	Boundary      string         `json:"boundary"`
	SourceAnchors []SourceAnchor `json:"source_anchors"`
}

type SourceAnchor struct {
	Path   string `json:"path"`
	Needle string `json:"needle"`
}

type Scenario struct {
	ID         string         `json:"id"`
	TicketGoal string         `json:"ticket_goal"`
	Steps      []ScenarioStep `json:"steps"`
}

type ScenarioStep struct {
	Section           string   `json:"section"`
	Inputs            []string `json:"inputs"`
	ServiceCalls      []string `json:"service_calls"`
	Outputs           []string `json:"outputs"`
	Mutations         []string `json:"mutations"`
	EvidenceSources   []string `json:"evidence_sources"`
	Bounds            []string `json:"bounds"`
	UnsupportedPlanes []string `json:"unsupported_planes"`
	HumanDecisions    []string `json:"human_decisions"`
}

type TerminologyTrace struct {
	WireField         string `json:"wire_field"`
	PersistedSource   string `json:"persisted_source"`
	ServiceProjection string `json:"service_projection"`
	HTTPProjection    string `json:"http_projection"`
	UIProjection      string `json:"ui_projection"`
	MCPProjection     string `json:"mcp_projection"`
	HonestUserTerm    string `json:"honest_user_term"`
	Qualification     string `json:"qualification"`
}

type TicketGate struct {
	Ticket               string   `json:"ticket"`
	Epic20Dependency     string   `json:"epic20_dependency"`
	LandingPosture       string   `json:"landing_posture"`
	RegistrationRequires []string `json:"registration_requires"`
}

func LoadGlossary(raw []byte) (Glossary, []byte, string, error) {
	if len(raw) == 0 || len(raw) > maxGlossaryBytes || !utf8.Valid(raw) {
		return Glossary{}, nil, "", errors.New("glossary input size or UTF-8 encoding is outside the allowed range")
	}
	var value Glossary
	if err := strictDecode(raw, &value); err != nil {
		return Glossary{}, nil, "", fmt.Errorf("decode glossary: %w", err)
	}
	normalizeGlossary(&value)
	if err := validateGlossary(value); err != nil {
		return Glossary{}, nil, "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return Glossary{}, nil, "", fmt.Errorf("encode canonical glossary: %w", err)
	}
	return value, canonical, digest("phebs-change-workbench-glossary-v1", canonical), nil
}

func LoadScenarioContract(raw []byte) (ScenarioContract, []byte, string, error) {
	if len(raw) == 0 || len(raw) > maxScenarioBytes || !utf8.Valid(raw) {
		return ScenarioContract{}, nil, "", errors.New("scenario contract input size or UTF-8 encoding is outside the allowed range")
	}
	var value ScenarioContract
	if err := strictDecode(raw, &value); err != nil {
		return ScenarioContract{}, nil, "", fmt.Errorf("decode scenario contract: %w", err)
	}
	normalizeScenarioContract(&value)
	if err := validateScenarioContract(value); err != nil {
		return ScenarioContract{}, nil, "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return ScenarioContract{}, nil, "", fmt.Errorf("encode canonical scenario contract: %w", err)
	}
	return value, canonical, digest("phebs-change-workbench-scenario-v1", canonical), nil
}

// ValidateContractPair keeps the persisted-to-user terminology trace bound to
// the canonical glossary. It is intentionally part of this non-production
// spike so T21.4 can replace it with the repository-wide drift guard.
func ValidateContractPair(glossary Glossary, scenarios ScenarioContract) error {
	terms := make(map[string]GlossaryTerm, len(glossary.Terms))
	aliases := make(map[string]bool)
	for _, term := range glossary.Terms {
		terms[term.ID] = term
		for _, alias := range term.WireAliases {
			aliases[alias] = true
		}
	}
	expectedTerms := map[string]string{
		"known_consumers":                    "matching_static_evidence",
		"unresolved_candidates":              "could_not_resolve",
		"coverage-certificate-v1 / coverage": "analysis_scope_and_gaps",
	}
	for _, trace := range scenarios.TerminologyTraces {
		wantTerm, ok := expectedTerms[trace.WireField]
		if !ok {
			return fmt.Errorf("terminology trace has unexpected wire field %q", trace.WireField)
		}
		if trace.HonestUserTerm != wantTerm {
			return fmt.Errorf("terminology trace %q projects %q, want %q",
				trace.WireField, trace.HonestUserTerm, wantTerm)
		}
		if _, ok := terms[trace.HonestUserTerm]; !ok {
			return fmt.Errorf("terminology trace %q references unknown glossary term %q",
				trace.WireField, trace.HonestUserTerm)
		}
		for _, wireAlias := range strings.Split(trace.WireField, " / ") {
			if !aliases[wireAlias] {
				return fmt.Errorf("terminology trace %q has unregistered wire alias %q",
					trace.WireField, wireAlias)
			}
		}
	}
	return nil
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

func normalizeGlossary(value *Glossary) {
	sortStrings(&value.Capabilities)
	for index := range value.Terms {
		term := &value.Terms[index]
		sortStrings(&term.Modes)
		sortStrings(&term.Surfaces)
		sortStrings(&term.WireAliases)
		sortStrings(&term.Availability.RequiresAll)
		sortStrings(&term.Availability.RequiresAny)
	}
	slices.SortFunc(value.Terms, func(left, right GlossaryTerm) int {
		return strings.Compare(left.ID, right.ID)
	})
}

func validateGlossary(value Glossary) error {
	if value.SchemaVersion != glossarySchema {
		return fmt.Errorf("unsupported glossary schema %q", value.SchemaVersion)
	}
	if len(value.Capabilities) == 0 || len(value.Capabilities) > maxArrayRows {
		return errors.New("glossary capabilities are outside the allowed range")
	}
	capabilities := make(map[string]bool, len(value.Capabilities))
	for _, capability := range value.Capabilities {
		if !capabilityIDPattern.MatchString(capability) {
			return fmt.Errorf("invalid glossary capability %q", capability)
		}
		if capabilities[capability] {
			return fmt.Errorf("duplicate glossary capability %q", capability)
		}
		capabilities[capability] = true
	}
	if len(value.Terms) == 0 || len(value.Terms) > maxTerms {
		return errors.New("glossary terms are outside the allowed range")
	}
	ids := make(map[string]bool, len(value.Terms))
	labels := make(map[string]bool, len(value.Terms))
	for _, term := range value.Terms {
		if !glossaryIDPattern.MatchString(term.ID) || ids[term.ID] {
			return fmt.Errorf("invalid or duplicate glossary term id %q", term.ID)
		}
		ids[term.ID] = true
		labelKey := strings.ToLower(term.Label)
		if labels[labelKey] {
			return fmt.Errorf("duplicate glossary label %q", term.Label)
		}
		labels[labelKey] = true
		for name, text := range map[string]string{
			"label": term.Label, "short_help": term.ShortHelp,
			"expanded_help":      term.ExpandedHelp,
			"evidence_boundary":  term.EvidenceBoundary,
			"authority_boundary": term.AuthorityBoundary,
			"unavailable_help":   term.Availability.UnavailableHelp,
		} {
			if err := validateText(term.ID+"."+name, text, maxTextBytes); err != nil {
				return err
			}
		}
		if len(term.Label) > 128 {
			return fmt.Errorf("glossary term %s label exceeds 128 bytes", term.ID)
		}
		if err := validateEnumSet(term.ID+".modes", term.Modes,
			map[string]bool{"add": true, "modify": true, "migrate": true, "retire": true}); err != nil {
			return err
		}
		if err := validateEnumSet(term.ID+".surfaces", term.Surfaces,
			map[string]bool{"atlas": true, "caller_map": true, "impact": true, "manual": true, "mcp": true, "workbench": true}); err != nil {
			return err
		}
		if term.WireAliases == nil || len(term.WireAliases) > maxArrayRows {
			return fmt.Errorf("glossary term %s wire aliases are invalid", term.ID)
		}
		if err := validateUniqueStrings(term.ID+".wire_aliases", term.WireAliases); err != nil {
			return err
		}
		for _, alias := range term.WireAliases {
			if err := validateText(term.ID+".wire_alias", alias, 256); err != nil {
				return err
			}
		}
		predicate := term.Availability
		if predicate.RequiresAll == nil || predicate.RequiresAny == nil ||
			len(predicate.RequiresAll)+len(predicate.RequiresAny) == 0 {
			return fmt.Errorf("glossary term %s has no capability predicate", term.ID)
		}
		seen := make(map[string]bool)
		for _, capability := range append(slices.Clone(predicate.RequiresAll), predicate.RequiresAny...) {
			if !capabilities[capability] {
				return fmt.Errorf("glossary term %s references unknown capability %q", term.ID, capability)
			}
			if seen[capability] {
				return fmt.Errorf("glossary term %s repeats capability %q", term.ID, capability)
			}
			seen[capability] = true
		}
	}
	return nil
}

func normalizeScenarioContract(value *ScenarioContract) {
	sortStrings(&value.NeutralFixture.SourcePaths)
	for index := range value.Services {
		service := &value.Services[index]
		slices.SortFunc(service.SourceAnchors, func(left, right SourceAnchor) int {
			if comparison := strings.Compare(left.Path, right.Path); comparison != 0 {
				return comparison
			}
			return strings.Compare(left.Needle, right.Needle)
		})
	}
	slices.SortFunc(value.Services, func(left, right ServiceInventory) int {
		return strings.Compare(left.ID, right.ID)
	})
	for index := range value.Scenarios {
		scenario := &value.Scenarios[index]
		for stepIndex := range scenario.Steps {
			step := &scenario.Steps[stepIndex]
			for _, list := range []*[]string{
				&step.Inputs, &step.ServiceCalls, &step.Outputs, &step.Mutations,
				&step.EvidenceSources, &step.Bounds, &step.UnsupportedPlanes,
				&step.HumanDecisions,
			} {
				sortStrings(list)
			}
		}
		slices.SortFunc(scenario.Steps, func(left, right ScenarioStep) int {
			return sectionOrder(left.Section) - sectionOrder(right.Section)
		})
	}
	slices.SortFunc(value.Scenarios, func(left, right Scenario) int {
		return scenarioOrder(left.ID) - scenarioOrder(right.ID)
	})
	slices.SortFunc(value.TerminologyTraces, func(left, right TerminologyTrace) int {
		return strings.Compare(left.WireField, right.WireField)
	})
	for index := range value.TicketGates {
		sortStrings(&value.TicketGates[index].RegistrationRequires)
	}
	slices.SortFunc(value.TicketGates, func(left, right TicketGate) int {
		return ticketNumber(left.Ticket) - ticketNumber(right.Ticket)
	})
}

func validateScenarioContract(value ScenarioContract) error {
	if value.SchemaVersion != scenarioSchema {
		return fmt.Errorf("unsupported scenario schema %q", value.SchemaVersion)
	}
	if value.SequenceDecision.Status != "confirmed" && value.SequenceDecision.Status != "revised" {
		return fmt.Errorf("invalid sequence decision %q", value.SequenceDecision.Status)
	}
	if err := validateText("sequence_decision.reason", value.SequenceDecision.Reason, maxTextBytes); err != nil {
		return err
	}
	if err := validateNeutralFixture(value.NeutralFixture); err != nil {
		return err
	}
	serviceIDs := make(map[string]bool, len(value.Services))
	if len(value.Services) == 0 || len(value.Services) > maxArrayRows {
		return errors.New("service inventory is outside the allowed range")
	}
	for _, service := range value.Services {
		if !glossaryIDPattern.MatchString(service.ID) || serviceIDs[service.ID] {
			return fmt.Errorf("invalid or duplicate service id %q", service.ID)
		}
		serviceIDs[service.ID] = true
		if err := validateText(service.ID+".boundary", service.Boundary, maxTextBytes); err != nil {
			return err
		}
		if len(service.SourceAnchors) == 0 || len(service.SourceAnchors) > 16 {
			return fmt.Errorf("service %s source anchors are outside the allowed range", service.ID)
		}
		anchors := make(map[string]bool, len(service.SourceAnchors))
		for _, anchor := range service.SourceAnchors {
			if err := validateText(service.ID+".source_path", anchor.Path, 1024); err != nil {
				return err
			}
			if !validRelativePath(anchor.Path) {
				return fmt.Errorf("service %s has invalid source path %q", service.ID, anchor.Path)
			}
			if err := validateText(service.ID+".needle", anchor.Needle, 512); err != nil {
				return err
			}
			key := anchor.Path + "\x00" + anchor.Needle
			if anchors[key] {
				return fmt.Errorf("service %s repeats source anchor %q", service.ID, anchor.Path)
			}
			anchors[key] = true
		}
	}
	expectedScenarios := []string{"add", "modify", "migrate", "retire"}
	if len(value.Scenarios) != len(expectedScenarios) {
		return fmt.Errorf("scenario count %d, want %d", len(value.Scenarios), len(expectedScenarios))
	}
	for index, scenario := range value.Scenarios {
		if scenario.ID != expectedScenarios[index] {
			return fmt.Errorf("scenario %d is %q, want %q", index, scenario.ID, expectedScenarios[index])
		}
		if err := validateText(scenario.ID+".ticket_goal", scenario.TicketGoal, maxTextBytes); err != nil {
			return err
		}
		if len(scenario.Steps) != 4 {
			return fmt.Errorf("scenario %s has %d steps, want 4", scenario.ID, len(scenario.Steps))
		}
		for stepIndex, step := range scenario.Steps {
			wantSection := []string{"why", "what", "where", "how"}[stepIndex]
			if step.Section != wantSection {
				return fmt.Errorf("scenario %s step %d is %q, want %q", scenario.ID, stepIndex, step.Section, wantSection)
			}
			for name, rows := range map[string][]string{
				"inputs": step.Inputs, "service_calls": step.ServiceCalls,
				"outputs": step.Outputs, "mutations": step.Mutations,
				"evidence_sources": step.EvidenceSources, "bounds": step.Bounds,
				"human_decisions": step.HumanDecisions,
			} {
				if len(rows) == 0 || len(rows) > maxArrayRows {
					return fmt.Errorf("scenario %s/%s %s is outside the allowed range", scenario.ID, step.Section, name)
				}
				if err := validateUniqueStrings(scenario.ID+"."+step.Section+"."+name, rows); err != nil {
					return err
				}
				for _, row := range rows {
					if err := validateText(scenario.ID+"."+step.Section+"."+name, row, maxTextBytes); err != nil {
						return err
					}
				}
			}
			if step.UnsupportedPlanes == nil || len(step.UnsupportedPlanes) > maxArrayRows {
				return fmt.Errorf("scenario %s/%s unsupported planes are invalid", scenario.ID, step.Section)
			}
			if err := validateUniqueStrings(scenario.ID+"."+step.Section+".unsupported_planes", step.UnsupportedPlanes); err != nil {
				return err
			}
			for _, service := range step.ServiceCalls {
				if !serviceIDs[service] {
					return fmt.Errorf("scenario %s/%s calls unknown service %q", scenario.ID, step.Section, service)
				}
			}
		}
	}
	if len(value.TerminologyTraces) != 3 {
		return fmt.Errorf("terminology trace count %d, want 3", len(value.TerminologyTraces))
	}
	for _, trace := range value.TerminologyTraces {
		for name, text := range map[string]string{
			"wire_field": trace.WireField, "persisted_source": trace.PersistedSource,
			"service_projection": trace.ServiceProjection,
			"http_projection":    trace.HTTPProjection, "ui_projection": trace.UIProjection,
			"mcp_projection": trace.MCPProjection, "honest_user_term": trace.HonestUserTerm,
			"qualification": trace.Qualification,
		} {
			if err := validateText("terminology_trace."+name, text, maxTextBytes); err != nil {
				return err
			}
		}
	}
	if len(value.TicketGates) != 14 {
		return fmt.Errorf("ticket gate count %d, want 14", len(value.TicketGates))
	}
	for index, gate := range value.TicketGates {
		want := "T21." + strconv.Itoa(index+1)
		if gate.Ticket != want || !ticketPattern.MatchString(gate.Ticket) {
			return fmt.Errorf("ticket gate %d is %q, want %q", index, gate.Ticket, want)
		}
		if err := validateText(gate.Ticket+".epic20_dependency", gate.Epic20Dependency, 64); err != nil {
			return err
		}
		if err := validateText(gate.Ticket+".landing_posture", gate.LandingPosture, 128); err != nil {
			return err
		}
		if !slices.Equal(gate.RegistrationRequires, []string{"ESTABLISHED", "pilot_continuation"}) {
			return fmt.Errorf("ticket %s weakens the retained registration gate", gate.Ticket)
		}
	}
	return nil
}

func validateNeutralFixture(value NeutralFixture) error {
	for name, text := range map[string]string{
		"ticket_reference": value.TicketReference, "repository": value.Repository,
		"revision": value.Revision, "current_operation": value.CurrentOperation,
		"replacement_operation": value.ReplacementOperation,
		"proposed_operation":    value.ProposedOperation,
	} {
		if err := validateText("neutral_fixture."+name, text, 1024); err != nil {
			return err
		}
	}
	if len(value.Revision) != 40 {
		return errors.New("neutral fixture revision must be a full 40-character object id")
	}
	if len(value.SourcePaths) == 0 || len(value.SourcePaths) > maxArrayRows {
		return errors.New("neutral fixture source paths are outside the allowed range")
	}
	if err := validateUniqueStrings("neutral_fixture.source_paths", value.SourcePaths); err != nil {
		return err
	}
	for _, sourcePath := range value.SourcePaths {
		if err := validateText("neutral_fixture.source_path", sourcePath, 1024); err != nil {
			return err
		}
		if !validRelativePath(sourcePath) {
			return fmt.Errorf("neutral fixture source path %q is invalid", sourcePath)
		}
	}
	if _, err := hex.DecodeString(value.Revision); err != nil {
		return errors.New("neutral fixture revision must be a hexadecimal object id")
	}
	return nil
}

func validateEnumSet(name string, values []string, allowed map[string]bool) error {
	if len(values) == 0 || len(values) > maxArrayRows {
		return fmt.Errorf("%s is outside the allowed range", name)
	}
	if err := validateUniqueStrings(name, values); err != nil {
		return err
	}
	for _, value := range values {
		if !allowed[value] {
			return fmt.Errorf("%s contains invalid value %q", name, value)
		}
	}
	return nil
}

func validateUniqueStrings(name string, values []string) error {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return fmt.Errorf("%s contains duplicate value %q", name, values[index])
		}
	}
	return nil
}

func validateText(name, value string, limit int) error {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is empty, oversized, non-UTF-8, or not trimmed", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	return nil
}

func validRelativePath(value string) bool {
	return value != "" && value == path.Clean(value) && !strings.HasPrefix(value, "/") &&
		value != "." && value != ".." && !strings.HasPrefix(value, "../") &&
		!strings.Contains(value, "\\") && utf8.ValidString(value)
}

func sortStrings(values *[]string) {
	slices.Sort(*values)
}

func scenarioOrder(value string) int {
	switch value {
	case "add":
		return 0
	case "modify":
		return 1
	case "migrate":
		return 2
	case "retire":
		return 3
	default:
		return 99
	}
}

func sectionOrder(value string) int {
	switch value {
	case "why":
		return 0
	case "what":
		return 1
	case "where":
		return 2
	case "how":
		return 3
	default:
		return 99
	}
}

func ticketNumber(value string) int {
	match := ticketPattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return 99
	}
	number, _ := strconv.Atoi(match[1])
	return number
}

func digest(domain string, content []byte) string {
	hash := sha256.New()
	hash.Write([]byte(domain))
	hash.Write([]byte{0})
	hash.Write(content)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
