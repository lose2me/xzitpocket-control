package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func (s *Store) ListOAuthClients(ctx context.Context) ([]OAuthClient, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT client_id, client_name, secret_hash, redirect_uris_json, scopes_json, status, created_at, updated_at
		FROM oauth_clients ORDER BY client_name, client_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OAuthClient
	for rows.Next() {
		var c OAuthClient
		var created, updated int64
		if err := rows.Scan(&c.ClientID, &c.ClientName, &c.SecretHash, &c.RedirectURIs, &c.Scopes, &c.Status, &created, &updated); err != nil {
			return nil, err
		}
		c.CreatedAt, c.UpdatedAt = fromMillis(created), fromMillis(updated)
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) GetOAuthClient(ctx context.Context, clientID string) (OAuthClient, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT client_id, client_name, secret_hash, redirect_uris_json, scopes_json, status, created_at, updated_at
		FROM oauth_clients WHERE client_id = ?`, clientID)
	var c OAuthClient
	var created, updated int64
	if err := row.Scan(&c.ClientID, &c.ClientName, &c.SecretHash, &c.RedirectURIs, &c.Scopes, &c.Status, &created, &updated); err != nil {
		return OAuthClient{}, err
	}
	c.CreatedAt, c.UpdatedAt = fromMillis(created), fromMillis(updated)
	return c, nil
}

func (s *Store) UpsertOAuthClient(ctx context.Context, c OAuthClient) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_clients(client_id, client_name, secret_hash, redirect_uris_json, scopes_json, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET client_name=excluded.client_name, secret_hash=excluded.secret_hash,
		redirect_uris_json=excluded.redirect_uris_json, scopes_json=excluded.scopes_json, status=excluded.status, updated_at=excluded.updated_at`,
		c.ClientID, c.ClientName, nullableString(c.SecretHash.String), c.RedirectURIs, c.Scopes, c.Status, millis(c.CreatedAt), millis(c.UpdatedAt))
	return err
}

func (s *Store) DeleteOAuthClient(ctx context.Context, clientID string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM oauth_clients WHERE client_id = ?", clientID)
	return err
}

func (s *Store) CreateOAuthCode(ctx context.Context, code OAuthCode) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_codes(code_hash, client_id, user_id, device_id, redirect_uri, scope, code_challenge, code_challenge_method, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, code.CodeHash, code.ClientID, code.UserID, nullableString(code.DeviceID.String), code.RedirectURI,
		code.Scope, nullableString(code.CodeChallenge.String), nullableString(code.CodeChallengeMethod.String), millis(code.ExpiresAt))
	return err
}

