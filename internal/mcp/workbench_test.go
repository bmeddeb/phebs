package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/store"
)

type workbenchToolService struct {
	preview    store.WorkbenchPreview
	view       store.WorkbenchView
	previewErr error
	createErr  error
	readErr    error
	calls      []string
}

func (service *workbenchToolService) Preview(
	_ context.Context,
	principal string,
	plan store.WorkbenchPlan,
) (*store.WorkbenchPreview, error) {
	service.calls = append(
		service.calls,
		"preview:"+principal+":"+plan.Title,
	)
	if service.previewErr != nil {
		return nil, service.previewErr
	}
	value := service.preview
	return &value, nil
}

func (service *workbenchToolService) Create(
	_ context.Context,
	principal string,
	request store.WorkbenchMutationRequest,
) (*store.WorkbenchView, error) {
	service.calls = append(
		service.calls,
		"create:"+principal+":"+request.IdempotencyKey,
	)
	if service.createErr != nil {
		return nil, service.createErr
	}
	value := service.view
	return &value, nil
}

func (service *workbenchToolService) Revise(
	context.Context,
	string,
	store.WorkbenchMutationRequest,
) (*store.WorkbenchView, error) {
	return nil, errors.New("revise is not an MCP Workbench tool")
}

func (service *workbenchToolService) Read(
	_ context.Context,
	principal string,
	investigationID string,
) (*store.WorkbenchView, error) {
	service.calls = append(
		service.calls,
		"read:"+principal+":"+investigationID,
	)
	if service.readErr != nil {
		return nil, service.readErr
	}
	value := service.view
	return &value, nil
}

type workbenchToolChecklist struct {
	disposition store.WorkbenchDisposition
	err         error
	calls       []string
}

func (checklist *workbenchToolChecklist) RecordDisposition(
	_ context.Context,
	principal string,
	mutation api.WorkbenchDispositionMutation,
) (*store.WorkbenchDisposition, error) {
	checklist.calls = append(
		checklist.calls,
		"disposition:"+principal+":"+mutation.IdempotencyKey,
	)
	if checklist.err != nil {
		return nil, checklist.err
	}
	value := checklist.disposition
	return &value, nil
}

