package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

var _ AuthStore = (*Surreal)(nil)

func userID(id string) models.RecordID        { return models.NewRecordID("user", id) }
func apiKeyID(id string) models.RecordID      { return models.NewRecordID("api_key", id) }
func authSessionID(id string) models.RecordID { return models.NewRecordID("auth_session", id) }

type userRec struct {
	RecID           *models.RecordID `json:"id"`
	Email           string           `json:"email"`
	NormalizedEmail string           `json:"normalized_email"`
	DisplayName     string           `json:"display_name"`
	PasswordHash    string           `json:"password_hash"`
	OIDCIssuer      string           `json:"oidc_issuer"`
	OIDCSubject     string           `json:"oidc_subject"`
	IsAdmin         bool             `json:"is_admin"`
	Disabled        bool             `json:"disabled"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	LastLoginAt     *time.Time       `json:"last_login_at"`
}

func (r userRec) user() User {
	id := ""
	if r.RecID != nil {
		id = r.RecID.ID.(string)
	}
	return User{
		ID: id, Email: r.Email, NormalizedEmail: r.NormalizedEmail,
		DisplayName: r.DisplayName, PasswordHash: r.PasswordHash,
		OIDCIssuer: r.OIDCIssuer, OIDCSubject: r.OIDCSubject,
		IsAdmin: r.IsAdmin, Disabled: r.Disabled, CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt, LastLoginAt: r.LastLoginAt,
	}
}

func firstUser(results *[]surrealdb.QueryResult[[]userRec]) *User {
	for _, result := range *results {
		for _, row := range result.Result {
			if row.RecID == nil || row.RecID.Table != "user" {
				continue
			}
			u := row.user()
			return &u
		}
	}
	return nil
}

func (s *Surreal) AuthStats(ctx context.Context) (AuthStats, error) {
	type counts struct {
		Users         int `json:"users"`
		PasswordUsers int `json:"password_users"`
	}
	results, err := surrealdb.Query[[]counts](ctx, s.db,
		`SELECT count() AS users, count(password_hash != NONE AND password_hash != '') AS password_users FROM user GROUP ALL`, nil)
	if err != nil {
		return AuthStats{}, err
	}
	stats := AuthStats{}
	for _, result := range *results {
		if len(result.Result) > 0 {
			stats.Users = result.Result[0].Users
			stats.PasswordUsers = result.Result[0].PasswordUsers
			break
		}
	}
	guard, err := surrealdb.Query[[]struct {
		RecID *models.RecordID `json:"id"`
	}](ctx, s.db, "SELECT id FROM $guard", map[string]any{
		"guard": models.NewRecordID("auth_setup", "first"),
	})
	if err != nil {
		return AuthStats{}, err
	}
	for _, result := range *guard {
		if len(result.Result) > 0 {
			stats.SetupComplete = true
			break
		}
	}
	return stats, nil
}

const createUserFields = `email = $email, normalized_email = $normalized_email,
 display_name = $display_name,
 password_hash = IF $password_hash = '' THEN NONE ELSE $password_hash END,
 oidc_issuer = IF $oidc_issuer = '' THEN NONE ELSE $oidc_issuer END,
 oidc_subject = IF $oidc_subject = '' THEN NONE ELSE $oidc_subject END,
 is_admin = $is_admin, admin_slot = IF $is_admin THEN 'admin' ELSE NONE END,
 disabled = false,
 created_at = $now, updated_at = $now`

func userVars(user User) map[string]any {
	now := user.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return map[string]any{
		"rid": userID(user.ID), "email": user.Email,
		"normalized_email": user.NormalizedEmail, "display_name": user.DisplayName,
		"password_hash": user.PasswordHash,
		"oidc_issuer":   user.OIDCIssuer, "oidc_subject": user.OIDCSubject,
		"is_admin": user.IsAdmin, "now": now,
	}
}

func (s *Surreal) CreateFirstUser(ctx context.Context, user User) (*User, error) {
	user.IsAdmin = true
	vars := userVars(user)
	vars["guard"] = models.NewRecordID("auth_setup", "first")
	results, err := surrealdb.Query[[]userRec](ctx, s.db,
		`BEGIN;
CREATE ONLY $guard SET completed_at = $now RETURN NONE;
LET $existing = (SELECT id FROM user LIMIT 1);
RETURN IF array::len($existing) = 0 THEN
    (CREATE $rid SET `+createUserFields+` RETURN AFTER)
ELSE [] END;
COMMIT;`, vars)
	if err != nil {
		return nil, wrapAuthConflict(err)
	}
	created := firstUser(results)
	if created == nil {
		return nil, fmt.Errorf("first user already exists: %w", ErrConflict)
	}
	return created, nil
}

func (s *Surreal) CreateUser(ctx context.Context, user User) (*User, error) {
	results, err := surrealdb.Query[[]userRec](ctx, s.db,
		"CREATE $rid SET "+createUserFields+" RETURN AFTER", userVars(user))
	if err != nil {
		return nil, wrapAuthConflict(err)
	}
	created := firstUser(results)
	if created == nil {
		return nil, errors.New("create user returned no row")
	}
	return created, nil
}

func (s *Surreal) GetUserByID(ctx context.Context, id string) (*User, error) {
	results, err := surrealdb.Query[[]userRec](ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": userID(id)})
	if err != nil {
		return nil, err
	}
	u := firstUser(results)
	if u == nil {
		return nil, fmt.Errorf("user %q: %w", id, ErrNotFound)
	}
	return u, nil
}

func (s *Surreal) GetUserByEmail(ctx context.Context, normalizedEmail string) (*User, error) {
	results, err := surrealdb.Query[[]userRec](ctx, s.db,
		"SELECT * FROM user WHERE normalized_email = $email LIMIT 1",
		map[string]any{"email": normalizedEmail})
	if err != nil {
		return nil, err
	}
	u := firstUser(results)
	if u == nil {
		return nil, fmt.Errorf("user email: %w", ErrNotFound)
	}
	return u, nil
}

// UpsertOIDCUser returns an existing issuer/subject identity or creates a new
// user. Email equality alone never links identities: an OIDC login colliding
// with an existing local or OIDC account fails closed.
func (s *Surreal) UpsertOIDCUser(ctx context.Context, issuer, subject, email, normalizedEmail, displayName string, emailVerified bool) (*User, error) {
	if !emailVerified {
		return nil, errors.New("OIDC email is not verified")
	}
	now := time.Now().UTC()
	id, err := authRandomID()
	if err != nil {
		return nil, err
	}
	vars := map[string]any{
		"rid": userID(id), "issuer": issuer, "subject": subject,
		"guard": models.NewRecordID("auth_setup", "first"),
		"email": email, "normalized_email": normalizedEmail,
		"display_name": displayName, "now": now,
	}
	results, err := surrealdb.Query[[]userRec](ctx, s.db,
		`BEGIN;
UPSERT $guard SET completed_at = IF completed_at = NONE THEN $now ELSE completed_at END RETURN NONE;
LET $identity = (SELECT id FROM user WHERE oidc_issuer = $issuer AND oidc_subject = $subject LIMIT 1)[0].id;
LET $by_email = (SELECT id FROM user WHERE normalized_email = $normalized_email LIMIT 1)[0];
LET $has_users = array::len(SELECT id FROM user LIMIT 1) > 0;
RETURN IF $identity != NONE THEN
    (UPDATE $identity SET display_name = IF $display_name != '' THEN $display_name ELSE display_name END,
        updated_at = $now RETURN AFTER)
ELSE IF $by_email.id = NONE THEN
    (CREATE $rid SET email = $email, normalized_email = $normalized_email,
        display_name = $display_name, password_hash = NONE,
        oidc_issuer = $issuer, oidc_subject = $subject,
        is_admin = !$has_users, admin_slot = IF !$has_users THEN 'admin' ELSE NONE END,
        disabled = false, created_at = $now, updated_at = $now
     RETURN AFTER)
ELSE [] END;
COMMIT;`, vars)
	if err != nil {
		return nil, wrapAuthConflict(err)
	}
	u := firstUser(results)
	if u == nil {
		return nil, fmt.Errorf("email belongs to a different OIDC identity: %w", ErrConflict)
	}
	return u, nil
}

func authRandomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate user id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *Surreal) MarkUserLogin(ctx context.Context, id string, at time.Time) error {
	results, err := surrealdb.Query[[]userRec](ctx, s.db,
		"UPDATE $rid SET last_login_at = $at, updated_at = $at RETURN AFTER",
		map[string]any{"rid": userID(id), "at": at})
	if err != nil {
		return err
	}
	if firstUser(results) == nil {
		return fmt.Errorf("user %q: %w", id, ErrNotFound)
	}
	return nil
}

type apiKeyRec struct {
	RecID      *models.RecordID `json:"id"`
	UserID     string           `json:"user_id"`
	Name       string           `json:"name"`
	Prefix     string           `json:"prefix"`
	Hash       string           `json:"hash"`
	CreatedAt  time.Time        `json:"created_at"`
	LastUsedAt *time.Time       `json:"last_used_at"`
	ExpiresAt  *time.Time       `json:"expires_at"`
	RevokedAt  *time.Time       `json:"revoked_at"`
}

func (r apiKeyRec) key() APIKey {
	id := ""
	if r.RecID != nil {
		id = r.RecID.ID.(string)
	}
	return APIKey{ID: id, UserID: r.UserID, Name: r.Name, Prefix: r.Prefix,
		Hash: r.Hash, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt,
		ExpiresAt: r.ExpiresAt, RevokedAt: r.RevokedAt}
}

func firstAPIKey(results *[]surrealdb.QueryResult[[]apiKeyRec]) *APIKey {
	for _, result := range *results {
		if len(result.Result) > 0 {
			key := result.Result[0].key()
			return &key
		}
	}
	return nil
}

func (s *Surreal) CreateAPIKey(ctx context.Context, key APIKey) (*APIKey, error) {
	results, err := surrealdb.Query[[]apiKeyRec](ctx, s.db,
		`CREATE $rid SET user_id = $user_id, name = $name, prefix = $prefix,
            hash = $hash, created_at = $created_at, expires_at = $expires_at RETURN AFTER`,
		map[string]any{"rid": apiKeyID(key.ID), "user_id": key.UserID, "name": key.Name,
			"prefix": key.Prefix, "hash": key.Hash, "created_at": key.CreatedAt,
			"expires_at": key.ExpiresAt})
	if err != nil {
		return nil, wrapAuthConflict(err)
	}
	created := firstAPIKey(results)
	if created == nil {
		return nil, errors.New("create API key returned no row")
	}
	return created, nil
}

func (s *Surreal) GetAPIKey(ctx context.Context, id string) (*APIKey, error) {
	results, err := surrealdb.Query[[]apiKeyRec](ctx, s.db, "SELECT * FROM $rid",
		map[string]any{"rid": apiKeyID(id)})
	if err != nil {
		return nil, err
	}
	key := firstAPIKey(results)
	if key == nil {
		return nil, fmt.Errorf("API key %q: %w", id, ErrNotFound)
	}
	return key, nil
}

func (s *Surreal) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	results, err := surrealdb.Query[[]apiKeyRec](ctx, s.db,
		"SELECT * FROM api_key WHERE user_id = $user_id AND revoked_at = NONE ORDER BY created_at DESC",
		map[string]any{"user_id": userID})
	if err != nil {
		return nil, err
	}
	var keys []APIKey
	for _, result := range *results {
		for _, row := range result.Result {
			keys = append(keys, row.key())
		}
	}
	return keys, nil
}

func (s *Surreal) RevokeAPIKey(ctx context.Context, id, userID string, at time.Time) error {
	results, err := surrealdb.Query[[]apiKeyRec](ctx, s.db,
		"UPDATE $rid SET revoked_at = $at WHERE user_id = $user_id AND revoked_at = NONE RETURN AFTER",
		map[string]any{"rid": apiKeyID(id), "user_id": userID, "at": at})
	if err != nil {
		return err
	}
	if firstAPIKey(results) == nil {
		return fmt.Errorf("API key %q: %w", id, ErrNotFound)
	}
	return nil
}

func (s *Surreal) TouchAPIKey(ctx context.Context, id string, at time.Time) error {
	_, err := surrealdb.Query[any](ctx, s.db, "UPDATE $rid SET last_used_at = $at",
		map[string]any{"rid": apiKeyID(id), "at": at})
	return err
}

const legacyAPIKeyID = "legacy-config"

func (s *Surreal) SetLegacyAPIKey(ctx context.Context, hash string, at time.Time) error {
	if hash == "" {
		_, err := surrealdb.Query[any](ctx, s.db, "DELETE $rid",
			map[string]any{"rid": apiKeyID(legacyAPIKeyID)})
		return err
	}
	_, err := surrealdb.Query[any](ctx, s.db,
		`UPSERT $rid SET user_id = '', name = 'Legacy config key', prefix = 'legacy',
            hash = $hash, created_at = IF created_at = NONE THEN $at ELSE created_at END,
            revoked_at = NONE`,
		map[string]any{"rid": apiKeyID(legacyAPIKeyID), "hash": hash, "at": at})
	return err
}

type authSessionRec struct {
	Data   string    `json:"data"`
	Expiry time.Time `json:"expiry"`
}

func (s *Surreal) CommitAuthSession(ctx context.Context, tokenHash string, data []byte, expiry time.Time) error {
	_, err := surrealdb.Query[any](ctx, s.db,
		"UPSERT $rid SET data = $data, expiry = $expiry",
		map[string]any{"rid": authSessionID(tokenHash), "data": base64.RawStdEncoding.EncodeToString(data), "expiry": expiry})
	return err
}

func (s *Surreal) FindAuthSession(ctx context.Context, tokenHash string, now time.Time) ([]byte, bool, error) {
	results, err := surrealdb.Query[[]authSessionRec](ctx, s.db,
		"SELECT data, expiry FROM $rid WHERE expiry > $now",
		map[string]any{"rid": authSessionID(tokenHash), "now": now})
	if err != nil {
		return nil, false, err
	}
	for _, result := range *results {
		if len(result.Result) == 0 {
			continue
		}
		data, err := base64.RawStdEncoding.DecodeString(result.Result[0].Data)
		if err != nil {
			return nil, false, fmt.Errorf("decode session: %w", err)
		}
		return data, true, nil
	}
	return nil, false, nil
}

func (s *Surreal) DeleteAuthSession(ctx context.Context, tokenHash string) error {
	_, err := surrealdb.Query[any](ctx, s.db, "DELETE $rid",
		map[string]any{"rid": authSessionID(tokenHash)})
	return err
}

func (s *Surreal) DeleteExpiredAuthSessions(ctx context.Context, now time.Time) (int, error) {
	results, err := surrealdb.Query[[]authSessionRec](ctx, s.db,
		"DELETE auth_session WHERE expiry <= $now RETURN BEFORE", map[string]any{"now": now})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, result := range *results {
		n += len(result.Result)
	}
	return n, nil
}

func wrapAuthConflict(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "unique") || strings.Contains(lower, "already exists") ||
		strings.Contains(lower, "already contains") || strings.Contains(lower, "duplicate") {
		return fmt.Errorf("%v: %w", err, ErrConflict)
	}
	return err
}
