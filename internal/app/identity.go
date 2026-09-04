package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"net/http"
	"strings"
	"time"

	controlcrypto "xzitpocket-control/internal/crypto"
	"xzitpocket-control/internal/store/sqlite"
)

const identityProvider = "xzit-oa"

type RegisterDeviceInput struct {
	InstallationID string `json:"installation_id"`
	PublicKey      string `json:"public_key"`
	Platform       string `json:"platform"`
	AppVersion     string `json:"app_version"`
	CreatedAt      string `json:"created_at"`
}

type RegisterDeviceOutput struct {
	DeviceSerial string `json:"device_serial"`
	DeviceToken  string `json:"device_token"`
	DeviceID     string `json:"device_id"`
}

func (a *App) RegisterDevice(ctx context.Context, in RegisterDeviceInput, signature string) (RegisterDeviceOutput, error) {
	in.InstallationID = strings.TrimSpace(in.InstallationID)
	in.PublicKey = strings.TrimSpace(in.PublicKey)
	in.Platform = strings.TrimSpace(in.Platform)
	in.AppVersion = strings.TrimSpace(in.AppVersion)
	if in.InstallationID == "" || len(in.InstallationID) > 128 || in.PublicKey == "" || len(in.PublicKey) > 4096 {
		return RegisterDeviceOutput{}, Err("invalid_device_request", "设备注册参数无效", http.StatusBadRequest)
	}
	if in.Platform == "" || len(in.Platform) > 32 || len(in.AppVersion) > 64 {
		return RegisterDeviceOutput{}, Err("invalid_device_request", "设备注册参数无效", http.StatusBadRequest)
	}
	createdAt, err := time.Parse(time.RFC3339, in.CreatedAt)
	if err != nil || absDuration(time.Since(createdAt)) > 10*time.Minute {
		return RegisterDeviceOutput{}, Err("invalid_timestamp", "created_at 时间无效", http.StatusBadRequest)
	}
	pub, err := controlcrypto.ParseP256PublicKey(in.PublicKey)
	if err != nil {
		return RegisterDeviceOutput{}, Err("invalid_public_key", "设备公钥无效", http.StatusBadRequest)
	}
	message, err := controlcrypto.CanonicalLines("xzitpocket-control-device", in.InstallationID, in.PublicKey, in.Platform, in.AppVersion, in.CreatedAt)
	if err != nil || !controlcrypto.VerifyP256Signature(pub, message, signature) {
		return RegisterDeviceOutput{}, Err("invalid_device_signature", "设备签名无效", http.StatusUnauthorized)
	}
	if existing, err := a.Store.GetDeviceByInstallation(ctx, in.InstallationID); err == nil {
		if existing.PublicKey == in.PublicKey && existing.RevokedAt == nil {
			return RegisterDeviceOutput{DeviceID: existing.ID, DeviceSerial: existing.DeviceSerial}, Err("device_already_registered", "该安装已注册，请使用已保存的设备令牌", http.StatusConflict)
		}
		return RegisterDeviceOutput{}, Err("installation_exists", "installation_id 已注册", http.StatusConflict)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return RegisterDeviceOutput{}, err
	}
	deviceID, err := controlcrypto.NewID("dev")
	if err != nil {
		return RegisterDeviceOutput{}, err
	}
	serial, err := newDeviceSerial()
	if err != nil {
		return RegisterDeviceOutput{}, err
	}
	token, err := controlcrypto.NewToken(32)
	if err != nil {
		return RegisterDeviceOutput{}, err
	}
	now := time.Now().UTC()
	device := sqlite.Device{ID: deviceID, DeviceSerial: serial, Installation: in.InstallationID, TokenHash: controlcrypto.HashToken(a.Cfg.TokenPepper, token), PublicKey: in.PublicKey, Platform: in.Platform, AppVersion: in.AppVersion, CreatedAt: now, LastSeenAt: now}
	if err := a.Store.CreateDevice(ctx, device); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return RegisterDeviceOutput{}, Err("installation_exists", "installation_id 已注册", http.StatusConflict)
		}
		return RegisterDeviceOutput{}, err
	}
	return RegisterDeviceOutput{DeviceID: deviceID, DeviceSerial: serial, DeviceToken: token}, nil
}

func newDeviceSerial() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "dev_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])), nil
}

