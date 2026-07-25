package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/store"
)

const contractCatalogFixtureSchema = "contract-atlas-fixture-v1"

// ContractCatalogFixture is a development-only synthetic catalog definition.
// It is deliberately not an EvidenceStore: make dev can demonstrate the
// browser without seeding, publishing, or impersonating production evidence.
type ContractCatalogFixture struct {
	SchemaVersion string                            `json:"schema_version"`
	Protocol      string                            `json:"protocol"`
	Package       string                            `json:"package"`
	ServiceFQN    string                            `json:"service_fqn"`
	Lineage       string                            `json:"lineage"`
	SourcePath    string                            `json:"source_path"`
	SourceLine    int                               `json:"source_line"`
	Operations    []contractCatalogFixtureOperation `json:"operations"`
}

type contractCatalogFixtureOperation struct {
	Method   string                        `json:"method"`
	Request  contractCatalogFixtureMessage `json:"request"`
	Response contractCatalogFixtureMessage `json:"response"`
}

type contractCatalogFixtureMessage struct {
	Name   string                        `json:"name"`
	Fields []contractCatalogFixtureField `json:"fields"`
}

type contractCatalogFixtureField struct {
	Name        string `json:"name"`
	Number      int    `json:"number"`
	Type        string `json:"type"`
	Cardinality string `json:"cardinality"`
}

type contractCatalogFixtureCursor struct {
	Schema   string `json:"schema"`
	Offset   int    `json:"offset"`
	Binding  string `json:"binding"`
	Checksum string `json:"checksum"`
}

// LoadContractCatalogFixture reads and validates an explicit development
// fixture. Merely having the file in a binary does not enable the capability;
// serve must bind the returned value through Options.
func LoadContractCatalogFixture(filename string) (*ContractCatalogFixture, error) {
	if strings.TrimSpace(filename) == "" {
		return nil, errors.New("contract catalog fixture path is required")
	}
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read contract catalog fixture: %w", err)
	}
	var fixture ContractCatalogFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return nil, fmt.Errorf("decode contract catalog fixture: %w", err)
	}
	if err := fixture.validate(); err != nil {
		return nil, fmt.Errorf("validate contract catalog fixture: %w", err)
	}
	return &fixture, nil
}

func (f *ContractCatalogFixture) validate() error {
	if f == nil || f.SchemaVersion != contractCatalogFixtureSchema {
		return fmt.Errorf("schema_version must be %q", contractCatalogFixtureSchema)
	}
	if _, ok := packForProtocol(f.Protocol); !ok {
		return fmt.Errorf("protocol must be one of: %s", strings.Join(catalogProtocols(), ", "))
	}
	if !validQueryIdentity(f.Package) || !validQueryIdentity(f.ServiceFQN) ||
		!validQueryIdentity(f.Lineage) {
		return errors.New("package, service_fqn, and lineage must be bounded identities")
	}
	cleanPath := path.Clean(f.SourcePath)
	if f.SourcePath == "" || cleanPath != f.SourcePath || strings.HasPrefix(cleanPath, "/") ||
		cleanPath == ".." || strings.HasPrefix(cleanPath, "../") || f.SourceLine < 1 {
		return errors.New("source_path must be a clean relative path and source_line must be positive")
	}
	if len(f.Operations) == 0 || len(f.Operations) > 32 {
		return errors.New("operations must contain 1 through 32 entries")
	}
	seenMethods := make(map[string]bool, len(f.Operations))
	for _, operation := range f.Operations {
		if !validQueryIdentity(operation.Method) || seenMethods[operation.Method] {
			return errors.New("operation methods must be unique bounded identities")
		}
		seenMethods[operation.Method] = true
		for _, message := range []contractCatalogFixtureMessage{operation.Request, operation.Response} {
			if !validQueryIdentity(message.Name) || len(message.Fields) > catalogFieldsPerMessageLimit {
				return errors.New("message names and field counts must satisfy catalog bounds")
			}
			seenFields := make(map[int]bool, len(message.Fields))
			for _, field := range message.Fields {
				if !validQueryIdentity(field.Name) || !validQueryIdentity(field.Type) ||
					field.Number < 1 || field.Number > 536_870_911 ||
					field.Number >= 19_000 && field.Number <= 19_999 ||
					seenFields[field.Number] ||
					field.Cardinality != "singular" && field.Cardinality != "repeated" {
					return errors.New("fixture fields must have unique valid numbers, identities, and cardinality")
				}
				seenFields[field.Number] = true
			}
		}
	}
	return nil
}

