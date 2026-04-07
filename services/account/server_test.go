package accountservice

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	accountv1 "github.com/niczy/gitslice/proto/account"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func bearerCtx(ctx context.Context, token string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func userCtx(ctx context.Context, username string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "User "+username))
}

func workOSBearerCtx(ctx context.Context, token string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func assertHomeSliceProvisioned(t *testing.T, ctx context.Context, st storage.Storage, username string) {
	t.Helper()

	homeSliceID := homeslice.IDForUsername(username)
	if _, err := st.GetSlice(ctx, homeSliceID); err != nil {
		t.Fatalf("home slice %s not found: %v", homeSliceID, err)
	}
	rootPath := homeslice.RelativeRootPath(username)
	if _, err := st.GetEntryByPath(ctx, homeSliceID, rootPath); err != nil {
		t.Fatalf("home slice path %s not found: %v", rootPath, err)
	}
	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if _, err := st.GetEntryByPath(ctx, rootSlice.ID, rootPath); err != nil {
		t.Fatalf("root slice path %s not found: %v", rootPath, err)
	}
}

func TestSignupLoginAndSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "password123",
		Name:     "Alice",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}
	if signupResp.GetAccessToken() == "" || signupResp.GetSession().GetId() == "" {
		t.Fatalf("signup should return token + session, got %#v", signupResp)
	}
	assertHomeSliceProvisioned(t, ctx, srv.st, "alice")

	listResp, err := srv.ListSessions(bearerCtx(ctx, signupResp.GetAccessToken()), &accountv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(listResp.GetSessions()) != 1 || !listResp.GetSessions()[0].GetCurrent() {
		t.Fatalf("unexpected session list: %#v", listResp)
	}

	if _, err := srv.Logout(bearerCtx(ctx, signupResp.GetAccessToken()), &accountv1.LogoutRequest{}); err != nil {
		t.Fatalf("Logout failed: %v", err)
	}
	if _, err := srv.ListSessions(bearerCtx(ctx, signupResp.GetAccessToken()), &accountv1.ListSessionsRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated after logout, got %v", err)
	}

	loginResp, err := srv.Login(ctx, &accountv1.LoginRequest{Username: "alice", Password: "password123"})
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if loginResp.GetAccessToken() == "" || loginResp.GetSession().GetId() == "" {
		t.Fatalf("login should return token + session, got %#v", loginResp)
	}

	if _, err := srv.DeleteSession(bearerCtx(ctx, loginResp.GetAccessToken()), &accountv1.DeleteSessionRequest{SessionId: loginResp.GetSession().GetId()}); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
	devList, err := srv.ListSessions(userCtx(ctx, "alice"), &accountv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions with dev fallback failed: %v", err)
	}
	if len(devList.GetSessions()) != 0 {
		t.Fatalf("expected zero sessions after revoke, got %#v", devList)
	}

	fallbackResp, err := srv.Login(ctx, &accountv1.LoginRequest{Username: "devonly"})
	if err != nil {
		t.Fatalf("dev fallback login failed: %v", err)
	}
	if fallbackResp.GetAccessToken() == "" {
		t.Fatalf("dev fallback login should still create a session token")
	}
	assertHomeSliceProvisioned(t, ctx, srv.st, "devonly")
}

func TestGetAuthContextReportsCredentialSource(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	anonymous, err := srv.GetAuthContext(ctx, &accountv1.GetAuthContextRequest{})
	if err != nil {
		t.Fatalf("GetAuthContext anonymous failed: %v", err)
	}
	if anonymous.GetAuthenticated() {
		t.Fatalf("expected anonymous auth context, got %#v", anonymous)
	}

	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "password123",
		Name:     "Alice",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}
	localCtx, err := srv.GetAuthContext(bearerCtx(ctx, signupResp.GetAccessToken()), &accountv1.GetAuthContextRequest{})
	if err != nil {
		t.Fatalf("GetAuthContext local session failed: %v", err)
	}
	if !localCtx.GetAuthenticated() || localCtx.GetUsername() != "alice" || localCtx.GetSessionId() == "" || localCtx.GetAuthSource() != "local_session" {
		t.Fatalf("unexpected local auth context: %#v", localCtx)
	}

	agentAccessExpiresAt := time.Now().Add(time.Hour)
	agentRefreshExpiresAt := time.Now().Add(24 * time.Hour)
	if err := srv.st.CreateAgentKey(ctx, &models.AgentKey{
		KeyID:       "agent-key-1",
		Username:    "alice",
		Name:        "test agent",
		Algorithm:   "ed25519",
		PublicKey:   []byte("public-key"),
		Fingerprint: "fingerprint-agent-key-1",
	}); err != nil {
		t.Fatalf("CreateAgentKey failed: %v", err)
	}
	if err := srv.st.CreateAuthSession(ctx, &models.AuthSession{
		SessionID:             "sess-agent",
		Username:              "alice",
		AgentKeyID:            "agent-key-1",
		Token:                 "gs_agent_session",
		RefreshToken:          "gsr_agent_session",
		CreatedAt:             time.Now(),
		LastSeenAt:            time.Now(),
		AccessTokenExpiresAt:  &agentAccessExpiresAt,
		RefreshTokenExpiresAt: &agentRefreshExpiresAt,
	}); err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}
	agentCtx, err := srv.GetAuthContext(bearerCtx(ctx, "gs_agent_session"), &accountv1.GetAuthContextRequest{})
	if err != nil {
		t.Fatalf("GetAuthContext agent session failed: %v", err)
	}
	if agentCtx.GetAuthSource() != "agent_key" || agentCtx.GetAgentKeyId() != "agent-key-1" {
		t.Fatalf("unexpected agent auth context: %#v", agentCtx)
	}
	if agentCtx.GetAccessTokenExpiresAt() == "" || agentCtx.GetRefreshTokenExpiresAt() == "" {
		t.Fatalf("expected token expiry metadata: %#v", agentCtx)
	}

	legacyCtx, err := srv.GetAuthContext(userCtx(ctx, "alice"), &accountv1.GetAuthContextRequest{})
	if err != nil {
		t.Fatalf("GetAuthContext legacy user failed: %v", err)
	}
	if legacyCtx.GetAuthSource() != "legacy_user" || legacyCtx.GetSessionId() != "" {
		t.Fatalf("unexpected legacy auth context: %#v", legacyCtx)
	}
}

func TestGetAuthContextReportsWorkOSLinkage(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	account := &models.Account{
		AccountID:  "acct_workos_123",
		OwnerMode:  models.AccountOwnerModeHumanAttached,
		ClaimState: models.AccountClaimStateClaimed,
	}
	if err := st.CreateAccount(ctx, account); err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "alice",
		AccountID:    account.AccountID,
		PrimaryEmail: "alice@example.com",
		AuthSource:   "workos",
		WorkOSUserID: "user_workos_123",
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	session := &models.AuthSession{
		SessionID:  "sess-workos-local",
		Username:   "alice",
		Token:      "gs_workos_local",
		CreatedAt:  time.Now(),
		LastSeenAt: time.Now(),
	}
	if err := st.CreateAuthSession(ctx, session); err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}
	srv := &accountServiceServer{st: st}

	authCtx, err := srv.GetAuthContext(bearerCtx(ctx, session.Token), &accountv1.GetAuthContextRequest{})
	if err != nil {
		t.Fatalf("GetAuthContext failed: %v", err)
	}
	if !authCtx.GetWorkosLinked() || authCtx.GetWorkosUserId() != "user_workos_123" || authCtx.GetAccountId() != account.AccountID {
		t.Fatalf("unexpected WorkOS-linked auth context: %#v", authCtx)
	}
}

func TestResetPasswordFlow(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "bob",
		Email:    "bob@example.com",
		Password: "oldpassword",
		Name:     "Bob",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}
	oldToken := signupResp.GetAccessToken()

	if _, err := srv.ResetPassword(ctx, &accountv1.ResetPasswordRequest{Username: "bob", NewPassword: "newpassword"}); err != nil {
		t.Fatalf("ResetPassword failed: %v", err)
	}

	if _, err := srv.Login(ctx, &accountv1.LoginRequest{Username: "bob", Password: "oldpassword"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected old password to fail, got %v", err)
	}
	if _, err := srv.Login(ctx, &accountv1.LoginRequest{Username: "bob", Password: "newpassword"}); err != nil {
		t.Fatalf("expected new password to work, got %v", err)
	}
	if _, err := srv.ListSessions(bearerCtx(ctx, oldToken), &accountv1.ListSessionsRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected old token invalidated by reset, got %v", err)
	}
}

