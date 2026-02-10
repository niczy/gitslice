package models

import "time"

// User is a fake account identified only by a username.
// There are no passwords or emails; identity is carried via request metadata.
type User struct {
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Organization groups users together.
type Organization struct {
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OrganizationRole string

const (
	OrganizationRoleOwner  OrganizationRole = "owner"
	OrganizationRoleMember OrganizationRole = "member"
)

type OrganizationMember struct {
	OrgSlug   string           `json:"org_slug"`
	Username  string           `json:"username"`
	Role      OrganizationRole `json:"role"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}
