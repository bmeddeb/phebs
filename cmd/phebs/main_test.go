package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/config"
	phebsmcp "github.com/bmeddeb/phebs/internal/mcp"
	"github.com/bmeddeb/phebs/internal/store"
)

type authenticationWorkbenchFake struct{}

type authenticationWorkbenchChecklistFake struct{}

func (authenticationWorkbenchFake) Preview(
	context.Context,
	string,
	store.WorkbenchPlan,
) (*store.WorkbenchPreview, error) {
	return &store.WorkbenchPreview{
		SchemaVersion: store.WorkbenchPreviewSchemaVersion,
		Operation:     "create",
		Ready:         true,
		PreviewDigest: "sha256:" + strings.Repeat("a", 64),
	}, nil
}

func (authenticationWorkbenchFake) Create(
	context.Context,
	string,
	store.WorkbenchMutationRequest,
) (*store.WorkbenchView, error) {
	return &store.WorkbenchView{}, nil
}

func (authenticationWorkbenchFake) Revise(
	context.Context,
	string,
	store.WorkbenchMutationRequest,
) (*store.WorkbenchView, error) {
	return &store.WorkbenchView{}, nil
}

func (authenticationWorkbenchFake) Read(
	context.Context,
	string,
	string,
) (*store.WorkbenchView, error) {
	return &store.WorkbenchView{}, nil
}

func (authenticationWorkbenchChecklistFake) RecordDisposition(
	context.Context,
	string,
	api.WorkbenchDispositionMutation,
) (*store.WorkbenchDisposition, error) {
	return &store.WorkbenchDisposition{}, nil
}

func TestHTTPHandlerAuthenticationBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	insecure := false
	authService, err := auth.New(ctx, auth.Options{Store: st, Config: config.Auth{
		APIKey: "legacy-integration-token", CookieSecure: &insecure,
		BootstrapUser: config.BootstrapUser{
			Email: "admin@example.com", Password: "integration-password",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	apiHandler := api.New(api.Options{
		Version: "test", Store: st, DataDir: t.TempDir(), WebhookSecret: "hook-secret",
		IsAdmin: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			return ok && principal.IsAdmin
		},
		Principal: func(ctx context.Context) string {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok {
				return ""
			}
			if principal.User != nil {
				return "user:" + principal.User.ID
			}
			if principal.APIKeyID != "" {
				return "api-key:" + principal.APIKeyID
			}
			return ""
		},
		InvestigationMutation: func(ctx context.Context) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok {
				return false
			}
			if principal.AuthMethod == "session" {
				return true
			}
			return principal.HasAPIKeyCapability(
				store.APIKeyCapabilityInvestigationWrite,
			)
		},
		Workbench: authenticationWorkbenchFake{},
	})
	mcpOpts := phebsmcp.Options{
		Version: "test", Workbench: authenticationWorkbenchFake{},
		WorkbenchChecklist: authenticationWorkbenchChecklistFake{},
		Principal: func(ctx context.Context) string {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok || principal.User == nil {
				return ""
			}
			return "user:" + principal.User.ID
		},
		InvestigationMutation: mcpInvestigationMutation,
	}
	mcpReadServer := phebsmcp.NewServer(mcpOpts)
	mcpOpts.AdvertiseWorkbenchMutations = true
	mcpWriteServer := phebsmcp.NewServer(mcpOpts)
	streamableMCPHandler := mcpsdk.NewStreamableHTTPHandler(
		func(request *http.Request) *mcpsdk.Server {
			if mcpInvestigationMutation(request.Context()) {
				return mcpWriteServer
			}
			return mcpReadServer
		},
		&mcpsdk.StreamableHTTPOptions{Stateless: true},
	)
	mcpHandler := http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		// Preserve the narrow routing smoke assertion below; official SDK
		// request/response coverage uses POST and disables standalone SSE.
		if request.Method == http.MethodGet {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		streamableMCPHandler.ServeHTTP(writer, request)
	})
	metricsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "metrics") })
	uiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ui") })
	server := httptest.NewServer(newHTTPHandler(authService, apiHandler, mcpHandler, metricsHandler, uiHandler))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	assertStatus(t, client, http.MethodGet, server.URL+"/api/health", "", nil, http.StatusOK, `"status":"ok"`)
	assertStatus(t, client, http.MethodGet, server.URL+"/api/openapi.json", "", nil, http.StatusOK, `"openapi"`)
	assertStatus(t, client, http.MethodGet, server.URL+"/metrics", "", nil, http.StatusOK, "metrics")
	assertStatus(t, client, http.MethodGet, server.URL+"/", "", nil, http.StatusOK, "ui")
	assertStatus(t, client, http.MethodGet, server.URL+"/api/repos", "", nil, http.StatusUnauthorized, "authentication required")
	assertStatus(t, client, http.MethodGet, server.URL+"/api/mcp", "", nil, http.StatusUnauthorized, "authentication required")
	legacyTools, err := mcpToolNamesWithBearer(
		t,
		server.URL+"/api/mcp",
		"legacy-integration-token",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"create_change_workbench",
		"record_change_disposition",
	} {
		if legacyTools[name] {
			t.Fatalf("legacy key discovered %s: %v", name, legacyTools)
		}
	}

	// The provider HMAC, not user auth, is the webhook trust boundary.
	assertStatus(t, client, http.MethodPost, server.URL+"/api/webhook", `{}`, nil, http.StatusUnauthorized, "bad signature")
	hookHeaders := http.Header{"X-Github-Event": []string{"ping"}}
	hookHeaders.Set("X-Hub-Signature-256", webhookSignature([]byte(`{}`), "hook-secret"))
	assertStatus(t, client, http.MethodPost, server.URL+"/api/webhook", `{}`, hookHeaders, http.StatusOK, "pong")

	login := assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"integration-password"}`,
		http.Header{"Content-Type": []string{"application/json"}}, http.StatusOK, `"authenticated":true`)
	var loginResult struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login, &loginResult); err != nil || loginResult.CSRFToken == "" {
		t.Fatalf("login response = %s, err %v", login, err)
	}
	sessionTools, err := mcpToolNamesWithBrowserSession(
		t,
		server.URL+"/api/mcp",
		jar,
		loginResult.CSRFToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"create_change_workbench",
		"record_change_disposition",
	} {
		if sessionTools[name] {
			t.Fatalf("browser session discovered %s: %v", name, sessionTools)
		}
	}
	assertStatus(t, client, http.MethodGet, server.URL+"/api/repos", "", nil, http.StatusOK, "[]")
	assertStatus(t, client, http.MethodGet, server.URL+"/api/evidence?domain=proto-contract", "", nil,
		http.StatusNotFound, "404")
	assertStatus(t, client, http.MethodPost, server.URL+"/api/reindex", `{"repo":"github.com/no/repo"}`,
		http.Header{"Content-Type": []string{"application/json"}}, http.StatusForbidden, "CSRF")
	assertStatus(t, client, http.MethodPost, server.URL+"/api/reindex", `{"repo":"github.com/no/repo"}`,
		http.Header{"Content-Type": []string{"application/json"}, "X-Csrf-Token": []string{loginResult.CSRFToken}},
		http.StatusNotFound, "unknown repo")
	workbenchPlan, err := json.Marshal(authenticationWorkbenchPlan())
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(
		t,
		client,
		http.MethodPost,
		server.URL+"/api/workbench_previews",
		string(workbenchPlan),
		http.Header{"Content-Type": []string{"application/json"}},
		http.StatusForbidden,
		"CSRF",
	)
	assertStatus(
		t,
		client,
		http.MethodPost,
		server.URL+"/api/workbench_previews",
		string(workbenchPlan),
		http.Header{
			"Content-Type": []string{"application/json"},
			"X-Csrf-Token": []string{loginResult.CSRFToken},
		},
		http.StatusOK,
		`"ready":true`,
	)

	keyHeaders := http.Header{"Content-Type": []string{"application/json"}, "X-Csrf-Token": []string{loginResult.CSRFToken}}
	created := assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/keys", `{"name":"integration"}`,
		keyHeaders, http.StatusCreated, `"token":"phebs_`)
	var keyResult struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(created, &keyResult); err != nil || keyResult.Token == "" {
		t.Fatalf("key response = %s, err %v", created, err)
	}
	bearerHeaders := http.Header{"Authorization": []string{"Bearer " + keyResult.Token}}
	bearerClient := &http.Client{}
	assertStatus(t, bearerClient, http.MethodGet, server.URL+"/api/repos", "", bearerHeaders, http.StatusOK, "[]")
	assertStatus(t, bearerClient, http.MethodGet, server.URL+"/api/mcp", "", bearerHeaders, http.StatusNoContent, "")
	readTools, err := mcpToolNamesWithBearer(
		t,
		server.URL+"/api/mcp",
		keyResult.Token,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"preview_change_workbench",
		"get_change_workbench",
	} {
		if !readTools[name] {
			t.Fatalf("read-only named key omitted %s: %v", name, readTools)
		}
	}
	for _, name := range []string{
		"create_change_workbench",
		"record_change_disposition",
	} {
		if readTools[name] {
			t.Fatalf("read-only named key discovered %s: %v", name, readTools)
		}
	}
	assertStatus(t, bearerClient, http.MethodGet, server.URL+"/api/auth/keys", "", bearerHeaders, http.StatusForbidden, "browser session")
	assertStatus(
		t,
		bearerClient,
		http.MethodPost,
		server.URL+"/api/workbench_previews",
		string(workbenchPlan),
		http.Header{
			"Authorization": []string{"Bearer " + keyResult.Token},
			"Content-Type":  []string{"application/json"},
		},
		http.StatusNotFound,
		"workbench resource not found",
	)

	writeCreated := assertStatus(
		t,
		client,
		http.MethodPost,
		server.URL+"/api/auth/keys",
		`{"name":"investigation agent","capabilities":["investigation:write"]}`,
		keyHeaders,
		http.StatusCreated,
		`"capabilities":["investigation:write"]`,
	)
	var writeKeyResult struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(writeCreated, &writeKeyResult); err != nil ||
		writeKeyResult.Key.ID == "" || writeKeyResult.Token == "" {
		t.Fatalf("write key response = %s, err %v", writeCreated, err)
	}
	writeBearerHeaders := http.Header{
		"Authorization": []string{"Bearer " + writeKeyResult.Token},
		"Content-Type":  []string{"application/json"},
	}
	writeTools, err := mcpToolNamesWithBearer(
		t,
		server.URL+"/api/mcp",
		writeKeyResult.Token,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"preview_change_workbench",
		"create_change_workbench",
		"get_change_workbench",
		"record_change_disposition",
	} {
		if !writeTools[name] {
			t.Fatalf("write-capable named key omitted %s: %v", name, writeTools)
		}
	}
	assertStatus(
		t,
		bearerClient,
		http.MethodPost,
		server.URL+"/api/workbench_previews",
		string(workbenchPlan),
		writeBearerHeaders,
		http.StatusOK,
		`"ready":true`,
	)
	assertStatus(
		t,
		client,
		http.MethodDelete,
		server.URL+"/api/auth/keys/"+writeKeyResult.Key.ID,
		"",
		keyHeaders,
		http.StatusNoContent,
		"",
	)
	assertStatus(
		t,
		bearerClient,
		http.MethodPost,
		server.URL+"/api/workbench_previews",
		string(workbenchPlan),
		writeBearerHeaders,
		http.StatusUnauthorized,
		"authentication required",
	)
	if _, err := mcpToolNamesWithBearer(
		t,
		server.URL+"/api/mcp",
		writeKeyResult.Token,
	); err == nil {
		t.Fatal("revoked named key discovered MCP tools")
	}
}

type mcpBearerTransport struct {
	token string
	csrf  string
}

func (transport mcpBearerTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	if transport.token != "" {
		request.Header.Set("Authorization", "Bearer "+transport.token)
	}
	if transport.csrf != "" {
		request.Header.Set("X-CSRF-Token", transport.csrf)
	}
	return http.DefaultTransport.RoundTrip(request)
}

func mcpToolNamesWithBearer(
	t *testing.T,
	endpoint string,
	token string,
) (map[string]bool, error) {
	t.Helper()
	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "t21.13-auth-test", Version: "1"},
		nil,
	)
	session, err := client.Connect(
		t.Context(),
		&mcpsdk.StreamableClientTransport{
			Endpoint: endpoint,
			HTTPClient: &http.Client{
				Transport: mcpBearerTransport{token: token},
			},
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		},
		nil,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	return names, nil
}

