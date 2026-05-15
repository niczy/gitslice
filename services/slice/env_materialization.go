package sliceservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

var sliceEnvKeyRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,127}$`)

type sliceEnvContext struct {
	slice            *models.Slice
	homeID           string
	homeSliceID      string
	sliceSlug        string
	requirementsPath string
	requirementsHash string
	requirements     *sliceEnvRequirementsFile
	found            bool
	issues           []*slicev1.SliceEnvIssue
}

type sliceEnvRequirementsFile struct {
	Version       int                        `yaml:"version"`
	Profiles      map[string]sliceEnvProfile `yaml:"profiles"`
	IgnoredPaths  []string                   `yaml:"ignored_paths"`
	profileNames  []string
	normalizedAll []*sliceEnvMaterializedFileSpec
}

type sliceEnvProfile struct {
	Files []sliceEnvMaterializedFileSpec `yaml:"files"`
}

type sliceEnvMaterializedFileSpec struct {
	Path            string   `yaml:"path"`
	Mode            string   `yaml:"mode"`
	Sensitive       *bool    `yaml:"sensitive"`
	Template        string   `yaml:"template"`
	RequiredSecrets []string `yaml:"required_secrets"`
	OptionalSecrets []string `yaml:"optional_secrets"`
	RequiredValues  []string `yaml:"required_values"`
	OptionalValues  []string `yaml:"optional_values"`
	Profile         string   `yaml:"-"`
}

func (s *sliceServiceServer) GetSliceEnvRequirements(ctx context.Context, req *slicev1.GetSliceEnvRequirementsRequest) (*slicev1.GetSliceEnvRequirementsResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	envCtx, err := s.loadSliceEnvContext(ctx, req.GetSliceId(), username)
	if err != nil {
		return nil, err
	}
	resp := &slicev1.GetSliceEnvRequirementsResponse{
		SliceId:          envCtx.slice.ID,
		HomeId:           envCtx.homeID,
		SliceSlug:        envCtx.sliceSlug,
		RequirementsPath: envCtx.requirementsPath,
		RequirementsHash: envCtx.requirementsHash,
		Found:            envCtx.found,
		Issues:           envCtx.issues,
	}
	if envCtx.requirements != nil {
		resp.Requirements = envCtx.requirements.toProto()
	}
	return resp, nil
}

func (s *sliceServiceServer) ListSliceEnvKV(ctx context.Context, req *slicev1.ListSliceEnvKVRequest) (*slicev1.ListSliceEnvKVResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	envCtx, err := s.loadSliceEnvContext(ctx, req.GetSliceId(), username)
	if err != nil {
		return nil, err
	}
	profile := strings.TrimSpace(req.GetProfile())
	if profile == "" {
		profile = "default"
	}
	entries, err := s.storage.ListEnvironmentKV(ctx, models.EnvironmentKVFilter{
		HomeID:  envCtx.homeID,
		SliceID: envCtx.slice.ID,
		Profile: profile,
	})
	if err != nil {
		return nil, storageErrorToStatus(err, "failed to list environment KV")
	}
	resp := &slicev1.ListSliceEnvKVResponse{Entries: make([]*slicev1.SliceEnvKVEntry, 0, len(entries))}
	for _, entry := range entries {
		resp.Entries = append(resp.Entries, environmentKVEntryToProto(entry, entry.Class == models.EnvironmentKVClassValue))
	}
	return resp, nil
}

func (s *sliceServiceServer) SetSliceEnvValue(ctx context.Context, req *slicev1.SetSliceEnvValueRequest) (*slicev1.SetSliceEnvValueResponse, error) {
	entry, err := s.setSliceEnvKV(ctx, req.GetSliceId(), req.GetProfile(), req.GetKey(), req.GetValue(), models.EnvironmentKVClassValue)
	if err != nil {
		return nil, err
	}
	return &slicev1.SetSliceEnvValueResponse{Entry: environmentKVEntryToProto(entry, true)}, nil
}

func (s *sliceServiceServer) SetSliceEnvSecret(ctx context.Context, req *slicev1.SetSliceEnvSecretRequest) (*slicev1.SetSliceEnvSecretResponse, error) {
	entry, err := s.setSliceEnvKV(ctx, req.GetSliceId(), req.GetProfile(), req.GetKey(), req.GetValue(), models.EnvironmentKVClassSecret)
	if err != nil {
		return nil, err
	}
	return &slicev1.SetSliceEnvSecretResponse{Entry: environmentKVEntryToProto(entry, false)}, nil
}

func (s *sliceServiceServer) DeleteSliceEnvKV(ctx context.Context, req *slicev1.DeleteSliceEnvKVRequest) (*slicev1.DeleteSliceEnvKVResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	envCtx, err := s.loadSliceEnvContext(ctx, req.GetSliceId(), username)
	if err != nil {
		return nil, err
	}
	if !canManageSliceVisibility(envCtx.slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized to update slice environment KV")
	}
	class := models.EnvironmentKVClass(strings.ToLower(strings.TrimSpace(req.GetClass())))
	err = s.storage.DeleteEnvironmentKV(ctx, models.EnvironmentKVFilter{
		HomeID:  envCtx.homeID,
		SliceID: envCtx.slice.ID,
		Profile: req.GetProfile(),
		Class:   class,
		Key:     req.GetKey(),
	})
	if err != nil {
		return nil, storageErrorToStatus(err, "failed to delete environment KV")
	}
	return &slicev1.DeleteSliceEnvKVResponse{Deleted: true}, nil
}

func (s *sliceServiceServer) RenderSliceEnvMaterialization(ctx context.Context, req *slicev1.RenderSliceEnvMaterializationRequest) (*slicev1.RenderSliceEnvMaterializationResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	envCtx, err := s.loadSliceEnvContext(ctx, req.GetSliceId(), username)
	if err != nil {
		return nil, err
	}
	profile := strings.ToLower(strings.TrimSpace(req.GetProfile()))
	if profile == "" {
		profile = "local"
	}
	resp := &slicev1.RenderSliceEnvMaterializationResponse{
		SliceId:          envCtx.slice.ID,
		HomeId:           envCtx.homeID,
		SliceSlug:        envCtx.sliceSlug,
		Profile:          profile,
		RequirementsPath: envCtx.requirementsPath,
		RequirementsHash: envCtx.requirementsHash,
		Found:            envCtx.found,
		Issues:           envCtx.issues,
	}
	if !envCtx.found || envCtx.requirements == nil {
		return resp, nil
	}
	if len(envCtx.issues) > 0 {
		return nil, status.Error(codes.InvalidArgument, "environment requirements are invalid")
	}
	profileSpec, ok := envCtx.requirements.Profiles[profile]
	if !ok && profile == "agent" {
		if fallback, fallbackOK := envCtx.requirements.Profiles["local"]; fallbackOK {
			profileSpec = fallback
			profile = "local"
			resp.Profile = profile
			ok = true
		}
	}
	if !ok {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("environment profile not found: %s", profile))
	}
	resp.IgnoredPaths = append([]string(nil), envCtx.requirements.IgnoredPaths...)
	sort.Strings(resp.IgnoredPaths)

	renderState := newSliceEnvRenderState(s.storage, envCtx, profile, strings.TrimSpace(req.GetCommitHash()))
	for _, file := range profileSpec.Files {
		rendered, err := renderState.renderFile(ctx, file)
		if err != nil {
			return nil, err
		}
		resp.Files = append(resp.Files, rendered)
	}
	resp.MissingEntries = renderState.missingList()
	resp.ResolvedRefs = renderState.refList()
	if req.GetStrict() && len(resp.MissingEntries) > 0 {
		return nil, status.Error(codes.FailedPrecondition, "missing required environment KV entries")
	}
	return resp, nil
}

func (s *sliceServiceServer) setSliceEnvKV(ctx context.Context, sliceID, profile, key, value string, class models.EnvironmentKVClass) (*models.EnvironmentKVEntry, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	envCtx, err := s.loadSliceEnvContext(ctx, sliceID, username)
	if err != nil {
		return nil, err
	}
	if !canManageSliceVisibility(envCtx.slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized to update slice environment KV")
	}
	entry, err := s.storage.UpsertEnvironmentKV(ctx, &models.EnvironmentKVEntry{
		HomeID:    envCtx.homeID,
		SliceID:   envCtx.slice.ID,
		SliceSlug: envCtx.sliceSlug,
		Profile:   profile,
		Key:       key,
		Class:     class,
		Value:     value,
		CreatedBy: username,
		UpdatedBy: username,
	})
	if err != nil {
		return nil, storageErrorToStatus(err, "failed to set environment KV")
	}
	return entry, nil
}

func (s *sliceServiceServer) loadSliceEnvContext(ctx context.Context, sliceID, username string) (*sliceEnvContext, error) {
	sliceID = strings.TrimSpace(sliceID)
	if sliceID == "" {
		return nil, status.Error(codes.InvalidArgument, "slice_id cannot be empty")
	}
	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) || errors.Is(err, storage.ErrEntryNotFound) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice: %v", err))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	homeID := sliceEnvHomeID(slice, username)
	if homeID == "" {
		return nil, status.Error(codes.InvalidArgument, "slice does not have a home owner")
	}
	slug := sliceEnvLocalSlug(slice, homeID)
	if slug == "" {
		return nil, status.Error(codes.InvalidArgument, "slice does not have a slug")
	}
	reqPath := path.Join(homeID, ".gitslice", "slices", slug, "env.yaml")
	envCtx := &sliceEnvContext{
		slice:            slice,
		homeID:           homeID,
		homeSliceID:      homeslice.IDForUsername(homeID),
		sliceSlug:        slug,
		requirementsPath: reqPath,
	}
	content, err := storage.ReadSliceFileContent(ctx, s.storage, envCtx.homeSliceID, reqPath)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) || errors.Is(err, storage.ErrSliceNotFound) {
			return envCtx, nil
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to read environment requirements: %v", err))
	}
	envCtx.found = true
	envCtx.requirementsHash = content.Hash
	reqs, issues := parseSliceEnvRequirements(content.Content, slug)
	envCtx.requirements = reqs
	envCtx.issues = issues
	return envCtx, nil
}

func sliceEnvHomeID(slice *models.Slice, username string) string {
	if slice == nil {
		return strings.TrimSpace(username)
	}
	if strings.HasPrefix(strings.TrimSpace(slice.ID), "home_") {
		return strings.TrimSpace(strings.TrimPrefix(slice.ID, "home_"))
	}
	if slice.IsRoot {
		return strings.TrimSpace(username)
	}
	if createdBy := strings.TrimSpace(slice.CreatedBy); createdBy != "" {
		return createdBy
	}
	for _, owner := range slice.Owners {
		if owner = strings.TrimSpace(owner); owner != "" {
			return owner
		}
	}
	return strings.TrimSpace(username)
}

func sliceEnvLocalSlug(slice *models.Slice, homeID string) string {
	if slice == nil {
		return ""
	}
	if slug := strings.Trim(strings.TrimSpace(slice.Slug), "/"); slug != "" && !strings.Contains(slug, "/") {
		return slug
	}
	external := strings.Trim(strings.TrimSpace(externalSliceSlug(slice)), "/")
	if owner, slug, ok := storage.SplitQualifiedSliceRef(external); ok && owner == homeID {
		return slug
	}
	if external != "" && !strings.Contains(external, "/") {
		return external
	}
	return common.CleanRelativePath(slice.ID)
}

func parseSliceEnvRequirements(content []byte, sliceSlug string) (*sliceEnvRequirementsFile, []*slicev1.SliceEnvIssue) {
	var reqs sliceEnvRequirementsFile
	if err := yaml.Unmarshal(content, &reqs); err != nil {
		return nil, []*slicev1.SliceEnvIssue{{Code: "parse_error", Message: err.Error()}}
	}
	issues := make([]*slicev1.SliceEnvIssue, 0)
	if reqs.Version != 1 {
		issues = append(issues, &slicev1.SliceEnvIssue{Code: "unsupported_version", Message: "environment requirements version must be 1"})
	}
	if len(reqs.Profiles) == 0 {
		issues = append(issues, &slicev1.SliceEnvIssue{Code: "missing_profiles", Message: "environment requirements must define at least one profile"})
	}
	for i, raw := range reqs.IgnoredPaths {
		cleaned, err := normalizeSliceEnvMaterializedPath(raw, sliceSlug)
		if err != nil {
			issues = append(issues, &slicev1.SliceEnvIssue{Code: "invalid_ignored_path", Path: raw, Message: err.Error()})
			continue
		}
		reqs.IgnoredPaths[i] = cleaned
	}
	reqs.IgnoredPaths = dedupeSortedStrings(reqs.IgnoredPaths)

	reqs.profileNames = make([]string, 0, len(reqs.Profiles))
	for rawProfile, profile := range reqs.Profiles {
		profileName := strings.ToLower(strings.TrimSpace(rawProfile))
		if profileName == "" {
			issues = append(issues, &slicev1.SliceEnvIssue{Code: "invalid_profile", Message: "profile name cannot be empty"})
			continue
		}
		reqs.profileNames = append(reqs.profileNames, profileName)
		for i := range profile.Files {
			file := profile.Files[i]
			file.Profile = profileName
			normalized, fileIssues := normalizeSliceEnvFileSpec(file, sliceSlug)
			issues = append(issues, fileIssues...)
			profile.Files[i] = normalized
			reqs.normalizedAll = append(reqs.normalizedAll, &profile.Files[i])
		}
		reqs.Profiles[profileName] = profile
		if profileName != rawProfile {
			delete(reqs.Profiles, rawProfile)
		}
	}
	sort.Strings(reqs.profileNames)
	return &reqs, issues
}

func normalizeSliceEnvFileSpec(file sliceEnvMaterializedFileSpec, sliceSlug string) (sliceEnvMaterializedFileSpec, []*slicev1.SliceEnvIssue) {
	issues := make([]*slicev1.SliceEnvIssue, 0)
	cleaned, err := normalizeSliceEnvMaterializedPath(file.Path, sliceSlug)
	if err != nil {
		issues = append(issues, &slicev1.SliceEnvIssue{Code: "invalid_file_path", Path: file.Path, Message: err.Error()})
	} else {
		file.Path = cleaned
	}
	file.Mode = strings.TrimSpace(file.Mode)
	if file.Mode == "" {
		if sliceEnvFileUsesSecrets(file) {
			file.Mode = "0600"
		} else {
			file.Mode = "0644"
		}
	}
	if file.Mode != "0600" && file.Mode != "0644" {
		issues = append(issues, &slicev1.SliceEnvIssue{Code: "invalid_file_mode", Path: file.Path, Message: "mode must be 0600 or 0644"})
	}
	file.RequiredSecrets = normalizeSliceEnvKeys(file.RequiredSecrets, &issues, file.Path, "invalid_required_secret")
	file.OptionalSecrets = normalizeSliceEnvKeys(file.OptionalSecrets, &issues, file.Path, "invalid_optional_secret")
	file.RequiredValues = normalizeSliceEnvKeys(file.RequiredValues, &issues, file.Path, "invalid_required_value")
	file.OptionalValues = normalizeSliceEnvKeys(file.OptionalValues, &issues, file.Path, "invalid_optional_value")
	return file, issues
}

func normalizeSliceEnvMaterializedPath(raw, sliceSlug string) (string, error) {
	if err := common.ValidateFilePath(strings.TrimSpace(raw)); err != nil {
		return "", err
	}
	cleaned := common.CleanRelativePath(raw)
	if cleaned == "" {
		return "", fmt.Errorf("path cannot be empty")
	}
	if strings.HasPrefix(cleaned, ".gitslice/") {
		blocked := path.Join(".gitslice", "slices", sliceSlug, "env.yaml")
		if cleaned == blocked {
			return "", fmt.Errorf("path cannot overwrite %s", blocked)
		}
	}
	return cleaned, nil
}

func normalizeSliceEnvKeys(values []string, issues *[]*slicev1.SliceEnvIssue, filePath, code string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		key := strings.TrimSpace(raw)
		if !sliceEnvKeyRE.MatchString(key) {
			*issues = append(*issues, &slicev1.SliceEnvIssue{Code: code, Path: filePath, Message: fmt.Sprintf("invalid key %q", raw)})
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sliceEnvFileUsesSecrets(file sliceEnvMaterializedFileSpec) bool {
	return len(file.RequiredSecrets) > 0 ||
		len(file.OptionalSecrets) > 0 ||
		strings.Contains(file.Template, `secret "`) ||
		strings.Contains(file.Template, `optionalSecret "`)
}

