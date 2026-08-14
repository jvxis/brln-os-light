package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"lightningos-light/internal/config"

	"golang.org/x/crypto/argon2"
)

const (
	authSessionCookieName   = "__Host-lightningos_session"
	authSessionTTL          = 12 * time.Hour
	authSetupTokenTTL       = 24 * time.Hour
	authRecoveryTokenTTL    = 30 * time.Minute
	authReauthTTL           = 3 * time.Minute
	authPasswordMinRunes    = 12
	authPasswordMaxRunes    = 128
	authHashMemoryKB        = 64 * 1024
	authHashIterations      = 3
	authHashParallelism     = 2
	authHashKeyLength       = 32
	authPasswordSaltLength  = 16
	authPasswordPepperBytes = 32
	authRawTokenBytes       = 24

	authSecretPasswordHashKey        = "UI_ADMIN_PASSWORD_HASH"
	authSecretPasswordPepperKey      = "UI_ADMIN_PASSWORD_PEPPER"
	authSecretSetupTokenHashKey      = "UI_ADMIN_SETUP_TOKEN_HASH"
	authSecretSetupTokenExpiryKey    = "UI_ADMIN_SETUP_TOKEN_EXPIRES_AT"
	authSecretRecoveryTokenHashKey   = "UI_ADMIN_RECOVERY_TOKEN_HASH"
	authSecretRecoveryTokenExpiryKey = "UI_ADMIN_RECOVERY_TOKEN_EXPIRES_AT"

	authScopeWalletSendExternal = "wallet_send_external"
	authScopeMacaroonExport     = "macaroon_export"
	authScopeNodeRetirement     = "node_retirement_control"
	authScopeSuccessionLive     = "succession_live_control"
	authScopeLoopSwap           = "loop_swap"
	authScopeLoopOutBRLN        = "loopout_brln"
	authScopeTerminalCredential = "terminal_credential"
	authScopeTerminalControl    = "terminal_control"
	authScopeLightningFunds     = "lightning_funds"
	authScopeLNDMaintenance     = "lnd_maintenance"
	authScopeBarkSeedReveal     = "bark_seed_reveal"
)

type authContextKey string

const authSessionContextKey authContextKey = "auth_session"

type authSession struct {
	ID           string
	CSRFToken    string
	CreatedAt    time.Time
	LastSeenAt   time.Time
	ExpiresAt    time.Time
	ReauthScopes map[string]time.Time
}

type authSessionSnapshot struct {
	ID        string
	CSRFToken string
	ExpiresAt time.Time
}

type authStateResponse struct {
	Enabled             bool   `json:"enabled"`
	PasswordConfigured  bool   `json:"password_configured"`
	SetupRequired       bool   `json:"setup_required"`
	Authenticated       bool   `json:"authenticated"`
	CSRFToken           string `json:"csrf_token,omitempty"`
	SessionExpiresAt    string `json:"session_expires_at,omitempty"`
	SetupTokenIssued    bool   `json:"setup_token_issued"`
	RecoveryTokenIssued bool   `json:"recovery_token_issued"`
}

type AuthStatus struct {
	PasswordConfigured  bool
	SetupTokenIssued    bool
	RecoveryTokenIssued bool
}

type AuthService struct {
	enabled bool
	logger  *log.Logger
	now     func() time.Time
	limiter *authRateLimiter
	audit   func(*http.Request, string, string, any)

	mu       sync.Mutex
	sessions map[string]*authSession
}

func NewAuthService(cfg *config.Config, logger *log.Logger) *AuthService {
	enabled := true
	if cfg != nil {
		enabled = cfg.Features.LoginEnabled()
	}
	return &AuthService{
		enabled:  enabled,
		logger:   logger,
		now:      time.Now,
		limiter:  newAuthRateLimiter(),
		sessions: make(map[string]*authSession),
	}
}

func (a *AuthService) Enabled() bool {
	return a != nil && a.enabled
}

func (a *AuthService) SetAuditRecorder(recorder func(*http.Request, string, string, any)) {
	if a != nil {
		a.audit = recorder
	}
}

func (a *AuthService) recordAudit(r *http.Request, action string, target string, metadata any) {
	if a != nil && a.audit != nil {
		a.audit(r, action, target, metadata)
	}
}