func (f *ContractCatalogFixture) list(
	ctx context.Context,
	opts Options,
	query ContractCatalogQuery,
	pageSize int,
	cursor string,
) (*ContractCatalogList, error) {
	visible, err := visibleRepositories(ctx, opts)
	if err != nil {
		return nil, err
	}
	if query.Repository != "" && !catalogRepositoryVisible(visible, query.Repository) {
		return nil, huma.Error404NotFound("contract catalog scope not found")
	}
	repo, commit, available := fixtureCatalogRepository(visible)
	certificate := fixtureCatalogCoverage(f, visible, repo, commit, available)
	binding := digestJSON(struct {
		Query      ContractCatalogQuery `json:"query"`
		PageSize   int                  `json:"page_size"`
		Principal  string               `json:"principal"`
		Visibility string               `json:"visibility"`
		Coverage   string               `json:"coverage"`
	}{
		query, pageSize, opts.Principal(ctx),
		digestJSON(catalogVisibilityContext(ctx, opts, visible)), certificate.Digest,
	})
	offset, err := decodeContractCatalogFixtureCursor(cursor, binding)
	if err != nil {
		return nil, err
	}

	items := make([]ContractCatalogItem, 0, len(f.Operations)+1)
	if available && f.matches(query, repo.Name) {
		items = append(items, f.catalogItem(repo.Name, commit, "", "service"))
		for _, operation := range f.Operations {
			items = append(items, f.catalogItem(repo.Name, commit, operation.Method, "operation"))
		}
	}
	if offset > len(items) {
		return nil, huma.Error409Conflict("contract catalog fixture cursor is no longer valid")
	}
	end := min(offset+pageSize, len(items))
	page := slices.Clone(items[offset:end])
	pagination := ContractCatalogPagination{Complete: end == len(items)}
	if end < len(items) {
		next, err := encodeContractCatalogFixtureCursor(end, binding)
		if err != nil {
			return nil, huma.Error500InternalServerError("encode contract catalog fixture cursor", err)
		}
		pagination = ContractCatalogPagination{
			Complete: false, Truncated: true, Reason: "page_size", NextCursor: next,
		}
	}
	return &ContractCatalogList{
		SchemaVersion:  contractCatalogSchemaVersion,
		Query:          query,
		Items:          page,
		Pagination:     pagination,
		CoverageDigest: certificate.Digest,
		Coverage:       certificate,
		Caveat: "Synthetic make dev fixture only; provisional source locations demonstrate navigation, " +
			"not extracted contract facts or runtime relationships.",
	}, nil
}

func (f *ContractCatalogFixture) operation(
	ctx context.Context,
	opts Options,
	repository, lineage, operationName string,
) (*ContractCatalogOperation, error) {
	visible, err := visibleRepositories(ctx, opts)
	if err != nil {
		return nil, err
	}
	repo, commit, available := fixtureCatalogRepository(visible)
	if !available || repo.Name != repository || f.Lineage != lineage {
		return nil, huma.Error404NotFound("contract catalog operation not found")
	}
	method := strings.TrimPrefix(operationName, "/"+f.ServiceFQN+"/")
	var spec *contractCatalogFixtureOperation
	for i := range f.Operations {
		if f.Operations[i].Method == method &&
			operationName == "/"+f.ServiceFQN+"/"+f.Operations[i].Method {
			spec = &f.Operations[i]
			break
		}
	}
	if spec == nil {
		return nil, huma.Error404NotFound("contract catalog operation not found")
	}
	certificate := fixtureCatalogCoverage(f, visible, repo, commit, true)
	runID := fixtureCatalogRunID(repo.Name, "proto-contract")
	declaration := f.claim(
		repo.Name, commit, runID, "operation-"+method,
		"DECLARES_OPERATION", f.ServiceFQN+"/"+method, f.Lineage, "exact", "",
	)
	request := f.message(repo.Name, commit, runID, spec.Request, "request-"+method)
	response := f.message(repo.Name, commit, runID, spec.Response, "response-"+method)
	fact := ContractCatalogOperationFactDetail{
		Schema: "proto-operation-detail-v1",
		Request: ContractCatalogTypeReference{
			Raw: spec.Request.Name, Kind: "message", Resolution: "same_file",
			Declaration: spec.Request.Name,
		},
		Response: ContractCatalogTypeReference{
			Raw: spec.Response.Name, Kind: "message", Resolution: "same_file",
			Declaration: spec.Response.Name,
		},
	}
	implementation := ContractCatalogRelationship{
		Kind: "implementation", Classification: "proven",
		Claim: f.claim(
			repo.Name, commit, fixtureCatalogRunID(repo.Name, "grpc-consumer"),
			"implementation-"+method, "REGISTERS_GRPC_SERVICE", f.ServiceFQN,
			f.Lineage, "derived", "production",
		),
	}
	caller := ContractCatalogRelationship{
		Kind: "caller", Classification: "unresolved_name_match",
		Reason: "DECLARATION_LINEAGE_UNPROVEN",
		Claim: f.claim(
			repo.Name, commit, fixtureCatalogRunID(repo.Name, "grpc-consumer"),
			"caller-"+method, "CALLS_OPERATION", operationName,
			"provisional_fixture_consumer_v1", "heuristic", "production",
		),
	}
	unresolved := ContractCatalogRelationship{
		Kind: "unresolved_candidate", Classification: "extractor_abstention",
		Reason: "OPERATION_DECLARATION_UNRESOLVED",
		Claim: f.claim(
			repo.Name, commit, fixtureCatalogRunID(repo.Name, "grpc-consumer"),
			"unresolved-"+method, "UNRESOLVED_GRPC_CALL", method,
			"provisional_fixture_consumer_v1", "unresolved", "production",
		),
	}
	return &ContractCatalogOperation{
		SchemaVersion: contractCatalogSchemaVersion,
		Repository:    repo.Name, DeclarationLineage: f.Lineage,
		ServiceFQN: f.ServiceFQN, Method: method, Operation: operationName,
		Declaration: declaration, FactDetail: fact,
		Request: request, Response: response,
		Implementations:      []ContractCatalogRelationship{implementation},
		Callers:              []ContractCatalogRelationship{caller},
		UnresolvedCandidates: []ContractCatalogRelationship{unresolved},
		CoverageDigest:       certificate.Digest, Coverage: certificate,
		Caveat: "Synthetic make dev fixture only; provisional source locations demonstrate navigation, " +
			"not extracted contract facts or runtime relationships.",
	}, nil
}