func (reqs *sliceEnvRequirementsFile) toProto() *slicev1.SliceEnvRequirements {
	if reqs == nil {
		return nil
	}
	out := &slicev1.SliceEnvRequirements{
		Version:      int32(reqs.Version),
		Profiles:     append([]string(nil), reqs.profileNames...),
		IgnoredPaths: append([]string(nil), reqs.IgnoredPaths...),
		Files:        make([]*slicev1.SliceEnvMaterializedFile, 0, len(reqs.normalizedAll)),
	}
	for _, file := range reqs.normalizedAll {
		if file == nil {
			continue
		}
		out.Files = append(out.Files, sliceEnvFileSpecToProto(*file))
	}
	return out
}

func sliceEnvFileSpecToProto(file sliceEnvMaterializedFileSpec) *slicev1.SliceEnvMaterializedFile {
	sensitive := false
	if file.Sensitive != nil {
		sensitive = *file.Sensitive
	} else {
		sensitive = sliceEnvFileUsesSecrets(file)
	}
	return &slicev1.SliceEnvMaterializedFile{
		Path:            file.Path,
		Mode:            file.Mode,
		Sensitive:       sensitive,
		RequiredSecrets: append([]string(nil), file.RequiredSecrets...),
		OptionalSecrets: append([]string(nil), file.OptionalSecrets...),
		RequiredValues:  append([]string(nil), file.RequiredValues...),
		OptionalValues:  append([]string(nil), file.OptionalValues...),
		Profile:         file.Profile,
	}
}