type ChallengeOutput struct {
	ChallengeID string `json:"challenge_id"`
	Challenge   string `json:"challenge"`
	ExpiresAt   string `json:"expires_at"`
}

func (a *App) CreateChallenge(ctx context.Context, principal DevicePrincipal) (ChallengeOutput, error) {
	id, err := controlcrypto.NewID("ch")
	if err != nil {
		return ChallengeOutput{}, err
	}
	challenge, err := controlcrypto.NewToken(32)
	if err != nil {
		return ChallengeOutput{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(5 * time.Minute)
	if err := a.Store.CreateChallenge(ctx, sqlite.Challenge{ID: id, DeviceID: principal.Device.ID, Challenge: challenge, ChallengeHash: controlcrypto.HashToken(a.Cfg.TokenPepper, challenge), ExpiresAt: expires}); err != nil {
		return ChallengeOutput{}, err
	}
	return ChallengeOutput{ChallengeID: id, Challenge: challenge, ExpiresAt: expires.Format(time.RFC3339)}, nil
}

type AssertionInput struct {
	ChallengeID  string `json:"challenge_id"`
	Challenge    string `json:"challenge"`
	DeviceSerial string `json:"device_serial"`
	StudentID    string `json:"student_id"`
	StudentAlias string `json:"student_alias"`
	DisplayName  string `json:"display_name"`
	AssertedAt   string `json:"asserted_at"`
}

type SessionOutput struct {
	User             UserView   `json:"user"`
	Device           DeviceView `json:"device"`
	AccessToken      string     `json:"access_token"`
	RefreshToken     string     `json:"refresh_token"`
	ExpiresAt        string     `json:"expires_at"`
	RefreshExpiresAt string     `json:"refresh_expires_at"`
}

func (a *App) AssertLogin(ctx context.Context, principal DevicePrincipal, in AssertionInput, signature, installationID, signedAt string, sourceHashes ...string) (SessionOutput, error) {
	sourceHash := ""
	if len(sourceHashes) > 0 {
		sourceHash = sourceHashes[0]
	}
	device := principal.Device
	if installationID == "" || installationID != device.Installation {
		return SessionOutput{}, Err("installation_mismatch", "安装标识不匹配", http.StatusUnauthorized)
	}
	if in.DeviceSerial != device.DeviceSerial {
		return SessionOutput{}, Err("device_mismatch", "设备码不匹配", http.StatusUnauthorized)
	}
	if err := controlcrypto.ValidateStudentID(in.StudentID); err != nil {
		return SessionOutput{}, Err("invalid_student_id", "学号格式无效", http.StatusBadRequest)
	}
	if len(in.DisplayName) > 128 {
		return SessionOutput{}, Err("invalid_display_name", "姓名过长", http.StatusBadRequest)
	}
	assertedAt, err := time.Parse(time.RFC3339, in.AssertedAt)
	if err != nil || absDuration(time.Since(assertedAt)) > 10*time.Minute {
		return SessionOutput{}, Err("invalid_timestamp", "asserted_at 时间无效", http.StatusBadRequest)
	}
	if signedAt == "" {
		return SessionOutput{}, Err("signature_required", "需要设备签名时间", http.StatusUnauthorized)
	}
	if t, parseErr := time.Parse(time.RFC3339, signedAt); parseErr != nil || absDuration(time.Since(t)) > 10*time.Minute {
		return SessionOutput{}, Err("invalid_timestamp", "设备签名时间无效", http.StatusBadRequest)
	}
	pub, err := controlcrypto.ParseP256PublicKey(device.PublicKey)
	if err != nil {
		return SessionOutput{}, err
	}
	message, err := controlcrypto.CanonicalLines("xzitpocket-control-login", in.ChallengeID, in.Challenge, in.DeviceSerial, in.StudentID, in.StudentAlias, in.DisplayName, in.AssertedAt)
	if err != nil || !controlcrypto.VerifyP256Signature(pub, message, signature) {
		a.recordLoginAttemptWithSource(ctx, "", device.ID, "signature_invalid", sourceHash)
		return SessionOutput{}, Err("invalid_device_signature", "设备签名无效", http.StatusUnauthorized)
	}
	expectedAlias := controlcrypto.StudentAlias(in.StudentID)
	if in.StudentAlias != expectedAlias {
		a.recordLoginAttemptWithSource(ctx, "", device.ID, "alias_mismatch", sourceHash)
		return SessionOutput{}, Err("invalid_student_alias", "student_alias 不匹配", http.StatusBadRequest)
	}
	ok, err := a.Store.ConsumeChallenge(ctx, in.ChallengeID, device.ID, controlcrypto.HashToken(a.Cfg.TokenPepper, in.Challenge), time.Now().UTC())
	if err != nil {
		return SessionOutput{}, err
	}
	if !ok {
		a.recordLoginAttemptWithSource(ctx, "", device.ID, "challenge_invalid", sourceHash)
		return SessionOutput{}, Err("challenge_expired", "登录挑战无效或已使用", http.StatusUnauthorized)
	}

	studentHash := controlcrypto.HMACHex(a.Cfg.IDPepper, in.StudentID)
	identity, err := a.Store.GetIdentityByHash(ctx, identityProvider, studentHash)
	if errors.Is(err, sql.ErrNoRows) {
		userID, idErr := controlcrypto.NewID("usr")
		if idErr != nil {
			return SessionOutput{}, idErr
		}
		now := time.Now().UTC()
		identityID, idErr := controlcrypto.NewID("idn")
		if idErr != nil {
			return SessionOutput{}, idErr
		}
		ciphertext, idErr := controlcrypto.Encrypt(a.Cfg.EncryptionKey, in.StudentID)
		if idErr != nil {
			return SessionOutput{}, idErr
		}
		identity = sqlite.Identity{ID: identityID, UserID: userID, Provider: identityProvider, StudentIDHash: studentHash, StudentAlias: in.StudentAlias, StudentIDCiphertext: ciphertext, CreatedAt: now, UpdatedAt: now}
		identity, err = a.Store.FindOrCreateIdentity(ctx, sqlite.User{ID: userID, Status: "active", DisplayName: in.DisplayName, CreatedAt: now, LastLoginAt: now}, identity)
		if err != nil {
			return SessionOutput{}, err
		}
	} else if err != nil {
		return SessionOutput{}, err
	}
	user, err := a.Store.GetUser(ctx, identity.UserID)
	if err != nil {
		return SessionOutput{}, err
	}
	if user.Status != "active" {
		a.recordLoginAttemptWithSource(ctx, user.ID, device.ID, "user_unavailable", sourceHash)
		return SessionOutput{}, Err("user_unavailable", "用户不可用", http.StatusForbidden)
	}
	now := time.Now().UTC()
	if err := a.Store.UpdateUserLogin(ctx, user.ID, in.DisplayName, now); err != nil {
		return SessionOutput{}, err
	}
	if err := a.Store.BindUserDevice(ctx, user.ID, device.ID, now); err != nil {
		return SessionOutput{}, err
	}
	// A device can switch accounts; invalidate only its previous account sessions.
	if err := a.Store.RevokeDeviceSessionsExcept(ctx, device.ID, user.ID, now); err != nil {
		a.Logger.Warn("revoke previous device sessions failed", "error", err)
	}
	access, err := controlcrypto.NewToken(32)
	if err != nil {
		return SessionOutput{}, err
	}
	refresh, err := controlcrypto.NewToken(48)
	if err != nil {
		return SessionOutput{}, err
	}
	sid, err := controlcrypto.NewID("ses")
	if err != nil {
		return SessionOutput{}, err
	}
	accessExpiry, refreshExpiry := now.Add(15*time.Minute), now.Add(30*24*time.Hour)
	if err := a.Store.CreateSession(ctx, sqlite.Session{ID: sid, UserID: user.ID, DeviceID: device.ID, AccessHash: controlcrypto.HashToken(a.Cfg.TokenPepper, access), RefreshHash: controlcrypto.HashToken(a.Cfg.TokenPepper, refresh), ExpiresAt: accessExpiry, RefreshExpires: refreshExpiry, CreatedAt: now, LastUsedAt: now}); err != nil {
		return SessionOutput{}, err
	}
	a.recordLoginAttemptWithSource(ctx, user.ID, device.ID, "success", sourceHash)
	a.recordRiskForDevices(ctx, user.ID, device.ID)
	user.LastLoginAt = now
	if in.DisplayName != "" {
		user.DisplayName = in.DisplayName
	}
	return SessionOutput{User: toUserView(user, identity.StudentAlias), Device: toDeviceView(device), AccessToken: access, RefreshToken: refresh, ExpiresAt: accessExpiry.Format(time.RFC3339), RefreshExpiresAt: refreshExpiry.Format(time.RFC3339)}, nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (a *App) ObserveLoginAttempt(ctx context.Context, deviceID, sourceHash, reason string) {
	a.recordLoginAttemptWithSource(ctx, "", deviceID, reason, sourceHash)
}

func (a *App) recordLoginAttemptWithSource(ctx context.Context, userID, deviceID, reason, sourceHash string) {
	now := time.Now().UTC()
	detailValues := map[string]string{"reason": reason}
	if sourceHash != "" {
		detailValues["source_ip_hash"] = sourceHash
	}
	detail := controlcrypto.JSON(detailValues)
	if err := a.Store.AddAudit(ctx, userID, "login_attempt", "device", deviceID, detail, now); err != nil {
		a.Logger.Warn("record login attempt failed", "error", err)
	}
	count, err := a.Store.CountLoginAttempts(ctx, userID, deviceID, now.Add(-a.Cfg.RiskLoginWindow))
	if err != nil {
		return
	}
	if count >= a.Cfg.RiskLoginCount {
		id, idErr := controlcrypto.NewID("risk")
		if idErr != nil {
			return
		}
		_ = a.Store.CreateRiskEvent(ctx, sqlite.RiskEvent{ID: id, Type: "login_attempt_burst", UserID: userID, DeviceID: deviceID, ObservedCount: count, WindowStart: now.Add(-a.Cfg.RiskLoginWindow), WindowEnd: now, Detail: detail, CreatedAt: now})
	}
}

func (a *App) recordRiskForDevices(ctx context.Context, userID, deviceID string) {
	now := time.Now().UTC()
	count, err := a.Store.CountUserDevices(ctx, userID)
	if err != nil || count <= a.Cfg.RiskDeviceCount {
		return
	}
	id, err := controlcrypto.NewID("risk")
	if err != nil {
		return
	}
	detail := controlcrypto.JSON(map[string]any{"device_count": count, "threshold": a.Cfg.RiskDeviceCount})
	_ = a.Store.CreateRiskEvent(ctx, sqlite.RiskEvent{ID: id, Type: "account_device_burst", UserID: userID, DeviceID: deviceID, ObservedCount: count, WindowStart: now, WindowEnd: now, Detail: detail, CreatedAt: now})
}

func (a *App) RefreshSession(ctx context.Context, refreshToken, signature, installationID, signedAt string) (SessionOutput, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return SessionOutput{}, Err("invalid_refresh_token", "刷新令牌无效", http.StatusUnauthorized)
	}
	hash := controlcrypto.HashToken(a.Cfg.TokenPepper, refreshToken)
	session, err := a.Store.GetSessionByRefreshHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionOutput{}, Err("invalid_refresh_token", "刷新令牌无效", http.StatusUnauthorized)
	}
	if err != nil {
		return SessionOutput{}, err
	}
	now := time.Now().UTC()
	if session.RevokedAt != nil || !session.RefreshExpires.After(now) {
		return SessionOutput{}, Err("refresh_token_expired", "刷新令牌已过期", http.StatusUnauthorized)
	}
	device, err := a.Store.GetDeviceByID(ctx, session.DeviceID)
	if err != nil {
		return SessionOutput{}, err
	}
	if installationID == "" || installationID != device.Installation {
		return SessionOutput{}, Err("installation_mismatch", "安装标识不匹配", http.StatusUnauthorized)
	}
	_ = a.Store.TouchDevice(ctx, device.ID, now)
	if signedAt == "" {
		return SessionOutput{}, Err("signature_required", "需要设备签名", http.StatusUnauthorized)
	}
	t, err := time.Parse(time.RFC3339, signedAt)
	if err != nil || absDuration(time.Since(t)) > 10*time.Minute {
		return SessionOutput{}, Err("invalid_timestamp", "设备签名时间无效", http.StatusBadRequest)
	}
	pub, err := controlcrypto.ParseP256PublicKey(device.PublicKey)
	if err != nil {
		return SessionOutput{}, err
	}
	message, err := controlcrypto.CanonicalLines("xzitpocket-control-refresh", device.DeviceSerial, refreshToken, signedAt)
	if err != nil || !controlcrypto.VerifyP256Signature(pub, message, signature) {
		return SessionOutput{}, Err("invalid_device_signature", "设备签名无效", http.StatusUnauthorized)
	}
	access, err := controlcrypto.NewToken(32)
	if err != nil {
		return SessionOutput{}, err
	}
	nextRefresh, err := controlcrypto.NewToken(48)
	if err != nil {
		return SessionOutput{}, err
	}
	accessExpiry, refreshExpiry := now.Add(15*time.Minute), session.RefreshExpires
	rotated, err := a.Store.RotateSession(ctx, session.ID, hash, controlcrypto.HashToken(a.Cfg.TokenPepper, access), controlcrypto.HashToken(a.Cfg.TokenPepper, nextRefresh), accessExpiry, refreshExpiry, now)
	if err != nil {
		return SessionOutput{}, Err("refresh_replayed", "刷新令牌已失效", http.StatusUnauthorized)
	}
	user, err := a.Store.GetUser(ctx, rotated.UserID)
	if err != nil {
		return SessionOutput{}, err
	}
	identity, _ := a.Store.GetIdentityByUser(ctx, user.ID, identityProvider)
	return SessionOutput{User: toUserView(user, identity.StudentAlias), Device: toDeviceView(device), AccessToken: access, RefreshToken: nextRefresh, ExpiresAt: accessExpiry.Format(time.RFC3339), RefreshExpiresAt: refreshExpiry.Format(time.RFC3339)}, nil
}

type UserView struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	DisplayName  string `json:"display_name"`
	StudentAlias string `json:"student_alias,omitempty"`
	CreatedAt    string `json:"created_at"`
	LastLoginAt  string `json:"last_login_at"`
}
type DeviceView struct {
	ID             string  `json:"id"`
	DeviceSerial   string  `json:"device_serial"`
	Platform       string  `json:"platform"`
	AppVersion     string  `json:"app_version"`
	InstallationID string  `json:"installation_id,omitempty"`
	CreatedAt      string  `json:"created_at"`
	LastSeenAt     string  `json:"last_seen_at"`
	RevokedAt      *string `json:"revoked_at,omitempty"`
}

