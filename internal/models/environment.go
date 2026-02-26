package models

import "time"

// Environment is a named runtime mapping used by slices and session creation.
type Environment struct {
	Name              string            `json:"name"`
	DisplayName       string            `json:"display_name"`
	Provider          string            `json:"provider"`
	ProviderID        string            `json:"provider_id"`
	ProviderConfig    map[string]string `json:"provider_config,omitempty"`
	Region            string            `json:"region"`
	DefaultAgentType  string            `json:"default_agent_type"`
	AllowedAgentTypes []string          `json:"allowed_agent_types"`
	CreatedBy         string            `json:"created_by"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}