func (s *Store) ConsumeOAuthCode(ctx context.Context, codeHash, clientID, redirectURI string, now time.Time) (OAuthCode, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return OAuthCode{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var code OAuthCode
	var deviceID, challenge, method sql.NullString
	var expires int64
	row := tx.QueryRowContext(ctx, `SELECT code_hash, client_id, user_id, device_id, redirect_uri, scope, code_challenge, code_challenge_method, expires_at, used_at
		FROM oauth_codes WHERE code_hash = ? AND client_id = ? AND redirect_uri = ?`, codeHash, clientID, redirectURI)
	var used sql.NullInt64
	if err := row.Scan(&code.CodeHash, &code.ClientID, &code.UserID, &deviceID, &code.RedirectURI, &code.Scope, &challenge, &method, &expires, &used); err != nil {
		return OAuthCode{}, err
	}
	code.DeviceID, code.CodeChallenge, code.CodeChallengeMethod = deviceID, challenge, method
	code.ExpiresAt, code.UsedAt = fromMillis(expires), nullableTime(used)
	if code.UsedAt != nil || !code.ExpiresAt.After(now) {
		return OAuthCode{}, sql.ErrNoRows
	}
	result, err := tx.ExecContext(ctx, "UPDATE oauth_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?", millis(now), codeHash, millis(now))
	if err != nil {
		return OAuthCode{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		if err != nil {
			return OAuthCode{}, err
		}
		return OAuthCode{}, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return OAuthCode{}, err
	}
	return code, nil
}

func (s *Store) CreateOAuthToken(ctx context.Context, token OAuthToken) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_tokens(token_hash, token_type, client_id, user_id, device_id, scope, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, token.TokenHash, token.TokenType, token.ClientID, token.UserID, nullableString(token.DeviceID.String), token.Scope, millis(token.ExpiresAt), millis(token.CreatedAt))
	return err
}

func (s *Store) GetOAuthToken(ctx context.Context, tokenHash string) (OAuthToken, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT token_hash, token_type, client_id, user_id, device_id, scope, expires_at, created_at, revoked_at
		FROM oauth_tokens WHERE token_hash = ?`, tokenHash)
	var token OAuthToken
	var deviceID sql.NullString
	var expires, created int64
	var revoked sql.NullInt64
	if err := row.Scan(&token.TokenHash, &token.TokenType, &token.ClientID, &token.UserID, &deviceID, &token.Scope, &expires, &created, &revoked); err != nil {
		return OAuthToken{}, err
	}
	token.DeviceID, token.ExpiresAt, token.CreatedAt, token.RevokedAt = deviceID, fromMillis(expires), fromMillis(created), nullableTime(revoked)
	return token, nil
}

func (s *Store) RevokeOAuthToken(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE oauth_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL", millis(now), tokenHash)
	return err
}

func (s *Store) ListOAuthProviders(ctx context.Context) ([]OAuthProvider, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, authorization_url, token_url, userinfo_url, client_id, secret_ciphertext, scopes_json, status, created_at, updated_at
		FROM oauth_providers ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OAuthProvider
	for rows.Next() {
		var p OAuthProvider
		var created, updated int64
		if err := rows.Scan(&p.ID, &p.Name, &p.AuthorizationURL, &p.TokenURL, &p.UserinfoURL, &p.ClientID, &p.SecretCiphertext, &p.Scopes, &p.Status, &created, &updated); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = fromMillis(created), fromMillis(updated)
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) GetOAuthProvider(ctx context.Context, id string) (OAuthProvider, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, authorization_url, token_url, userinfo_url, client_id, secret_ciphertext, scopes_json, status, created_at, updated_at
		FROM oauth_providers WHERE id = ?`, id)
	var p OAuthProvider
	var created, updated int64
	if err := row.Scan(&p.ID, &p.Name, &p.AuthorizationURL, &p.TokenURL, &p.UserinfoURL, &p.ClientID, &p.SecretCiphertext, &p.Scopes, &p.Status, &created, &updated); err != nil {
		return OAuthProvider{}, err
	}
	p.CreatedAt, p.UpdatedAt = fromMillis(created), fromMillis(updated)
	return p, nil
}

func (s *Store) UpsertOAuthProvider(ctx context.Context, p OAuthProvider) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_providers(id, name, authorization_url, token_url, userinfo_url, client_id, secret_ciphertext, scopes_json, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, authorization_url=excluded.authorization_url, token_url=excluded.token_url,
		userinfo_url=excluded.userinfo_url, client_id=excluded.client_id, secret_ciphertext=excluded.secret_ciphertext, scopes_json=excluded.scopes_json,
		status=excluded.status, updated_at=excluded.updated_at`, p.ID, p.Name, p.AuthorizationURL, p.TokenURL, nullableString(p.UserinfoURL.String), p.ClientID,
		p.SecretCiphertext, p.Scopes, p.Status, millis(p.CreatedAt), millis(p.UpdatedAt))
	return err
}

func (s *Store) ListOAuthAccounts(ctx context.Context, userID string) ([]OAuthAccount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, provider_id, external_subject, display_name, access_ciphertext, refresh_ciphertext, expires_at, scope, created_at, updated_at
		FROM oauth_accounts WHERE user_id = ? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []OAuthAccount
	for rows.Next() {
		var a OAuthAccount
		var expires, created, updated sql.NullInt64
		if err := rows.Scan(&a.ID, &a.UserID, &a.ProviderID, &a.ExternalSubject, &a.DisplayName, &a.AccessCiphertext, &a.RefreshCiphertext, &expires, &a.Scope, &created, &updated); err != nil {
			return nil, err
		}
		a.ExpiresAt = expires
		if created.Valid {
			a.CreatedAt = fromMillis(created.Int64)
		}
		if updated.Valid {
			a.UpdatedAt = fromMillis(updated.Int64)
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Store) UpsertOAuthAccount(ctx context.Context, a OAuthAccount) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_accounts(id, user_id, provider_id, external_subject, display_name, access_ciphertext, refresh_ciphertext, expires_at, scope, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_id, external_subject) DO UPDATE SET user_id=excluded.user_id, display_name=excluded.display_name,
		access_ciphertext=excluded.access_ciphertext, refresh_ciphertext=excluded.refresh_ciphertext, expires_at=excluded.expires_at, scope=excluded.scope, updated_at=excluded.updated_at`,
		a.ID, a.UserID, a.ProviderID, a.ExternalSubject, a.DisplayName, a.AccessCiphertext, nullableString(a.RefreshCiphertext.String), nullInt64(a.ExpiresAt), a.Scope, millis(a.CreatedAt), millis(a.UpdatedAt))
	return err
}

