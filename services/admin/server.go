package adminservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authresolver"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/sliceconfig"
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

var orgSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)

func slugifyOrg(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.TrimRight(out[:40], "-")
	}
	return out
}

func userToProto(user *models.User) *adminv1.UserInfo {
	if user == nil {
		return nil
	}
	return &adminv1.UserInfo{
		Username:  user.Username,
		CreatedAt: user.CreatedAt.Unix(),
	}
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

func orgToProto(org *models.Organization) *adminv1.OrganizationInfo {
	if org == nil {
		return nil
	}
	return &adminv1.OrganizationInfo{
		Slug:      org.Slug,
		Name:      org.Name,
		CreatedBy: org.CreatedBy,
		CreatedAt: org.CreatedAt.Unix(),
	}
}

func environmentToProto(env *models.Environment) *adminv1.EnvironmentInfo {
	if env == nil {
		return nil
	}
	return &adminv1.EnvironmentInfo{
		Name:           env.Name,
		DisplayName:    env.DisplayName,
		Provider:       env.Provider,
		ProviderId:     env.ProviderID,
		ProviderConfig: copyStringMap(env.ProviderConfig),
		Region:         env.Region,
		CreatedBy:      env.CreatedBy,
		CreatedAt:      env.CreatedAt.Unix(),
		UpdatedAt:      env.UpdatedAt.Unix(),
	}
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (s *adminServiceServer) requireUser(ctx context.Context) (string, *models.User, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return "", nil, err
	}
	user, err := s.storage.EnsureUser(ctx, username)
	if err != nil {
		return "", nil, status.Error(codes.InvalidArgument, "invalid user")
	}
	return username, user, nil
}

func (s *adminServiceServer) requireUsername(ctx context.Context) (string, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.storage)
	if err != nil {
		return "", err
	}
	return identity.Username, nil
}

func (s *adminServiceServer) optionalUsername(ctx context.Context) (string, error) {
	identity, err := authresolver.OptionalGRPCIdentity(ctx, s.storage)
	if err != nil {
		return "", err
	}
	if identity == nil {
		return "", nil
	}
	return identity.Username, nil
}

func (s *adminServiceServer) Login(ctx context.Context, req *adminv1.LoginRequest) (*adminv1.MeResponse, error) {
	username := strings.TrimSpace(req.GetUsername())
	if !auth.ValidateUsername(username) {
		return nil, status.Error(codes.InvalidArgument, "invalid username")
	}

	user, err := s.storage.EnsureUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid username")
	}
	orgs, err := s.storage.ListOrganizationsForUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list organizations")
	}

	outOrgs := make([]*adminv1.OrganizationInfo, 0, len(orgs))
	for _, org := range orgs {
		outOrgs = append(outOrgs, orgToProto(org))
	}
	return &adminv1.MeResponse{
		User:          userToProto(user),
		Organizations: outOrgs,
		Now:           time.Now().Unix(),
	}, nil
}

func (s *adminServiceServer) Me(ctx context.Context, req *adminv1.MeRequest) (*adminv1.MeResponse, error) {
	username, user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	orgs, err := s.storage.ListOrganizationsForUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list organizations")
	}
	outOrgs := make([]*adminv1.OrganizationInfo, 0, len(orgs))
	for _, org := range orgs {
		outOrgs = append(outOrgs, orgToProto(org))
	}
	return &adminv1.MeResponse{
		User:          userToProto(user),
		Organizations: outOrgs,
		Now:           time.Now().Unix(),
	}, nil
}

func (s *adminServiceServer) ListOrganizations(ctx context.Context, req *adminv1.ListOrganizationsRequest) (*adminv1.ListOrganizationsResponse, error) {
	username, _, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	orgs, err := s.storage.ListOrganizationsForUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list organizations")
	}
	outOrgs := make([]*adminv1.OrganizationInfo, 0, len(orgs))
	for _, org := range orgs {
		outOrgs = append(outOrgs, orgToProto(org))
	}
	return &adminv1.ListOrganizationsResponse{Organizations: outOrgs}, nil
}

