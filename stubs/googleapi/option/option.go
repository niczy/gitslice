// Package option provides options for Google API clients.
// This is a build stub that provides just enough surface area
// to allow offline compilation without network access.
package option

// ClientOption is an option for a Google API client.
type ClientOption interface {
	// Apply applies the option.
	Apply(interface{})
}

type clientOption struct{}

func (clientOption) Apply(interface{}) {}

// WithEndpoint returns a ClientOption that overrides the default endpoint.
func WithEndpoint(url string) ClientOption { return clientOption{} }

// WithoutAuthentication returns a ClientOption that disables authentication.
func WithoutAuthentication() ClientOption { return clientOption{} }

// WithCredentialsFile returns a ClientOption that authenticates using a JSON credentials file.
func WithCredentialsFile(filename string) ClientOption { return clientOption{} }

// WithCredentialsJSON returns a ClientOption that authenticates using JSON credentials.
func WithCredentialsJSON(p []byte) ClientOption { return clientOption{} }

// WithGRPCDialOption returns a ClientOption that passes a gRPC dial option.
func WithGRPCDialOption(opt interface{}) ClientOption { return clientOption{} }

// WithTokenSource returns a ClientOption that uses the given token source.
func WithTokenSource(src interface{}) ClientOption { return clientOption{} }

// WithScopes returns a ClientOption that overrides the default scopes.
func WithScopes(scope ...string) ClientOption { return clientOption{} }

// WithUserAgent returns a ClientOption that sets a user agent string.
func WithUserAgent(ua string) ClientOption { return clientOption{} }

// WithHTTPClient returns a ClientOption that uses the given HTTP client.
func WithHTTPClient(client interface{}) ClientOption { return clientOption{} }

// WithAPIKey returns a ClientOption that uses the provided API key.
func WithAPIKey(apiKey string) ClientOption { return clientOption{} }

// WithQuotaProject returns a ClientOption that sets the quota project.
func WithQuotaProject(quotaProject string) ClientOption { return clientOption{} }

// WithRequestReason returns a ClientOption that sets the request reason.
func WithRequestReason(requestReason string) ClientOption { return clientOption{} }