func (a *AuthService) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a == nil || !a.Enabled() {
				next.ServeHTTP(w, r)
				return
			}

			path := r.URL.Path
			if !authProtectedPath(path) && !authOriginCheckPath(path) {
				next.ServeHTTP(w, r)
				return
			}

			if authRequestNeedsOriginCheck(r, path) {
				if err := validateSameOriginRequest(r); err != nil {
					writeErrorCode(w, http.StatusForbidden, "auth_origin_invalid", "invalid request origin")
					return
				}
			}

			if !authRequiresSession(path) {
				next.ServeHTTP(w, r)
				return
			}

			sessionID := sessionIDFromRequest(r)
			snapshot, ok := a.authenticateSession(sessionID)
			if !ok {
				a.clearSessionCookie(w)
				if strings.HasPrefix(path, "/terminal") {
					http.Redirect(w, r, "/", http.StatusSeeOther)
					return
				}
				writeErrorCode(w, http.StatusUnauthorized, "auth_required", "authentication required")
				return
			}

			if authRequiresCSRF(r, path) && !a.validateCSRF(snapshot, r.Header.Get("X-CSRF-Token")) {
				writeErrorCode(w, http.StatusForbidden, "csrf_invalid", "invalid csrf token")
				return
			}

			a.writeSessionCookie(w, snapshot)
			ctx := context.WithValue(r.Context(), authSessionContextKey, snapshot)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (a *AuthService) HandleState(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	writeJSON(w, http.StatusOK, a.stateForRequest(w, r))
}