func (s *adminServiceServer) CreateOrganization(ctx context.Context, req *adminv1.CreateOrganizationRequest) (*adminv1.CreateOrganizationResponse, error) {
	username, _, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" || len(name) > 80 {
		return nil, status.Error(codes.InvalidArgument, "invalid org name")
	}
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		slug = slugifyOrg(name)
	}
	if !orgSlugRE.MatchString(slug) {
		return nil, status.Error(codes.InvalidArgument, "invalid org slug")
	}

	base := slug
	for i := 0; i < 100; i++ {
		if _, lookupErr := s.storage.GetOrganization(ctx, slug); lookupErr != nil {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i+2)
	}

	org := &models.Organization{
		Slug:      slug,
		Name:      name,
		CreatedBy: username,
	}
	if err := s.storage.CreateOrganization(ctx, org); err != nil {
		return nil, status.Error(codes.AlreadyExists, "organization already exists")
	}
	_ = s.storage.AddOrganizationMember(ctx, &models.OrganizationMember{
		OrgSlug:   slug,
		Username:  username,
		Role:      models.OrganizationRoleOwner,
		CreatedAt: time.Now(),
	})
	created, err := s.storage.GetOrganization(ctx, slug)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	return &adminv1.CreateOrganizationResponse{Organization: orgToProto(created)}, nil
}

func (s *adminServiceServer) ListEnvironments(ctx context.Context, req *adminv1.ListEnvironmentsRequest) (*adminv1.ListEnvironmentsResponse, error) {
	if _, _, err := s.requireUser(ctx); err != nil {
		return nil, err
	}

	limit := int(req.GetLimit())
	offset := int(req.GetOffset())
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid offset")
	}

	envs, err := s.storage.ListEnvironments(ctx, limit, offset)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list environments")
	}
	out := make([]*adminv1.EnvironmentInfo, 0, len(envs))
	for _, env := range envs {
		out = append(out, environmentToProto(env))
	}
	return &adminv1.ListEnvironmentsResponse{Environments: out}, nil
}

func (s *adminServiceServer) CreateEnvironment(ctx context.Context, req *adminv1.CreateEnvironmentRequest) (*adminv1.EnvironmentInfo, error) {
	username, _, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	if err := storage.ValidateEnvironmentProviderConfig(req.GetProvider(), req.GetProviderConfig()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	env := &models.Environment{
		Name:           name,
		DisplayName:    req.GetDisplayName(),
		Provider:       req.GetProvider(),
		ProviderID:     req.GetProviderId(),
		ProviderConfig: copyStringMap(req.GetProviderConfig()),
		Region:         req.GetRegion(),
		CreatedBy:      username,
	}
	if err := s.storage.CreateEnvironment(ctx, env); err != nil {
		switch err {
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid environment")
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "environment already exists")
		default:
			return nil, status.Error(codes.Internal, "failed to create environment")
		}
	}
	created, err := s.storage.GetEnvironment(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load environment")
	}
	return environmentToProto(created), nil
}

func (s *adminServiceServer) GetEnvironment(ctx context.Context, req *adminv1.GetEnvironmentRequest) (*adminv1.EnvironmentInfo, error) {
	if _, _, err := s.requireUser(ctx); err != nil {
		return nil, err
	}
	env, err := s.storage.GetEnvironment(ctx, strings.TrimSpace(req.GetName()))
	if err != nil {
		switch err {
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid environment name")
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "environment not found")
		default:
			return nil, status.Error(codes.Internal, "failed to load environment")
		}
	}
	return environmentToProto(env), nil
}

func (s *adminServiceServer) UpdateEnvironment(ctx context.Context, req *adminv1.UpdateEnvironmentRequest) (*adminv1.EnvironmentInfo, error) {
	if _, _, err := s.requireUser(ctx); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	current, err := s.storage.GetEnvironment(ctx, name)
	if err != nil {
		switch err {
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid environment name")
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "environment not found")
		default:
			return nil, status.Error(codes.Internal, "failed to load environment")
		}
	}
	current.DisplayName = req.GetDisplayName()
	current.Provider = req.GetProvider()
	current.ProviderID = req.GetProviderId()
	if err := storage.ValidateEnvironmentProviderConfig(current.Provider, req.GetProviderConfig()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	current.ProviderConfig = copyStringMap(req.GetProviderConfig())
	current.Region = req.GetRegion()
	if err := s.storage.UpdateEnvironment(ctx, current); err != nil {
		switch err {
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid environment")
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "environment not found")
		default:
			return nil, status.Error(codes.Internal, "failed to update environment")
		}
	}
	updated, err := s.storage.GetEnvironment(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load environment")
	}
	return environmentToProto(updated), nil
}

func (s *adminServiceServer) DeleteEnvironment(ctx context.Context, req *adminv1.DeleteEnvironmentRequest) (*adminv1.DeleteEnvironmentResponse, error) {
	if _, _, err := s.requireUser(ctx); err != nil {
		return nil, err
	}
	if err := s.storage.DeleteEnvironment(ctx, strings.TrimSpace(req.GetName())); err != nil {
		switch err {
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid environment name")
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "environment not found")
		default:
			return nil, status.Error(codes.Internal, "failed to delete environment")
		}
	}
	return &adminv1.DeleteEnvironmentResponse{}, nil
}