func TestGetMeRejectsDirectWorkOSBearerToken(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	account := &models.Account{
		AccountID:  "acct_123",
		OwnerMode:  models.AccountOwnerModeHumanAttached,
		ClaimState: models.AccountClaimStateClaimed,
	}
	if err := st.CreateAccount(ctx, account); err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	user := &models.User{
		Username:     "alice",
		AccountID:    account.AccountID,
		Name:         "Alice",
		PrimaryEmail: "alice@example.com",
		AuthSource:   "workos",
		WorkOSUserID: "user_123",
	}
	if err := st.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if _, err := homeslice.EnsureUserHomeSlice(ctx, st, "alice"); err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	assertHomeSliceProvisioned(t, ctx, st, "alice")

	srv := &accountServiceServer{st: st}
	token := signAccountWorkOSToken(t, privateKey, "client_test_123", "user_123", "sess_workos_123")

	if _, err := srv.GetMe(workOSBearerCtx(ctx, token), &accountv1.GetMeRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected direct WorkOS bearer to be exchange-only, got %v", err)
	}
}

func TestEnsureWorkOSLocalIdentityCreatesNewLocalUser(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}
	token := signAccountWorkOSToken(t, privateKey, "client_test_123", "user_new_123", "sess_workos_123")

	resp, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		PreferredUsername: "alice-workos",
		Name:              "Alice WorkOS",
		PrimaryEmail:      "alice@example.com",
	})
	if err != nil {
		t.Fatalf("EnsureWorkOSLocalIdentity failed: %v", err)
	}
	if !resp.GetCreatedAccount() || !resp.GetCreatedUser() || resp.GetLinkedExistingUser() {
		t.Fatalf("unexpected response flags: %#v", resp)
	}
	if resp.GetUser().GetUsername() != "alice-workos" {
		t.Fatalf("unexpected username: %#v", resp)
	}
	assertHomeSliceProvisioned(t, ctx, srv.st, "alice-workos")
}

func TestEnsureWorkOSLocalIdentityCanIssueRefreshableLocalSession(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}
	token := signAccountWorkOSToken(t, privateKey, "client_test_123", "user_issue_123", "sess_workos_123")

	resp, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		PreferredUsername: "alice-local-session",
		Name:              "Alice Local Session",
		PrimaryEmail:      "alice-local@example.com",
		IssueLocalSession: true,
	})
	if err != nil {
		t.Fatalf("EnsureWorkOSLocalIdentity failed: %v", err)
	}
	localAuth := resp.GetLocalAuth()
	if localAuth == nil {
		t.Fatalf("expected local auth response, got %#v", resp)
	}
	if localAuth.GetAccessToken() == "" || localAuth.GetRefreshToken() == "" {
		t.Fatalf("expected refreshable local auth tokens, got %#v", localAuth)
	}
	if localAuth.GetSession() == nil || localAuth.GetSession().GetId() == "" {
		t.Fatalf("expected local session metadata, got %#v", localAuth)
	}
	if localAuth.GetUser().GetUsername() != "alice-local-session" {
		t.Fatalf("unexpected local auth user: %#v", localAuth)
	}
	if _, err := srv.ListSessions(bearerCtx(ctx, localAuth.GetAccessToken()), &accountv1.ListSessionsRequest{}); err != nil {
		t.Fatalf("issued local access token should be usable, got %v", err)
	}
	refreshResp, err := srv.RefreshAccessToken(ctx, &accountv1.RefreshAccessTokenRequest{RefreshToken: localAuth.GetRefreshToken()})
	if err != nil {
		t.Fatalf("RefreshAccessToken failed: %v", err)
	}
	if refreshResp.GetAccessToken() == "" || refreshResp.GetAccessToken() == localAuth.GetAccessToken() {
		t.Fatalf("expected rotated local access token, got %#v", refreshResp)
	}
}

func TestEnsureWorkOSLocalIdentityRequiresClaimTokenForAgentOnlyAccount(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	account := &models.Account{
		AccountID:  "acct_agent_123",
		OwnerMode:  models.AccountOwnerModeAgentOnly,
		ClaimState: models.AccountClaimStateUnclaimed,
	}
	if err := st.CreateAccount(ctx, account); err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "agentalice",
		AccountID:    account.AccountID,
		Name:         "Agent Alice",
		PrimaryEmail: "alice@example.com",
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	srv := &accountServiceServer{st: st}
	token := signAccountWorkOSToken(t, privateKey, "client_test_123", "user_link_123", "sess_workos_123")

	_, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		Name:         "Alice Human",
		PrimaryEmail: "alice@example.com",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition, got %v", err)
	}
	linkedUser, err := st.GetUser(ctx, "agentalice")
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if linkedUser.WorkOSUserID != "" || linkedUser.AuthSource != "" {
		t.Fatalf("expected user to remain unlinked, got %#v", linkedUser)
	}
}

func TestEnsureWorkOSLocalIdentityIsIdempotentForLinkedUser(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	account := &models.Account{
		AccountID:  "acct_123",
		OwnerMode:  models.AccountOwnerModeHumanAttached,
		ClaimState: models.AccountClaimStateClaimed,
	}
	if err := st.CreateAccount(ctx, account); err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "alice",
		AccountID:    account.AccountID,
		PrimaryEmail: "alice@example.com",
		AuthSource:   "workos",
		WorkOSUserID: "user_123",
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	srv := &accountServiceServer{st: st}
	token := signAccountWorkOSToken(t, privateKey, "client_test_123", "user_123", "sess_workos_123")

	first, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		Name:         "Alice",
		PrimaryEmail: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("first EnsureWorkOSLocalIdentity failed: %v", err)
	}
	second, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		Name:         "Alice",
		PrimaryEmail: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("second EnsureWorkOSLocalIdentity failed: %v", err)
	}
	if first.GetUser().GetUsername() != "alice" || second.GetUser().GetUsername() != "alice" {
		t.Fatalf("unexpected usernames: first=%#v second=%#v", first, second)
	}
	if second.GetCreatedUser() || second.GetCreatedAccount() || second.GetLinkedExistingUser() {
		t.Fatalf("expected idempotent second ensure, got %#v", second)
	}
}

func TestCreateAccountClaimTokenReturnsClaimURL(t *testing.T) {
	t.Setenv("PUBLIC_WEB_BASE_URL", "https://gitslice.io")

	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	authResp, err := srv.SignupWithAgentKey(ctx, &accountv1.SignupWithAgentKeyRequest{
		Username:  "agentclaim",
		Email:     "agentclaim@example.com",
		Name:      "Agent Claim",
		Algorithm: "ed25519",
		PublicKey: publicKey,
		KeyName:   "claim-runner",
	})
	if err != nil {
		t.Fatalf("SignupWithAgentKey failed: %v", err)
	}

	resp, err := srv.CreateAccountClaimToken(bearerCtx(ctx, authResp.GetAccessToken()), &accountv1.CreateAccountClaimTokenRequest{})
	if err != nil {
		t.Fatalf("CreateAccountClaimToken failed: %v", err)
	}
	if resp.GetAccountId() == "" || resp.GetClaimToken() == "" {
		t.Fatalf("expected claim token response, got %#v", resp)
	}
	if !strings.HasPrefix(resp.GetClaimUrl(), "https://gitslice.io/auth/claim-account?token=") {
		t.Fatalf("unexpected claim url: %q", resp.GetClaimUrl())
	}
	account, err := srv.st.GetAccount(ctx, resp.GetAccountId())
	if err != nil {
		t.Fatalf("GetAccount failed: %v", err)
	}
	if account.ClaimTokenHash != hashClaimToken(resp.GetClaimToken()) {
		t.Fatalf("expected stored claim token hash, got %#v", account)
	}
}

