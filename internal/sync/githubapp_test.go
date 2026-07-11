package sync

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/store"
)

func testAppKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	path := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return key, path
}

// verifyAppJWT checks structure, claims, and the RS256 signature.
func verifyAppJWT(t *testing.T, jwt string, pub *rsa.PublicKey, wantIss string) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature invalid: %v", err)
	}
	claimJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	if err := json.Unmarshal(claimJSON, &claims); err != nil {
		t.Fatalf("claims: %v", err)
	}
	now := time.Now().Unix()
	if claims.Iss != wantIss || claims.Iat >= now || claims.Exp <= now || claims.Exp-claims.Iat > 600 {
		t.Errorf("claims out of spec: %+v (now %d)", claims, now)
	}
}

// TestSyncGitHubAppMode drives an App-authed connection with no selectors:
// JWT → installation token → paginated /installation/repositories → mirror.
// Each sync run must exchange a fresh token, and /user must never be hit
// (installation tokens have no user identity).
func TestSyncGitHubAppMode(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	allowFileClones(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	key, keyPath := testAppKey(t)

	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "init")

	var tokenExchanges int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/77/access_tokens":
			jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			verifyAppJWT(t, jwt, &key.PublicKey, "12345")
			tokenExchanges++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_inst"}`))
		case r.URL.Path == "/installation/repositories":
			if r.Header.Get("Authorization") != "Bearer ghs_inst" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`{"repositories":[]}`))
				return
			}
			w.Header().Set("Link", `<http://`+r.Host+`/installation/repositories?page=2>; rel="next"`)
			page := struct {
				Repositories []ghRepo `json:"repositories"`
			}{[]ghRepo{{ID: 9, FullName: "granted/repo", CloneURL: "file://" + origin,
				DefaultBranch: "main", Private: true}}}
			_ = json.NewEncoder(w).Encode(page)
		case r.URL.Path == "/user" || r.URL.Path == "/user/repos":
			t.Errorf("app mode must not call %s", r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	old := ghAPIBase
	ghAPIBase = srv.URL
	t.Cleanup(func() { ghAPIBase = old })

	dataDir := t.TempDir()
	st, err := store.OpenLocal(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	conn := config.Connection{Name: "gha", Type: "github",
		App: config.GitHubApp{ID: 12345, InstallationID: 77, PrivateKeyPath: keyPath}}

	for run := 1; run <= 2; run++ {
		names, err := SyncConnection(ctx, st, dataDir, conn)
		if err != nil {
			t.Fatalf("sync run %d: %v", run, err)
		}
		if len(names) != 1 || names[0] != "github.com/granted/repo" {
			t.Fatalf("run %d synced names = %v", run, names)
		}
	}
	// tokens refresh: every run exchanges anew, no stale cache
	if tokenExchanges != 2 {
		t.Errorf("token exchanges = %d, want 2 (one per sync run)", tokenExchanges)
	}

	repo, err := st.GetRepo(ctx, "github.com/granted/repo")
	if err != nil {
		t.Fatal(err)
	}
	if repo.IsPublic || repo.ExternalID != "9" {
		t.Errorf("metadata not persisted, got %+v", repo)
	}
}

func TestSyncGitHubAppUserSelectorUsesInstallationRepos(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	allowFileClones(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, keyPath := testAppKey(t)
	origin := t.TempDir()
	gitc(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "private.go"), []byte("package private\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(t, origin, "add", ".")
	gitc(t, origin, "commit", "-m", "init")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/88/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_inst"}`))
		case r.URL.Path == "/installation/repositories":
			if r.Header.Get("Authorization") != "Bearer ghs_inst" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			page := struct {
				Repositories []ghRepo `json:"repositories"`
			}{[]ghRepo{
				{ID: 10, FullName: "Ben/secret", CloneURL: "file://" + origin, DefaultBranch: "main", Private: true},
				{ID: 11, FullName: "other/repo", CloneURL: "file://" + origin, DefaultBranch: "main", Private: true},
			}}
			_ = json.NewEncoder(w).Encode(page)
		case strings.HasPrefix(r.URL.Path, "/users/") || r.URL.Path == "/user" || r.URL.Path == "/user/repos":
			t.Errorf("App user selector must use installation repositories, got %s", r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	old := ghAPIBase
	ghAPIBase = srv.URL
	t.Cleanup(func() { ghAPIBase = old })
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	conn := config.Connection{Name: "gha", Type: "github", Users: []string{"ben"},
		App: config.GitHubApp{ID: 12345, InstallationID: 88, PrivateKeyPath: keyPath}}
	names, err := SyncConnection(ctx, st, t.TempDir(), conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "github.com/Ben/secret" {
		t.Fatalf("synced names = %v, want selected private repository", names)
	}
}

func TestLoadAppKeyForms(t *testing.T) {
	key, path := testAppKey(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	inline8 := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))

	cases := []struct {
		name    string
		app     config.GitHubApp
		wantErr string
	}{
		{"pkcs1 from file", config.GitHubApp{PrivateKeyPath: path}, ""},
		{"pkcs8 inline", config.GitHubApp{PrivateKey: inline8}, ""},
		{"no pem", config.GitHubApp{PrivateKey: "not a key"}, "no PEM block"},
		{"missing file", config.GitHubApp{PrivateKeyPath: "/nonexistent.pem"}, "no such file"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadAppKey(tt.app)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("loadAppKey: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("loadAppKey err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
