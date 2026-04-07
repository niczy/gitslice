package auth

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"

	"google.golang.org/grpc/metadata"
)

const (
	authorizationHeaderKey = "authorization"
	userAuthScheme         = "User "
	bearerAuthScheme       = "Bearer "
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{2,31}$`)

// ValidateUsername enforces a conservative username format.
//   - 3..32 chars
//   - starts with alnum
//   - contains only alnum, '_' and '-'
func ValidateUsername(username string) bool {
	username = strings.TrimSpace(username)
	return usernameRE.MatchString(username)
}

// LegacyUserAuthAllowed gates the historical `Authorization: User <username>`
// shortcut. It remains available by default for local/dev tests, but production
// has to opt in explicitly.
func LegacyUserAuthAllowed() bool {
	if truthyEnv("ALLOW_LEGACY_USER_AUTH") || truthyEnv("ALLOW_DEV_LOGIN") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DEPLOY_ENV"))) {
	case "prod", "production":
		return false
	default:
		return true
	}
}

func truthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

// UsernameFromAuthorizationHeader extracts a username from an Authorization header.
// Supported forms:
//   - "User <username>"
func UsernameFromAuthorizationHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, userAuthScheme) {
		if !LegacyUserAuthAllowed() {
			return ""
		}
		u := strings.TrimSpace(strings.TrimPrefix(value, userAuthScheme))
		if ValidateUsername(u) {
			return u
		}
		return ""
	}
	return ""
}

// TokenFromAuthorizationHeader extracts a bearer token from an Authorization header.
// Supported forms:
//   - "Bearer <token>"
func TokenFromAuthorizationHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, bearerAuthScheme) {
		return strings.TrimSpace(strings.TrimPrefix(value, bearerAuthScheme))
	}
	return ""
}

// UsernameFromGRPCContext reads the incoming gRPC metadata Authorization header.
func UsernameFromGRPCContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(authorizationHeaderKey)
	for _, v := range vals {
		if u := UsernameFromAuthorizationHeader(v); u != "" {
			return u
		}
	}
	return ""
}

// TokenFromGRPCContext reads a bearer token from incoming gRPC metadata.
func TokenFromGRPCContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(authorizationHeaderKey)
	for _, v := range vals {
		if tok := TokenFromAuthorizationHeader(v); tok != "" {
			return tok
		}
	}
	return ""
}

// UsernameFromHTTPRequest reads the Authorization header from an HTTP request.
func UsernameFromHTTPRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return UsernameFromAuthorizationHeader(r.Header.Get("Authorization"))
}

// TokenFromHTTPRequest reads a bearer token from an HTTP request.
func TokenFromHTTPRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return TokenFromAuthorizationHeader(r.Header.Get("Authorization"))
}