func TestEnsureWorkOSLocalIdentityClaimsAgentAccountWithClaimToken(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	account := &models.Account{
		AccountID:      "acct_agent_123",
		OwnerMode:      models.AccountOwnerModeAgentOnly,
		ClaimState:     models.AccountClaimStateUnclaimed,
		ClaimTokenHash: hashClaimToken("claim_token_123"),
	}
	if err := st.CreateAccount(ctx, account); err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "agentalice",
		AccountID:    account.AccountID,
		Name:         "Agent Alice",
		PrimaryEmail: "alice@example.com",
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	srv := &accountServiceServer{st: st}
	token := signAccountWorkOSToken(t, privateKey, "client_test_123", "user_link_123", "sess_workos_123")

	resp, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		Name:         "Alice Human",
		PrimaryEmail: "alice@example.com",
		ClaimToken:   "claim_token_123",
	})
	if err != nil {
		t.Fatalf("EnsureWorkOSLocalIdentity failed: %v", err)
	}
	if !resp.GetClaimedAccount() || !resp.GetLinkedExistingUser() {
		t.Fatalf("expected claimed linked user response, got %#v", resp)
	}
	linkedUser, err := st.GetUser(ctx, "agentalice")
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if linkedUser.WorkOSUserID != "user_link_123" || linkedUser.AuthSource != "workos" {
		t.Fatalf("unexpected linked user: %#v", linkedUser)
	}
	linkedAccount, err := st.GetAccount(ctx, account.AccountID)
	if err != nil {
		t.Fatalf("GetAccount failed: %v", err)
	}
	if linkedAccount.OwnerMode != models.AccountOwnerModeHumanAttached || linkedAccount.ClaimState != models.AccountClaimStateClaimed || linkedAccount.ClaimTokenHash != "" {
		t.Fatalf("unexpected linked account: %#v", linkedAccount)
	}
	assertHomeSliceProvisioned(t, ctx, st, "agentalice")
}

func TestListAuthMethodsSynthesizesPasswordAndWorkOSMethods(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	account := &models.Account{
		AccountID:  "acct_123",
		OwnerMode:  models.AccountOwnerModeHumanAttached,
		ClaimState: models.AccountClaimStateClaimed,
	}
	if err := st.CreateAccount(ctx, account); err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	passwordHash, err := hashPassword("secret123")
	if err != nil {
		t.Fatalf("hashPassword failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "alice",
		AccountID:    account.AccountID,
		PrimaryEmail: "alice@example.com",
		PasswordHash: passwordHash,
		AuthSource:   "workos",
		WorkOSUserID: "user_123",
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	srv := &accountServiceServer{st: st}
	token := signAccountWorkOSToken(t, privateKey, "client_test_123", "user_123", "sess_workos_123")
	identityResp, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		PreferredUsername: "alice",
		PrimaryEmail:      "alice@example.com",
		IssueLocalSession: true,
	})
	if err != nil {
		t.Fatalf("EnsureWorkOSLocalIdentity failed: %v", err)
	}
	if identityResp.GetLocalAuth().GetAccessToken() == "" {
		t.Fatalf("expected local auth token")
	}

	resp, err := srv.ListAuthMethods(bearerCtx(ctx, identityResp.GetLocalAuth().GetAccessToken()), &accountv1.ListAuthMethodsRequest{})
	if err != nil {
		t.Fatalf("ListAuthMethods failed: %v", err)
	}
	if len(resp.GetMethods()) != 2 {
		t.Fatalf("expected 2 auth methods, got %#v", resp.GetMethods())
	}
	if resp.GetMethods()[0].GetId() != "password" && resp.GetMethods()[1].GetId() != "password" {
		t.Fatalf("expected password method, got %#v", resp.GetMethods())
	}
	if resp.GetMethods()[0].GetId() != "oauth:workos" && resp.GetMethods()[1].GetId() != "oauth:workos" {
		t.Fatalf("expected WorkOS method, got %#v", resp.GetMethods())
	}
}

func TestLinkAuthMethodAttachesCurrentWorkOSIdentity(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}
	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "secret123",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}
	token := signAccountWorkOSToken(t, privateKey, "client_test_123", "user_attach_123", "sess_workos_123")

	method, err := srv.LinkAuthMethod(bearerCtx(ctx, signupResp.GetAccessToken()), &accountv1.LinkAuthMethodRequest{
		Type:     accountv1.AuthMethodType_AUTH_METHOD_TYPE_OAUTH,
		Provider: "workos",
		Token:    token,
	})
	if err != nil {
		t.Fatalf("LinkAuthMethod failed: %v", err)
	}
	if method.GetId() != "oauth:workos" {
		t.Fatalf("unexpected method: %#v", method)
	}
	user, err := srv.st.GetUser(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if user.WorkOSUserID != "user_attach_123" || user.AuthSource != "workos" {
		t.Fatalf("unexpected linked user: %#v", user)
	}
}

func TestDeleteAuthMethodRejectsRemovingOnlyHumanMethod(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	srv := &accountServiceServer{st: st}

	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "secret123",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}

	if _, err := srv.DeleteAuthMethod(bearerCtx(ctx, signupResp.GetAccessToken()), &accountv1.DeleteAuthMethodRequest{MethodId: "password"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected failed precondition when deleting only auth method, got %v", err)
	}
}

func TestEnsureWorkOSLocalIdentityLinksMatchingWorkOSOrganizationMembership(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	srv := &accountServiceServer{st: st}

	ownerSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "orgowner",
		Email:    "owner@example.com",
		Password: "secret123",
	})
	if err != nil {
		t.Fatalf("owner Signup failed: %v", err)
	}
	if _, err := srv.CreateOrganization(bearerCtx(ctx, ownerSignup.GetAccessToken()), &accountv1.CreateOrganizationRequest{
		Slug: "acme",
		Name: "Acme",
	}); err != nil {
		t.Fatalf("CreateOrganization failed: %v", err)
	}
	org, err := st.GetOrganization(ctx, "acme")
	if err != nil {
		t.Fatalf("GetOrganization failed: %v", err)
	}
	org.WorkOSOrganizationID = "org_workos_123"
	if err := st.UpdateOrganization(ctx, org); err != nil {
		t.Fatalf("UpdateOrganization failed: %v", err)
	}

	token := signAccountWorkOSTokenWithClaims(t, privateKey, "client_test_123", "user_workos_123", "sess_workos_123", "org_workos_123", "admin")
	resp, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		PreferredUsername: "alice",
		Name:              "Alice",
		PrimaryEmail:      "alice@example.com",
		IssueLocalSession: true,
	})
	if err != nil {
		t.Fatalf("EnsureWorkOSLocalIdentity failed: %v", err)
	}
	if resp.GetLocalAuth().GetAccessToken() == "" {
		t.Fatalf("expected local auth from WorkOS exchange, got %#v", resp)
	}

	member, err := st.GetOrganizationMember(ctx, "acme", resp.GetUser().GetUsername())
	if err != nil {
		t.Fatalf("GetOrganizationMember failed: %v", err)
	}
	if member.Role != models.OrganizationRoleAdmin {
		t.Fatalf("expected admin membership, got %#v", member)
	}

	orgs, err := srv.ListOrganizations(bearerCtx(ctx, resp.GetLocalAuth().GetAccessToken()), &accountv1.ListOrganizationsRequest{})
	if err != nil {
		t.Fatalf("ListOrganizations failed: %v", err)
	}
	if len(orgs.GetOrganizations()) != 1 || orgs.GetOrganizations()[0].GetSlug() != "acme" {
		t.Fatalf("unexpected organizations: %#v", orgs.GetOrganizations())
	}
}

func TestCreateOrganizationUsesLocalSessionAfterWorkOSExchange(t *testing.T) {
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")

	privateKey, jwksServer := startAccountWorkOSJWKSFixture(t)
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	token := signAccountWorkOSTokenWithClaims(t, privateKey, "client_test_123", "user_creator_123", "sess_workos_123", "org_workos_456", "admin")
	identityResp, err := srv.EnsureWorkOSLocalIdentity(workOSBearerCtx(ctx, token), &accountv1.EnsureWorkOSLocalIdentityRequest{
		PreferredUsername: "creator",
		Name:              "Creator",
		PrimaryEmail:      "creator@example.com",
		IssueLocalSession: true,
	})
	if err != nil {
		t.Fatalf("EnsureWorkOSLocalIdentity failed: %v", err)
	}
	if identityResp.GetUser().GetUsername() == "" {
		t.Fatalf("expected local user username, got %#v", identityResp)
	}
	if identityResp.GetLocalAuth().GetAccessToken() == "" {
		t.Fatalf("expected local auth from WorkOS exchange, got %#v", identityResp)
	}

	created, err := srv.CreateOrganization(bearerCtx(ctx, identityResp.GetLocalAuth().GetAccessToken()), &accountv1.CreateOrganizationRequest{
		Slug: "workos-linked",
		Name: "WorkOS Linked",
	})
	if err != nil {
		t.Fatalf("CreateOrganization failed: %v", err)
	}
	stored, err := srv.st.GetOrganization(ctx, created.GetSlug())
	if err != nil {
		t.Fatalf("GetOrganization failed: %v", err)
	}
	if stored.WorkOSOrganizationID != "" {
		t.Fatalf("expected local API session to avoid implicit WorkOS organization binding, got %#v", stored)
	}
}