type sliceEnvRenderState struct {
	store      storage.Storage
	envCtx     *sliceEnvContext
	profile    string
	commitHash string
	missing    map[string]*slicev1.SliceEnvMissingKV
	refs       map[string]*slicev1.SliceEnvKVReference
}

func newSliceEnvRenderState(store storage.Storage, envCtx *sliceEnvContext, profile, commitHash string) *sliceEnvRenderState {
	return &sliceEnvRenderState{
		store:      store,
		envCtx:     envCtx,
		profile:    profile,
		commitHash: commitHash,
		missing:    make(map[string]*slicev1.SliceEnvMissingKV),
		refs:       make(map[string]*slicev1.SliceEnvKVReference),
	}
}

func (r *sliceEnvRenderState) renderFile(ctx context.Context, file sliceEnvMaterializedFileSpec) (*slicev1.RenderedSliceEnvFile, error) {
	for _, key := range file.RequiredSecrets {
		_, err := r.resolve(ctx, models.EnvironmentKVClassSecret, key, true)
		if err != nil {
			return nil, err
		}
	}
	for _, key := range file.RequiredValues {
		_, err := r.resolve(ctx, models.EnvironmentKVClassValue, key, true)
		if err != nil {
			return nil, err
		}
	}
	funcs := template.FuncMap{
		"secret": func(key string) (string, error) {
			return r.resolve(ctx, models.EnvironmentKVClassSecret, key, true)
		},
		"value": func(key string) (string, error) {
			return r.resolve(ctx, models.EnvironmentKVClassValue, key, true)
		},
		"optionalSecret": func(key string) (string, error) {
			return r.resolve(ctx, models.EnvironmentKVClassSecret, key, false)
		},
		"optionalValue": func(key string) (string, error) {
			return r.resolve(ctx, models.EnvironmentKVClassValue, key, false)
		},
		"runtimeEnv": func(key string) (string, error) {
			return "", fmt.Errorf("runtimeEnv is not enabled for server-side materialization")
		},
		"slice": func(field string) string {
			switch strings.ToLower(strings.TrimSpace(field)) {
			case "id", "slice_id":
				return r.envCtx.slice.ID
			case "slug", "slice_slug":
				return r.envCtx.sliceSlug
			case "home", "home_id":
				return r.envCtx.homeID
			default:
				return ""
			}
		},
		"checkout": func(field string) string {
			switch strings.ToLower(strings.TrimSpace(field)) {
			case "commit", "commit_hash":
				return r.commitHash
			case "profile":
				return r.profile
			default:
				return ""
			}
		},
	}
	tmpl, err := template.New("env").Funcs(funcs).Option("missingkey=error").Parse(file.Template)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid template for %s: %v", file.Path, err))
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{}); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("failed to render %s: %v", file.Path, err))
	}
	protoFile := sliceEnvFileSpecToProto(file)
	return &slicev1.RenderedSliceEnvFile{
		Path:       file.Path,
		Mode:       file.Mode,
		Sensitive:  protoFile.Sensitive,
		Content:    buf.Bytes(),
		SecretKeys: dedupeSortedStrings(append(append([]string{}, file.RequiredSecrets...), file.OptionalSecrets...)),
		ValueKeys:  dedupeSortedStrings(append(append([]string{}, file.RequiredValues...), file.OptionalValues...)),
	}, nil
}

