// Package t323 authors the independent neutral service-authority corpus and
// load profiles retained by T32.3. Production packages must not import spike
// packages.
package t323

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	CatalogSchema     = "t323-neutral-authority-input-v1"
	OracleSchema      = "t323-independent-correctness-oracle-v1"
	LoadProfileSchema = "t323-service-load-profile-v1"
	ReceiptSchema     = "t323-neutral-corpus-receipt-v1"
	RepositoryName    = "example.invalid/t323-neutral-services"
)

var allowedRoles = stringSet(
	"primary", "supporting", "shared", "generated", "typed", "unowned",
)

var allowedDispositions = stringSet(
	"accepted", "proposal", "conflicting", "tombstone",
)

var allowedAuthorityKinds = stringSet("operator", "build", "deployment")

var allowedVisibility = stringSet("reader", "restricted", "none")

var allowedStates = stringSet(
	"current", "stale", "partial", "unavailable", "conflict", "removed",
)

type Catalog struct {
	Schema       string             `json:"schema"`
	Revision     string             `json:"revision"`
	Records      []AuthorityRecord  `json:"records"`
	UnownedPaths []string           `json:"unowned_paths"`
	Invalid      []InvalidAuthority `json:"invalid"`
}

type AuthorityRecord struct {
	AuthorityID string      `json:"authority_id"`
	Kind        string      `json:"kind"`
	ServiceKey  string      `json:"service_key"`
	DisplayName string      `json:"display_name"`
	Disposition string      `json:"disposition"`
	Visibility  string      `json:"visibility"`
	Placements  []Placement `json:"placements"`
	Successors  []string    `json:"successors,omitempty"`
}

type Placement struct {
	Path string `json:"path"`
	Role string `json:"role"`
}

type InvalidAuthority struct {
	CaseID         string          `json:"case_id"`
	ExpectedReason string          `json:"expected_reason"`
	Payload        json.RawMessage `json:"payload"`
}

type Oracle struct {
	Schema        string                    `json:"schema"`
	Revision      string                    `json:"revision"`
	Memberships   []Membership              `json:"memberships"`
	UnownedPaths  []string                  `json:"unowned_paths"`
	Ambiguities   []Ambiguity               `json:"ambiguities"`
	Queries       []QueryExpectation        `json:"queries"`
	Relationships []RelationshipExpectation `json:"relationships"`
	States        []ServiceState            `json:"states"`
	Tombstones    []Tombstone               `json:"tombstones"`
}

type Membership struct {
	ServiceKey string `json:"service_key"`
	Path       string `json:"path"`
	Role       string `json:"role"`
}

type Ambiguity struct {
	ServiceKey   string   `json:"service_key"`
	AuthorityIDs []string `json:"authority_ids"`
	Paths        []string `json:"paths"`
	State        string   `json:"state"`
}

type QueryExpectation struct {
	ID         string   `json:"id"`
	Principal  string   `json:"principal"`
	Scope      string   `json:"scope"`
	ServiceKey string   `json:"service_key,omitempty"`
	Expression string   `json:"expression"`
	State      string   `json:"state"`
	Paths      []string `json:"paths"`
}

type RelationshipExpectation struct {
	Kind          string   `json:"kind"`
	Identity      string   `json:"identity"`
	Provider      string   `json:"provider"`
	Consumer      string   `json:"consumer"`
	EvidencePaths []string `json:"evidence_paths"`
	State         string   `json:"state"`
	Reason        string   `json:"reason,omitempty"`
}

type ServiceState struct {
	ServiceKey      string `json:"service_key"`
	DesiredRevision string `json:"desired_revision"`
	ActiveRevision  string `json:"active_revision,omitempty"`
	Catalog         string `json:"catalog"`
	Search          string `json:"search"`
	Relationships   string `json:"relationships"`
}

type Tombstone struct {
	ServiceKey string   `json:"service_key"`
	Reason     string   `json:"reason"`
	Successors []string `json:"successors"`
}