func startAccountWorkOSJWKSFixture(t *testing.T) (*rsa.PrivateKey, *httptest.Server) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kid": "test-kid",
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.E)).Bytes()),
			}},
		})
	}))
	t.Cleanup(server.Close)
	return privateKey, server
}

func signAccountWorkOSToken(t *testing.T, privateKey *rsa.PrivateKey, audience, subject, sessionID string) string {
	return signAccountWorkOSTokenWithClaims(t, privateKey, audience, subject, sessionID, "", "")
}

func signAccountWorkOSTokenWithClaims(t *testing.T, privateKey *rsa.PrivateKey, audience, subject, sessionID, organizationID, role string) string {
	t.Helper()

	now := time.Now()
	claims := jwt.MapClaims{
		"aud": []string{audience},
		"exp": now.Add(10 * time.Minute).Unix(),
		"iat": now.Unix(),
		"iss": "https://api.workos.com/",
		"sub": subject,
		"sid": sessionID,
	}
	if strings.TrimSpace(organizationID) != "" {
		claims["org_id"] = strings.TrimSpace(organizationID)
	}
	if strings.TrimSpace(role) != "" {
		claims["role"] = strings.TrimSpace(role)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-kid"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("SignedString failed: %v", err)
	}
	return signed
}

func TestDeviceAuthorizationFlowAndRefresh(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	if _, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "deviceuser",
		Email:    "device@example.com",
		Password: "password123",
		Name:     "Device User",
	}); err != nil {
		t.Fatalf("Signup failed: %v", err)
	}

	startResp, err := srv.StartDeviceAuthorization(ctx, &accountv1.StartDeviceAuthorizationRequest{})
	if err != nil {
		t.Fatalf("StartDeviceAuthorization failed: %v", err)
	}
	if startResp.GetDeviceCode() == "" || startResp.GetUserCode() == "" {
		t.Fatalf("expected device + user code, got %#v", startResp)
	}
	if startResp.GetVerificationUri() == "" || startResp.GetVerificationUriComplete() == "" {
		t.Fatalf("expected verification URIs, got %#v", startResp)
	}

	pendingResp, err := srv.PollDeviceAuthorization(ctx, &accountv1.PollDeviceAuthorizationRequest{DeviceCode: startResp.GetDeviceCode()})
	if err != nil {
		t.Fatalf("PollDeviceAuthorization pending failed: %v", err)
	}
	if pendingResp.GetStatus() != accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_PENDING {
		t.Fatalf("expected pending status, got %#v", pendingResp)
	}
	if pendingResp.GetAuth() != nil {
		t.Fatalf("pending device auth should not return tokens: %#v", pendingResp)
	}

	approveResp, err := srv.ApproveDeviceAuthorization(userCtx(ctx, "deviceuser"), &accountv1.ApproveDeviceAuthorizationRequest{UserCode: startResp.GetUserCode()})
	if err != nil {
		t.Fatalf("ApproveDeviceAuthorization failed: %v", err)
	}
	if approveResp.GetUser().GetUsername() != "deviceuser" {
		t.Fatalf("expected approved user deviceuser, got %#v", approveResp)
	}
	assertHomeSliceProvisioned(t, ctx, srv.st, "deviceuser")

	approvedResp, err := srv.PollDeviceAuthorization(ctx, &accountv1.PollDeviceAuthorizationRequest{DeviceCode: startResp.GetDeviceCode()})
	if err != nil {
		t.Fatalf("PollDeviceAuthorization approved failed: %v", err)
	}
	if approvedResp.GetStatus() != accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_APPROVED {
		t.Fatalf("expected approved status, got %#v", approvedResp)
	}
	if approvedResp.GetAuth().GetAccessToken() == "" || approvedResp.GetAuth().GetRefreshToken() == "" {
		t.Fatalf("expected auth tokens, got %#v", approvedResp)
	}
	if approvedResp.GetAuth().GetAccessTokenExpiresAt() == "" || approvedResp.GetAuth().GetRefreshTokenExpiresAt() == "" {
		t.Fatalf("expected token expiry metadata, got %#v", approvedResp)
	}

	if _, err := srv.ListSessions(bearerCtx(ctx, approvedResp.GetAuth().GetAccessToken()), &accountv1.ListSessionsRequest{}); err != nil {
		t.Fatalf("device auth access token should be usable, got %v", err)
	}

	oldAccessToken := approvedResp.GetAuth().GetAccessToken()
	refreshResp, err := srv.RefreshAccessToken(ctx, &accountv1.RefreshAccessTokenRequest{RefreshToken: approvedResp.GetAuth().GetRefreshToken()})
	if err != nil {
		t.Fatalf("RefreshAccessToken failed: %v", err)
	}
	if refreshResp.GetAccessToken() == "" || refreshResp.GetAccessToken() == oldAccessToken {
		t.Fatalf("expected rotated access token, got %#v", refreshResp)
	}
	if refreshResp.GetRefreshToken() != approvedResp.GetAuth().GetRefreshToken() {
		t.Fatalf("expected refresh token to remain stable, got %#v", refreshResp)
	}

	if _, err := srv.ListSessions(bearerCtx(ctx, oldAccessToken), &accountv1.ListSessionsRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected old access token to be invalid after refresh, got %v", err)
	}
	if _, err := srv.ListSessions(bearerCtx(ctx, refreshResp.GetAccessToken()), &accountv1.ListSessionsRequest{}); err != nil {
		t.Fatalf("refreshed access token should be usable, got %v", err)
	}
}

func TestApproveDeviceAuthorizationBackfillsHomeSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	srv := &accountServiceServer{st: st}

	if err := st.CreateUser(ctx, &models.User{
		Username:     "backfilluser",
		Name:         "Backfill User",
		PrimaryEmail: "backfill@example.com",
		PasswordHash: "unused",
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	startResp, err := srv.StartDeviceAuthorization(ctx, &accountv1.StartDeviceAuthorizationRequest{})
	if err != nil {
		t.Fatalf("StartDeviceAuthorization failed: %v", err)
	}
	if _, err := srv.ApproveDeviceAuthorization(userCtx(ctx, "backfilluser"), &accountv1.ApproveDeviceAuthorizationRequest{UserCode: startResp.GetUserCode()}); err != nil {
		t.Fatalf("ApproveDeviceAuthorization failed: %v", err)
	}

	assertHomeSliceProvisioned(t, ctx, st, "backfilluser")
}

func TestDeviceAuthorizationExpiry(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	srv := &accountServiceServer{st: st}

	if _, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "expireuser",
		Email:    "expire@example.com",
		Password: "password123",
		Name:     "Expire User",
	}); err != nil {
		t.Fatalf("Signup failed: %v", err)
	}

	startResp, err := srv.StartDeviceAuthorization(ctx, &accountv1.StartDeviceAuthorizationRequest{})
	if err != nil {
		t.Fatalf("StartDeviceAuthorization failed: %v", err)
	}
	record, err := st.GetDeviceAuthorizationByDeviceCode(ctx, startResp.GetDeviceCode())
	if err != nil {
		t.Fatalf("GetDeviceAuthorizationByDeviceCode failed: %v", err)
	}
	record.ExpiresAt = time.Now().Add(-1 * time.Minute)
	if err := st.UpdateDeviceAuthorization(ctx, record); err != nil {
		t.Fatalf("UpdateDeviceAuthorization failed: %v", err)
	}

	pollResp, err := srv.PollDeviceAuthorization(ctx, &accountv1.PollDeviceAuthorizationRequest{DeviceCode: startResp.GetDeviceCode()})
	if err != nil {
		t.Fatalf("PollDeviceAuthorization failed: %v", err)
	}
	if pollResp.GetStatus() != accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_EXPIRED {
		t.Fatalf("expected expired status, got %#v", pollResp)
	}
	if _, err := srv.ApproveDeviceAuthorization(userCtx(ctx, "expireuser"), &accountv1.ApproveDeviceAuthorizationRequest{UserCode: startResp.GetUserCode()}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for expired approval, got %v", err)
	}
}

