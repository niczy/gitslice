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