func (a *AuthService) HandleSetup(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if a == nil || !a.Enabled() {
		writeErrorCode(w, http.StatusBadRequest, "auth_disabled", "login protection is disabled")
		return
	}
	if !a.allowAuthRequest(w, r, "setup") {
		return
	}

	var req struct {
		SetupToken      string `json:"setup_token"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if !readAuthJSON(w, r, &req) {
		return
	}

	snapshot, err := a.setup(req.SetupToken, req.Password, req.ConfirmPassword)
	if err != nil {
		a.recordAudit(r, "auth.setup.failed", "", map[string]any{"reason": authAuditReason(err)})
		a.writeSetupError(w, err)
		return
	}

	a.writeSessionCookie(w, snapshot)
	a.recordAudit(r, "auth.setup.succeeded", "", nil)
	writeJSON(w, http.StatusOK, a.stateFromSnapshot(snapshot))
}

func (a *AuthService) HandleLogin(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if a == nil || !a.Enabled() {
		writeErrorCode(w, http.StatusBadRequest, "auth_disabled", "login protection is disabled")
		return
	}
	if !a.allowAuthRequest(w, r, "login") {
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if !readAuthJSON(w, r, &req) {
		return
	}

	snapshot, err := a.login(req.Password)
	if err != nil {
		a.recordAudit(r, "auth.login.failed", "", map[string]any{"reason": authAuditReason(err)})
		a.writeLoginError(w, err)
		return
	}

	a.writeSessionCookie(w, snapshot)
	a.recordAudit(r, "auth.login.succeeded", "", nil)
	writeJSON(w, http.StatusOK, a.stateFromSnapshot(snapshot))
}

func (a *AuthService) HandleLogout(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	a.recordAudit(r, "auth.logout", "", nil)
	if a != nil && a.Enabled() {
		a.logout(sessionIDFromRequest(r))
	}
	a.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *AuthService) HandleRecovery(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if a == nil || !a.Enabled() {
		writeErrorCode(w, http.StatusBadRequest, "auth_disabled", "login protection is disabled")
		return
	}
	if !a.allowAuthRequest(w, r, "recovery") {
		return
	}

	var req struct {
		RecoveryToken   string `json:"recovery_token"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if !readAuthJSON(w, r, &req) {
		return
	}

	snapshot, err := a.recover(req.RecoveryToken, req.Password, req.ConfirmPassword)
	if err != nil {
		a.recordAudit(r, "auth.recovery.failed", "", map[string]any{"reason": authAuditReason(err)})
		a.writeRecoveryError(w, err)
		return
	}

	a.writeSessionCookie(w, snapshot)
	a.recordAudit(r, "auth.recovery.succeeded", "", nil)
	writeJSON(w, http.StatusOK, a.stateFromSnapshot(snapshot))
}

func (a *AuthService) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if a == nil || !a.Enabled() {
		writeErrorCode(w, http.StatusBadRequest, "auth_disabled", "login protection is disabled")
		return
	}
	if !a.allowAuthRequest(w, r, "password") {
		return
	}

	session, ok := authSessionFromContext(r.Context())
	if !ok {
		writeErrorCode(w, http.StatusUnauthorized, "auth_required", "authentication required")
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if !readAuthJSON(w, r, &req) {
		return
	}

	snapshot, err := a.changePassword(session.ID, req.CurrentPassword, req.Password, req.ConfirmPassword)
	if err != nil {
		a.recordAudit(r, "auth.password_change.failed", "", map[string]any{"reason": authAuditReason(err)})
		a.writeChangePasswordError(w, err)
		return
	}

	a.writeSessionCookie(w, snapshot)
	a.recordAudit(r, "auth.password_change.succeeded", "", nil)
	writeJSON(w, http.StatusOK, a.stateFromSnapshot(snapshot))
}

func (a *AuthService) HandleReauth(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if a == nil || !a.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if !a.allowAuthRequest(w, r, "reauth") {
		return
	}

	session, ok := authSessionFromContext(r.Context())
	if !ok {
		writeErrorCode(w, http.StatusUnauthorized, "auth_required", "authentication required")
		return
	}

	var req struct {
		Password string `json:"password"`
		Scope    string `json:"scope"`
	}
	if !readAuthJSON(w, r, &req) {
		return
	}

	expiresAt, err := a.reauth(session.ID, req.Password, req.Scope)
	if err != nil {
		target := "invalid_scope"
		if authScopeValid(req.Scope) {
			target = strings.TrimSpace(req.Scope)
		}
		a.recordAudit(r, "auth.reauth.failed", target, map[string]any{"reason": authAuditReason(err)})
		if errors.Is(err, errInvalidScope) {
			writeErrorCode(w, http.StatusBadRequest, "auth_scope_invalid", "invalid reauthentication scope")
			return
		}
		if errors.Is(err, errInvalidCredentials) {
			writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "invalid credentials")
			return
		}
		writeErrorCode(w, http.StatusInternalServerError, "auth_reauth_failed", "reauthentication failed")
		return
	}
	a.recordAudit(r, "auth.reauth.succeeded", strings.TrimSpace(req.Scope), nil)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"scope":      req.Scope,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

func (a *AuthService) State() AuthStatus {
	status := AuthStatus{
		PasswordConfigured: authPasswordConfigured(),
	}
	now := time.Now()
	status.SetupTokenIssued = authHasActiveToken(authSecretSetupTokenHashKey, authSecretSetupTokenExpiryKey, now)
	status.RecoveryTokenIssued = authHasActiveToken(authSecretRecoveryTokenHashKey, authSecretRecoveryTokenExpiryKey, now)
	return status
}

func (a *AuthService) stateForRequest(w http.ResponseWriter, r *http.Request) authStateResponse {
	if a == nil || !a.Enabled() {
		return authStateResponse{
			Enabled:             false,
			PasswordConfigured:  authPasswordConfigured(),
			Authenticated:       true,
			SetupTokenIssued:    authHasActiveToken(authSecretSetupTokenHashKey, authSecretSetupTokenExpiryKey, time.Now()),
			RecoveryTokenIssued: authHasActiveToken(authSecretRecoveryTokenHashKey, authSecretRecoveryTokenExpiryKey, time.Now()),
		}
	}

	sessionID := sessionIDFromRequest(r)
	snapshot, ok := a.authenticateSession(sessionID)
	if ok {
		a.writeSessionCookie(w, snapshot)
		return a.stateFromSnapshot(snapshot)
	}

	a.clearSessionCookie(w)
	return a.stateWithoutSession()
}

func (a *AuthService) stateWithoutSession() authStateResponse {
	passwordConfigured := authPasswordConfigured()
	now := a.currentTime()
	return authStateResponse{
		Enabled:             true,
		PasswordConfigured:  passwordConfigured,
		SetupRequired:       !passwordConfigured,
		Authenticated:       false,
		SetupTokenIssued:    authHasActiveToken(authSecretSetupTokenHashKey, authSecretSetupTokenExpiryKey, now),
		RecoveryTokenIssued: authHasActiveToken(authSecretRecoveryTokenHashKey, authSecretRecoveryTokenExpiryKey, now),
	}
}

func (a *AuthService) stateFromSnapshot(snapshot authSessionSnapshot) authStateResponse {
	state := a.stateWithoutSession()
	state.Authenticated = true
	state.CSRFToken = snapshot.CSRFToken
	state.SessionExpiresAt = snapshot.ExpiresAt.UTC().Format(time.RFC3339)
	return state
}

func (a *AuthService) setup(setupToken string, password string, confirmPassword string) (authSessionSnapshot, error) {
	if authPasswordConfigured() {
		return authSessionSnapshot{}, errPasswordAlreadyConfigured
	}
	if err := validateAdminPassword(password, confirmPassword); err != nil {
		return authSessionSnapshot{}, err
	}
	if err := authVerifyOneTimeToken(setupToken, authSecretSetupTokenHashKey, authSecretSetupTokenExpiryKey, a.currentTime()); err != nil {
		return authSessionSnapshot{}, err
	}

	if err := authPersistPassword(password); err != nil {
		return authSessionSnapshot{}, err
	}

	return a.resetSessionsAndCreateNew()
}

func (a *AuthService) login(password string) (authSessionSnapshot, error) {
	if !authPasswordConfigured() {
		return authSessionSnapshot{}, errSetupRequired
	}
	if err := authVerifyPassword(password); err != nil {
		return authSessionSnapshot{}, err
	}
	return a.newSession()
}

func (a *AuthService) recover(recoveryToken string, password string, confirmPassword string) (authSessionSnapshot, error) {
	if !authPasswordConfigured() {
		return authSessionSnapshot{}, errSetupRequired
	}
	if err := validateAdminPassword(password, confirmPassword); err != nil {
		return authSessionSnapshot{}, err
	}
	if err := authVerifyOneTimeToken(recoveryToken, authSecretRecoveryTokenHashKey, authSecretRecoveryTokenExpiryKey, a.currentTime()); err != nil {
		return authSessionSnapshot{}, err
	}

	if err := authPersistPassword(password); err != nil {
		return authSessionSnapshot{}, err
	}

	return a.resetSessionsAndCreateNew()
}

func (a *AuthService) changePassword(sessionID string, currentPassword string, password string, confirmPassword string) (authSessionSnapshot, error) {
	if strings.TrimSpace(sessionID) == "" {
		return authSessionSnapshot{}, errInvalidSession
	}
	if !authPasswordConfigured() {
		return authSessionSnapshot{}, errSetupRequired
	}
	if err := authVerifyPassword(currentPassword); err != nil {
		return authSessionSnapshot{}, err
	}
	if err := validateAdminPassword(password, confirmPassword); err != nil {
		return authSessionSnapshot{}, err
	}
	if err := authPersistPassword(password); err != nil {
		return authSessionSnapshot{}, err
	}
	return a.resetSessionsAndCreateNew()
}

func (a *AuthService) reauth(sessionID string, password string, scope string) (time.Time, error) {
	scope = strings.TrimSpace(scope)
	if !authScopeValid(scope) {
		return time.Time{}, errInvalidScope
	}
	if err := authVerifyPassword(password); err != nil {
		return time.Time{}, err
	}

	now := a.currentTime()
	expiresAt := now.Add(authReauthTTL)

	a.mu.Lock()
	defer a.mu.Unlock()

	session, ok := a.sessions[sessionID]
	if !ok || now.After(session.ExpiresAt) {
		return time.Time{}, errInvalidSession
	}
	if session.ReauthScopes == nil {
		session.ReauthScopes = make(map[string]time.Time)
	}
	session.ReauthScopes[scope] = expiresAt
	return expiresAt, nil
}

func authScopeValid(scope string) bool {
	switch strings.TrimSpace(scope) {
	case authScopeWalletSendExternal, authScopeMacaroonExport, authScopeNodeRetirement, authScopeSuccessionLive, authScopeLoopSwap, authScopeLoopOutBRLN, authScopeTerminalCredential, authScopeTerminalControl, authScopeLightningFunds, authScopeLNDMaintenance, authScopeBarkSeedReveal:
		return true
	default:
		return false
	}
}

func (a *AuthService) HasRecentReauth(sessionID string, scope string) bool {
	if a == nil || !a.Enabled() {
		return true
	}

	now := a.currentTime()
	a.mu.Lock()
	defer a.mu.Unlock()

	session, ok := a.sessions[sessionID]
	if !ok || now.After(session.ExpiresAt) {
		return false
	}

	expiresAt, ok := session.ReauthScopes[strings.TrimSpace(scope)]
	if !ok {
		return false
	}
	if now.After(expiresAt) {
		delete(session.ReauthScopes, scope)
		return false
	}
	return true
}

func (a *AuthService) authenticateSession(sessionID string) (authSessionSnapshot, bool) {
	if strings.TrimSpace(sessionID) == "" {
		return authSessionSnapshot{}, false
	}

	now := a.currentTime()
	a.mu.Lock()
	defer a.mu.Unlock()

	session, ok := a.sessions[sessionID]
	if !ok {
		return authSessionSnapshot{}, false
	}
	if now.After(session.ExpiresAt) {
		delete(a.sessions, sessionID)
		return authSessionSnapshot{}, false
	}

	session.LastSeenAt = now
	session.ExpiresAt = now.Add(authSessionTTL)
	for scope, expiry := range session.ReauthScopes {
		if now.After(expiry) {
			delete(session.ReauthScopes, scope)
		}
	}

	return authSessionSnapshot{
		ID:        session.ID,
		CSRFToken: session.CSRFToken,
		ExpiresAt: session.ExpiresAt,
	}, true
}

func (a *AuthService) newSession() (authSessionSnapshot, error) {
	now := a.currentTime()
	sessionID, err := authRandomToken("session", authRawTokenBytes)
	if err != nil {
		return authSessionSnapshot{}, err
	}
	csrfToken, err := authRandomToken("csrf", authRawTokenBytes)
	if err != nil {
		return authSessionSnapshot{}, err
	}
	session := &authSession{
		ID:           sessionID,
		CSRFToken:    csrfToken,
		CreatedAt:    now,
		LastSeenAt:   now,
		ExpiresAt:    now.Add(authSessionTTL),
		ReauthScopes: make(map[string]time.Time),
	}

	a.mu.Lock()
	a.sessions[session.ID] = session
	a.mu.Unlock()

	return authSessionSnapshot{
		ID:        session.ID,
		CSRFToken: session.CSRFToken,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

func (a *AuthService) resetSessionsAndCreateNew() (authSessionSnapshot, error) {
	a.mu.Lock()
	a.sessions = make(map[string]*authSession)
	a.mu.Unlock()
	return a.newSession()
}

func (a *AuthService) logout(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	a.mu.Lock()
	delete(a.sessions, sessionID)
	a.mu.Unlock()
}

func (a *AuthService) writeSessionCookie(w http.ResponseWriter, snapshot authSessionSnapshot) {
	http.SetCookie(w, &http.Cookie{
		Name:     authSessionCookieName,
		Value:    snapshot.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  snapshot.ExpiresAt,
	})
}

func (a *AuthService) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func (a *AuthService) validateCSRF(session authSessionSnapshot, headerToken string) bool {
	trimmed := strings.TrimSpace(headerToken)
	if trimmed == "" || session.CSRFToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(trimmed), []byte(session.CSRFToken)) == 1
}

func (a *AuthService) currentTime() time.Time {
	if a != nil && a.now != nil {
		return a.now()
	}
	return time.Now()
}

func authSessionFromContext(ctx context.Context) (authSessionSnapshot, bool) {
	value := ctx.Value(authSessionContextKey)
	snapshot, ok := value.(authSessionSnapshot)
	return snapshot, ok
}

func sessionIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(authSessionCookieName)
	if err != nil || cookie == nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func authProtectedPath(path string) bool {
	if strings.HasPrefix(path, "/terminal") {
		return true
	}
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	return true
}

func authOriginCheckPath(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/terminal")
}

func authRequiresSession(path string) bool {
	switch path {
	case "/api/health", "/api/auth/state", "/api/auth/login", "/api/auth/setup", "/api/auth/recovery",
		"/api/tls/info", "/api/tls/ca", "/api/tls/windows":
		return false
	default:
		return authProtectedPath(path)
	}
}

func authRequestNeedsOriginCheck(r *http.Request, path string) bool {
	if isUnsafeMethod(r.Method) {
		return authOriginCheckPath(path)
	}
	return isWebsocketUpgrade(r) && strings.HasPrefix(path, "/terminal")
}

func authRequiresCSRF(r *http.Request, path string) bool {
	return authRequiresSession(path) && isUnsafeMethod(r.Method)
}

func isUnsafeMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func isWebsocketUpgrade(r *http.Request) bool {
	if r == nil {
		return false
	}
	connection := strings.ToLower(strings.TrimSpace(r.Header.Get("Connection")))
	upgrade := strings.ToLower(strings.TrimSpace(r.Header.Get("Upgrade")))
	return strings.Contains(connection, "upgrade") && upgrade == "websocket"
}

func validateSameOriginRequest(r *http.Request) error {
	if r == nil {
		return errors.New("request required")
	}

	candidate := strings.TrimSpace(r.Header.Get("Origin"))
	if candidate == "" {
		candidate = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if candidate == "" {
		return errors.New("missing origin")
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return err
	}
	if parsed.Host == "" {
		return errors.New("origin host missing")
	}
	if !strings.EqualFold(parsed.Host, r.Host) {
		return errors.New("origin mismatch")
	}
	return nil
}

var (
	errInvalidCredentials        = errors.New("invalid credentials")
	errInvalidSession            = errors.New("invalid session")
	errInvalidScope              = errors.New("invalid scope")
	errSetupRequired             = errors.New("setup required")
	errPasswordAlreadyConfigured = errors.New("password already configured")
	errTokenRequired             = errors.New("token required")
	errTokenMissing              = errors.New("token missing")
	errTokenExpired              = errors.New("token expired")
	errTokenInvalid              = errors.New("token invalid")
)

func authAuditReason(err error) string {
	switch {
	case errors.Is(err, errInvalidCredentials):
		return "invalid_credentials"
	case errors.Is(err, errInvalidSession):
		return "invalid_session"
	case errors.Is(err, errInvalidScope):
		return "invalid_scope"
	case errors.Is(err, errSetupRequired):
		return "setup_required"
	case errors.Is(err, errPasswordAlreadyConfigured):
		return "already_configured"
	case errors.Is(err, errTokenRequired):
		return "token_required"
	case errors.Is(err, errTokenMissing):
		return "token_missing"
	case errors.Is(err, errTokenExpired):
		return "token_expired"
	case errors.Is(err, errTokenInvalid):
		return "token_invalid"
	default:
		return "request_failed"
	}
}

func validateAdminPassword(password string, confirmPassword string) error {
	if subtle.ConstantTimeCompare([]byte(password), []byte(confirmPassword)) != 1 {
		return errors.New("password confirmation does not match")
	}

	trimmed := strings.TrimSpace(password)
	if trimmed == "" {
		return errors.New("password is required")
	}

	length := utf8.RuneCountInString(password)
	if length < authPasswordMinRunes {
		return fmt.Errorf("password must be at least %d characters", authPasswordMinRunes)
	}
	if length > authPasswordMaxRunes {
		return fmt.Errorf("password must be at most %d characters", authPasswordMaxRunes)
	}
	return nil
}

func authPasswordConfigured() bool {
	value, err := readEnvFileValue(secretsPath, authSecretPasswordHashKey)
	if err != nil {
		return false
	}
	return strings.TrimSpace(value) != ""
}

func authPersistSecrets(updates map[string]string) error {
	if err := ensureSecretsDir(); err != nil {
		return err
	}
	return writeEnvFileValues(secretsPath, updates)
}

func authPersistPassword(password string) error {
	passwordHash, pepper, err := authHashPassword(password)
	if err != nil {
		return err
	}
	return authPersistSecrets(map[string]string{
		authSecretPasswordHashKey:        passwordHash,
		authSecretPasswordPepperKey:      pepper,
		authSecretSetupTokenHashKey:      "",
		authSecretSetupTokenExpiryKey:    "",
		authSecretRecoveryTokenHashKey:   "",
		authSecretRecoveryTokenExpiryKey: "",
	})
}

func authReadSecret(key string) string {
	value, err := readEnvFileValue(secretsPath, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func authHasActiveToken(hashKey string, expiryKey string, now time.Time) bool {
	hashValue := authReadSecret(hashKey)
	expiryValue := authReadSecret(expiryKey)
	if hashValue == "" || expiryValue == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, expiryValue)
	if err != nil {
		return false
	}
	return now.Before(expiresAt)
}

func authHashPassword(password string) (string, string, error) {
	pepperBytes := make([]byte, authPasswordPepperBytes)
	if _, err := rand.Read(pepperBytes); err != nil {
		return "", "", err
	}
	salt := make([]byte, authPasswordSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}

	pepper := base64.RawStdEncoding.EncodeToString(pepperBytes)
	hash := argon2.IDKey([]byte(password+pepper), salt, authHashIterations, authHashMemoryKB, authHashParallelism, authHashKeyLength)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		authHashMemoryKB,
		authHashIterations,
		authHashParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, pepper, nil
}

func authVerifyPassword(password string) error {
	hashValue := authReadSecret(authSecretPasswordHashKey)
	pepper := authReadSecret(authSecretPasswordPepperKey)
	if hashValue == "" || pepper == "" {
		return errSetupRequired
	}

	params, salt, expectedHash, err := parseArgon2Hash(hashValue)
	if err != nil {
		return errInvalidCredentials
	}
	computed := argon2.IDKey([]byte(password+pepper), salt, params.iterations, params.memoryKB, params.parallelism, uint32(len(expectedHash)))
	if subtle.ConstantTimeCompare(computed, expectedHash) != 1 {
		return errInvalidCredentials
	}
	return nil
}

type argon2Params struct {
	memoryKB    uint32
	iterations  uint32
	parallelism uint8
}

func parseArgon2Hash(value string) (argon2Params, []byte, []byte, error) {
	parts := strings.Split(value, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argon2Params{}, nil, nil, errors.New("invalid hash format")
	}
	if parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return argon2Params{}, nil, nil, errors.New("invalid hash version")
	}

	var params argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memoryKB, &params.iterations, &params.parallelism); err != nil {
		return argon2Params{}, nil, nil, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2Params{}, nil, nil, err
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2Params{}, nil, nil, err
	}
	return params, salt, hash, nil
}

func authTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func authVerifyOneTimeToken(token string, hashKey string, expiryKey string, now time.Time) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errTokenRequired
	}

	expectedHash := authReadSecret(hashKey)
	expiryValue := authReadSecret(expiryKey)
	if expectedHash == "" || expiryValue == "" {
		return errTokenMissing
	}

	expiresAt, err := time.Parse(time.RFC3339, expiryValue)
	if err != nil {
		return errTokenMissing
	}
	if !now.Before(expiresAt) {
		return errTokenExpired
	}

	if subtle.ConstantTimeCompare([]byte(authTokenHash(token)), []byte(expectedHash)) != 1 {
		return errTokenInvalid
	}
	return nil
}