func (s *adminServiceServer) GetSliceEnvironment(ctx context.Context, req *adminv1.GetSliceEnvironmentRequest) (*adminv1.SliceEnvironmentResponse, error) {
	username, _, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	sliceID := strings.TrimSpace(req.GetSliceId())
	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "slice not found")
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	return &adminv1.SliceEnvironmentResponse{
		SliceId:     slice.ID,
		Environment: slice.Environment,
	}, nil
}

func (s *adminServiceServer) UpdateSliceEnvironment(ctx context.Context, req *adminv1.UpdateSliceEnvironmentRequest) (*adminv1.SliceEnvironmentResponse, error) {
	username, _, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	sliceID := strings.TrimSpace(req.GetSliceId())
	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "slice not found")
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	environment := strings.TrimSpace(req.GetEnvironment())
	if err := s.storage.UpdateSliceEnvironment(ctx, sliceID, environment); err != nil {
		return nil, status.Error(codes.Internal, "failed to update environment")
	}
	return &adminv1.SliceEnvironmentResponse{
		SliceId:     sliceID,
		Environment: environment,
	}, nil
}

func (s *adminServiceServer) BatchMerge(ctx context.Context, req *adminv1.BatchMergeRequest) (*adminv1.BatchMergeResponse, error) {
	log.Printf("BatchMerge called: max_slices=%v", req.MaxSlices)
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

	rootSlice, err := s.storage.GetRootSlice(ctx)
	if errors.Is(err, storage.ErrSliceNotFound) {
		if initErr := s.storage.InitializeRootSlice(ctx); initErr != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to initialize root slice: %v", initErr))
		}
		rootSlice, err = s.storage.GetRootSlice(ctx)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load root slice: %v", err))
	}

	conflicts, err := s.storage.ListConflicts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list conflicts: %v", err))
	}
	if len(conflicts) > 0 {
		return nil, status.Error(codes.FailedPrecondition, "conflicts present; resolve before merging")
	}

	allSlices, err := s.storage.ListSlicesByOwner(ctx, username, int(^uint(0)>>1), 0)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slices: %v", err))
	}

	// Filter out the root slice and apply the max_slices limit if provided.
	mergeCandidates := make([]*models.Slice, 0, len(allSlices))
	for _, slice := range allSlices {
		if slice.IsRoot {
			continue
		}
		mergeCandidates = append(mergeCandidates, slice)
	}

	maxSlices := req.GetMaxSlices()
	if maxSlices > 0 && int(maxSlices) < len(mergeCandidates) {
		mergeCandidates = mergeCandidates[:maxSlices]
	}

	rootMetadata, err := s.storage.GetSliceMetadata(ctx, rootSlice.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load root metadata: %v", err))
	}

	mergedFiles := make(map[string]bool)
	for _, file := range rootMetadata.ModifiedFiles {
		mergedFiles[file] = true
	}
	for _, file := range rootSlice.Files {
		mergedFiles[file] = true
	}

	mergedSliceIDs := make([]string, 0, len(mergeCandidates))
	for _, slice := range mergeCandidates {
		mergedSliceIDs = append(mergedSliceIDs, slice.ID)

		metadata, err := s.storage.GetSliceMetadata(ctx, slice.ID)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice metadata: %v", err))
		}

		filesToMerge := make(map[string]bool)
		for _, fileID := range slice.Files {
			filesToMerge[fileID] = true
		}
		for _, fileID := range metadata.ModifiedFiles {
			filesToMerge[fileID] = true
		}

		for fileID := range filesToMerge {
			if err := s.storage.AddFileToSlice(ctx, fileID, rootSlice.ID); err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to add file to root slice: %v", err))
			}
			if err := s.storage.RemoveFileFromSlice(ctx, fileID, slice.ID); err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to remove file from slice: %v", err))
			}

			mergedFiles[fileID] = true
		}

		metadata.HeadCommitHash = fmt.Sprintf("merged-%s-%d", slice.ID, time.Now().UnixNano())
		metadata.ModifiedFiles = []string{}
		metadata.ModifiedFilesCount = 0

		if err := s.storage.UpdateSliceMetadata(ctx, slice.ID, metadata); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update slice metadata: %v", err))
		}
	}

	mergedFileList := make([]string, 0, len(mergedFiles))
	for file := range mergedFiles {
		mergedFileList = append(mergedFileList, file)
	}
	sort.Strings(mergedFileList)

	commitTime := time.Now()
	globalCommitHash := fmt.Sprintf("global-%d", commitTime.UnixNano())
	rootMetadata.HeadCommitHash = globalCommitHash
	rootMetadata.ModifiedFiles = mergedFileList
	rootMetadata.ModifiedFilesCount = len(mergedFileList)

	if err := s.storage.UpdateSliceMetadata(ctx, rootSlice.ID, rootMetadata); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update root metadata: %v", err))
	}

	state, err := s.storage.GetGlobalState(ctx)
	if err != nil {
		if !errors.Is(err, storage.ErrInvalidInput) {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load global state: %v", err))
		}
		state = &models.GlobalState{}
	}

	newHistory := &models.GlobalCommit{
		CommitHash:     globalCommitHash,
		Timestamp:      commitTime,
		MergedSliceIDs: mergedSliceIDs,
	}

	state.GlobalCommitHash = globalCommitHash
	state.Timestamp = commitTime
	state.History = append([]*models.GlobalCommit{newHistory}, state.History...)

	if err := s.storage.UpdateGlobalState(ctx, state); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update global state: %v", err))
	}

	return &adminv1.BatchMergeResponse{
		GlobalCommitHash: globalCommitHash,
		MergedSliceCount: int32(len(mergeCandidates)),
		MergedSliceIds:   mergedSliceIDs,
		Timestamp:        commitTime.Unix(),
	}, nil
}

