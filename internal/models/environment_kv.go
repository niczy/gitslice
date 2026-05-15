package models

import "time"

type EnvironmentKVClass string

const (
	EnvironmentKVClassSecret EnvironmentKVClass = "secret"
	EnvironmentKVClassValue  EnvironmentKVClass = "value"
)

// EnvironmentKVEntry stores one server-side materialization value. Secret
// entries use the same model as readable values, but API callers must not
// receive Value except during server-side materialization.
type EnvironmentKVEntry struct {
	ID        string
	HomeID    string
	SliceID   string
	SliceSlug string
	Profile   string
	Key       string
	Class     EnvironmentKVClass
	Value     string
	ValueHash string
	Version   int64
	CreatedBy string
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

type EnvironmentKVFilter struct {
	HomeID  string
	SliceID string
	Profile string
	Class   EnvironmentKVClass
	Key     string
}