type LoadProfile struct {
	Schema      string             `json:"schema"`
	Name        string             `json:"name"`
	Seed        string             `json:"seed"`
	Services    []ProfileService   `json:"services"`
	Files       []ProfileFile      `json:"files"`
	Cardinality ProfileCardinality `json:"cardinality"`
	Claims      ProfileClaims      `json:"claims"`
}

type ProfileService struct {
	ServiceKey string      `json:"service_key"`
	Placements []Placement `json:"placements"`
}

type ProfileFile struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type ProfileCardinality struct {
	Services         int            `json:"services"`
	Files            int            `json:"files"`
	DistinctContents int            `json:"distinct_contents"`
	ContentBytes     int64          `json:"content_bytes"`
	Memberships      int            `json:"memberships"`
	RoleMemberships  map[string]int `json:"role_memberships"`
	MaxPathFanout    int            `json:"max_path_fanout"`
	SharedFiles      int            `json:"shared_files"`
	ContractFiles    int            `json:"contract_files"`
	UnownedFiles     int            `json:"unowned_files"`
}

type ProfileClaims struct {
	Synthetic              bool `json:"synthetic"`
	EstablishesTargetSLO   bool `json:"establishes_target_slo"`
	EstablishesAccuracy    bool `json:"establishes_accuracy"`
	SelectsTopology        bool `json:"selects_topology"`
	ProductionRegistration bool `json:"production_registration"`
}

type Receipt struct {
	Schema     string             `json:"schema"`
	Repository string             `json:"repository"`
	Bundle     ArtifactIdentity   `json:"bundle"`
	Revisions  []RevisionIdentity `json:"revisions"`
	Artifacts  []ArtifactIdentity `json:"artifacts"`
	Claims     ProfileClaims      `json:"claims"`
}

type RevisionIdentity struct {
	Revision     string `json:"revision"`
	Commit       string `json:"commit"`
	Tree         string `json:"tree"`
	RegularFiles int    `json:"regular_files"`
}

type ArtifactIdentity struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type Snapshot struct {
	Revision string
	Date     string
	Message  string
	Files    map[string][]byte
	Catalog  Catalog
	Oracle   Oracle
}

func MarshalCanonical(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func DecodeStrict[T any](data []byte) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return value, err
	}
	return value, nil
}

func ValidateSnapshot(snapshot Snapshot) error {
	if snapshot.Revision == "" || snapshot.Catalog.Schema != CatalogSchema ||
		snapshot.Catalog.Revision != snapshot.Revision || snapshot.Oracle.Schema != OracleSchema ||
		snapshot.Oracle.Revision != snapshot.Revision {
		return errors.New("snapshot schemas or revision bindings are invalid")
	}
	for filePath, content := range snapshot.Files {
		if !validPath(filePath) || content == nil {
			return fmt.Errorf("invalid corpus file %q", filePath)
		}
	}
	catalogBytes, err := MarshalCanonical(snapshot.Catalog)
	if err != nil {
		return err
	}
	if got := snapshot.Files[".phebs/service-authority.json"]; !bytes.Equal(got, catalogBytes) {
		return errors.New("tracked authority input differs from retained catalog bytes")
	}
	if err := validateCatalog(snapshot.Catalog); err != nil {
		return err
	}
	acceptedPlacements := []Placement{}
	for _, record := range snapshot.Catalog.Records {
		for _, placement := range record.Placements {
			selected := false
			for filePath := range snapshot.Files {
				selected = selected || filePath == placement.Path || strings.HasPrefix(filePath, placement.Path+"/")
			}
			if !selected {
				return fmt.Errorf("authority placement %q selects no corpus file", placement.Path)
			}
		}
		if record.Disposition == "accepted" {
			acceptedPlacements = append(acceptedPlacements, record.Placements...)
		}
	}
	unowned := make(map[string]struct{}, len(snapshot.Catalog.UnownedPaths))
	for _, unownedPath := range snapshot.Catalog.UnownedPaths {
		if _, ok := snapshot.Files[unownedPath]; !ok {
			return fmt.Errorf("unowned path %q is absent from the corpus", unownedPath)
		}
		if pathSelectedBy(acceptedPlacements, unownedPath) {
			return fmt.Errorf("unowned path %q is also accepted membership", unownedPath)
		}
		unowned[unownedPath] = struct{}{}
	}
	for filePath := range snapshot.Files {
		if !stringIn(path.Ext(filePath), ".go", ".proto", ".scip") {
			continue
		}
		_, explicitlyUnowned := unowned[filePath]
		if !explicitlyUnowned && !pathSelectedBy(acceptedPlacements, filePath) {
			return fmt.Errorf("source path %q lacks accepted or unowned classification", filePath)
		}
	}
	return validateOracle(snapshot)
}