func authIssueToken(prefix string, ttl time.Duration, hashKey string, expiryKey string) (string, time.Time, error) {
	token, err := authRandomToken(prefix, authRawTokenBytes)
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(ttl).UTC()
	if err := authPersistSecrets(map[string]string{
		hashKey:   authTokenHash(token),
		expiryKey: expiresAt.Format(time.RFC3339),
	}); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

var authRandomRead = rand.Read

func authRandomToken(prefix string, rawBytes int) (string, error) {
	if rawBytes < 16 {
		rawBytes = 16
	}
	buf := make([]byte, rawBytes)
	if _, err := authRandomRead(buf); err != nil {
		return "", fmt.Errorf("secure random generation failed: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(buf)
	if strings.TrimSpace(prefix) == "" {
		return encoded, nil
	}
	return fmt.Sprintf("los-%s-%s", prefix, encoded), nil
}

func IssueSetupToken() (string, time.Time, error) {
	if authPasswordConfigured() {
		return "", time.Time{}, errPasswordAlreadyConfigured
	}
	return authIssueToken("setup", authSetupTokenTTL, authSecretSetupTokenHashKey, authSecretSetupTokenExpiryKey)
}

func IssueRecoveryToken() (string, time.Time, error) {
	if !authPasswordConfigured() {
		return "", time.Time{}, errSetupRequired
	}
	return authIssueToken("recovery", authRecoveryTokenTTL, authSecretRecoveryTokenHashKey, authSecretRecoveryTokenExpiryKey)
}

func LoadAuthStatus() AuthStatus {
	now := time.Now()
	return AuthStatus{
		PasswordConfigured:  authPasswordConfigured(),
		SetupTokenIssued:    authHasActiveToken(authSecretSetupTokenHashKey, authSecretSetupTokenExpiryKey, now),
		RecoveryTokenIssued: authHasActiveToken(authSecretRecoveryTokenHashKey, authSecretRecoveryTokenExpiryKey, now),
	}
}

func (a *AuthService) writeSetupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPasswordAlreadyConfigured):
		writeErrorCode(w, http.StatusConflict, "auth_password_already_configured", "admin password already configured")
	case errors.Is(err, errTokenRequired):
		writeErrorCode(w, http.StatusBadRequest, "auth_setup_token_required", "setup token required")
	case errors.Is(err, errTokenMissing):
		writeErrorCode(w, http.StatusConflict, "auth_setup_token_missing", "setup token not issued or expired")
	case errors.Is(err, errTokenExpired):
		writeErrorCode(w, http.StatusConflict, "auth_setup_token_expired", "setup token expired")
	case errors.Is(err, errTokenInvalid):
		writeErrorCode(w, http.StatusUnauthorized, "auth_setup_token_invalid", "invalid setup token")
	default:
		if strings.Contains(err.Error(), "password") {
			writeErrorCode(w, http.StatusBadRequest, "auth_password_invalid", err.Error())
			return
		}
		writeErrorCode(w, http.StatusInternalServerError, "auth_setup_failed", "failed to configure admin password")
	}
}

