package models

import "time"

// Environment is a named runtime mapping used by slices and session creation.
type Environment struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Provider    string    `json:"provider"`
	ProviderID  string    `json:"provider_id"`
	Region      string    `json:"region"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