func validateCatalog(catalog Catalog) error {
	authorityIDs := map[string]struct{}{}
	acceptedKeys := map[string]struct{}{}
	for _, record := range catalog.Records {
		if record.AuthorityID == "" || record.ServiceKey == "" || record.DisplayName == "" ||
			!allowedAuthorityKinds[record.Kind] || !allowedDispositions[record.Disposition] ||
			!allowedVisibility[record.Visibility] {
			return fmt.Errorf("invalid authority record %+v", record)
		}
		if _, duplicate := authorityIDs[record.AuthorityID]; duplicate {
			return fmt.Errorf("duplicate authority id %q", record.AuthorityID)
		}
		authorityIDs[record.AuthorityID] = struct{}{}
		if record.Disposition == "accepted" {
			if _, duplicate := acceptedKeys[record.ServiceKey]; duplicate {
				return fmt.Errorf("duplicate accepted service key %q", record.ServiceKey)
			}
			acceptedKeys[record.ServiceKey] = struct{}{}
		}
		if record.Disposition == "tombstone" && len(record.Placements) != 0 {
			return fmt.Errorf("tombstone %q carries placements", record.ServiceKey)
		}
		placements := map[string]struct{}{}
		for _, placement := range record.Placements {
			if !validPath(placement.Path) || !allowedRoles[placement.Role] || placement.Role == "unowned" {
				return fmt.Errorf("invalid placement %+v", placement)
			}
			key := placement.Path + "\x00" + placement.Role
			if _, duplicate := placements[key]; duplicate {
				return fmt.Errorf("duplicate placement %+v", placement)
			}
			placements[key] = struct{}{}
		}
	}
	unownedPaths := map[string]struct{}{}
	for _, unowned := range catalog.UnownedPaths {
		if !validPath(unowned) {
			return fmt.Errorf("invalid unowned path %q", unowned)
		}
		if _, duplicate := unownedPaths[unowned]; duplicate {
			return fmt.Errorf("duplicate unowned path %q", unowned)
		}
		unownedPaths[unowned] = struct{}{}
	}
	if len(catalog.Invalid) == 0 {
		return errors.New("catalog omits malformed-authority cases")
	}
	for _, invalid := range catalog.Invalid {
		if invalid.CaseID == "" || invalid.ExpectedReason == "" || !json.Valid(invalid.Payload) {
			return errors.New("invalid malformed-authority fixture")
		}
	}
	return nil
}

