package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

func TestAuthUsersAndOIDCIdentity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	admin, err := s.CreateFirstUser(ctx, store.User{
		ID: "admin", Email: "Admin@example.com", NormalizedEmail: "admin@example.com",
		DisplayName: "Admin", PasswordHash: "$argon2id$redacted", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateFirstUser: %v", err)
	}
	if !admin.IsAdmin || admin.PasswordHash == "" {
		t.Fatalf("first user = %+v, want admin with password hash", admin)
	}
	if _, err := s.CreateFirstUser(ctx, store.User{
		ID: "other", Email: "other@example.com", NormalizedEmail: "other@example.com",
		PasswordHash: "hash", CreatedAt: now,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second CreateFirstUser err = %v, want ErrConflict", err)
	}

	stats, err := s.AuthStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Users != 1 || stats.PasswordUsers != 1 {
		t.Fatalf("AuthStats = %+v, want 1 user/password user", stats)
	}

	if _, err := s.UpsertOIDCUser(ctx, "https://idp.example", "subject-1",
		"Admin@example.com", "admin@example.com", "OIDC Admin", true); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("OIDC email collision err = %v, want ErrConflict", err)
	}
	stillLocal, err := s.GetUserByID(ctx, admin.ID)
	if err != nil || stillLocal.OIDCIssuer != "" || stillLocal.OIDCSubject != "" {
		t.Fatalf("OIDC collision modified local admin: user=%+v err=%v", stillLocal, err)
	}
	if _, err := s.UpsertOIDCUser(ctx, "https://idp.example", "subject-2",
		"Admin@example.com", "admin@example.com", "Attacker", true); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflicting subject err = %v, want ErrConflict", err)
	}
	if _, err := s.UpsertOIDCUser(ctx, "https://idp.example", "subject-3",
		"new@example.com", "new@example.com", "New User", false); err == nil {
		t.Fatal("unverified OIDC email unexpectedly accepted")
	}
	newUser, err := s.UpsertOIDCUser(ctx, "https://idp.example", "subject-3",
		"new@example.com", "new@example.com", "New User", true)
	if err != nil {
		t.Fatalf("create OIDC user: %v", err)
	}
	if newUser.IsAdmin {
		t.Fatal("second user unexpectedly became admin")
	}
	repeated, err := s.UpsertOIDCUser(ctx, "https://idp.example", "subject-3",
		"new@example.com", "new@example.com", "Updated Name", true)
	if err != nil || repeated.ID != newUser.ID {
		t.Fatalf("repeat OIDC identity = %+v, %v; want user %q", repeated, err, newUser.ID)
	}
}

func TestAuthMultipleLocalUsersDoNotCollideOnEmptyOIDCIdentity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := s.CreateFirstUser(ctx, store.User{
		ID: "local-1", Email: "one@example.com", NormalizedEmail: "one@example.com",
		PasswordHash: "hash-1", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateUser(ctx, store.User{
		ID: "local-2", Email: "two@example.com", NormalizedEmail: "two@example.com",
		PasswordHash: "hash-2", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("second local user collided on absent OIDC fields: %v", err)
	}
	if second.IsAdmin || second.OIDCIssuer != "" || second.OIDCSubject != "" {
		t.Fatalf("second local user = %+v", second)
	}
	if _, err := s.CreateUser(ctx, store.User{
		ID: "local-3", Email: "duplicate@example.com", NormalizedEmail: "two@example.com",
		PasswordHash: "hash-3", CreatedAt: now,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate normalized email err = %v, want ErrConflict", err)
	}
}

func TestAuthCreateFirstUserIsAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const contenders = 16
	var wg sync.WaitGroup
	results := make(chan error, contenders)
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.CreateFirstUser(ctx, store.User{
				ID: fmt.Sprintf("candidate-%d", i), Email: fmt.Sprintf("user-%d@example.com", i),
				NormalizedEmail: fmt.Sprintf("user-%d@example.com", i), PasswordHash: "hash",
				CreatedAt: time.Now().UTC(),
			})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	successes := 0
	var failures []string
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures = append(failures, err.Error())
		}
	}
	stats, err := s.AuthStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if successes != 1 || stats.Users != 1 {
		t.Fatalf("concurrent first-user setup: successes=%d stats=%+v failures=%v, want exactly one", successes, stats, failures)
	}
}

func TestAuthOIDCFirstAdminIsAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const contenders = 16
	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			email := fmt.Sprintf("oidc-%d@example.com", i)
			_, _ = s.UpsertOIDCUser(ctx, "https://idp.example", fmt.Sprintf("subject-%d", i),
				email, email, fmt.Sprintf("OIDC %d", i), true)
		}(i)
	}
	wg.Wait()
	users, admins := 0, 0
	for i := range contenders {
		email := fmt.Sprintf("oidc-%d@example.com", i)
		user, err := s.GetUserByEmail(ctx, email)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		users++
		if user.IsAdmin {
			admins++
		}
	}
	if users == 0 || admins != 1 {
		t.Fatalf("concurrent OIDC creation produced users=%d admins=%d, want users>0 and exactly one admin", users, admins)
	}
}

func TestAuthAPIKeysAndSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	key, err := s.CreateAPIKey(ctx, store.APIKey{
		ID: "key1", UserID: "user1", Name: "CI", Prefix: "phebs_key1",
		Hash: "digest-not-plaintext", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if key.Hash != "digest-not-plaintext" {
		t.Fatalf("stored key hash = %q", key.Hash)
	}
	if key.Capabilities == nil || len(key.Capabilities) != 0 {
		t.Fatalf("default key capabilities = %#v, want explicit empty set", key.Capabilities)
	}
	writeKey, err := s.CreateAPIKey(ctx, store.APIKey{
		ID: "key2", UserID: "user1", Name: "Investigation agent",
		Prefix: "phebs_key2", Hash: "second-digest",
		Capabilities: []store.APIKeyCapability{
			store.APIKeyCapabilityInvestigationWrite,
		},
		CreatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("CreateAPIKey(write): %v", err)
	}
	if got := writeKey.Capabilities; len(got) != 1 ||
		got[0] != store.APIKeyCapabilityInvestigationWrite {
		t.Fatalf("write key capabilities = %#v", got)
	}
	for _, test := range []struct {
		name         string
		capabilities []store.APIKeyCapability
	}{
		{"unknown", []store.APIKeyCapability{"repository:admin"}},
		{"future", []store.APIKeyCapability{"investigation:write:v2"}},
		{"malformed", []store.APIKeyCapability{" investigation:write"}},
		{"duplicate", []store.APIKeyCapability{
			store.APIKeyCapabilityInvestigationWrite,
			store.APIKeyCapabilityInvestigationWrite,
		}},
	} {
		t.Run("capability_"+test.name, func(t *testing.T) {
			_, createErr := s.CreateAPIKey(ctx, store.APIKey{
				ID: "invalid-" + test.name, UserID: "user1", Name: test.name,
				Prefix: "invalid", Hash: "invalid",
				Capabilities: test.capabilities, CreatedAt: now,
			})
			if createErr == nil {
				t.Fatal("invalid capability set was persisted")
			}
		})
	}
	keys, err := s.ListAPIKeys(ctx, "user1")
	if err != nil || len(keys) != 2 {
		t.Fatalf("ListAPIKeys = %+v, %v", keys, err)
	}
	if err := s.TouchAPIKey(ctx, "key1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAPIKey(ctx, "key1", "different-user", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-user revoke err = %v, want ErrNotFound", err)
	}
	if err := s.RevokeAPIKey(ctx, "key1", "user1", now); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAPIKey(ctx, "key2", "user1", now); err != nil {
		t.Fatal(err)
	}
	if keys, err := s.ListAPIKeys(ctx, "user1"); err != nil || len(keys) != 0 {
		t.Fatalf("active keys after revoke = %+v, %v", keys, err)
	}

	payload := []byte("encoded-scs-session")
	if err := s.CommitAuthSession(ctx, "token-digest", payload, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.FindAuthSession(ctx, "token-digest", now)
	if err != nil || !found || !bytes.Equal(got, payload) {
		t.Fatalf("FindAuthSession = %q, %v, %v", got, found, err)
	}
	if _, found, err := s.FindAuthSession(ctx, "token-digest", now.Add(2*time.Hour)); err != nil || found {
		t.Fatalf("expired FindAuthSession found=%v err=%v", found, err)
	}
	if n, err := s.DeleteExpiredAuthSessions(ctx, now.Add(2*time.Hour)); err != nil || n != 1 {
		t.Fatalf("DeleteExpiredAuthSessions = %d, %v; want 1", n, err)
	}
}

func TestAuthSessionPersistsAcrossReopen(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx := context.Background()
	dir := t.TempDir()
	expiry := time.Now().UTC().Add(time.Hour)
	s, err := store.OpenLocal(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitAuthSession(ctx, "persisted-token-digest", []byte("persisted"), expiry); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s, err = store.OpenLocal(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	data, found, err := s.FindAuthSession(ctx, "persisted-token-digest", time.Now().UTC())
	if err != nil || !found || string(data) != "persisted" {
		t.Fatalf("persisted session = %q, %v, %v", data, found, err)
	}
}