func mcpToolNamesWithBrowserSession(
	t *testing.T,
	endpoint string,
	jar http.CookieJar,
	csrf string,
) (map[string]bool, error) {
	t.Helper()
	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "t21.13-session-test", Version: "1"},
		nil,
	)
	session, err := client.Connect(
		t.Context(),
		&mcpsdk.StreamableClientTransport{
			Endpoint: endpoint,
			HTTPClient: &http.Client{
				Jar:       jar,
				Transport: mcpBearerTransport{csrf: csrf},
			},
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		},
		nil,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	return names, nil
}

func authenticationWorkbenchPlan() store.WorkbenchPlan {
	return store.WorkbenchPlan{
		Referent:    "grpc:/shop.v1.Checkout/Submit",
		ClaimFamily: "change-workbench",
		Title:       "Checkout change",
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
			What: store.WorkbenchWhatDraft{
				Selections: []store.ChangeBriefContractSelection{{
					Role:               store.ChangeBriefCurrent,
					Protocol:           "protobuf",
					Repository:         "example/contracts",
					DeclarationLineage: "proto/shop.proto:shop.Checkout",
					CanonicalOperation: "/shop.Checkout/Submit",
				}},
			},
		},
		Repositories: []string{"example/contracts"},
		Capabilities: []string{"contract-atlas"},
	}
}