func validateOracle(snapshot Snapshot) error {
	oracle := snapshot.Oracle
	accepted := map[string]AuthorityRecord{}
	conflicts := map[string][]AuthorityRecord{}
	for _, record := range snapshot.Catalog.Records {
		switch record.Disposition {
		case "accepted":
			accepted[record.ServiceKey] = record
		case "conflicting":
			conflicts[record.ServiceKey] = append(conflicts[record.ServiceKey], record)
		}
	}
	membershipSet := map[string]struct{}{}
	for _, membership := range oracle.Memberships {
		record, ok := accepted[membership.ServiceKey]
		if !ok || !allowedRoles[membership.Role] || membership.Role == "unowned" {
			return fmt.Errorf("oracle membership lacks accepted authority: %+v", membership)
		}
		found := false
		for _, placement := range record.Placements {
			found = found || placement.Path == membership.Path && placement.Role == membership.Role
		}
		if !found {
			return fmt.Errorf("oracle membership differs from authority: %+v", membership)
		}
		membershipSet[membershipKey(membership.ServiceKey, membership.Path, membership.Role)] = struct{}{}
	}
	for key, record := range accepted {
		for _, placement := range record.Placements {
			if _, ok := membershipSet[membershipKey(key, placement.Path, placement.Role)]; !ok {
				return fmt.Errorf("oracle omits membership %s %s %s", key, placement.Role, placement.Path)
			}
		}
	}
	if !slices.Equal(oracle.UnownedPaths, snapshot.Catalog.UnownedPaths) {
		return errors.New("oracle unowned paths differ from authority input")
	}
	for _, ambiguity := range oracle.Ambiguities {
		if ambiguity.State != "conflict" || len(conflicts[ambiguity.ServiceKey]) < 2 {
			return fmt.Errorf("oracle ambiguity is not backed by conflicting authority: %+v", ambiguity)
		}
	}
	queryIDs := map[string]struct{}{}
	for _, query := range oracle.Queries {
		if query.ID == "" || query.Expression == "" ||
			!stringIn(query.Principal, "admin", "reader") ||
			!stringIn(query.Scope, "all-code", "service") ||
			!stringIn(query.State, "exact", "stale", "not_found", "unavailable") {
			return fmt.Errorf("invalid query expectation %+v", query)
		}
		if _, duplicate := queryIDs[query.ID]; duplicate {
			return fmt.Errorf("duplicate query id %q", query.ID)
		}
		queryIDs[query.ID] = struct{}{}
		for _, resultPath := range query.Paths {
			content, ok := snapshot.Files[resultPath]
			if !ok || !bytes.Contains(content, []byte(query.Expression)) {
				return fmt.Errorf("query %q result %q lacks its literal", query.ID, resultPath)
			}
		}
	}
	for _, relationship := range oracle.Relationships {
		if !stringIn(relationship.Kind, "rpc", "topic") || relationship.Identity == "" ||
			!stringIn(relationship.State, "resolved", "unresolved", "ambiguous") {
			return fmt.Errorf("invalid relationship %+v", relationship)
		}
		for _, evidencePath := range relationship.EvidencePaths {
			if _, ok := snapshot.Files[evidencePath]; !ok {
				return fmt.Errorf("relationship evidence %q is absent", evidencePath)
			}
		}
		if relationship.State == "resolved" {
			if _, ok := accepted[relationship.Provider]; !ok {
				return fmt.Errorf("resolved relationship provider %q lacks accepted authority", relationship.Provider)
			}
			if _, ok := accepted[relationship.Consumer]; !ok {
				return fmt.Errorf("resolved relationship consumer %q lacks accepted authority", relationship.Consumer)
			}
		}
	}
	stateKeys := map[string]struct{}{}
	for _, state := range oracle.States {
		if state.ServiceKey == "" || state.DesiredRevision != snapshot.Revision ||
			!allowedStates[state.Catalog] || !allowedStates[state.Search] ||
			!allowedStates[state.Relationships] {
			return fmt.Errorf("invalid service state %+v", state)
		}
		if _, duplicate := stateKeys[state.ServiceKey]; duplicate {
			return fmt.Errorf("duplicate service state %q", state.ServiceKey)
		}
		stateKeys[state.ServiceKey] = struct{}{}
	}
	for key := range accepted {
		if _, ok := stateKeys[key]; !ok {
			return fmt.Errorf("oracle omits state for accepted service %q", key)
		}
	}
	catalogTombstones := map[string][]string{}
	for _, record := range snapshot.Catalog.Records {
		if record.Disposition == "tombstone" {
			catalogTombstones[record.ServiceKey] = record.Successors
		}
	}
	for _, tombstone := range oracle.Tombstones {
		if successors, ok := catalogTombstones[tombstone.ServiceKey]; !ok ||
			!slices.Equal(successors, tombstone.Successors) ||
			!stringIn(tombstone.Reason, "split", "merge", "removed") {
			return fmt.Errorf("oracle tombstone differs from authority: %+v", tombstone)
		}
		delete(catalogTombstones, tombstone.ServiceKey)
	}
	if len(catalogTombstones) != 0 {
		return errors.New("oracle omits an authority tombstone")
	}
	return nil
}