func TestAgentKeyManagementLifecycle(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "agentowner",
		Email:    "agentowner@example.com",
		Password: "password123",
		Name:     "Agent Owner",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}
	authCtx := bearerCtx(ctx, signupResp.GetAccessToken())

	listResp, err := srv.ListAgentKeys(authCtx, &accountv1.ListAgentKeysRequest{})
	if err != nil {
		t.Fatalf("ListAgentKeys before create failed: %v", err)
	}
	if len(listResp.GetKeys()) != 0 {
		t.Fatalf("expected no agent keys initially, got %#v", listResp)
	}

	publicKey := make([]byte, ed25519.PublicKeySize)
	for i := range publicKey {
		publicKey[i] = byte(i + 1)
	}
	createdKey, err := srv.CreateAgentKey(authCtx, &accountv1.CreateAgentKeyRequest{
		Name:      "ci-runner",
		Algorithm: "ed25519",
		PublicKey: publicKey,
	})
	if err != nil {
		t.Fatalf("CreateAgentKey failed: %v", err)
	}
	if createdKey.GetId() == "" || createdKey.GetFingerprint() == "" || createdKey.GetRevoked() {
		t.Fatalf("unexpected created key: %#v", createdKey)
	}

	listResp, err = srv.ListAgentKeys(authCtx, &accountv1.ListAgentKeysRequest{})
	if err != nil {
		t.Fatalf("ListAgentKeys after create failed: %v", err)
	}
	if len(listResp.GetKeys()) != 1 || listResp.GetKeys()[0].GetId() != createdKey.GetId() {
		t.Fatalf("unexpected agent key list after create: %#v", listResp)
	}

	if _, err := srv.CreateAgentKey(authCtx, &accountv1.CreateAgentKeyRequest{
		Name:      "duplicate",
		Algorithm: "ed25519",
		PublicKey: publicKey,
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists for duplicate key, got %v", err)
	}

	if _, err := srv.CreateAgentKey(authCtx, &accountv1.CreateAgentKeyRequest{
		Name:      "bad",
		Algorithm: "rsa",
		PublicKey: []byte("bad"),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for invalid algorithm, got %v", err)
	}

	if _, err := srv.DeleteAgentKey(authCtx, &accountv1.DeleteAgentKeyRequest{KeyId: createdKey.GetId()}); err != nil {
		t.Fatalf("DeleteAgentKey failed: %v", err)
	}

	listResp, err = srv.ListAgentKeys(authCtx, &accountv1.ListAgentKeysRequest{})
	if err != nil {
		t.Fatalf("ListAgentKeys after revoke failed: %v", err)
	}
	if len(listResp.GetKeys()) != 1 || !listResp.GetKeys()[0].GetRevoked() || listResp.GetKeys()[0].GetRevokedAt() == "" {
		t.Fatalf("expected revoked key to remain listed, got %#v", listResp)
	}

	if _, err := srv.DeleteAgentKey(authCtx, &accountv1.DeleteAgentKeyRequest{KeyId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for missing key revoke, got %v", err)
	}
}

func createAgentKeyForLogin(t *testing.T, ctx context.Context, srv *accountServiceServer, username, email string) (context.Context, ed25519.PublicKey, ed25519.PrivateKey, *accountv1.AgentKey) {
	t.Helper()

	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: username,
		Email:    email,
		Password: "password123",
		Name:     "Agent Login User",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}
	authCtx := bearerCtx(ctx, signupResp.GetAccessToken())
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	key, err := srv.CreateAgentKey(authCtx, &accountv1.CreateAgentKeyRequest{
		Name:      "agent-key",
		Algorithm: "ed25519",
		PublicKey: publicKey,
	})
	if err != nil {
		t.Fatalf("CreateAgentKey failed: %v", err)
	}
	return authCtx, publicKey, privateKey, key
}

func TestAgentKeyLoginFlow(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	_, _, privateKey, key := createAgentKeyForLogin(t, ctx, srv, "agentlogin", "agentlogin@example.com")

	startResp, err := srv.StartAgentKeyLogin(ctx, &accountv1.StartAgentKeyLoginRequest{
		Fingerprint: key.GetFingerprint(),
	})
	if err != nil {
		t.Fatalf("StartAgentKeyLogin failed: %v", err)
	}
	if startResp.GetChallengeId() == "" || len(startResp.GetChallenge()) == 0 || startResp.GetExpiresAt() == "" {
		t.Fatalf("expected challenge response, got %#v", startResp)
	}

	signature := ed25519.Sign(privateKey, startResp.GetChallenge())
	authResp, err := srv.CompleteAgentKeyLogin(ctx, &accountv1.CompleteAgentKeyLoginRequest{
		ChallengeId: startResp.GetChallengeId(),
		Signature:   signature,
	})
	if err != nil {
		t.Fatalf("CompleteAgentKeyLogin failed: %v", err)
	}
	if authResp.GetAccessToken() == "" || authResp.GetRefreshToken() == "" || authResp.GetSession().GetId() == "" {
		t.Fatalf("expected refreshable auth response, got %#v", authResp)
	}

	session, err := srv.st.GetAuthSession(ctx, authResp.GetSession().GetId())
	if err != nil {
		t.Fatalf("GetAuthSession failed: %v", err)
	}
	if session.AgentKeyID != key.GetId() {
		t.Fatalf("expected session agent key %q, got %#v", key.GetId(), session)
	}

	storedKey, err := srv.st.GetAgentKey(ctx, key.GetId())
	if err != nil {
		t.Fatalf("GetAgentKey failed: %v", err)
	}
	if storedKey.LastUsedAt == nil {
		t.Fatalf("expected last_used_at to be updated, got %#v", storedKey)
	}

	sessionList, err := srv.ListSessions(bearerCtx(ctx, authResp.GetAccessToken()), &accountv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	foundAgentSession := false
	for _, session := range sessionList.GetSessions() {
		if session.GetId() == authResp.GetSession().GetId() {
			foundAgentSession = true
			if session.GetAgentKeyId() != key.GetId() {
				t.Fatalf("expected session agent key id %q, got %#v", key.GetId(), session)
			}
		}
	}
	if !foundAgentSession {
		t.Fatalf("expected to find agent session %#v in %#v", authResp.GetSession(), sessionList)
	}
}

func TestAgentKeyLoginRejectsExpiredChallenge(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	_, _, privateKey, key := createAgentKeyForLogin(t, ctx, srv, "agentexpired", "agentexpired@example.com")
	expiredChallenge := &models.AgentKeyChallenge{
		ChallengeID: "agent-expired-challenge",
		AgentKeyID:  key.GetId(),
		Username:    "agentexpired",
		Challenge:   []byte("expired challenge"),
		ExpiresAt:   time.Now().Add(-time.Minute),
	}
	if err := srv.st.CreateAgentKeyChallenge(ctx, expiredChallenge); err != nil {
		t.Fatalf("CreateAgentKeyChallenge failed: %v", err)
	}

	_, err := srv.CompleteAgentKeyLogin(ctx, &accountv1.CompleteAgentKeyLoginRequest{
		ChallengeId: expiredChallenge.ChallengeID,
		Signature:   ed25519.Sign(privateKey, expiredChallenge.Challenge),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for expired challenge, got %v", err)
	}
}

func TestAgentKeyLoginRejectsReplayAndInvalidSignatureAndRevokedKey(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	authCtx, publicKey, privateKey, key := createAgentKeyForLogin(t, ctx, srv, "agentreplay", "agentreplay@example.com")

	startResp, err := srv.StartAgentKeyLogin(ctx, &accountv1.StartAgentKeyLoginRequest{
		PublicKey: publicKey,
	})
	if err != nil {
		t.Fatalf("StartAgentKeyLogin failed: %v", err)
	}

	otherPublic, otherPrivate, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	_ = otherPublic
	if _, err := srv.CompleteAgentKeyLogin(ctx, &accountv1.CompleteAgentKeyLoginRequest{
		ChallengeId: startResp.GetChallengeId(),
		Signature:   ed25519.Sign(otherPrivate, startResp.GetChallenge()),
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for invalid signature, got %v", err)
	}

	signature := ed25519.Sign(privateKey, startResp.GetChallenge())
	if _, err := srv.CompleteAgentKeyLogin(ctx, &accountv1.CompleteAgentKeyLoginRequest{
		ChallengeId: startResp.GetChallengeId(),
		Signature:   signature,
	}); err != nil {
		t.Fatalf("CompleteAgentKeyLogin first use failed: %v", err)
	}
	if _, err := srv.CompleteAgentKeyLogin(ctx, &accountv1.CompleteAgentKeyLoginRequest{
		ChallengeId: startResp.GetChallengeId(),
		Signature:   signature,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for replayed challenge, got %v", err)
	}

	startResp, err = srv.StartAgentKeyLogin(ctx, &accountv1.StartAgentKeyLoginRequest{
		KeyId: key.GetId(),
	})
	if err != nil {
		t.Fatalf("StartAgentKeyLogin second challenge failed: %v", err)
	}
	if _, err := srv.DeleteAgentKey(authCtx, &accountv1.DeleteAgentKeyRequest{KeyId: key.GetId()}); err != nil {
		t.Fatalf("DeleteAgentKey failed: %v", err)
	}
	if _, err := srv.CompleteAgentKeyLogin(ctx, &accountv1.CompleteAgentKeyLoginRequest{
		ChallengeId: startResp.GetChallengeId(),
		Signature:   ed25519.Sign(privateKey, startResp.GetChallenge()),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for revoked key, got %v", err)
	}
	if _, err := srv.StartAgentKeyLogin(ctx, &accountv1.StartAgentKeyLoginRequest{
		KeyId: key.GetId(),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for revoked key start, got %v", err)
	}
}

func TestDeleteAgentKeyRevokesAttributedSessions(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	authCtx, publicKey, privateKey, key := createAgentKeyForLogin(t, ctx, srv, "agentrevoke", "agentrevoke@example.com")
	startResp, err := srv.StartAgentKeyLogin(ctx, &accountv1.StartAgentKeyLoginRequest{
		PublicKey: publicKey,
	})
	if err != nil {
		t.Fatalf("StartAgentKeyLogin failed: %v", err)
	}
	authResp, err := srv.CompleteAgentKeyLogin(ctx, &accountv1.CompleteAgentKeyLoginRequest{
		ChallengeId: startResp.GetChallengeId(),
		Signature:   ed25519.Sign(privateKey, startResp.GetChallenge()),
	})
	if err != nil {
		t.Fatalf("CompleteAgentKeyLogin failed: %v", err)
	}

	if _, err := srv.DeleteAgentKey(authCtx, &accountv1.DeleteAgentKeyRequest{KeyId: key.GetId()}); err != nil {
		t.Fatalf("DeleteAgentKey failed: %v", err)
	}

	keysResp, err := srv.ListAgentKeys(authCtx, &accountv1.ListAgentKeysRequest{})
	if err != nil {
		t.Fatalf("ListAgentKeys failed: %v", err)
	}
	if len(keysResp.GetKeys()) != 1 || !keysResp.GetKeys()[0].GetRevoked() || keysResp.GetKeys()[0].GetLastUsedAt() == "" {
		t.Fatalf("expected revoked key with usage metadata, got %#v", keysResp)
	}

	if _, err := srv.ListSessions(bearerCtx(ctx, authResp.GetAccessToken()), &accountv1.ListSessionsRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected revoked agent-key access token to be rejected, got %v", err)
	}
	if _, err := srv.RefreshAccessToken(ctx, &accountv1.RefreshAccessTokenRequest{RefreshToken: authResp.GetRefreshToken()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected revoked agent-key refresh token to be rejected, got %v", err)
	}
	if _, err := srv.StartAgentKeyLogin(ctx, &accountv1.StartAgentKeyLoginRequest{KeyId: key.GetId()}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected revoked key login start to fail, got %v", err)
	}
}

func TestSignupWithAgentKeyFlow(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	authResp, err := srv.SignupWithAgentKey(ctx, &accountv1.SignupWithAgentKeyRequest{
		Username:  "agentsignup",
		Email:     "agentsignup@example.com",
		Name:      "Agent Signup",
		Algorithm: "ed25519",
		PublicKey: publicKey,
		KeyName:   "codex-runner",
	})
	if err != nil {
		t.Fatalf("SignupWithAgentKey failed: %v", err)
	}
	if authResp.GetAccessToken() == "" || authResp.GetRefreshToken() == "" || authResp.GetSession().GetId() == "" {
		t.Fatalf("expected refreshable auth response, got %#v", authResp)
	}
	assertHomeSliceProvisioned(t, ctx, srv.st, "agentsignup")

	user, err := srv.st.GetUser(ctx, "agentsignup")
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if user.PrimaryEmail != "agentsignup@example.com" || user.AccountID == "" {
		t.Fatalf("unexpected user: %#v", user)
	}
	account, err := srv.st.GetAccount(ctx, user.AccountID)
	if err != nil {
		t.Fatalf("GetAccount failed: %v", err)
	}
	if account.OwnerMode != models.AccountOwnerModeAgentOnly || account.ClaimState != models.AccountClaimStateUnclaimed {
		t.Fatalf("unexpected account: %#v", account)
	}

	keys, err := srv.st.ListAgentKeysByUser(ctx, "agentsignup")
	if err != nil {
		t.Fatalf("ListAgentKeysByUser failed: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "codex-runner" {
		t.Fatalf("expected one created key, got %#v", keys)
	}

	session, err := srv.st.GetAuthSession(ctx, authResp.GetSession().GetId())
	if err != nil {
		t.Fatalf("GetAuthSession failed: %v", err)
	}
	if session.AgentKeyID != keys[0].KeyID {
		t.Fatalf("expected session agent key %q, got %#v", keys[0].KeyID, session)
	}
}

func TestSignupWithAgentKeyRejectsDuplicateUsername(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	if _, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "dupuser",
		Email:    "dupuser@example.com",
		Password: "password123",
		Name:     "Dup User",
	}); err != nil {
		t.Fatalf("Signup failed: %v", err)
	}

	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	_, err = srv.SignupWithAgentKey(ctx, &accountv1.SignupWithAgentKeyRequest{
		Username:  "dupuser",
		Email:     "other@example.com",
		Name:      "Duplicate",
		Algorithm: "ed25519",
		PublicKey: publicKey,
		KeyName:   "dup-key",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists for duplicate username, got %v", err)
	}
}

func TestSignupWithAgentKeyRejectsDuplicateKey(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	if _, err := srv.SignupWithAgentKey(ctx, &accountv1.SignupWithAgentKeyRequest{
		Username:  "agentdup1",
		Email:     "agentdup1@example.com",
		Name:      "Agent Dup One",
		Algorithm: "ed25519",
		PublicKey: publicKey,
		KeyName:   "runner-1",
	}); err != nil {
		t.Fatalf("first SignupWithAgentKey failed: %v", err)
	}

	_, err = srv.SignupWithAgentKey(ctx, &accountv1.SignupWithAgentKeyRequest{
		Username:  "agentdup2",
		Email:     "agentdup2@example.com",
		Name:      "Agent Dup Two",
		Algorithm: "ed25519",
		PublicKey: publicKey,
		KeyName:   "runner-2",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists for duplicate key, got %v", err)
	}
	if _, err := srv.st.GetUser(ctx, "agentdup2"); err != storage.ErrEntryNotFound {
		t.Fatalf("expected duplicate-key signup rollback, got %v", err)
	}
}

func TestSignupWithAgentKeyRejectsInvalidPublicKey(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	_, err := srv.SignupWithAgentKey(ctx, &accountv1.SignupWithAgentKeyRequest{
		Username:  "badagent",
		Email:     "badagent@example.com",
		Name:      "Bad Agent",
		Algorithm: "ed25519",
		PublicKey: []byte("not-an-ed25519-key"),
		KeyName:   "bad-key",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for invalid public key, got %v", err)
	}
}

func TestUsersAPIGetUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "charlie",
		Email:    "charlie@example.com",
		Password: "charlie123",
		Name:     "Charlie",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}
	authCtx := bearerCtx(ctx, signupResp.GetAccessToken())

	me, err := srv.GetMe(authCtx, &accountv1.GetMeRequest{})
	if err != nil {
		t.Fatalf("GetMe failed: %v", err)
	}
	if me.GetUsername() != "charlie" {
		t.Fatalf("GetMe username mismatch: %#v", me)
	}

	updated, err := srv.UpdateMe(authCtx, &accountv1.UpdateMeRequest{
		Name:         "Charlie Updated",
		PrimaryEmail: "charlie+new@example.com",
	})
	if err != nil {
		t.Fatalf("UpdateMe failed: %v", err)
	}
	if updated.GetName() != "Charlie Updated" || updated.GetPrimaryEmail() != "charlie+new@example.com" {
		t.Fatalf("UpdateMe mismatch: %#v", updated)
	}

	otherSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "dave",
		Email:    "dave@example.com",
		Password: "davepass1",
		Name:     "Dave",
	})
	if err != nil {
		t.Fatalf("Second signup failed: %v", err)
	}
	otherCtx := bearerCtx(ctx, otherSignup.GetAccessToken())
	otherView, err := srv.GetUser(otherCtx, &accountv1.GetUserRequest{UserId: "charlie"})
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if otherView.GetPrimaryEmail() != "" {
		t.Fatalf("GetUser should not expose another user's email: %#v", otherView)
	}

	if _, err := srv.DeleteMe(authCtx, &accountv1.DeleteMeRequest{}); err != nil {
		t.Fatalf("DeleteMe failed: %v", err)
	}
	if _, err := srv.GetMe(authCtx, &accountv1.GetMeRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected token to be invalid after delete, got %v", err)
	}
	if _, err := srv.Login(ctx, &accountv1.LoginRequest{Username: "charlie", Password: "charlie123"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected deleted account login to fail, got %v", err)
	}
}

func TestDeleteMeFailsForOrgOwner(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	srv := &accountServiceServer{st: st}

	signupResp, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "owner",
		Email:    "owner@example.com",
		Password: "ownerpass1",
		Name:     "Owner",
	})
	if err != nil {
		t.Fatalf("Signup failed: %v", err)
	}

	if err := st.CreateOrganization(ctx, &models.Organization{
		Slug:      "acme",
		Name:      "Acme",
		CreatedBy: "owner",
	}); err != nil {
		t.Fatalf("CreateOrganization failed: %v", err)
	}
	if err := st.AddOrganizationMember(ctx, &models.OrganizationMember{
		OrgSlug:  "acme",
		Username: "owner",
		Role:     models.OrganizationRoleOwner,
	}); err != nil {
		t.Fatalf("AddOrganizationMember failed: %v", err)
	}

	if _, err := srv.DeleteMe(bearerCtx(ctx, signupResp.GetAccessToken()), &accountv1.DeleteMeRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for org owner delete, got %v", err)
	}
}

func TestOrganizationsCRUDAndPermissions(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	ownerSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "orgowner",
		Email:    "owner@example.com",
		Password: "ownerpass1",
		Name:     "Org Owner",
	})
	if err != nil {
		t.Fatalf("owner signup failed: %v", err)
	}
	memberSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "orgmember",
		Email:    "member@example.com",
		Password: "memberpass1",
		Name:     "Org Member",
	})
	if err != nil {
		t.Fatalf("member signup failed: %v", err)
	}

	ownerCtx := bearerCtx(ctx, ownerSignup.GetAccessToken())
	memberCtx := bearerCtx(ctx, memberSignup.GetAccessToken())

	created, err := srv.CreateOrganization(ownerCtx, &accountv1.CreateOrganizationRequest{
		Slug: "acme-labs",
		Name: "Acme Labs",
	})
	if err != nil {
		t.Fatalf("CreateOrganization failed: %v", err)
	}
	if created.GetSlug() != "acme-labs" || created.GetOwnerUserId() != "orgowner" {
		t.Fatalf("unexpected created organization: %#v", created)
	}

	ownerOrgs, err := srv.ListOrganizations(ownerCtx, &accountv1.ListOrganizationsRequest{})
	if err != nil {
		t.Fatalf("owner ListOrganizations failed: %v", err)
	}
	if len(ownerOrgs.GetOrganizations()) != 1 || ownerOrgs.GetOrganizations()[0].GetSlug() != "acme-labs" {
		t.Fatalf("unexpected owner org listing: %#v", ownerOrgs)
	}
	memberOrgs, err := srv.ListOrganizations(memberCtx, &accountv1.ListOrganizationsRequest{})
	if err != nil {
		t.Fatalf("member ListOrganizations failed: %v", err)
	}
	if len(memberOrgs.GetOrganizations()) != 0 {
		t.Fatalf("member should not see owner orgs: %#v", memberOrgs)
	}

	if _, err := srv.GetOrganization(ownerCtx, &accountv1.GetOrganizationRequest{OrgId: "acme-labs"}); err != nil {
		t.Fatalf("owner GetOrganization failed: %v", err)
	}
	if _, err := srv.GetOrganization(memberCtx, &accountv1.GetOrganizationRequest{OrgId: "acme-labs"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for non-member get, got %v", err)
	}

	if _, err := srv.UpdateOrganization(memberCtx, &accountv1.UpdateOrganizationRequest{
		OrgId: "acme-labs",
		Name:  "Acme Labs Updated",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for non-owner update, got %v", err)
	}
	updated, err := srv.UpdateOrganization(ownerCtx, &accountv1.UpdateOrganizationRequest{
		OrgId: "acme-labs",
		Name:  "Acme Labs Updated",
	})
	if err != nil {
		t.Fatalf("owner UpdateOrganization failed: %v", err)
	}
	if updated.GetName() != "Acme Labs Updated" {
		t.Fatalf("unexpected updated org: %#v", updated)
	}

	if _, err := srv.CreateOrganization(ownerCtx, &accountv1.CreateOrganizationRequest{
		Slug: "orgmember",
		Name: "Collision Org",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists for user/org slug collision, got %v", err)
	}

	if _, err := srv.DeleteOrganization(memberCtx, &accountv1.DeleteOrganizationRequest{OrgId: "acme-labs"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for non-owner delete, got %v", err)
	}
	if _, err := srv.DeleteOrganization(ownerCtx, &accountv1.DeleteOrganizationRequest{OrgId: "acme-labs"}); err != nil {
		t.Fatalf("owner DeleteOrganization failed: %v", err)
	}
	if _, err := srv.GetOrganization(ownerCtx, &accountv1.GetOrganizationRequest{OrgId: "acme-labs"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound after org delete, got %v", err)
	}
}

func TestInvitesAndMembershipManagement(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	ownerSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "ownerx",
		Email:    "ownerx@example.com",
		Password: "ownerpass1",
		Name:     "Owner X",
	})
	if err != nil {
		t.Fatalf("owner signup failed: %v", err)
	}
	adminSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "adminx",
		Email:    "adminx@example.com",
		Password: "adminpass1",
		Name:     "Admin X",
	})
	if err != nil {
		t.Fatalf("admin signup failed: %v", err)
	}
	userSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "userx",
		Email:    "userx@example.com",
		Password: "userpass11",
		Name:     "User X",
	})
	if err != nil {
		t.Fatalf("user signup failed: %v", err)
	}

	ownerCtx := bearerCtx(ctx, ownerSignup.GetAccessToken())
	adminCtx := bearerCtx(ctx, adminSignup.GetAccessToken())
	userCtx := bearerCtx(ctx, userSignup.GetAccessToken())

	if _, err := srv.CreateOrganization(ownerCtx, &accountv1.CreateOrganizationRequest{
		Slug: "teamx",
		Name: "Team X",
	}); err != nil {
		t.Fatalf("CreateOrganization failed: %v", err)
	}

	adminInvite, err := srv.CreateInvite(ownerCtx, &accountv1.CreateInviteRequest{
		OrgId:       "teamx",
		TargetEmail: "adminx@example.com",
		Role:        accountv1.Role_ROLE_ADMIN,
	})
	if err != nil {
		t.Fatalf("CreateInvite admin failed: %v", err)
	}
	if adminInvite.GetStatus() != accountv1.InviteStatus_INVITE_STATUS_PENDING {
		t.Fatalf("expected pending invite, got %#v", adminInvite)
	}
	if _, err := srv.AcceptInvite(adminCtx, &accountv1.AcceptInviteRequest{
		OrgId:    "teamx",
		InviteId: adminInvite.GetId(),
	}); err != nil {
		t.Fatalf("AcceptInvite admin failed: %v", err)
	}

	if _, err := srv.ListMembers(adminCtx, &accountv1.ListMembersRequest{OrgId: "teamx"}); err != nil {
		t.Fatalf("admin ListMembers failed: %v", err)
	}

	userInvite, err := srv.CreateInvite(adminCtx, &accountv1.CreateInviteRequest{
		OrgId:       "teamx",
		TargetEmail: "userx@example.com",
		Role:        accountv1.Role_ROLE_USER,
	})
	if err != nil {
		t.Fatalf("CreateInvite user failed: %v", err)
	}
	if _, err := srv.CreateInvite(ownerCtx, &accountv1.CreateInviteRequest{
		OrgId:       "teamx",
		TargetEmail: "userx@example.com",
		Role:        accountv1.Role_ROLE_USER,
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists for duplicate pending invite, got %v", err)
	}
	if _, err := srv.AcceptInvite(ownerCtx, &accountv1.AcceptInviteRequest{
		OrgId:    "teamx",
		InviteId: userInvite.GetId(),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for email-mismatched invite accept, got %v", err)
	}
	if _, err := srv.AcceptInvite(userCtx, &accountv1.AcceptInviteRequest{
		OrgId:    "teamx",
		InviteId: userInvite.GetId(),
	}); err != nil {
		t.Fatalf("AcceptInvite user failed: %v", err)
	}

	membersResp, err := srv.ListMembers(ownerCtx, &accountv1.ListMembersRequest{OrgId: "teamx"})
	if err != nil {
		t.Fatalf("owner ListMembers failed: %v", err)
	}
	if len(membersResp.GetMembers()) != 3 {
		t.Fatalf("expected 3 members after invite acceptance, got %#v", membersResp)
	}

	if _, err := srv.UpdateMember(ownerCtx, &accountv1.UpdateMemberRequest{
		OrgId:    "teamx",
		MemberId: "adminx",
		Role:     accountv1.Role_ROLE_USER,
	}); err != nil {
		t.Fatalf("UpdateMember demote admin failed: %v", err)
	}
	if _, err := srv.CreateInvite(adminCtx, &accountv1.CreateInviteRequest{
		OrgId:       "teamx",
		TargetEmail: "newuser@example.com",
		Role:        accountv1.Role_ROLE_USER,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for non-admin invite creation, got %v", err)
	}

	if _, err := srv.UpdateMember(ownerCtx, &accountv1.UpdateMemberRequest{
		OrgId:    "teamx",
		MemberId: "ownerx",
		Role:     accountv1.Role_ROLE_USER,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for owner role change, got %v", err)
	}
	if _, err := srv.DeleteMember(ownerCtx, &accountv1.DeleteMemberRequest{
		OrgId:    "teamx",
		MemberId: "ownerx",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for owner removal, got %v", err)
	}
	if _, err := srv.DeleteMember(ownerCtx, &accountv1.DeleteMemberRequest{
		OrgId:    "teamx",
		MemberId: "userx",
	}); err != nil {
		t.Fatalf("DeleteMember user failed: %v", err)
	}
}

func TestTeamsCRUDAndMembershipAuthorization(t *testing.T) {
	ctx := context.Background()
	srv := &accountServiceServer{st: storage.NewInMemoryStorage()}

	ownerSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "teamowner",
		Email:    "teamowner@example.com",
		Password: "ownerpass1",
		Name:     "Team Owner",
	})
	if err != nil {
		t.Fatalf("owner signup failed: %v", err)
	}
	adminSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "teamadmin",
		Email:    "teamadmin@example.com",
		Password: "adminpass1",
		Name:     "Team Admin",
	})
	if err != nil {
		t.Fatalf("admin signup failed: %v", err)
	}
	userSignup, err := srv.Signup(ctx, &accountv1.SignupRequest{
		Username: "teamuser",
		Email:    "teamuser@example.com",
		Password: "userpass11",
		Name:     "Team User",
	})
	if err != nil {
		t.Fatalf("user signup failed: %v", err)
	}

	ownerCtx := bearerCtx(ctx, ownerSignup.GetAccessToken())
	adminCtx := bearerCtx(ctx, adminSignup.GetAccessToken())
	userCtx := bearerCtx(ctx, userSignup.GetAccessToken())

	if _, err := srv.CreateOrganization(ownerCtx, &accountv1.CreateOrganizationRequest{
		Slug: "acme-teams",
		Name: "Acme Teams",
	}); err != nil {
		t.Fatalf("CreateOrganization failed: %v", err)
	}

	adminInvite, err := srv.CreateInvite(ownerCtx, &accountv1.CreateInviteRequest{
		OrgId:       "acme-teams",
		TargetEmail: "teamadmin@example.com",
		Role:        accountv1.Role_ROLE_ADMIN,
	})
	if err != nil {
		t.Fatalf("CreateInvite admin failed: %v", err)
	}
	if _, err := srv.AcceptInvite(adminCtx, &accountv1.AcceptInviteRequest{
		OrgId:    "acme-teams",
		InviteId: adminInvite.GetId(),
	}); err != nil {
		t.Fatalf("AcceptInvite admin failed: %v", err)
	}
	userInvite, err := srv.CreateInvite(ownerCtx, &accountv1.CreateInviteRequest{
		OrgId:       "acme-teams",
		TargetEmail: "teamuser@example.com",
		Role:        accountv1.Role_ROLE_USER,
	})
	if err != nil {
		t.Fatalf("CreateInvite user failed: %v", err)
	}
	if _, err := srv.AcceptInvite(userCtx, &accountv1.AcceptInviteRequest{
		OrgId:    "acme-teams",
		InviteId: userInvite.GetId(),
	}); err != nil {
		t.Fatalf("AcceptInvite user failed: %v", err)
	}

	createdTeam, err := srv.CreateTeam(adminCtx, &accountv1.CreateTeamRequest{
		OrgId: "acme-teams",
		Name:  "Platform",
	})
	if err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
	}
	if createdTeam.GetId() == "" {
		t.Fatalf("expected created team id, got %#v", createdTeam)
	}

	listResp, err := srv.ListTeams(ownerCtx, &accountv1.ListTeamsRequest{OrgId: "acme-teams"})
	if err != nil {
		t.Fatalf("ListTeams owner failed: %v", err)
	}
	if len(listResp.GetTeams()) != 1 {
		t.Fatalf("expected one team, got %#v", listResp)
	}
	if _, err := srv.ListTeams(userCtx, &accountv1.ListTeamsRequest{OrgId: "acme-teams"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for non-admin list teams, got %v", err)
	}

	if _, err := srv.AddTeamMember(adminCtx, &accountv1.AddTeamMemberRequest{
		OrgId:    "acme-teams",
		TeamId:   createdTeam.GetId(),
		MemberId: "teamuser",
	}); err != nil {
		t.Fatalf("AddTeamMember failed: %v", err)
	}
	if _, err := srv.AddTeamMember(userCtx, &accountv1.AddTeamMemberRequest{
		OrgId:    "acme-teams",
		TeamId:   createdTeam.GetId(),
		MemberId: "teamowner",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for non-admin add team member, got %v", err)
	}

	updatedTeam, err := srv.UpdateTeam(adminCtx, &accountv1.UpdateTeamRequest{
		OrgId:  "acme-teams",
		TeamId: createdTeam.GetId(),
		Name:   "Platform Updated",
	})
	if err != nil {
		t.Fatalf("UpdateTeam failed: %v", err)
	}
	if updatedTeam.GetName() != "Platform Updated" {
		t.Fatalf("unexpected updated team: %#v", updatedTeam)
	}
	if _, err := srv.UpdateTeam(userCtx, &accountv1.UpdateTeamRequest{
		OrgId:  "acme-teams",
		TeamId: createdTeam.GetId(),
		Name:   "Nope",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for non-admin update team, got %v", err)
	}

	if _, err := srv.DeleteTeamMember(adminCtx, &accountv1.DeleteTeamMemberRequest{
		OrgId:    "acme-teams",
		TeamId:   createdTeam.GetId(),
		MemberId: "teamuser",
	}); err != nil {
		t.Fatalf("DeleteTeamMember failed: %v", err)
	}
	if _, err := srv.DeleteTeam(adminCtx, &accountv1.DeleteTeamRequest{
		OrgId:  "acme-teams",
		TeamId: createdTeam.GetId(),
	}); err != nil {
		t.Fatalf("DeleteTeam failed: %v", err)
	}
}