func TestWorkbenchToolSchemasAndCredentialDiscovery(t *testing.T) {
	workbench, checklist := workbenchToolFixture()
	tests := []struct {
		name                string
		workbench           store.InvestigationWorkbench
		checklist           WorkbenchChecklistQueries
		principal           func(context.Context) string
		advertiseMutations  bool
		investigationWriter bool
		allExistingAnnexes  bool
		wantCount           int
		wantWorkbench       []string
	}{
		{name: "dark", wantCount: 10},
		{
			name:      "workbench alone stays dark",
			workbench: workbench,
			principal: func(context.Context) string { return "user:owner" },
			wantCount: 10,
		},
		{
			name:      "checklist alone stays dark",
			checklist: checklist,
			principal: func(context.Context) string { return "user:owner" },
			wantCount: 10,
		},
		{
			name:      "missing principal projection stays dark",
			workbench: workbench,
			checklist: checklist,
			wantCount: 10,
		},
		{
			name:      "read only named key",
			workbench: workbench,
			checklist: checklist,
			principal: func(context.Context) string { return "user:owner" },
			wantCount: 12,
			wantWorkbench: []string{
				"get_change_workbench",
				"preview_change_workbench",
			},
		},
		{
			name:                "write capable named key",
			workbench:           workbench,
			checklist:           checklist,
			principal:           func(context.Context) string { return "user:owner" },
			advertiseMutations:  true,
			investigationWriter: true,
			wantCount:           14,
			wantWorkbench: []string{
				"create_change_workbench",
				"get_change_workbench",
				"preview_change_workbench",
				"record_change_disposition",
			},
		},
		{
			name:               "all existing annexes read only",
			workbench:          workbench,
			checklist:          checklist,
			principal:          func(context.Context) string { return "user:owner" },
			allExistingAnnexes: true,
			wantCount:          22,
			wantWorkbench: []string{
				"get_change_workbench",
				"preview_change_workbench",
			},
		},
		{
			name:                "all existing annexes write capable",
			workbench:           workbench,
			checklist:           checklist,
			principal:           func(context.Context) string { return "user:owner" },
			advertiseMutations:  true,
			investigationWriter: true,
			allExistingAnnexes:  true,
			wantCount:           24,
			wantWorkbench: []string{
				"create_change_workbench",
				"get_change_workbench",
				"preview_change_workbench",
				"record_change_disposition",
			},
		},
	}
	wantDigests := map[string]string{
		"preview_change_workbench":  "sha256:3312d418e98d5725388eb67652c7c947127de14020a10061d941a52d393da850",
		"create_change_workbench":   "sha256:745cd55f472b066da575ffac55160911cc83aa778277a12376edb2251360b595",
		"get_change_workbench":      "sha256:0ee9d08b20bb3f61b225ae849c39fa3f2b1225d633df68e21f6803b69a8b8f1e",
		"record_change_disposition": "sha256:63c341ce5fc23b7eb6638ec523ad9a6ecd764bcf876638424a76e8e42e1381ba",
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := Options{
				Version:                     "test",
				Workbench:                   test.workbench,
				WorkbenchChecklist:          test.checklist,
				Principal:                   test.principal,
				AdvertiseWorkbenchMutations: test.advertiseMutations,
				InvestigationMutation: func(context.Context) bool {
					return test.investigationWriter
				},
			}
			if test.allExistingAnnexes {
				options.Proofs = schemaProofQueries{}
				options.Compatibility = schemaCompatibilityQueries{}
				options.ContractCatalog = schemaContractCatalogQueries{}
				options.CallerMap = schemaCallerMapQueries{}
				options.CallerComparison = schemaCallerComparisonQueries{}
			}
			server := NewServer(options)
			session := connectWorkbenchToolSession(t, server)
			listed, err := session.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			found := make(map[string]*sdk.Tool, len(listed.Tools))
			for _, tool := range listed.Tools {
				found[tool.Name] = tool
			}
			if len(found) != test.wantCount {
				t.Fatalf(
					"tool count = %d, want %d: %v",
					len(found),
					test.wantCount,
					found,
				)
			}
			workbenchNames := make([]string, 0)
			for name, tool := range found {
				if !strings.Contains(name, "change_workbench") &&
					name != "record_change_disposition" {
					continue
				}
				workbenchNames = append(workbenchNames, name)
				input, err := json.Marshal(tool.InputSchema)
				if err != nil {
					t.Fatal(err)
				}
				output, err := json.Marshal(tool.OutputSchema)
				if err != nil {
					t.Fatal(err)
				}
				schemaBytes := append(append(input, '\n'), output...)
				digest := sha256.Sum256(schemaBytes)
				gotDigest := "sha256:" + hex.EncodeToString(digest[:])
				if gotDigest != wantDigests[name] {
					t.Fatalf(
						"%s input/output schema digest = %s, want %s",
						name,
						gotDigest,
						wantDigests[name],
					)
				}
				if !strings.Contains(string(input), `"additionalProperties":false`) {
					t.Fatalf("%s input is not strict: %s", name, input)
				}
				if output == nil || !strings.Contains(string(output), `"type":"object"`) {
					t.Fatalf("%s output schema is not an object: %s", name, output)
				}
				if name == "create_change_workbench" ||
					name == "record_change_disposition" {
					description := strings.ToLower(tool.Description)
					if !strings.Contains(description, "durabl") ||
						!strings.Contains(description, "explicit write") {
						t.Fatalf(
							"%s does not state its durable write effect: %q",
							name,
							tool.Description,
						)
					}
					if tool.Annotations == nil ||
						tool.Annotations.ReadOnlyHint ||
						!tool.Annotations.IdempotentHint ||
						tool.Annotations.DestructiveHint == nil ||
						*tool.Annotations.DestructiveHint {
						t.Fatalf(
							"%s mutation annotations = %+v",
							name,
							tool.Annotations,
						)
					}
				} else if tool.Annotations == nil ||
					!tool.Annotations.ReadOnlyHint {
					t.Fatalf(
						"%s read annotations = %+v",
						name,
						tool.Annotations,
					)
				}
			}
			slices.Sort(workbenchNames)
			if !slices.Equal(workbenchNames, test.wantWorkbench) {
				t.Fatalf(
					"Workbench discovery = %v, want %v",
					workbenchNames,
					test.wantWorkbench,
				)
			}
		})
	}
}