func (r *sliceEnvRenderState) resolve(ctx context.Context, class models.EnvironmentKVClass, key string, required bool) (string, error) {
	key = strings.TrimSpace(key)
	if !sliceEnvKeyRE.MatchString(key) {
		return "", status.Error(codes.InvalidArgument, fmt.Sprintf("invalid environment key: %s", key))
	}
	entry, err := r.store.ResolveEnvironmentKV(ctx, r.envCtx.homeID, r.envCtx.slice.ID, r.profile, class, key)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			if required {
				r.addMissing(class, key)
			}
			return "", nil
		}
		return "", storageErrorToStatus(err, "failed to resolve environment KV")
	}
	r.addRef(class, key, entry)
	return entry.Value, nil
}

func (r *sliceEnvRenderState) addMissing(class models.EnvironmentKVClass, key string) {
	mapKey := string(class) + "\x00" + key
	if _, ok := r.missing[mapKey]; ok {
		return
	}
	r.missing[mapKey] = &slicev1.SliceEnvMissingKV{
		Class:   string(class),
		Key:     key,
		Profile: r.profile,
	}
}

func (r *sliceEnvRenderState) addRef(class models.EnvironmentKVClass, key string, entry *models.EnvironmentKVEntry) {
	mapKey := strings.Join([]string{string(class), key, entry.Profile}, "\x00")
	if _, ok := r.refs[mapKey]; ok {
		return
	}
	r.refs[mapKey] = &slicev1.SliceEnvKVReference{
		Class:     string(class),
		Key:       key,
		Profile:   entry.Profile,
		Version:   entry.Version,
		ValueHash: entry.ValueHash,
	}
}

