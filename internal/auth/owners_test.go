package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

type ownerAuthStore struct {
	*memoryAuthStore
	boundary string
	enabled  atomic.Bool
	entered  chan struct{}
	release  chan struct{}
}

func (state *ownerAuthStore) block(ctx context.Context, boundary string) error {
	if state.boundary != boundary || !state.enabled.CompareAndSwap(true, false) {
		return nil
	}
	close(state.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-state.release:
		return nil
	}
}

func (state *ownerAuthStore) TouchAPIKey(ctx context.Context, id string, at time.Time) error {
	if err := state.block(ctx, "auth_touch"); err != nil {
		return err
	}
	return state.memoryAuthStore.TouchAPIKey(ctx, id, at)
}

func (state *ownerAuthStore) CommitAuthSession(ctx context.Context, token string, data []byte, expiry time.Time) error {
	if err := state.block(ctx, "session_commit"); err != nil {
		return err
	}
	return state.memoryAuthStore.CommitAuthSession(ctx, token, data, expiry)
}

func (state *ownerAuthStore) DeleteExpiredAuthSessions(ctx context.Context, at time.Time) (int, error) {
	if err := state.block(ctx, "expiration"); err != nil {
		return 0, err
	}
	return state.memoryAuthStore.DeleteExpiredAuthSessions(ctx, at)
}

func TestAuthOwnersRequestIncludesAuthenticationAndSessionTail(t *testing.T) {
	for _, boundary := range []string{"auth_touch", "session_commit"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
			if err != nil {
				t.Fatal(err)
			}
			state := &ownerAuthStore{memoryAuthStore: newMemoryAuthStore(), boundary: boundary,
				entered: make(chan struct{}), release: make(chan struct{})}
			service, err := New(ctx, Options{Store: state, Owners: owners,
				Config: config.Auth{APIKey: "owner-test-credential", CookieSecure: insecureCookieConfig()}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cancel(); service.WaitCleanup() })
			state.enabled.Store(true)
			handler := service.LoadAndSave(service.Require(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if boundary == "session_commit" {
					service.sessions.Put(request.Context(), "owner-test", "changed")
				}
				writer.WriteHeader(http.StatusNoContent)
			})))
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/owner-test", nil)
			request.Header.Set("Authorization", "Bearer owner-test-credential")
			done := make(chan struct{})
			go func() {
				defer close(done)
				turn, err := owners.EnterRequest(ctx)
				if err != nil {
					return
				}
				defer turn.End()
				handler.ServeHTTP(response, request)
			}()
			t.Cleanup(func() { cancel(); <-done })
			authOwnerSignal(t, ctx, state.entered)
			fenced := make(chan error, 1)
			go func() { fenced <- owners.FenceRequests(ctx) }()
			// The actual auth/session store operation remains blocked.
			select {
			case err := <-fenced:
				t.Fatalf("request fence skipped %s: %v", boundary, err)
			case <-time.After(5 * time.Millisecond):
			}
			close(state.release)
			authOwnerSignal(t, ctx, done)
			select {
			case err := <-fenced:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("request tail did not drain")
			}
			if response.Code != http.StatusNoContent {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAuthOwnersExpirationDrainAndJoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	owners, err := dispatchadmission.NewOwners(ctx, dispatchadmission.OwnerLimits{Owners: 1, Requests: 1})
	if err != nil {
		t.Fatal(err)
	}
	state := &ownerAuthStore{memoryAuthStore: newMemoryAuthStore(), boundary: "expiration",
		entered: make(chan struct{}), release: make(chan struct{})}
	service, err := New(ctx, Options{Store: state, Owners: owners})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); service.WaitCleanup() })
	if service.cleanupDone == nil {
		t.Fatal("exact expiration loop has no joined lifetime")
	}
	state.enabled.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); service.expireSessions(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	authOwnerSignal(t, ctx, state.entered)
	paused := make(chan error, 1)
	go func() { paused <- owners.Pause(ctx) }()
	select {
	case err := <-paused:
		t.Fatalf("pause skipped session expiration: %v", err)
	case <-time.After(5 * time.Millisecond):
	}
	close(state.release)
	authOwnerSignal(t, ctx, done)
	select {
	case err := <-paused:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("expiration turn did not drain")
	}
	cancel()
	service.WaitCleanup()
	if owners.Err() != nil {
		t.Fatal("ordinary shutdown latched an owner failure")
	}
}

func authOwnerSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal("auth owner boundary did not arrive")
	}
}