func toUserView(u sqlite.User, alias string) UserView {
	return UserView{ID: u.ID, Status: u.Status, DisplayName: u.DisplayName, StudentAlias: alias, CreatedAt: u.CreatedAt.Format(time.RFC3339), LastLoginAt: u.LastLoginAt.Format(time.RFC3339)}
}
func toDeviceView(d sqlite.Device) DeviceView {
	var revoked *string
	if d.RevokedAt != nil {
		v := d.RevokedAt.Format(time.RFC3339)
		revoked = &v
	}
	return DeviceView{ID: d.ID, DeviceSerial: d.DeviceSerial, Platform: d.Platform, AppVersion: d.AppVersion, InstallationID: d.Installation, CreatedAt: d.CreatedAt.Format(time.RFC3339), LastSeenAt: d.LastSeenAt.Format(time.RFC3339), RevokedAt: revoked}
}

func (a *App) CurrentUser(ctx context.Context, p SessionPrincipal) (map[string]any, error) {
	identity, err := a.Store.GetIdentityByUser(ctx, p.User.ID, identityProvider)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	devices, err := a.Store.ListUserDevices(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"user": toUserView(p.User, identity.StudentAlias), "device": toDeviceView(p.Device), "devices": userDevicesView(devices)}, nil
}

func userDevicesView(devices []sqlite.UserDevice) []DeviceView {
	out := make([]DeviceView, 0, len(devices))
	for _, d := range devices {
		out = append(out, toDeviceView(d.Device))
	}
	return out
}

func (a *App) UserDevices(ctx context.Context, p SessionPrincipal) ([]DeviceView, error) {
	devices, err := a.Store.ListUserDevices(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}
	return userDevicesView(devices), nil
}

func (a *App) RevokeSession(ctx context.Context, p SessionPrincipal) error {
	now := time.Now().UTC()
	if err := a.Store.RevokeSession(ctx, p.Session.ID, now); err != nil {
		return err
	}
	_ = a.Store.AddAudit(ctx, p.User.ID, "session_revoke", "session", p.Session.ID, "{}", now)
	return nil
}