func (r *sliceEnvRenderState) missingList() []*slicev1.SliceEnvMissingKV {
	out := make([]*slicev1.SliceEnvMissingKV, 0, len(r.missing))
	for _, missing := range r.missing {
		out = append(out, missing)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class < out[j].Class
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func (r *sliceEnvRenderState) refList() []*slicev1.SliceEnvKVReference {
	out := make([]*slicev1.SliceEnvKVReference, 0, len(r.refs))
	for _, ref := range r.refs {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class < out[j].Class
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func environmentKVEntryToProto(entry *models.EnvironmentKVEntry, includeValue bool) *slicev1.SliceEnvKVEntry {
	if entry == nil {
		return nil
	}
	value := ""
	if includeValue {
		value = entry.Value
	}
	return &slicev1.SliceEnvKVEntry{
		Id:        entry.ID,
		HomeId:    entry.HomeID,
		SliceId:   entry.SliceID,
		SliceSlug: entry.SliceSlug,
		Profile:   entry.Profile,
		Key:       entry.Key,
		Class:     string(entry.Class),
		Value:     value,
		ValueHash: entry.ValueHash,
		Version:   entry.Version,
		CreatedBy: entry.CreatedBy,
		UpdatedBy: entry.UpdatedBy,
		CreatedAt: entry.CreatedAt.Unix(),
		UpdatedAt: entry.UpdatedAt.Unix(),
		HasValue:  entry.Value != "",
	}
}

func storageErrorToStatus(err error, prefix string) error {
	switch {
	case errors.Is(err, storage.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, prefix)
	case errors.Is(err, storage.ErrEntryNotFound), errors.Is(err, storage.ErrSliceNotFound):
		return status.Error(codes.NotFound, prefix)
	case errors.Is(err, storage.ErrPermissionDenied):
		return status.Error(codes.PermissionDenied, prefix)
	default:
		return status.Error(codes.Internal, fmt.Sprintf("%s: %v", prefix, err))
	}
}

func dedupeSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
