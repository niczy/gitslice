package adminservice

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	clerkAdminClaimsMetadataKey = "x-gitslice-clerk-admin-claims"
	clerkAdminClaimsMaxLifetime = 5 * time.Minute
	clerkAdminClaimsClockSkew   = time.Minute
)

type clerkAdminClaims struct {
	Provider          string `json:"provider"`
	UserID            string `json:"userId"`
	SessionID         string `json:"sessionId"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferredUsername"`
	ImageURL          string `json:"imageUrl"`
	IssuedAtMs        int64  `json:"issuedAtMs"`
	ExpiresAtMs       int64  `json:"expiresAtMs"`
}

func configuredAdminEmails() map[string]struct{} {
	out := make(map[string]struct{})
	for _, email := range parseAdminEmailValue(os.Getenv("ADMIN_USER_EMAILS")) {
		out[email] = struct{}{}
	}
	return out
}

func parseAdminEmailValue(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var parsed []string
	if strings.HasPrefix(raw, "[") && json.Unmarshal([]byte(raw), &parsed) == nil {
		return normalizeAdminEmails(parsed)
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	return normalizeAdminEmails(parts)
}

func normalizeAdminEmails(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		email := strings.ToLower(strings.Trim(strings.TrimSpace(value), `"'`))
		if email == "" || !strings.Contains(email, "@") {
			continue
		}
		out = append(out, email)
	}
	return out
}

func adminStatusForEmail(email string) (configured bool, isAdmin bool, primaryEmail string) {
	admins := configuredAdminEmails()
	primaryEmail = strings.ToLower(strings.TrimSpace(email))
	if primaryEmail == "" {
		return len(admins) > 0, false, ""
	}
	_, isAdmin = admins[primaryEmail]
	return len(admins) > 0, isAdmin, primaryEmail
}

func signedClerkAdminClaimsFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, value := range md.Get(clerkAdminClaimsMetadataKey) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func verifySignedClerkAdminClaims(rawValue string, now time.Time) (*clerkAdminClaims, error) {
	secret := strings.TrimSpace(os.Getenv("AUTH_SECRET"))
	if secret == "" {
		return nil, status.Error(codes.FailedPrecondition, "AUTH_SECRET is not configured")
	}
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return nil, status.Error(codes.Unauthenticated, "Clerk admin session required")
	}
	payloadPart, signaturePart, ok := strings.Cut(rawValue, ".")
	if !ok || strings.TrimSpace(payloadPart) == "" || strings.TrimSpace(signaturePart) == "" {
		return nil, status.Error(codes.Unauthenticated, "invalid Clerk admin session")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payloadPart))
	expectedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signaturePart), []byte(expectedSignature)) {
		return nil, status.Error(codes.Unauthenticated, "invalid Clerk admin session signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid Clerk admin session payload")
	}
	var claims clerkAdminClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid Clerk admin session payload")
	}
	if strings.ToLower(strings.TrimSpace(claims.Provider)) != "clerk" {
		return nil, status.Error(codes.Unauthenticated, "Clerk admin session required")
	}
	if strings.TrimSpace(claims.UserID) == "" {
		return nil, status.Error(codes.Unauthenticated, "Clerk admin user is required")
	}
	if strings.TrimSpace(claims.Email) == "" || !strings.Contains(claims.Email, "@") {
		return nil, status.Error(codes.Unauthenticated, "Clerk admin email is required")
	}
	if claims.IssuedAtMs <= 0 || claims.ExpiresAtMs <= 0 {
		return nil, status.Error(codes.Unauthenticated, "Clerk admin session timestamps are required")
	}
	issuedAt := time.UnixMilli(claims.IssuedAtMs)
	expiresAt := time.UnixMilli(claims.ExpiresAtMs)
	if expiresAt.Before(issuedAt) || expiresAt.Sub(issuedAt) > clerkAdminClaimsMaxLifetime {
		return nil, status.Error(codes.Unauthenticated, "invalid Clerk admin session lifetime")
	}
	if issuedAt.After(now.Add(clerkAdminClaimsClockSkew)) {
		return nil, status.Error(codes.Unauthenticated, "Clerk admin session is not valid yet")
	}
	if !expiresAt.After(now.Add(-clerkAdminClaimsClockSkew)) {
		return nil, status.Error(codes.Unauthenticated, "Clerk admin session has expired")
	}
	claims.Email = strings.ToLower(strings.TrimSpace(claims.Email))
	claims.UserID = strings.TrimSpace(claims.UserID)
	claims.SessionID = strings.TrimSpace(claims.SessionID)
	return &claims, nil
}

func (s *adminServiceServer) loadUserForClerkAdminClaims(ctx context.Context, claims *clerkAdminClaims) (*models.User, error) {
	if claims == nil {
		return nil, status.Error(codes.Unauthenticated, "Clerk admin session required")
	}
	if strings.TrimSpace(claims.UserID) != "" {
		user, err := s.storage.GetUserByClerkUserID(ctx, claims.UserID)
		if err == nil {
			return user, nil
		}
		if err != storage.ErrEntryNotFound {
			return nil, status.Error(codes.Internal, "failed to load Clerk admin user")
		}
	}
	user, err := s.storage.GetUserByEmail(ctx, claims.Email)
	if err == nil {
		return user, nil
	}
	if err == storage.ErrEntryNotFound {
		return nil, status.Error(codes.FailedPrecondition, "Clerk admin account has not completed local setup")
	}
	return nil, status.Error(codes.Internal, "failed to load Clerk admin user")
}

func (s *adminServiceServer) requireClerkAdminClaims(ctx context.Context) (*clerkAdminClaims, error) {
	claims, err := verifySignedClerkAdminClaims(signedClerkAdminClaimsFromContext(ctx), time.Now())
	if err != nil {
		return nil, err
	}
	adminConfigured, isAdmin, _ := adminStatusForEmail(claims.Email)
	if !adminConfigured {
		return nil, status.Error(codes.PermissionDenied, "admin users are not configured")
	}
	if !isAdmin {
		return nil, status.Error(codes.PermissionDenied, "admin access required")
	}
	return claims, nil
}

func (s *adminServiceServer) requireAdminUser(ctx context.Context) (*models.User, error) {
	claims, err := s.requireClerkAdminClaims(ctx)
	if err != nil {
		return nil, err
	}
	return s.loadUserForClerkAdminClaims(ctx, claims)
}