func (s *adminServiceServer) GetConflicts(ctx context.Context, req *adminv1.ConflictsRequest) (*adminv1.ConflictsResponse, error) {
	log.Printf("GetConflicts called: slice_id=%v", req.SliceId)
	if _, err := s.requireUsername(ctx); err != nil {
		return nil, err
	}

	conflicts, err := s.storage.ListConflicts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list conflicts: %v", err))
	}

	var protoConflicts []*adminv1.Conflict
	for _, conflict := range conflicts {
		if req.SliceId != "" {
			contains := false
			for _, id := range conflict.ConflictingSlices {
				if id == req.GetSliceId() {
					contains = true
					break
				}
			}
			if !contains {
				continue
			}
		}

		protoConflicts = append(protoConflicts, &adminv1.Conflict{
			FileId:              conflict.FileID,
			ConflictingSliceIds: conflict.ConflictingSlices,
		})
	}

	return &adminv1.ConflictsResponse{
		Conflicts:      protoConflicts,
		TotalConflicts: int32(len(protoConflicts)),
	}, nil
}

func (s *adminServiceServer) ResolveConflict(ctx context.Context, req *adminv1.ResolveConflictRequest) (*adminv1.ResolveConflictResponse, error) {
	log.Printf("ResolveConflict called: file_id=%s preferred_slice_id=%s", req.FileId, req.PreferredSliceId)
	if _, err := s.requireUsername(ctx); err != nil {
		return nil, err
	}

	if req.FileId == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}

	conflict, err := s.storage.ResolveConflict(ctx, req.FileId, req.PreferredSliceId)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve conflict: %v", err))
	}

	return &adminv1.ResolveConflictResponse{
		ResolvedConflict: &adminv1.Conflict{
			FileId:              conflict.FileID,
			ConflictingSliceIds: conflict.ConflictingSlices,
		},
	}, nil
}

func (s *adminServiceServer) GetGlobalState(ctx context.Context, req *adminv1.GlobalStateRequest) (*adminv1.GlobalStateResponse, error) {
	log.Printf("GetGlobalState called: include_history=%v", req.IncludeHistory)

	state, err := s.storage.GetGlobalState(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load global state: %v", err))
	}

	response := &adminv1.GlobalStateResponse{
		GlobalCommitHash: state.GlobalCommitHash,
		Timestamp:        state.Timestamp.Unix(),
		History:          []*adminv1.GlobalCommitHistory{},
	}

	if req.IncludeHistory {
		for _, commit := range state.History {
			response.History = append(response.History, &adminv1.GlobalCommitHistory{
				CommitHash:     commit.CommitHash,
				Timestamp:      commit.Timestamp.Unix(),
				MergedSliceIds: commit.MergedSliceIDs,
			})
		}
	}

	return response, nil
}