func (f *ContractCatalogFixture) matches(query ContractCatalogQuery, repository string) bool {
	return (query.Repository == "" || query.Repository == repository) &&
		(query.Package == "" || query.Package == f.Package) &&
		(query.Protocol == "" || query.Protocol == f.Protocol) &&
		(query.Lineage == "" || query.Lineage == f.Lineage)
}

func (f *ContractCatalogFixture) catalogItem(
	repository, commit, method, kind string,
) ContractCatalogItem {
	predicate, object, suffix := "DECLARES_SERVICE", f.ServiceFQN, "service"
	if method != "" {
		predicate, object, suffix = "DECLARES_OPERATION", f.ServiceFQN+"/"+method, "operation-"+method
	}
	item := ContractCatalogItem{
		Kind: kind, Protocol: f.Protocol, Repository: repository,
		Lineage: f.Lineage, Package: f.Package, ServiceFQN: f.ServiceFQN,
		Method: method,
		Declaration: f.claim(
			repository, commit, fixtureCatalogRunID(repository, "proto-contract"),
			suffix, predicate, object, f.Lineage, "exact", "",
		),
	}
	if method != "" {
		item.Operation = "/" + f.ServiceFQN + "/" + method
	}
	return item
}

func (f *ContractCatalogFixture) message(
	repository, commit, runID string,
	spec contractCatalogFixtureMessage,
	id string,
) ContractCatalogMessage {
	declaration := f.claim(
		repository, commit, runID, id, "DECLARES_MESSAGE", spec.Name,
		f.Lineage, "exact", "",
	)
	message := ContractCatalogMessage{
		Raw: spec.Name, State: "resolved", DeclarationName: spec.Name,
		Declaration: &declaration,
		Fields:      make([]ContractCatalogFieldShape, 0, len(spec.Fields)),
	}
	for _, field := range spec.Fields {
		detail := ContractCatalogFieldDetail{
			Schema: "proto-field-detail-v2", Name: field.Name,
			Type: &ContractCatalogTypeReference{
				Raw: field.Type, Kind: "scalar", Resolution: "intrinsic",
			},
			Cardinality: field.Cardinality,
		}
		detailJSON, _ := json.Marshal(detail)
		object := fmt.Sprintf("%s#%d", spec.Name, field.Number)
		message.Fields = append(message.Fields, ContractCatalogFieldShape{
			Object: object, FieldNumber: field.Number, Detail: detail,
			Declaration: f.claim(
				repository, commit, runID,
				fmt.Sprintf("%s-field-%d", id, field.Number),
				"DECLARES_FIELD", object, f.Lineage, "exact", "",
				detailJSON...,
			),
		})
	}
	return message
}

