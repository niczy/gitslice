package workosauth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultIssuer       = "https://api.workos.com/"
	defaultJWKSBaseURL  = "https://api.workos.com/sso/jwks/"
	defaultJWKSCacheTTL = 15 * time.Minute
)

var (
	ErrNotConfigured = errors.New("workos auth is not configured")
	ErrInvalidToken  = errors.New("invalid WorkOS access token")
)

type Claims struct {
	jwt.RegisteredClaims
	SessionID            string   `json:"sid"`
	OrganizationID       string   `json:"org_id"`
	Role                 string   `json:"role"`
	Permissions          []string `json:"permissions"`
	Email                string   `json:"email"`
	AuthenticationMethod string   `json:"authentication_method"`
}

type VerifierConfig struct {
	ClientID      string
	JWKSURL       string
	AuthKitDomain string
	HTTPClient    *http.Client
	JWKSCacheTTL  time.Duration
}

type Verifier struct {
	clientID   string
	jwksURL    string
	issuers    map[string]struct{}
	httpClient *http.Client
	cacheTTL   time.Duration

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	KeyID     string `json:"kid"`
	KeyType   string `json:"kty"`
	Algorithm string `json:"alg"`
	Use       string `json:"use"`
	N         string `json:"n"`
	E         string `json:"e"`
}

func DefaultJWKSURL(clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ""
	}
	return defaultJWKSBaseURL + clientID
}

func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	clientID := strings.TrimSpace(cfg.ClientID)
	jwksURL := strings.TrimSpace(cfg.JWKSURL)
	if clientID == "" && jwksURL == "" {
		return nil, ErrNotConfigured
	}
	if jwksURL == "" {
		jwksURL = DefaultJWKSURL(clientID)
	}
	if jwksURL == "" {
		return nil, ErrNotConfigured
	}

	cacheTTL := cfg.JWKSCacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultJWKSCacheTTL
	}

	issuers := allowedIssuers(clientID, cfg.AuthKitDomain)

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	return &Verifier{
		clientID:   clientID,
		jwksURL:    jwksURL,
		issuers:    issuers,
		httpClient: httpClient,
		cacheTTL:   cacheTTL,
		keys:       make(map[string]*rsa.PublicKey),
	}, nil
}

func allowedIssuers(clientID, authKitDomain string) map[string]struct{} {
	issuers := map[string]struct{}{
		defaultIssuer: {},
	}
	if userManagementIssuer := userManagementIssuer(defaultIssuer, clientID); userManagementIssuer != "" {
		issuers[userManagementIssuer] = struct{}{}
	}
	if authKitIssuer := normalizeIssuer(authKitDomain); authKitIssuer != "" {
		issuers[authKitIssuer] = struct{}{}
		if userManagementIssuer := userManagementIssuer(authKitIssuer, clientID); userManagementIssuer != "" {
			issuers[userManagementIssuer] = struct{}{}
		}
	}
	return issuers
}

func userManagementIssuer(baseIssuer, clientID string) string {
	baseIssuer = normalizeIssuer(baseIssuer)
	clientID = strings.TrimSpace(clientID)
	if baseIssuer == "" || clientID == "" {
		return ""
	}
	return normalizeIssuer(baseIssuer + "user_management/" + clientID)
}

func (v *Verifier) VerifyAccessToken(ctx context.Context, tokenString string) (*Claims, error) {
	tokenString = strings.TrimSpace(tokenString)
	if tokenString == "" {
		return nil, ErrInvalidToken
	}

	claims := &Claims{}
	parsedToken, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method == nil || token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, ErrInvalidToken
		}
		kid, _ := token.Header["kid"].(string)
		if strings.TrimSpace(kid) == "" {
			return nil, ErrInvalidToken
		}
		return v.lookupKey(ctx, kid)
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))
	if err != nil || parsedToken == nil || !parsedToken.Valid {
		return nil, ErrInvalidToken
	}

	if claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	if !v.isAllowedIssuer(claims.Issuer) {
		return nil, ErrInvalidToken
	}
	if v.clientID != "" && !containsAudience(claims.Audience, v.clientID) {
		return nil, ErrInvalidToken
	}
	if claims.ExpiresAt == nil || claims.ExpiresAt.Time.Before(time.Now()) {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

func (v *Verifier) isAllowedIssuer(issuer string) bool {
	issuer = normalizeIssuer(issuer)
	if issuer == "" {
		return false
	}
	_, ok := v.issuers[issuer]
	return ok
}

func normalizeIssuer(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		value = "https://" + value
	}
	return strings.TrimRight(value, "/") + "/"
}

func containsAudience(audience jwt.ClaimStrings, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	for _, candidate := range audience {
		if strings.TrimSpace(candidate) == expected {
			return true
		}
	}
	return false
}

func (v *Verifier) lookupKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if key := v.cachedKey(kid); key != nil {
		return key, nil
	}
	if err := v.refreshKeys(ctx, false); err != nil {
		return nil, err
	}
	if key := v.cachedKey(kid); key != nil {
		return key, nil
	}
	if err := v.refreshKeys(ctx, true); err != nil {
		return nil, err
	}
	if key := v.cachedKey(kid); key != nil {
		return key, nil
	}
	return nil, ErrInvalidToken
}

func (v *Verifier) cachedKey(kid string) *rsa.PublicKey {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if len(v.keys) == 0 {
		return nil
	}
	if v.cacheTTL > 0 && !v.fetchedAt.IsZero() && time.Since(v.fetchedAt) > v.cacheTTL {
		return nil
	}
	return v.keys[kid]
}

func (v *Verifier) refreshKeys(ctx context.Context, force bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !force && len(v.keys) > 0 && (v.cacheTTL <= 0 || time.Since(v.fetchedAt) <= v.cacheTTL) {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch WorkOS JWKS: unexpected status %d", resp.StatusCode)
	}

	var doc jwksDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode WorkOS JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, key := range doc.Keys {
		publicKey, err := jwkToRSAPublicKey(key)
		if err != nil {
			return err
		}
		keys[key.KeyID] = publicKey
	}
	v.keys = keys
	v.fetchedAt = time.Now()
	return nil
}

func jwkToRSAPublicKey(key jwk) (*rsa.PublicKey, error) {
	if strings.TrimSpace(key.KeyID) == "" || strings.TrimSpace(key.N) == "" || strings.TrimSpace(key.E) == "" {
		return nil, ErrInvalidToken
	}
	if key.KeyType != "" && !strings.EqualFold(key.KeyType, "RSA") {
		return nil, ErrInvalidToken
	}

	modulusBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(key.N))
	if err != nil {
		return nil, fmt.Errorf("decode WorkOS jwk modulus: %w", err)
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(key.E))
	if err != nil {
		return nil, fmt.Errorf("decode WorkOS jwk exponent: %w", err)
	}

	modulus := new(big.Int).SetBytes(modulusBytes)
	exponent := new(big.Int).SetBytes(exponentBytes)
	if modulus.Sign() <= 0 || exponent.Sign() <= 0 {
		return nil, ErrInvalidToken
	}

	return &rsa.PublicKey{
		N: modulus,
		E: int(exponent.Int64()),
	}, nil
}