func (s *adminServiceServer) ImportGitRepo(ctx context.Context, req *adminv1.ImportGitRepoRequest) (*adminv1.ImportGitRepoResponse, error) {
	if _, err := s.requireUsername(ctx); err != nil {
		return nil, err
	}

	repoPath := strings.TrimSpace(req.GetRepoPath())
	repoURL := strings.TrimSpace(req.GetRepoUrl())
	ref := strings.TrimSpace(req.GetRef())
	sliceID := strings.TrimSpace(req.GetSliceId())
	mountPath := strings.TrimSpace(req.GetMountPath())
	maxCommits := int(req.GetMaxCommits())

	if (repoPath == "" && repoURL == "") || (repoPath != "" && repoURL != "") {
		return nil, status.Error(codes.InvalidArgument, "exactly one of repo_path or repo_url must be set")
	}

	// Defaults.
	if sliceID == "" {
		sliceID = "root_slice"
	}
	if ref == "" {
		ref = "HEAD"
	}

	log.Printf("ImportGitRepo called: repo_path=%q repo_url=%q ref=%q slice_id=%q mount_path=%q reset=%v first_parent=%v max_commits=%d",
		repoPath, repoURL, ref, sliceID, mountPath, req.GetResetStorage(), req.GetFirstParent(), maxCommits)

	res, err := importGitRepo(ctx, s.storage, repoPath, repoURL, ref, sliceID, mountPath, req.GetResetStorage(), req.GetFirstParent(), maxCommits)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("git import failed: %v", err))
	}
	if err := sliceconfig.ApplyFromFileTree(ctx, s.storage); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to sync %s: %v", sliceconfig.ConfigFilePath, err))
	}

	return &adminv1.ImportGitRepoResponse{
		ImportedCommits: int32(res.ImportedCommits),
		HeadCommitHash:  res.HeadCommitHash,
		Warnings:        res.Warnings,
	}, nil
}

func (s *adminServiceServer) ListSlices(ctx context.Context, req *adminv1.ListSlicesRequest) (*adminv1.ListSlicesResponse, error) {
	log.Printf("ListSlices called: limit=%v offset=%v", req.Limit, req.Offset)

	limit := int(req.Limit)
	offset := int(req.Offset)
	if limit <= 0 {
		limit = int(^uint(0) >> 1)
	}

	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}
	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load root slice: %v", err))
	}

	var slices []*models.Slice
	if username == "" {
		slices = []*models.Slice{rootSlice}
	} else {
		owned, err := s.storage.ListSlicesByOwner(ctx, username, int(^uint(0)>>1), 0)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slices: %v", err))
		}
		slices = make([]*models.Slice, 0, len(owned))
		for _, slice := range owned {
			if slice.IsRoot {
				continue
			}
			slices = append(slices, slice)
		}
	}

	total := len(slices)
	if offset >= len(slices) {
		slices = []*models.Slice{}
	} else {
		end := offset + limit
		if end > len(slices) {
			end = len(slices)
		}
		slices = slices[offset:end]
	}

	response := &adminv1.ListSlicesResponse{
		Slices: make([]*adminv1.SliceInfo, 0, len(slices)),
		Total:  int32(total),
	}

	for _, slice := range slices {
		response.Slices = append(response.Slices, &adminv1.SliceInfo{
			SliceId:     slice.ID,
			Name:        slice.Name,
			Description: slice.Description,
			Owners:      slice.Owners,
			CreatedAt:   slice.CreatedAt.Unix(),
			UpdatedAt:   slice.UpdatedAt.Unix(),
			FileCount:   int32(len(slice.Files)),
			IsRoot:      slice.IsRoot,
			Environment: slice.Environment,
		})
	}

	return response, nil
}

func (s *adminServiceServer) BackfillHomeSlices(ctx context.Context, req *adminv1.BackfillHomeSlicesRequest) (*adminv1.BackfillHomeSlicesResponse, error) {
	if _, _, err := s.requireUser(ctx); err != nil {
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

func (s *adminServiceServer) WatchConflicts(req *adminv1.WatchConflictsRequest, stream adminv1.AdminService_WatchConflictsServer) error {
	log.Printf("WatchConflicts called: slice_id=%v", req.SliceId)
	if _, err := s.requireUsername(stream.Context()); err != nil {
		return err
	}

	conflicts, err := s.storage.ListConflicts(stream.Context())
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to list conflicts: %v", err))
	}

	var protoConflicts []*adminv1.Conflict
	for _, conflict := range conflicts {
		if req.SliceId != "" {
			matches := false
			for _, id := range conflict.ConflictingSlices {
				if id == req.GetSliceId() {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
		}

		protoConflicts = append(protoConflicts, &adminv1.Conflict{
			FileId:              conflict.FileID,
			ConflictingSliceIds: conflict.ConflictingSlices,
		})
	}

	if err := stream.Send(&adminv1.ConflictUpdate{NewConflicts: protoConflicts}); err != nil {
		return status.Error(codes.Unavailable, fmt.Sprintf("failed to stream conflicts: %v", err))
	}

	return nil
}