func TestVersionCapabilitiesRequireAuthenticatedPrincipal(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	insecure := false
	authService, err := auth.New(ctx, auth.Options{Store: st, Config: config.Auth{
		CookieSecure: &insecure,
		BootstrapUser: config.BootstrapUser{
			Email: "admin@example.com", Password: "integration-password",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	apiHandler := api.New(api.Options{
		Version: "test", Store: st, Evidence: st, ProofBundles: st,
		Principal: func(ctx context.Context) string {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok {
				return ""
			}
			if principal.User != nil {
				return "user:" + principal.User.ID
			}
			if principal.APIKeyID != "" {
				return "api-key:" + principal.APIKeyID
			}
			return "authenticated:" + principal.AuthMethod
		},
	})
	notFound := http.NotFoundHandler()
	server := httptest.NewServer(newHTTPHandler(authService, apiHandler, notFound, notFound, notFound))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	anonymous := assertStatus(t, client, http.MethodGet, server.URL+"/api/version", "", nil, http.StatusOK, `"version":"test"`)
	if strings.Contains(string(anonymous), "contract-impact-report") {
		t.Fatalf("anonymous version leaked capabilities: %s", anonymous)
	}
	assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"integration-password"}`,
		http.Header{"Content-Type": []string{"application/json"}}, http.StatusOK, `"authenticated":true`)
	assertStatus(t, client, http.MethodGet, server.URL+"/api/version", "", nil,
		http.StatusOK, `"contract-impact-report"`)

	invalid := assertStatus(t, client, http.MethodGet, server.URL+"/api/version", "",
		http.Header{"Authorization": []string{"Bearer invalid"}}, http.StatusOK, `"version":"test"`)
	if strings.Contains(string(invalid), "contract-impact-report") {
		t.Fatalf("invalid credentials leaked capabilities: %s", invalid)
	}
}

func TestBindSyntheticWorkbenchIsExplicitAndFixtureCoupled(t *testing.T) {
	fixtureViews, err := api.NewInvestigationFixtureViews(
		"../../docs/fixtures/investigations",
	)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := func() api.Options {
		return api.Options{
			InvestigationViews:     fixtureViews,
			ContractCatalogFixture: &api.ContractCatalogFixture{},
		}
	}
	workbench := store.InvestigationWorkbenchService{}

	t.Run("ordinary serve remains dark", func(t *testing.T) {
		opts := fixtures()
		if err := bindSyntheticWorkbench(&opts, "", workbench); err != nil {
			t.Fatal(err)
		}
		if opts.Workbench != nil {
			t.Fatal("empty synthetic setting registered the Workbench")
		}
	})

	t.Run("exact setting and both fixtures register", func(t *testing.T) {
		opts := fixtures()
		if err := bindSyntheticWorkbench(&opts, "1", workbench); err != nil {
			t.Fatal(err)
		}
		if opts.Workbench == nil {
			t.Fatal("synthetic adapter did not register the Workbench")
		}
	})

	t.Run("missing fixtures fail closed", func(t *testing.T) {
		opts := api.Options{}
		err := bindSyntheticWorkbench(&opts, "1", workbench)
		if err == nil || !strings.Contains(err.Error(), "requires Investigation") {
			t.Fatalf("missing fixture error = %v", err)
		}
		if opts.Workbench != nil {
			t.Fatal("fixture refusal registered the Workbench")
		}
	})

	t.Run("invalid setting and unavailable service fail closed", func(t *testing.T) {
		opts := fixtures()
		for _, setting := range []string{"true", " 1 "} {
			if err := bindSyntheticWorkbench(
				&opts,
				setting,
				workbench,
			); err == nil {
				t.Fatalf("invalid synthetic setting %q was accepted", setting)
			}
		}
		if err := bindSyntheticWorkbench(&opts, "1", nil); err == nil {
			t.Fatal("nil synthetic service was accepted")
		}
		if opts.Workbench != nil {
			t.Fatal("failed registration changed Workbench options")
		}
	})
}

type provisionalWorkbenchEvidence struct {
	store.EvidenceStore
}

func TestBindProvisionalWorkbenchRequiresRealEvidenceAuthority(t *testing.T) {
	ready := func() api.Options {
		return api.Options{
			Evidence:        &provisionalWorkbenchEvidence{},
			ContractCatalog: &api.ContractCatalogService{},
		}
	}
	workbench := store.InvestigationWorkbenchService{}

	t.Run("protocol evidence and store catalog register", func(t *testing.T) {
		opts := ready()
		if err := bindProvisionalWorkbench(&opts, true, "", workbench); err != nil {
			t.Fatal(err)
		}
		if opts.Workbench == nil {
			t.Fatal("provisional Workbench was not registered")
		}
	})

	t.Run("flag alone fails prerequisite", func(t *testing.T) {
		opts := ready()
		err := bindProvisionalWorkbench(&opts, false, "", workbench)
		if !errors.Is(err, errWorkbenchEvidencePrerequisite) {
			t.Fatalf("missing evidence error = %v", err)
		}
		if opts.Workbench != nil {
			t.Fatal("prerequisite refusal registered the Workbench")
		}
	})

	t.Run("synthetic authorities conflict", func(t *testing.T) {
		for _, row := range []struct {
			name    string
			setting string
			fixture *api.ContractCatalogFixture
		}{
			{name: "synthetic setting", setting: "1"},
			{name: "catalog fixture", fixture: &api.ContractCatalogFixture{}},
		} {
			t.Run(row.name, func(t *testing.T) {
				opts := ready()
				opts.ContractCatalogFixture = row.fixture
				err := bindProvisionalWorkbench(
					&opts,
					true,
					row.setting,
					workbench,
				)
				if !errors.Is(err, errWorkbenchAuthorityConflict) {
					t.Fatalf("authority conflict error = %v", err)
				}
				if opts.Workbench != nil {
					t.Fatal("authority refusal registered the Workbench")
				}
			})
		}
	})

	t.Run("invalid synthetic setting retains strict parsing precedence", func(t *testing.T) {
		opts := ready()
		err := bindProvisionalWorkbench(&opts, true, "0", workbench)
		if !errors.Is(err, errSyntheticWorkbenchSetting) {
			t.Fatalf("invalid setting error = %v", err)
		}
		if errors.Is(err, errWorkbenchAuthorityConflict) {
			t.Fatalf("invalid setting was misclassified as authority conflict: %v", err)
		}
		if opts.Workbench != nil {
			t.Fatal("invalid setting registered the Workbench")
		}
	})

	t.Run("missing services fail prerequisite", func(t *testing.T) {
		for _, opts := range []api.Options{
			{},
			{Evidence: &provisionalWorkbenchEvidence{}},
			{ContractCatalog: &api.ContractCatalogService{}},
		} {
			err := bindProvisionalWorkbench(&opts, true, "", workbench)
			if !errors.Is(err, errWorkbenchEvidencePrerequisite) {
				t.Fatalf("missing service error = %v", err)
			}
			if opts.Workbench != nil {
				t.Fatal("service refusal registered the Workbench")
			}
		}
	})
}

func TestBindSyntheticThriftFieldDemoIsExplicitAndPipelineBacked(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join(
		"..", "..", "docs", "fixtures", "thrift-field", "t225-thrift-field-demo.bundle",
	))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ordinary serve remains dark", func(t *testing.T) {
		cfg := &config.Config{}
		if err := bindSyntheticThriftFieldDemo(cfg, ""); err != nil {
			t.Fatal(err)
		}
		if cfg.Experimental.ProvisionalThriftFieldExtraction || len(cfg.Connections) != 0 {
			t.Fatalf("empty setting changed config: %+v", cfg)
		}
	})

	t.Run("exact bundle registers ordinary pipeline input", func(t *testing.T) {
		cfg := &config.Config{}
		if err := bindSyntheticThriftFieldDemo(cfg, fixture); err != nil {
			t.Fatal(err)
		}
		if !cfg.Experimental.ProvisionalThriftFieldExtraction ||
			len(cfg.Connections) != 1 ||
			cfg.Connections[0].Name != "t22-thrift-field-demo" ||
			cfg.Connections[0].Type != "git" ||
			cfg.Connections[0].URL != fixture {
			t.Fatalf("synthetic fixture config = %+v", cfg)
		}
	})

	t.Run("existing exact source is not duplicated", func(t *testing.T) {
		cfg := &config.Config{Connections: []config.Connection{{
			Name: "already-present", Type: "git", URL: fixture,
		}}}
		if err := bindSyntheticThriftFieldDemo(cfg, fixture); err != nil {
			t.Fatal(err)
		}
		if !cfg.Experimental.ProvisionalThriftFieldExtraction || len(cfg.Connections) != 1 {
			t.Fatalf("existing fixture source changed unexpectedly: %+v", cfg)
		}
	})

	t.Run("invalid or colliding settings fail before mutation", func(t *testing.T) {
		rows := []struct {
			name    string
			fixture string
			cfg     *config.Config
		}{
			{name: "relative", fixture: "docs/fixtures/thrift-field/t225-thrift-field-demo.bundle", cfg: &config.Config{}},
			{name: "surrounding space", fixture: fixture + " ", cfg: &config.Config{}},
			{name: "wrong basename", fixture: filepath.Join(filepath.Dir(fixture), "other.bundle"), cfg: &config.Config{}},
			{name: "missing", fixture: filepath.Join(t.TempDir(), "t225-thrift-field-demo.bundle"), cfg: &config.Config{}},
			{
				name: "connection collision", fixture: fixture,
				cfg: &config.Config{Connections: []config.Connection{{
					Name: "t22-thrift-field-demo", Type: "git", URL: "/different/source",
				}}},
			},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				before := *row.cfg
				before.Connections = append([]config.Connection(nil), row.cfg.Connections...)
				if err := bindSyntheticThriftFieldDemo(row.cfg, row.fixture); err == nil {
					t.Fatalf("invalid fixture setting %q succeeded", row.fixture)
				}
				if !reflect.DeepEqual(*row.cfg, before) {
					t.Fatalf("failed binding changed config: before=%+v after=%+v", before, *row.cfg)
				}
			})
		}
	})
}

func TestBindSyntheticWorkbenchClosureDemoIsExplicitAndPipelineBacked(
	t *testing.T,
) {
	fixture, err := filepath.Abs(filepath.Join(
		"..", "..", "docs", "fixtures", "change-workbench",
		"t2114-workbench-closure.bundle",
	))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ordinary serve remains dark", func(t *testing.T) {
		cfg := &config.Config{}
		if err := bindSyntheticWorkbenchClosureDemo(cfg, ""); err != nil {
			t.Fatal(err)
		}
		if cfg.Experimental.ProvisionalProtoExtraction ||
			cfg.Experimental.ProvisionalThriftExtraction ||
			cfg.Experimental.ProvisionalKafkaExtraction ||
			len(cfg.Connections) != 0 {
			t.Fatalf("empty setting changed config: %+v", cfg)
		}
	})

	t.Run("exact bundle registers only reviewed pipeline inputs", func(t *testing.T) {
		cfg := &config.Config{}
		if err := bindSyntheticWorkbenchClosureDemo(cfg, fixture); err != nil {
			t.Fatal(err)
		}
		if !cfg.Experimental.ProvisionalProtoExtraction ||
			!cfg.Experimental.ProvisionalThriftExtraction ||
			cfg.Experimental.ProvisionalThriftFieldExtraction ||
			cfg.Experimental.ProvisionalKafkaExtraction ||
			len(cfg.Connections) != 1 ||
			cfg.Connections[0].Name != "t21-workbench-closure" ||
			cfg.Connections[0].Type != "git" ||
			cfg.Connections[0].URL != fixture {
			t.Fatalf("synthetic fixture config = %+v", cfg)
		}
	})

	t.Run("existing exact source is not duplicated", func(t *testing.T) {
		cfg := &config.Config{Connections: []config.Connection{{
			Name: "already-present", Type: "git", URL: fixture,
		}}}
		if err := bindSyntheticWorkbenchClosureDemo(cfg, fixture); err != nil {
			t.Fatal(err)
		}
		if len(cfg.Connections) != 1 ||
			!cfg.Experimental.ProvisionalProtoExtraction ||
			!cfg.Experimental.ProvisionalThriftExtraction {
			t.Fatalf("existing fixture source changed unexpectedly: %+v", cfg)
		}
	})

	t.Run("invalid or colliding settings fail before mutation", func(t *testing.T) {
		rows := []struct {
			name    string
			fixture string
			cfg     *config.Config
		}{
			{
				name: "relative",
				fixture: "docs/fixtures/change-workbench/" +
					"t2114-workbench-closure.bundle",
				cfg: &config.Config{},
			},
			{name: "surrounding space", fixture: fixture + " ", cfg: &config.Config{}},
			{
				name:    "wrong basename",
				fixture: filepath.Join(filepath.Dir(fixture), "other.bundle"),
				cfg:     &config.Config{},
			},
			{
				name:    "missing",
				fixture: filepath.Join(t.TempDir(), "t2114-workbench-closure.bundle"),
				cfg:     &config.Config{},
			},
			{
				name: "connection collision", fixture: fixture,
				cfg: &config.Config{Connections: []config.Connection{{
					Name: "t21-workbench-closure", Type: "git",
					URL: "/different/source",
				}}},
			},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				before := *row.cfg
				before.Connections = append(
					[]config.Connection(nil),
					row.cfg.Connections...,
				)
				if err := bindSyntheticWorkbenchClosureDemo(
					row.cfg,
					row.fixture,
				); err == nil {
					t.Fatalf("invalid fixture setting %q succeeded", row.fixture)
				}
				if !reflect.DeepEqual(*row.cfg, before) {
					t.Fatalf(
						"failed binding changed config: before=%+v after=%+v",
						before,
						*row.cfg,
					)
				}
			})
		}
	})
}

func TestPrintVersion(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{name: "exact build identity", want: version + "\n"},
		{name: "reject argument", args: []string{"extra"}, wantErr: "version accepts no arguments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := printVersion(test.args, &output)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("printVersion error = %v, want %q", err, test.wantErr)
				}
				if output.Len() != 0 {
					t.Fatalf("printVersion output = %q, want empty", output.String())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("printVersion output = %q, want %q", output.String(), test.want)
			}
		})
	}
}

func TestEvidenceViewUsesAuthenticatedPrincipal(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	const (
		visibleRepo = "github.com/allowed/evidence"
		hiddenRepo  = "github.com/secret/hidden-evidence-service"
	)
	seedHTTPTestEvidence(t, st, visibleRepo, "visible.Service/Get")
	seedHTTPTestEvidence(t, st, hiddenRepo, "secret.HiddenService/Call")

	insecure := false
	authService, err := auth.New(ctx, auth.Options{Store: st, Config: config.Auth{
		CookieSecure: &insecure,
		BootstrapUser: config.BootstrapUser{
			Email: "admin@example.com", Password: "integration-password",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var principalResolutions atomic.Int32
	apiHandler := api.New(api.Options{
		Version: "test", Store: st, Evidence: st,
		Visible: func(ctx context.Context) func(store.Repo) bool {
			principal, ok := auth.PrincipalFromContext(ctx)
			if !ok || principal.User == nil || principal.User.NormalizedEmail != "admin@example.com" {
				return func(store.Repo) bool { return false }
			}
			principalResolutions.Add(1)
			return func(repo store.Repo) bool { return repo.Name == visibleRepo }
		},
	})
	server := httptest.NewServer(newHTTPHandler(
		authService,
		apiHandler,
		http.NotFoundHandler(),
		http.NotFoundHandler(),
		http.NotFoundHandler(),
	))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	assertStatus(t, client, http.MethodGet, server.URL+"/api/evidence?domain=proto-contract", "", nil,
		http.StatusUnauthorized, "authentication required")
	if principalResolutions.Load() != 0 {
		t.Fatal("unauthenticated evidence request reached the visibility resolver")
	}
	assertStatus(t, client, http.MethodPost, server.URL+"/api/auth/login",
		`{"email":"admin@example.com","password":"integration-password"}`,
		http.Header{"Content-Type": []string{"application/json"}}, http.StatusOK, `"authenticated":true`)
	response := assertStatus(t, client, http.MethodGet, server.URL+"/api/evidence?domain=proto-contract", "", nil,
		http.StatusOK, `"visible_repository_count":1`)
	if principalResolutions.Load() != 1 {
		t.Fatalf("authenticated visibility resolutions = %d, want 1", principalResolutions.Load())
	}
	if !bytes.Contains(response, []byte(visibleRepo)) || bytes.Contains(response, []byte(hiddenRepo)) ||
		bytes.Contains(response, []byte("HiddenService")) {
		t.Fatalf("authenticated evidence visibility = %s", response)
	}
}

func seedHTTPTestEvidence(t *testing.T, st *store.Surreal, repo, object string) {
	t.Helper()
	ctx := t.Context()
	commit := "commit-" + strings.ReplaceAll(repo, "/", "-")
	if err := st.UpsertRepo(ctx, store.Repo{Name: repo, CloneURL: "https://example.test/repo.git"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(ctx, repo, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run, err := st.BeginExtractionRun(ctx, repo, commit, "proto-contract", "test")
	if err != nil {
		t.Fatal(err)
	}
	atom := store.EvidenceAtom{
		SchemaVersion: "test-v1", BlobDigest: "sha256:" + strings.Repeat("a", 64),
		StartByte: 0, EndByte: 1, RuleID: "test-rule", ExtractorVersion: "test",
		AdapterConfigDigest: "sha256:" + strings.Repeat("b", 64), FactFingerprint: object,
	}
	atom.ID = store.ComputeAtomID(atom)
	if err := st.AddEvidence(ctx, run.ID,
		[]store.EvidenceAtom{atom},
		[]store.SnapshotEvidence{{
			AtomID: atom.ID, Repo: repo, Commit: commit, Path: "api.proto",
			StartLine: 1, EndLine: 1, VisibilityScope: "repo:" + repo,
		}},
		[]store.Assertion{{
			Predicate: "DECLARES_OPERATION", Subject: "api.proto", Object: object,
			Tier: store.TierExact, Repo: repo, Supporting: []string{atom.ID},
		}},
	); err != nil {
		t.Fatal(err)
	}
	coverage := store.CoverageManifest{
		CorpusFileCount: 1, CandidateFileCount: 1, ReadFileCount: 1, ReadBytes: 1,
		SourceScopeDigest: "sha256:" + strings.Repeat("c", 64), AssertionCount: 1, AtomCount: 1,
	}
	if err := st.PublishExtractionRun(ctx, run.ID, coverage); err != nil {
		t.Fatal(err)
	}
}

func assertStatus(t *testing.T, client *http.Client, method, target, body string, headers http.Header, want int, contains string) []byte {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want || contains != "" && !bytes.Contains(data, []byte(contains)) {
		t.Fatalf("%s %s = %d %s, want %d containing %q", method, target, response.StatusCode, data, want, contains)
	}
	return data
}

func webhookSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type extractionBackfillStore struct {
	store.Store
	repos      []store.Repo
	listErr    error
	enqueueErr error
	pending    map[string]store.Job
	created    int
}

func (s *extractionBackfillStore) ListRepos(context.Context) ([]store.Repo, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.repos, nil
}

func (s *extractionBackfillStore) EnqueuePending(
	_ context.Context, kind store.JobKind, target string, force bool,
) (*store.Job, error) {
	if s.enqueueErr != nil {
		return nil, s.enqueueErr
	}
	if kind != store.JobExtract {
		return nil, errors.New("unexpected non-extraction job")
	}
	if s.pending == nil {
		s.pending = make(map[string]store.Job)
	}
	if job, ok := s.pending[target]; ok {
		return &job, nil
	}
	job := store.Job{Kind: kind, Target: target, Status: store.StatusPending, Force: force}
	s.pending[target] = job
	s.created++
	return &job, nil
}

func TestEnqueueExtractionBackfillIndexedLiveReposOnlyAndDedupes(t *testing.T) {
	st := &extractionBackfillStore{repos: []store.Repo{
		{Name: "example.com/live/one", IndexedCommitHash: "a"},
		{Name: "example.com/unindexed"},
		{Name: "example.com/deleting", IndexedCommitHash: "b", Deleting: true},
		{Name: "example.com/live/two", IndexedCommitHash: "c"},
	}}
	if err := enqueueExtractionBackfill(t.Context(), st); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if err := enqueueExtractionBackfill(t.Context(), st); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if st.created != 2 || len(st.pending) != 2 {
		t.Fatalf("created=%d pending=%v, want one job for each of two live indexed repos", st.created, st.pending)
	}
	for _, target := range []string{"example.com/live/one", "example.com/live/two"} {
		if _, ok := st.pending[target]; !ok {
			t.Errorf("missing backfill job for %s", target)
		}
	}
}

func TestEnqueueExtractionBackfillPropagatesListError(t *testing.T) {
	want := errors.New("list failed")
	err := enqueueExtractionBackfill(t.Context(), &extractionBackfillStore{listErr: want})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
}

func TestEnqueueExtractionBackfillPropagatesEnqueueError(t *testing.T) {
	want := errors.New("enqueue failed")
	st := &extractionBackfillStore{
		repos:      []store.Repo{{Name: "example.com/live", IndexedCommitHash: "a"}},
		enqueueErr: want,
	}
	err := enqueueExtractionBackfill(t.Context(), st)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
}

func TestEnqueueExtractionAfterIndexClassifiesEnqueueError(t *testing.T) {
	want := errors.New("enqueue failed")
	err := enqueueExtractionAfterIndex(t.Context(), &extractionBackfillStore{enqueueErr: want},
		"example.com/live", "abc123")
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
	if class := store.Classify(err); class != store.ClassExtract {
		t.Fatalf("class = %q, want %q", class, store.ClassExtract)
	}
}

type evidenceMaintenanceStore struct {
	store.EvidenceStore
	calls       chan time.Duration
	results     []evidenceSweepResult
	resultIndex int
	count       int
	err         error
}

type evidenceSweepResult struct {
	count    int
	progress store.EvidenceSweepProgress
	err      error
}

type proofBundleMaintenanceStore struct {
	cutoffs     chan time.Time
	results     []evidenceSweepResult
	resultIndex int
}

func (s *proofBundleMaintenanceStore) SweepProofBundles(_ context.Context, activeAfter time.Time) (int, error) {
	s.cutoffs <- activeAfter
	if s.resultIndex >= len(s.results) {
		return 0, nil
	}
	result := s.results[s.resultIndex]
	s.resultIndex++
	return result.count, result.err
}

func (s *evidenceMaintenanceStore) SweepEvidence(
	_ context.Context, _ time.Time, staleStagedAfter time.Duration,
) (store.EvidenceSweepProgress, error) {
	s.calls <- staleStagedAfter
	if s.resultIndex < len(s.results) {
		result := s.results[s.resultIndex]
		s.resultIndex++
		if result.progress.DidWork() {
			return result.progress, result.err
		}
		return store.EvidenceSweepProgress{RunsDeleted: result.count}, result.err
	}
	return store.EvidenceSweepProgress{RunsDeleted: s.count}, s.err
}

func TestEvidenceSweepPassStopsWhenDrained(t *testing.T) {
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, 3),
		results: []evidenceSweepResult{
			{count: 1}, {count: 1}, {count: 0},
		},
	}
	progress, backlog, err := runEvidenceSweepPass(t.Context(), st, 24*time.Hour)
	if err != nil || progress.RunsDeleted != 2 || backlog {
		t.Fatalf("runEvidenceSweepPass = (%+v, %v, %v), want (2 runs, false, nil)", progress, backlog, err)
	}
	if len(st.calls) != 3 {
		t.Fatalf("sweep calls = %d, want 3", len(st.calls))
	}
}

func TestEvidenceSweepPassSeparatesLogicalAndPhysicalProgress(t *testing.T) {
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, 4),
		results: []evidenceSweepResult{
			{progress: store.EvidenceSweepProgress{RunsMarkedDeleting: 1}},
			{progress: store.EvidenceSweepProgress{
				AssociationRowsDeleted: 512, AtomRowsDeleted: 7,
			}},
			{progress: store.EvidenceSweepProgress{
				AssertionRowsDeleted: 400, RunsDeleted: 1,
			}},
			{},
		},
	}
	progress, backlog, err := runEvidenceSweepPass(t.Context(), st, 24*time.Hour)
	if err != nil || backlog {
		t.Fatalf("runEvidenceSweepPass = (%+v, %v, %v)", progress, backlog, err)
	}
	if progress.RunsMarkedDeleting != 1 || progress.RunsDeleted != 1 ||
		progress.AssociationRowsDeleted != 512 || progress.AssertionRowsDeleted != 400 ||
		progress.AtomRowsDeleted != 7 || progress.PhysicalRowsDeleted() != 919 {
		t.Fatalf("aggregated progress = %+v", progress)
	}
}

func TestProofBundleSweepPassUsesConfiguredLifetimeAndStopsWhenDrained(t *testing.T) {
	lifetime := 7 * 24 * time.Hour
	st := &proofBundleMaintenanceStore{
		cutoffs: make(chan time.Time, 3),
		results: []evidenceSweepResult{{count: 1}, {count: 1}, {count: 0}},
	}
	before := time.Now().UTC().Add(-lifetime)
	deleted, backlog, err := runProofBundleSweepPass(t.Context(), st, lifetime)
	after := time.Now().UTC().Add(-lifetime)
	if err != nil || deleted != 2 || backlog {
		t.Fatalf("runProofBundleSweepPass = (%d, %v, %v), want (2, false, nil)", deleted, backlog, err)
	}
	if len(st.cutoffs) != 3 {
		t.Fatalf("proof bundle sweep calls = %d, want 3", len(st.cutoffs))
	}
	for cutoff := range 3 {
		got := <-st.cutoffs
		if got.Before(before) || got.After(after) {
			t.Fatalf("cutoff %d = %s, want within [%s, %s]", cutoff, got, before, after)
		}
	}
}

func TestProofBundleMaintenanceCanceledBeforeBootDoesNoWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	st := &proofBundleMaintenanceStore{cutoffs: make(chan time.Time, 1)}
	runProofBundleMaintenance(ctx, st, 24*time.Hour, time.Hour, time.Second)
	if len(st.cutoffs) != 0 {
		t.Fatalf("canceled proof maintenance made %d sweep call(s)", len(st.cutoffs))
	}
}

func TestEvidenceSweepPassCapsLikelyBacklog(t *testing.T) {
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, evidenceSweepMaxStepsPerPass), count: 1,
	}
	progress, backlog, err := runEvidenceSweepPass(t.Context(), st, 24*time.Hour)
	if err != nil || progress.RunsDeleted != evidenceSweepMaxStepsPerPass || !backlog {
		t.Fatalf("runEvidenceSweepPass = (%+v, %v, %v), want (%d runs, true, nil)",
			progress, backlog, err, evidenceSweepMaxStepsPerPass)
	}
	if len(st.calls) != evidenceSweepMaxStepsPerPass {
		t.Fatalf("sweep calls = %d, want %d", len(st.calls), evidenceSweepMaxStepsPerPass)
	}
}

func TestEvidenceSweepPassStopsOnError(t *testing.T) {
	want := errors.New("sweep failed")
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, 2),
		results: []evidenceSweepResult{
			{count: 1}, {err: want},
		},
	}
	progress, backlog, err := runEvidenceSweepPass(t.Context(), st, 24*time.Hour)
	if progress.RunsDeleted != 1 || backlog || !errors.Is(err, want) {
		t.Fatalf("runEvidenceSweepPass = (%+v, %v, %v), want (1 run, false, %v)", progress, backlog, err, want)
	}
	if len(st.calls) != 2 {
		t.Fatalf("sweep calls = %d, want 2", len(st.calls))
	}
}

func TestEvidenceMaintenanceUsesBacklogDelayThenReturnsIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st := &evidenceMaintenanceStore{
		calls: make(chan time.Duration, evidenceSweepMaxStepsPerPass+2),
		results: append(
			make([]evidenceSweepResult, evidenceSweepMaxStepsPerPass),
			evidenceSweepResult{count: 0},
		),
	}
	for i := range evidenceSweepMaxStepsPerPass {
		st.results[i].count = 1
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runEvidenceMaintenance(ctx, st, time.Hour, 10*time.Millisecond, 24*time.Hour)
	}()

	for i := 0; i < evidenceSweepMaxStepsPerPass+1; i++ {
		select {
		case staleAge := <-st.calls:
			if staleAge != 24*time.Hour {
				t.Fatalf("stale staged age = %s, want 24h", staleAge)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d maintenance sweep call(s)", i)
		}
	}
	select {
	case <-st.calls:
		t.Fatal("drained maintenance pass did not return to the idle interval")
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("evidence maintenance did not stop after cancellation")
	}
}

func TestEvidenceMaintenanceIdleBootWaitsForIdleInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st := &evidenceMaintenanceStore{calls: make(chan time.Duration, 2)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runEvidenceMaintenance(ctx, st, time.Hour, 10*time.Millisecond, 24*time.Hour)
	}()
	select {
	case staleAge := <-st.calls:
		if staleAge != 24*time.Hour {
			t.Fatalf("stale staged age = %s, want 24h", staleAge)
		}
	case <-time.After(time.Second):
		t.Fatal("boot evidence sweep did not run")
	}
	select {
	case <-st.calls:
		t.Fatal("idle maintenance retried on the backlog delay")
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("evidence maintenance did not stop after cancellation")
	}
}

func TestEvidenceMaintenanceCanceledBeforeBootDoesNoWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	st := &evidenceMaintenanceStore{calls: make(chan time.Duration, 1)}
	runEvidenceMaintenance(ctx, st, time.Hour, time.Second, 24*time.Hour)
	if len(st.calls) != 0 {
		t.Fatalf("canceled maintenance made %d sweep call(s)", len(st.calls))
	}
}

// T20.11-review blocker regression: the serve path assigns typed service
// pointers into MCP interface options, and a nil typed pointer stored in an
// interface is non-nil — which silently registered the "all-or-none" Caller
// Map annex on every dark deployment. The conversion is now a function, and
// this test drives it with exactly the serve-path expressions in a dark
// configuration: nil services must yield nil interfaces, so the annex's
// registration gate actually fires.
func TestMCPCallerMapServicesPreserveNilness(t *testing.T) {
	var apiOpts api.Options
	apiOpts.ContractCatalog = api.NewContractCatalogService(apiOpts)
	apiOpts.CallerMap = api.NewCallerMapService(apiOpts)
	apiOpts.CallerComparison = api.NewCallerComparisonService(apiOpts)
	if apiOpts.ContractCatalog != nil || apiOpts.CallerMap != nil || apiOpts.CallerComparison != nil {
		t.Fatalf("dark constructors returned non-nil services: %+v", apiOpts)
	}
	catalog, callerMap, comparison := mcpCallerMapServices(
		apiOpts.ContractCatalog, apiOpts.CallerMap, apiOpts.CallerComparison,
	)
	if catalog != nil || callerMap != nil || comparison != nil {
		t.Fatal("typed-nil services leaked into non-nil MCP interfaces; the dark annex gate would never fire")
	}
	// Partial availability must stay partial: a catalog without a Caller
	// Map keeps the annex dark (its gate requires both), and the interface
	// nilness is what that gate reads.
	fixture := &api.ContractCatalogFixture{}
	partial := api.Options{ContractCatalogFixture: fixture, Store: &extractionBackfillStore{}, Principal: func(context.Context) string { return "user:test" }}
	partial.ContractCatalog = api.NewContractCatalogService(partial)
	partial.CallerMap = api.NewCallerMapService(partial)
	if partial.ContractCatalog == nil {
		t.Fatal("fixture-backed catalog service unexpectedly nil")
	}
	gotCatalog, gotCallerMap, _ := mcpCallerMapServices(partial.ContractCatalog, partial.CallerMap, nil)
	if gotCatalog == nil || gotCallerMap != nil {
		t.Fatalf("partial conversion wrong: catalog=%v callerMap=%v", gotCatalog, gotCallerMap)
	}
}

func TestEvidenceExtractorsRemainValidationGated(t *testing.T) {
	if got := evidenceExtractors(false, false, false, false); len(got) != 0 {
		t.Fatalf("default extractor registry = %d entries, want disabled", len(got))
	}
	got := evidenceExtractors(true, false, false, false)
	// T17.2: the registry pins the shape-aware protodecl v3 alongside the
	// T13.1/T13.2 readers behind the proto experimental flag; default runtime
	// activation remains disabled.
	if len(got) != 4 || got[0].Domain() != "proto-contract" ||
		got[1].Domain() != "grpc-consumer" ||
		got[2].Domain() != "scip-proto-field" || got[2].Version() != "1.1.0" ||
		got[3].Domain() != "grpc-caller" || got[3].Version() != "1.3.0" ||
		got[0].Version() != "3.0.0" {
		t.Fatalf("proto-only extractor registry = %#v", got)
	}
	// T19.2/T19.3: the Thrift packs ride their own dark flag and compose with
	// the proto pack without reordering it.
	thriftOnly := evidenceExtractors(false, true, false, false)
	if len(thriftOnly) != 3 || thriftOnly[0].Domain() != "thrift-contract" ||
		thriftOnly[0].Version() != "1.0.0" || thriftOnly[1].Domain() != "thrift-consumer" ||
		thriftOnly[1].Version() != "1.1.0" ||
		thriftOnly[2].Domain() != "thrift-caller" ||
		thriftOnly[2].Version() != "1.3.0" {
		t.Fatalf("thrift-only extractor registry = %#v", thriftOnly)
	}
	thriftFieldOnly := evidenceExtractors(false, false, true, false)
	if len(thriftFieldOnly) != 1 ||
		thriftFieldOnly[0].Domain() != "scip-thrift-field" ||
		thriftFieldOnly[0].Version() != "1.1.0" {
		t.Fatalf("thrift-field-only extractor registry = %#v", thriftFieldOnly)
	}
	// T23.2: both Kafka planes ride one dark flag at 1.0.0 and compose after
	// the existing packs without reordering them.
	kafkaOnly := evidenceExtractors(false, false, false, true)
	if len(kafkaOnly) != 2 || kafkaOnly[0].Domain() != "kafka-producer" ||
		kafkaOnly[0].Version() != "1.1.0" || kafkaOnly[1].Domain() != "kafka-consumer" ||
		kafkaOnly[1].Version() != "1.1.0" {
		t.Fatalf("kafka-only extractor registry = %#v", kafkaOnly)
	}
	withoutKafka := evidenceExtractors(true, true, true, false)
	if len(withoutKafka) != 8 || withoutKafka[0].Domain() != "proto-contract" ||
		withoutKafka[3].Domain() != "grpc-caller" ||
		withoutKafka[4].Domain() != "thrift-contract" ||
		withoutKafka[5].Domain() != "thrift-consumer" ||
		withoutKafka[6].Domain() != "thrift-caller" ||
		withoutKafka[7].Domain() != "scip-thrift-field" {
		t.Fatalf("combined extractor registry = %#v", withoutKafka)
	}
	withoutThriftField := evidenceExtractors(true, true, false, true)
	if len(withoutThriftField) != 9 ||
		withoutThriftField[7].Domain() != "kafka-producer" ||
		withoutThriftField[8].Domain() != "kafka-consumer" {
		t.Fatalf("combined extractor registry without Thrift fields = %#v", withoutThriftField)
	}
	all := evidenceExtractors(true, true, true, true)
	if len(all) != 10 || all[7].Domain() != "scip-thrift-field" ||
		all[8].Domain() != "kafka-producer" ||
		all[9].Domain() != "kafka-consumer" {
		t.Fatalf("all-flags extractor registry = %#v", all)
	}
}
