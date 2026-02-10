package auth

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"google.golang.org/grpc/metadata"
)

const (
	authorizationHeaderKey = "authorization"
	userAuthScheme         = "User "
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

// UsernameFromAuthorizationHeader extracts a username from an Authorization header.
// Supported forms:
//   - "User <username>"
func UsernameFromAuthorizationHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, userAuthScheme) {
		u := strings.TrimSpace(strings.TrimPrefix(value, userAuthScheme))
		if ValidateUsername(u) {
			return u
		}
		return ""
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

// UsernameFromHTTPRequest reads the Authorization header from an HTTP request.
func UsernameFromHTTPRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return UsernameFromAuthorizationHeader(r.Header.Get("Authorization"))
}