func (a *AuthService) writeLoginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSetupRequired):
		writeErrorCode(w, http.StatusConflict, "auth_setup_required", "admin password is not configured")
	case errors.Is(err, errInvalidCredentials):
		writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "invalid credentials")
	default:
		writeErrorCode(w, http.StatusInternalServerError, "auth_login_failed", "login failed")
	}
}

func (a *AuthService) writeRecoveryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSetupRequired):
		writeErrorCode(w, http.StatusConflict, "auth_setup_required", "admin password is not configured")
	case errors.Is(err, errTokenRequired):
		writeErrorCode(w, http.StatusBadRequest, "auth_recovery_token_required", "recovery token required")
	case errors.Is(err, errTokenMissing):
		writeErrorCode(w, http.StatusConflict, "auth_recovery_token_missing", "recovery token not issued or expired")
	case errors.Is(err, errTokenExpired):
		writeErrorCode(w, http.StatusConflict, "auth_recovery_token_expired", "recovery token expired")
	case errors.Is(err, errTokenInvalid):
		writeErrorCode(w, http.StatusUnauthorized, "auth_recovery_token_invalid", "invalid recovery token")
	default:
		if strings.Contains(err.Error(), "password") {
			writeErrorCode(w, http.StatusBadRequest, "auth_password_invalid", err.Error())
			return
		}
		writeErrorCode(w, http.StatusInternalServerError, "auth_recovery_failed", "recovery failed")
	}
}

func (a *AuthService) writeChangePasswordError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSetupRequired):
		writeErrorCode(w, http.StatusConflict, "auth_setup_required", "admin password is not configured")
	case errors.Is(err, errInvalidSession):
		writeErrorCode(w, http.StatusUnauthorized, "auth_required", "authentication required")
	case errors.Is(err, errInvalidCredentials):
		writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "invalid credentials")
	default:
		if strings.Contains(err.Error(), "password") {
			writeErrorCode(w, http.StatusBadRequest, "auth_password_invalid", err.Error())
			return
		}
		writeErrorCode(w, http.StatusInternalServerError, "auth_change_password_failed", "failed to change admin password")
	}
}
