package accountservice

import (
	"context"
	"testing"

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