func (s *Store) DeleteOAuthAccount(ctx context.Context, userID, providerID string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM oauth_accounts WHERE user_id = ? AND provider_id = ?", userID, providerID)
	return err
}

func (s *Store) CreateOAuthTransaction(ctx context.Context, tx OAuthTransaction) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO oauth_transactions(id, user_id, device_id, provider_id, state_hash, code_verifier_ciphertext, redirect_uri, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, tx.ID, tx.UserID, nullableString(tx.DeviceID.String), tx.ProviderID, tx.StateHash, tx.CodeVerifierCiphertext, tx.RedirectURI, millis(tx.ExpiresAt))
	return err
}

func (s *Store) ConsumeOAuthTransaction(ctx context.Context, stateHash string, now time.Time) (OAuthTransaction, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return OAuthTransaction{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var out OAuthTransaction
	var deviceID sql.NullString
	var expires int64
	var used sql.NullInt64
	row := tx.QueryRowContext(ctx, `SELECT id, user_id, device_id, provider_id, state_hash, code_verifier_ciphertext, redirect_uri, expires_at, used_at
		FROM oauth_transactions WHERE state_hash = ?`, stateHash)
	if err := row.Scan(&out.ID, &out.UserID, &deviceID, &out.ProviderID, &out.StateHash, &out.CodeVerifierCiphertext, &out.RedirectURI, &expires, &used); err != nil {
		return OAuthTransaction{}, err
	}
	out.DeviceID, out.ExpiresAt, out.UsedAt = deviceID, fromMillis(expires), nullableTime(used)
	if out.UsedAt != nil || !out.ExpiresAt.After(now) {
		return OAuthTransaction{}, sql.ErrNoRows
	}
	res, err := tx.ExecContext(ctx, "UPDATE oauth_transactions SET used_at = ? WHERE state_hash = ? AND used_at IS NULL AND expires_at > ?", millis(now), stateHash, millis(now))
	if err != nil {
		return OAuthTransaction{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return OAuthTransaction{}, err
	}
	if affected != 1 {
		return OAuthTransaction{}, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return OAuthTransaction{}, err
	}
	return out, nil
}

func (s *Store) ListServiceClients(ctx context.Context) ([]ServiceClient, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, audience, secret_ciphertext, scopes_json, status, created_at, updated_at FROM service_clients ORDER BY audience, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ServiceClient
	for rows.Next() {
		var c ServiceClient
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.Audience, &c.SecretCiphertext, &c.Scopes, &c.Status, &created, &updated); err != nil {
			return nil, err
		}
		c.CreatedAt, c.UpdatedAt = fromMillis(created), fromMillis(updated)
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) GetServiceClientByAudience(ctx context.Context, audience string) (ServiceClient, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, audience, secret_ciphertext, scopes_json, status, created_at, updated_at FROM service_clients WHERE audience = ?`, audience)
	var c ServiceClient
	var created, updated int64
	if err := row.Scan(&c.ID, &c.Audience, &c.SecretCiphertext, &c.Scopes, &c.Status, &created, &updated); err != nil {
		return ServiceClient{}, err
	}
	c.CreatedAt, c.UpdatedAt = fromMillis(created), fromMillis(updated)
	return c, nil
}

func (s *Store) UpsertServiceClient(ctx context.Context, c ServiceClient) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO service_clients(id, audience, secret_ciphertext, scopes_json, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(audience) DO UPDATE SET id=excluded.id, secret_ciphertext=excluded.secret_ciphertext, scopes_json=excluded.scopes_json, status=excluded.status, updated_at=excluded.updated_at`,
		c.ID, c.Audience, nullableString(c.SecretCiphertext.String), c.Scopes, c.Status, millis(c.CreatedAt), millis(c.UpdatedAt))
	return err
}

func nullInt64(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

func encodeJSON(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(b)
}
