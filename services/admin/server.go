package adminservice

import (
	"context"
	"fmt"
	"strings"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type adminServiceServer struct {
	adminv1.UnimplementedAdminServiceServer
	storage storage.Storage
}

func newAdminServiceServer(st storage.Storage) *adminServiceServer {
	return &adminServiceServer{
		storage: st,
	}
}

// RegisterGRPCServer registers the admin service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	adminv1.RegisterAdminServiceServer(srv, newAdminServiceServer(st))
}

// NewGRPCServer constructs a gRPC server for the admin service using the provided storage backend.
func NewGRPCServer(st storage.Storage) *grpc.Server {
	srv := grpc.NewServer()
	RegisterGRPCServer(srv, st)
	return srv
}

// NewService constructs the admin service implementation for use without gRPC.
func NewService(st storage.Storage) adminv1.AdminServiceServer {
	return newAdminServiceServer(st)
}

func backfillResultToProto(result *homeslice.BackfillResult) *adminv1.HomeSliceBackfillResult {
	if result == nil {
		return nil
	}
	return &adminv1.HomeSliceBackfillResult{
		Username:          result.Username,
		HomeSliceId:       result.HomeSliceID,
		Created:           result.Created,
		Seeded:            result.Seeded,
		FilesCopied:       int32(result.FilesCopied),
		DirectoriesCopied: int32(result.DirectoriesCopied),
	}
}

func (s *adminServiceServer) GetAdminStatus(ctx context.Context, req *adminv1.AdminStatusRequest) (*adminv1.AdminStatusResponse, error) {
	claims, err := s.requireClerkAdminClaims(ctx)
	if err != nil {
		return nil, err
	}
	adminConfigured, isAdmin, primaryEmail := adminStatusForEmail(claims.Email)
	username := ""
	if user, userErr := s.loadUserForClerkAdminClaims(ctx, claims); userErr == nil && user != nil {
		username = user.Username
	} else if status.Code(userErr) != codes.FailedPrecondition {
		return nil, userErr
	}
	return &adminv1.AdminStatusResponse{
		AdminConfigured: adminConfigured,
		IsAdmin:         isAdmin,
		Username:        username,
		PrimaryEmail:    primaryEmail,
	}, nil
}

func (s *adminServiceServer) DeleteUser(ctx context.Context, req *adminv1.DeleteUserRequest) (*adminv1.DeleteUserResponse, error) {
	adminUser, err := s.requireAdminUser(ctx)
	if err != nil {
		return nil, err
	}
	username := strings.TrimSpace(req.GetUsername())
	if !auth.ValidateUsername(username) {
		return nil, status.Error(codes.InvalidArgument, "invalid username")
	}
	target, err := s.storage.GetUser(ctx, username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	return s.deleteUser(ctx, adminUser, target)
}

func (s *adminServiceServer) DeleteUserByEmail(ctx context.Context, req *adminv1.DeleteUserByEmailRequest) (*adminv1.DeleteUserResponse, error) {
	adminUser, err := s.requireAdminUser(ctx)
	if err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(req.GetEmail()))
	if email == "" || !strings.Contains(email, "@") {
		return nil, status.Error(codes.InvalidArgument, "invalid email")
	}
	target, err := s.storage.GetUserByEmail(ctx, email)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	return s.deleteUser(ctx, adminUser, target)
}

func (s *adminServiceServer) deleteUser(ctx context.Context, adminUser, target *models.User) (*adminv1.DeleteUserResponse, error) {
	if target == nil || !auth.ValidateUsername(target.Username) {
		return nil, status.Error(codes.InvalidArgument, "invalid user")
	}
	if adminUser != nil && target.Username == adminUser.Username {
		return nil, status.Error(codes.FailedPrecondition, "admin users cannot delete their own account")
	}
	orgs, err := s.storage.ListOrganizationsForUser(ctx, target.Username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to validate user organizations")
	}
	for _, org := range orgs {
		if org != nil && org.CreatedBy == target.Username {
			return nil, status.Error(codes.FailedPrecondition, "cannot delete user while they own organizations")
		}
	}

	sessions, _ := s.storage.ListAuthSessionsByUser(ctx, target.Username)
	agentKeys, _ := s.storage.ListAgentKeysByUser(ctx, target.Username)

	ownedSlices, err := s.storage.ListSlicesByOwner(ctx, target.Username, int(^uint(0)>>1), 0)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list user slices")
	}
	deletedSlices := 0
	for _, slice := range ownedSlices {
		if slice == nil || slice.IsRoot {
			continue
		}
		if err := s.storage.DeleteSlice(ctx, slice.ID); err != nil {
			if err == storage.ErrSliceNotFound {
				continue
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete slice %s: %v", slice.ID, err))
		}
		deletedSlices++
	}

	if err := s.storage.DeleteUser(ctx, target.Username); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete user: %v", err))
	}
	return &adminv1.DeleteUserResponse{
		Username:         target.Username,
		PrimaryEmail:     strings.ToLower(strings.TrimSpace(target.PrimaryEmail)),
		DeletedSlices:    int32(deletedSlices),
		DeletedSessions:  int32(len(sessions)),
		DeletedAgentKeys: int32(len(agentKeys)),
	}, nil
}

func (s *adminServiceServer) BackfillHomeSlices(ctx context.Context, req *adminv1.BackfillHomeSlicesRequest) (*adminv1.BackfillHomeSlicesResponse, error) {
	if _, err := s.requireAdminUser(ctx); err != nil {
		return nil, err
	}

	var users []*models.User
	if len(req.GetUsernames()) > 0 {
		seen := make(map[string]struct{}, len(req.GetUsernames()))
		users = make([]*models.User, 0, len(req.GetUsernames()))
		for _, rawUsername := range req.GetUsernames() {
			username := strings.TrimSpace(rawUsername)
			if username == "" {
				continue
			}
			if _, ok := seen[username]; ok {
				continue
			}
			seen[username] = struct{}{}
			user, err := s.storage.GetUser(ctx, username)
			if err != nil {
				if err == storage.ErrEntryNotFound {
					return nil, status.Error(codes.NotFound, fmt.Sprintf("user not found: %s", username))
				}
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load user %s: %v", username, err))
			}
			users = append(users, user)
		}
	} else {
		listedUsers, err := s.storage.ListUsers(ctx, int(req.GetLimit()), int(req.GetOffset()))
		if err != nil {
			if err == storage.ErrInvalidInput {
				return nil, status.Error(codes.InvalidArgument, "invalid limit or offset")
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list users: %v", err))
		}
		users = listedUsers
	}

	response := &adminv1.BackfillHomeSlicesResponse{
		Results: make([]*adminv1.HomeSliceBackfillResult, 0, len(users)),
	}
	for _, user := range users {
		result, err := homeslice.BackfillUserHomeSlice(ctx, s.storage, user.Username)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to backfill home slice for %s: %v", user.Username, err))
		}
		response.Results = append(response.Results, backfillResultToProto(result))
		response.Processed++
		if result.Created {
			response.Created++
		}
		if result.Seeded {
			response.Seeded++
		}
	}

	return response, nil
}