func ValidateProfile(profile LoadProfile) error {
	if profile.Schema != LoadProfileSchema ||
		(profile.Cardinality.Services != 1_000 && profile.Cardinality.Services != 5_000) ||
		len(profile.Services) != profile.Cardinality.Services ||
		len(profile.Files) != profile.Cardinality.Files || !profile.Claims.Synthetic ||
		profile.Claims.EstablishesTargetSLO || profile.Claims.EstablishesAccuracy ||
		profile.Claims.SelectsTopology || profile.Claims.ProductionRegistration {
		return errors.New("load-profile envelope is invalid")
	}
	serviceKeys := map[string]struct{}{}
	pathFanout := map[string]int{}
	roleCounts := map[string]int{}
	for _, service := range profile.Services {
		if service.ServiceKey == "" {
			return errors.New("empty profile service key")
		}
		if _, duplicate := serviceKeys[service.ServiceKey]; duplicate {
			return fmt.Errorf("duplicate profile service %q", service.ServiceKey)
		}
		serviceKeys[service.ServiceKey] = struct{}{}
		for _, placement := range service.Placements {
			if !validPath(placement.Path) || !allowedRoles[placement.Role] || placement.Role == "unowned" {
				return fmt.Errorf("invalid profile placement %+v", placement)
			}
			pathFanout[placement.Path]++
			roleCounts[placement.Role]++
		}
	}
	filePaths, selectedDirectories := map[string]struct{}{}, map[string]struct{}{}
	contentDigests := map[string]struct{}{}
	sharedFiles, contractFiles, unownedFiles := 0, 0, 0
	var contentBytes int64
	for _, file := range profile.Files {
		if !validPath(file.Path) || file.Bytes < 0 || !isSHA256(file.SHA256) {
			return fmt.Errorf("invalid profile file %+v", file)
		}
		if _, duplicate := filePaths[file.Path]; duplicate {
			return fmt.Errorf("duplicate profile file %q", file.Path)
		}
		filePaths[file.Path], contentDigests[file.SHA256] = struct{}{}, struct{}{}
		switch {
		case strings.HasPrefix(file.Path, "shared/"):
			sharedFiles++
		case strings.HasPrefix(file.Path, "contracts/"):
			contractFiles++
		case strings.HasPrefix(file.Path, "tools/unowned-"):
			unownedFiles++
		}
		for directory := path.Dir(file.Path); directory != "."; directory = path.Dir(directory) {
			selectedDirectories[directory] = struct{}{}
		}
		contentBytes += int64(file.Bytes)
	}
	for _, service := range profile.Services {
		for _, placement := range service.Placements {
			_, exactFile := filePaths[placement.Path]
			_, populatedDirectory := selectedDirectories[placement.Path]
			if !exactFile && !populatedDirectory {
				return fmt.Errorf("profile placement %q selects no file", placement.Path)
			}
		}
	}
	maxFanout, memberships := 0, 0
	for _, count := range pathFanout {
		memberships += count
		maxFanout = max(maxFanout, count)
	}
	cardinality := profile.Cardinality
	if len(contentDigests) != cardinality.DistinctContents || contentBytes != cardinality.ContentBytes ||
		memberships != cardinality.Memberships || maxFanout != cardinality.MaxPathFanout ||
		sharedFiles != cardinality.SharedFiles || contractFiles != cardinality.ContractFiles ||
		unownedFiles != cardinality.UnownedFiles ||
		!mapsEqual(roleCounts, cardinality.RoleMemberships) {
		return errors.New("load-profile cardinality does not match generated records")
	}
	return nil
}

func membershipKey(serviceKey, path, role string) string {
	return serviceKey + "\x00" + path + "\x00" + role
}

func pathSelectedBy(placements []Placement, filePath string) bool {
	for _, placement := range placements {
		if filePath == placement.Path || strings.HasPrefix(filePath, placement.Path+"/") {
			return true
		}
	}
	return false
}

func validPath(value string) bool {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.Contains(value, "\\") ||
		strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func isSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && value == strings.ToLower(value)
}

func mapsEqual(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func stringIn(value string, allowed ...string) bool {
	return slices.Contains(allowed, value)
}