func TestWorkbenchToolsOfficialSDKParityAndEvidenceReuse(t *testing.T) {
	catalog, callerMap, _, evidenceStore := callerToolFixture(t)
	workbench, checklist := workbenchToolFixture()
	server := NewServer(Options{
		Version: "test", Store: evidenceStore,
		ContractCatalog: catalog, CallerMap: callerMap,
		Workbench: workbench, WorkbenchChecklist: checklist,
		Principal: func(context.Context) string {
			return "user:workbench-owner"
		},
		InvestigationMutation: func(context.Context) bool {
			return true
		},
		AdvertiseWorkbenchMutations: true,
	})
	handler := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true},
	)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t21.13-agent", Version: "1"},
		nil,
	)
	session, err := client.Connect(
		t.Context(),
		&sdk.StreamableClientTransport{
			Endpoint:             httpServer.URL,
			DisableStandaloneSSE: true,
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	plan := workbenchToolPlan()
	preview, result := callToolSession[store.WorkbenchPreview](
		t,
		session,
		"preview_change_workbench",
		workbenchToolArguments(t, plan),
	)
	if result.IsError {
		t.Fatalf("preview tool error: %s", textContent(t, result))
	}
	assertSameJSON(t, preview, workbench.preview)

	mutation := store.WorkbenchMutationRequest{
		Plan:           plan,
		PreviewDigest:  preview.PreviewDigest,
		IdempotencyKey: "create-once",
	}
	created, result := callToolSession[store.WorkbenchView](
		t,
		session,
		"create_change_workbench",
		workbenchToolArguments(t, mutation),
	)
	if result.IsError {
		t.Fatalf("create tool error: %s", textContent(t, result))
	}
	assertSameJSON(t, created, workbench.view)

	got, result := callToolSession[store.WorkbenchView](
		t,
		session,
		"get_change_workbench",
		map[string]any{
			"investigation_id": workbench.view.Investigation.ID,
		},
	)
	if result.IsError {
		t.Fatalf("get tool error: %s", textContent(t, result))
	}
	assertSameJSON(t, got, workbench.view)

	// T21.13 does not add a second Workbench evidence engine. The same session
	// discovers the exact endpoint and exhausts the existing paged Caller Map;
	// its retained helper also proves that hidden repository changes affect
	// neither response bytes nor the shared-service call ledger.
	assertCallerMapToolsSession(t, session, catalog, callerMap, evidenceStore)

	disposition, result := callToolSession[store.WorkbenchDisposition](
		t,
		session,
		"record_change_disposition",
		workbenchToolArguments(
			t,
			workbenchToolDispositionMutation(checklist.disposition.Suggestion),
		),
	)
	if result.IsError {
		t.Fatalf("disposition tool error: %s", textContent(t, result))
	}
	assertSameJSON(t, disposition, checklist.disposition)

	if want := []string{
		"preview:user:workbench-owner:Checkout change",
		"create:user:workbench-owner:create-once",
		"read:user:workbench-owner:inv_workbench",
	}; !slices.Equal(workbench.calls, want) {
		t.Fatalf("Workbench shared-service ledger = %v, want %v", workbench.calls, want)
	}
	if want := []string{
		"disposition:user:workbench-owner:record-once",
	}; !slices.Equal(checklist.calls, want) {
		t.Fatalf("checklist shared-service ledger = %v, want %v", checklist.calls, want)
	}
}

func TestWorkbenchToolsFailClosedForCredentialSchemaAndStaleState(
	t *testing.T,
) {
	t.Run("read only credential", func(t *testing.T) {
		workbench, checklist := workbenchToolFixture()
		server := NewServer(Options{
			Version: "test", Workbench: workbench,
			WorkbenchChecklist: checklist,
			Principal: func(context.Context) string {
				return "user:owner"
			},
			InvestigationMutation: func(context.Context) bool {
				return false
			},
		})
		session := connectWorkbenchToolSession(t, server)
		_, result := callToolSession[store.WorkbenchPreview](
			t,
			session,
			"preview_change_workbench",
			workbenchToolArguments(t, workbenchToolPlan()),
		)
		if !result.IsError ||
			!strings.Contains(textContent(t, result), "workbench resource not found") {
			t.Fatalf("read-only preview refusal = %+v", result)
		}
		_, err := session.CallTool(
			t.Context(),
			&sdk.CallToolParams{
				Name: "create_change_workbench",
				Arguments: workbenchToolArguments(
					t,
					store.WorkbenchMutationRequest{},
				),
			},
		)
		if err == nil ||
			!strings.Contains(err.Error(), `unknown tool "create_change_workbench"`) {
			t.Fatal("read-only registry invoked an undiscoverable mutation tool")
		}
		if len(workbench.calls) != 0 {
			t.Fatalf("credential refusal reached shared service: %v", workbench.calls)
		}
	})

	t.Run("strict schema", func(t *testing.T) {
		workbench, checklist := workbenchToolFixture()
		server := NewServer(Options{
			Version: "test", Workbench: workbench,
			WorkbenchChecklist: checklist,
			Principal: func(context.Context) string {
				return "user:owner"
			},
			InvestigationMutation: func(context.Context) bool {
				return true
			},
			AdvertiseWorkbenchMutations: true,
		})
		arguments := workbenchToolArguments(t, workbenchToolPlan())
		arguments["future_authority"] = true
		_, result := callTool[store.WorkbenchPreview](
			t,
			server,
			"preview_change_workbench",
			arguments,
		)
		if !result.IsError {
			t.Fatal("unknown Workbench input field was accepted")
		}
		if len(workbench.calls) != 0 {
			t.Fatalf("invalid schema reached shared service: %v", workbench.calls)
		}
	})

	t.Run("stale preview and revision", func(t *testing.T) {
		workbench, checklist := workbenchToolFixture()
		workbench.createErr = store.ErrConflict
		checklist.err = huma.Error409Conflict("workbench revision changed")
		server := NewServer(Options{
			Version: "test", Workbench: workbench,
			WorkbenchChecklist: checklist,
			Principal: func(context.Context) string {
				return "user:owner"
			},
			InvestigationMutation: func(context.Context) bool {
				return true
			},
			AdvertiseWorkbenchMutations: true,
		})
		_, createResult := callTool[store.WorkbenchView](
			t,
			server,
			"create_change_workbench",
			workbenchToolArguments(t, store.WorkbenchMutationRequest{
				Plan:           workbenchToolPlan(),
				PreviewDigest:  "sha256:" + strings.Repeat("0", 64),
				IdempotencyKey: "stale-create",
			}),
		)
		if !createResult.IsError ||
			!strings.Contains(
				strings.ToLower(textContent(t, createResult)),
				"conflict",
			) {
			t.Fatalf("stale preview refusal = %+v", createResult)
		}
		_, dispositionResult := callTool[store.WorkbenchDisposition](
			t,
			NewServer(Options{
				Version: "test", Workbench: workbench,
				WorkbenchChecklist: checklist,
				Principal: func(context.Context) string {
					return "user:owner"
				},
				InvestigationMutation: func(context.Context) bool {
					return true
				},
				AdvertiseWorkbenchMutations: true,
			}),
			"record_change_disposition",
			workbenchToolArguments(
				t,
				workbenchToolDispositionMutation(
					checklist.disposition.Suggestion,
				),
			),
		)
		if !dispositionResult.IsError ||
			!strings.Contains(
				textContent(t, dispositionResult),
				"workbench revision changed",
			) {
			t.Fatalf("stale revision refusal = %+v", dispositionResult)
		}
	})

	t.Run("unknown and unauthorized are indistinguishable", func(t *testing.T) {
		refusals := make([]string, 0, 2)
		for _, refusal := range []error{
			store.ErrNotFound,
			huma.Error404NotFound("owner denied"),
		} {
			workbench, checklist := workbenchToolFixture()
			workbench.readErr = refusal
			server := NewServer(Options{
				Version: "test", Workbench: workbench,
				WorkbenchChecklist: checklist,
				Principal: func(context.Context) string {
					return "user:reader"
				},
				InvestigationMutation: func(context.Context) bool {
					return false
				},
			})
			_, result := callTool[store.WorkbenchView](
				t,
				server,
				"get_change_workbench",
				map[string]any{"investigation_id": "inv_hidden"},
			)
			if !result.IsError {
				t.Fatal("unknown/unauthorized Workbench read succeeded")
			}
			refusals = append(refusals, textContent(t, result))
		}
		if refusals[0] != refusals[1] ||
			!strings.Contains(refusals[0], "workbench resource not found") {
			t.Fatalf("non-disclosure responses differ: %q / %q", refusals[0], refusals[1])
		}
	})
}

func connectWorkbenchToolSession(
	t *testing.T,
	server *sdk.Server,
) *sdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() {
		_, _ = server.Connect(t.Context(), serverTransport, nil)
	}()
	client := sdk.NewClient(
		&sdk.Implementation{Name: "t21.13-test", Version: "1"},
		nil,
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func workbenchToolFixture() (
	*workbenchToolService,
	*workbenchToolChecklist,
) {
	now := time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC)
	suggestion := store.WorkbenchSuggestion{
		SchemaVersion:          store.WorkbenchSuggestionSchemaVersion,
		ID:                     "iws_workbench",
		InvestigationID:        "inv_workbench",
		RevisionID:             "irev_workbench",
		Kind:                   "review_resolved_caller",
		Summary:                "Review the cited caller.",
		SelectionRule:          "exact declaration-proven caller",
		EvidenceSnapshotDigest: "sha256:" + strings.Repeat("b", 64),
		Evidence:               []store.WorkbenchSuggestionEvidence{},
		ContentDigest:          "sha256:" + strings.Repeat("c", 64),
	}
	view := store.WorkbenchView{
		Investigation: store.Investigation{
			ID: "inv_workbench", Referent: "grpc:/shop.Checkout/Submit",
			ClaimFamily: "change-workbench", Title: "Checkout change",
			Owner: "user:workbench-owner", Lifecycle: "active",
			CurrentRevisionID: "irev_workbench",
			CreatedAt:         now, UpdatedAt: now,
		},
		Revision: store.Revision{
			ID: "irev_workbench", InvestigationID: "inv_workbench", Seq: 1,
			NormalizedQuestion: "what changes?",
			DecisionSought:     "approve change",
			DeclaredUniverse:   `["example/contracts"]`,
			SnapshotPolicy:     "exact commit",
			BuildConfiguration: "linux/arm64",
			PackSelection:      `["caller-map-exact-identity"]`,
			EnumerationMethod:  "exact selection",
			Creator:            "user:workbench-owner",
			CreatedAt:          now,
			ContentDigest:      "sha256:" + strings.Repeat("d", 64),
		},
		Brief: store.ChangeBrief{
			ID: "icb_workbench", SchemaVersion: store.ChangeBriefSchemaVersion,
			InvestigationID: "inv_workbench", RevisionID: "irev_workbench",
			TicketKind:      store.ChangeBriefModify,
			Problem:         "Receipt identifiers are unstable.",
			DesiredOutcome:  "Receipt identifiers are stable.",
			SuccessCriteria: []string{"The identifier is stable."},
			NonGoals:        []string{},
			Assumptions:     []string{},
			OpenQuestions:   []string{},
			What: store.ChangeBriefWhat{
				Selections: []store.ChangeBriefContractSelection{{
					Role: store.ChangeBriefCurrent, Protocol: "protobuf",
					Repository:         "example/contracts",
					DeclarationLineage: "contract_scip_package_v1 example",
					CanonicalOperation: "/shop.Checkout/Submit",
				}},
			},
			Creator: "user:workbench-owner", CreatedAt: now,
			ContentDigest: "sha256:" + strings.Repeat("e", 64),
		},
	}
	preview := store.WorkbenchPreview{
		SchemaVersion: store.WorkbenchPreviewSchemaVersion,
		Operation:     "create", Ready: true,
		PreviewDigest:        "sha256:" + strings.Repeat("a", 64),
		Referent:             view.Investigation.Referent,
		ClaimFamily:          view.Investigation.ClaimFamily,
		Title:                view.Investigation.Title,
		NextRevisionSequence: 1,
		Revision: store.WorkbenchRevisionCommitment{
			NormalizedQuestion: view.Revision.NormalizedQuestion,
			DecisionSought:     view.Revision.DecisionSought,
			DeclaredUniverse:   view.Revision.DeclaredUniverse,
			SnapshotPolicy:     view.Revision.SnapshotPolicy,
			BuildConfiguration: view.Revision.BuildConfiguration,
			PackSelection:      view.Revision.PackSelection,
			EnumerationMethod:  view.Revision.EnumerationMethod,
		},
		Brief: store.WorkbenchBriefCommitment{
			SchemaVersion:   store.ChangeBriefSchemaVersion,
			TicketKind:      store.ChangeBriefModify,
			Problem:         view.Brief.Problem,
			DesiredOutcome:  view.Brief.DesiredOutcome,
			SuccessCriteria: append([]string(nil), view.Brief.SuccessCriteria...),
			NonGoals:        []string{},
			Assumptions:     []string{},
			OpenQuestions:   []string{},
			What:            view.Brief.What,
			Creator:         view.Brief.Creator,
		},
		AuthorizationDigest: "sha256:" + strings.Repeat("f", 64),
		Repositories: []store.WorkbenchRepositorySnapshot{{
			Name: "example/contracts", Commit: strings.Repeat("1", 40),
		}},
		Endpoints:    []store.WorkbenchEndpointSnapshot{},
		Capabilities: []store.WorkbenchCapabilitySnapshot{},
		Compatibility: store.WorkbenchCompatibilityPreview{
			Status: "unavailable", Reason: "not requested",
		},
		Blockers: []store.WorkbenchBlocker{},
	}
	disposition := store.WorkbenchDisposition{
		SchemaVersion: store.WorkbenchDispositionSchemaVersion,
		ID:            "iwd_workbench", InvestigationID: "inv_workbench",
		RevisionID: "irev_workbench", Suggestion: suggestion,
		Category: "accepted", Actor: "user:workbench-owner",
		Authority: "investigation-owner", Sequence: 1,
		CreatedAt:     now,
		ContentDigest: "sha256:" + strings.Repeat("9", 64),
	}
	return &workbenchToolService{
		preview: preview,
		view:    view,
	}, &workbenchToolChecklist{disposition: disposition}
}

func workbenchToolPlan() store.WorkbenchPlan {
	return store.WorkbenchPlan{
		Referent: "grpc:/shop.Checkout/Submit", ClaimFamily: "change-workbench",
		Title: "Checkout change",
		Revision: store.WorkbenchRevisionDraft{
			NormalizedQuestion: "what changes?",
			DecisionSought:     "approve change",
			SnapshotPolicy:     "exact commit",
			BuildConfiguration: "linux/arm64",
			EnumerationMethod:  "exact selection",
		},
		Brief: store.WorkbenchChangeBriefDraft{
			TicketKind:      store.ChangeBriefModify,
			Problem:         "Receipt identifiers are unstable.",
			DesiredOutcome:  "Receipt identifiers are stable.",
			SuccessCriteria: []string{"The identifier is stable."},
			NonGoals:        []string{},
			Assumptions:     []string{},
			OpenQuestions:   []string{},
			What: store.WorkbenchWhatDraft{
				Selections: []store.ChangeBriefContractSelection{{
					Role: store.ChangeBriefCurrent, Protocol: "protobuf",
					Repository:         "example/contracts",
					DeclarationLineage: "contract_scip_package_v1 example",
					CanonicalOperation: "/shop.Checkout/Submit",
				}},
				ProposalProtocol: "protobuf",
				ProposalSources: []store.WorkbenchProposalSource{{
					Path:    "checkout.proto",
					Content: "syntax = \"proto3\";\nservice Checkout {}\n",
				}},
			},
		},
		Repositories: []string{"example/contracts"},
		Capabilities: []string{"caller-map-exact-identity"},
	}
}

func workbenchToolDispositionMutation(
	suggestion store.WorkbenchSuggestion,
) api.WorkbenchDispositionMutation {
	return api.WorkbenchDispositionMutation{
		InvestigationID: "inv_workbench", ExpectedRevisionID: "irev_workbench",
		IdempotencyKey: "record-once",
		Evidence: api.WorkbenchChecklistEvidenceInput{
			ImpactFilters: api.WorkbenchImpactFilters{},
			Anchors:       []api.WorkbenchImplementationAnchor{},
		},
		Suggestion: suggestion,
		Category:   "accepted",
	}
}

func workbenchToolArguments(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var arguments map[string]any
	if err := json.Unmarshal(encoded, &arguments); err != nil {
		t.Fatal(err)
	}
	return arguments
}