func (f *ContractCatalogFixture) claim(
	repository, commit, runID, id, predicate, object, lineage, tier, codeRole string,
	detail ...byte,
) ContractCatalogClaim {
	assertionID := "fixture-" + id
	return ContractCatalogClaim{
		AssertionID: assertionID, RunID: runID, Predicate: predicate,
		Object: object, Lineage: lineage, Tier: tier, CodeRole: codeRole,
		Detail: slices.Clone(detail),
		Sources: []ContractCatalogSource{{
			Repository: repository, Commit: commit, Path: f.SourcePath,
			StartByte: 0, EndByte: 1, StartLine: f.SourceLine, EndLine: f.SourceLine,
			AssertionID: assertionID, RunID: runID, AtomID: "fixture-atom-" + id,
		}},
	}
}

func fixtureCatalogRepository(repositories []store.Repo) (store.Repo, string, bool) {
	for _, repo := range repositories {
		commit := repo.IndexedCommitHash
		if commit == "" && len(repo.IndexedRevisions) > 0 {
			commit = repo.IndexedRevisions[0].Commit
		}
		if commit != "" {
			return repo, commit, true
		}
	}
	return store.Repo{}, "", false
}

func fixtureCatalogCoverage(
	fixture *ContractCatalogFixture,
	repositories []store.Repo,
	selected store.Repo,
	commit string,
	available bool,
) extract.CoverageCertificate {
	repos := slices.Clone(repositories)
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	certificate := extract.CoverageCertificate{
		SchemaVersion: "coverage-certificate-v1",
		Domains:       slices.Clone(catalogDomains), RepositoryCount: len(repos),
		Repositories: make([]extract.CertificateRepository, 0, len(repos)),
	}
	for _, repo := range repos {
		indexed := repo.IndexedCommitHash
		if indexed == "" && len(repo.IndexedRevisions) > 0 {
			indexed = repo.IndexedRevisions[0].Commit
		}
		entry := extract.CertificateRepository{
			Repository: repo.Name, IndexedCommit: indexed, SCIPIndex: "absent",
			Runs: make([]extract.CertificateRun, 0, len(catalogDomains)),
		}
		for _, domain := range catalogDomains {
			run := extract.CertificateRun{Domain: domain, Status: "unpublished"}
			if available && repo.Name == selected.Name {
				fixturePack, _ := packForProtocol(fixture.Protocol)
				assertionCount := 3 * len(fixture.Operations)
				protocols := []string{"grpc"}
				if fixture.Protocol == "thrift" {
					protocols = []string{"thrift-go"}
				}
				if domain == fixturePack.declarationDomain {
					assertionCount = 1
					protocols = []string{fixture.Protocol, fixture.Protocol + "-shapes"}
					for _, operation := range fixture.Operations {
						assertionCount += 3 +
							len(operation.Request.Fields) + len(operation.Response.Fields)
					}
				}
				run = extract.CertificateRun{
					Domain: domain, Status: "published",
					RunID:     fixtureCatalogRunID(repo.Name, domain),
					Extractor: "synthetic-make-dev-fixture@1", Commit: commit,
					Fresh: commit == indexed, Protocols: protocols,
					CorpusFileCount: 1, CandidateFileCount: 1, ReadFileCount: 1,
					ReadBytes: 1, AssertionCount: assertionCount, AtomCount: assertionCount,
				}
			}
			entry.Runs = append(entry.Runs, run)
		}
		certificate.Repositories = append(certificate.Repositories, entry)
	}
	certificate.Digest = digestJSON(certificate)
	return certificate
}

func fixtureCatalogRunID(repository, domain string) string {
	return "fixture-" + strings.ReplaceAll(repository, "/", "-") + "-" + domain
}

func encodeContractCatalogFixtureCursor(offset int, binding string) (string, error) {
	cursor := contractCatalogFixtureCursor{
		Schema: contractCatalogFixtureSchema, Offset: offset, Binding: binding,
	}
	cursor.Checksum = digestJSON(cursor)
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeContractCatalogFixtureCursor(raw, binding string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	if len(raw) > catalogCursorBytesLimit {
		return 0, huma.Error400BadRequest("contract catalog fixture cursor is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, huma.Error400BadRequest("contract catalog fixture cursor is invalid")
	}
	var cursor contractCatalogFixtureCursor
	if json.Unmarshal(decoded, &cursor) != nil || cursor.Schema != contractCatalogFixtureSchema ||
		cursor.Offset < 0 || cursor.Binding != binding {
		return 0, huma.Error409Conflict("contract catalog fixture cursor is no longer valid")
	}
	checksum := cursor.Checksum
	cursor.Checksum = ""
	if checksum == "" || checksum != digestJSON(cursor) {
		return 0, huma.Error400BadRequest("contract catalog fixture cursor is invalid")
	}
	return cursor.Offset, nil
}
