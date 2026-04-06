package models

import "time"

type AccountOwnerMode string

const (
	AccountOwnerModeAgentOnly     AccountOwnerMode = "agent_only"
	AccountOwnerModeHumanAttached AccountOwnerMode = "human_attached"
	AccountOwnerModeOrgManaged    AccountOwnerMode = "org_managed"
)

type AccountClaimState string

const (
	AccountClaimStateUnclaimed AccountClaimState = "unclaimed"
	AccountClaimStateClaimed   AccountClaimState = "claimed"
)

// Account is the root local identity record. Human WorkOS identities may attach
// later, but agent-created accounts can exist before any human auth exists.
type Account struct {
	AccountID      string            `json:"account_id"`
	OwnerMode      AccountOwnerMode  `json:"owner_mode"`
	ClaimState     AccountClaimState `json:"claim_state"`
	ClaimTokenHash string            `json:"claim_token_hash"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// User is the local username-facing identity that owns a home slice and may be
// linked to a local account and later to a WorkOS user.
type User struct {
	Username     string    `json:"username"`
	AccountID    string    `json:"account_id"`
	Name         string    `json:"name"`
	PrimaryEmail string    `json:"primary_email"`
	PasswordHash string    `json:"password_hash"`
	AuthSource   string    `json:"auth_source"`
	WorkOSUserID string    `json:"workos_user_id"`
	RootPath     string    `json:"root_path"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Organization groups users together.
type Organization struct {
	Slug                 string    `json:"slug"`
	Name                 string    `json:"name"`
	CreatedBy            string    `json:"created_by"`
	WorkOSOrganizationID string    `json:"workos_organization_id"`
	RootPath             string    `json:"root_path"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type OrganizationRole string

const (
	OrganizationRoleOwner  OrganizationRole = "owner"
	OrganizationRoleAdmin  OrganizationRole = "admin"
	OrganizationRoleMember OrganizationRole = "member"
)

type OrganizationMember struct {
	OrgSlug            string           `json:"org_slug"`
	Username           string           `json:"username"`
	Role               OrganizationRole `json:"role"`
	WorkOSMembershipID string           `json:"workos_membership_id"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

type OrganizationInviteStatus string

const (
	OrganizationInvitePending  OrganizationInviteStatus = "pending"
	OrganizationInviteAccepted OrganizationInviteStatus = "accepted"
	OrganizationInviteDeclined OrganizationInviteStatus = "declined"
)

type OrganizationInvite struct {
	InviteID    string                   `json:"invite_id"`
	OrgSlug     string                   `json:"org_slug"`
	TargetEmail string                   `json:"target_email"`
	Role        OrganizationRole         `json:"role"`
	Status      OrganizationInviteStatus `json:"status"`
	CreatedBy   string                   `json:"created_by"`
	CreatedAt   time.Time                `json:"created_at"`
	UpdatedAt   time.Time                `json:"updated_at"`
}

type Team struct {
	TeamID    string    `json:"team_id"`
	OrgSlug   string    `json:"org_slug"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TeamMember struct {
	TeamID   string    `json:"team_id"`
	Username string    `json:"username"`
	AddedAt  time.Time `json:"added_at"`
}

type AuthSession struct {
	SessionID             string     `json:"session_id"`
	Username              string     `json:"username"`
	AgentKeyID            string     `json:"agent_key_id,omitempty"`
	Token                 string     `json:"token"`
	RefreshToken          string     `json:"refresh_token,omitempty"`
	DeviceInfo            string     `json:"device_info"`
	CreatedAt             time.Time  `json:"created_at"`
	LastSeenAt            time.Time  `json:"last_seen_at"`
	AccessTokenExpiresAt  *time.Time `json:"access_token_expires_at,omitempty"`
	RefreshTokenExpiresAt *time.Time `json:"refresh_token_expires_at,omitempty"`
	RevokedAt             *time.Time `json:"revoked_at,omitempty"`
}

type AgentKeyState string

const (
	AgentKeyStateActive  AgentKeyState = "active"
	AgentKeyStateRevoked AgentKeyState = "revoked"
)

type AgentKey struct {
	KeyID       string        `json:"key_id"`
	Username    string        `json:"username"`
	Name        string        `json:"name"`
	Algorithm   string        `json:"algorithm"`
	PublicKey   []byte        `json:"public_key"`
	Fingerprint string        `json:"fingerprint"`
	State       AgentKeyState `json:"state"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	LastUsedAt  *time.Time    `json:"last_used_at,omitempty"`
	RevokedAt   *time.Time    `json:"revoked_at,omitempty"`
}

type AgentKeyChallenge struct {
	ChallengeID string     `json:"challenge_id"`
	AgentKeyID  string     `json:"agent_key_id"`
	Username    string     `json:"username"`
	Challenge   []byte     `json:"challenge"`
	DeviceInfo  string     `json:"device_info"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	UsedAt      *time.Time `json:"used_at,omitempty"`
}

type DeviceAuthorizationStatus string

const (
	DeviceAuthorizationPending  DeviceAuthorizationStatus = "pending"
	DeviceAuthorizationApproved DeviceAuthorizationStatus = "approved"
	DeviceAuthorizationDenied   DeviceAuthorizationStatus = "denied"
)

type DeviceAuthorization struct {
	DeviceCode string                    `json:"device_code"`
	UserCode   string                    `json:"user_code"`
	Username   string                    `json:"username,omitempty"`
	SessionID  string                    `json:"session_id,omitempty"`
	DeviceInfo string                    `json:"device_info"`
	Status     DeviceAuthorizationStatus `json:"status"`
	CreatedAt  time.Time                 `json:"created_at"`
	ExpiresAt  time.Time                 `json:"expires_at"`
	ApprovedAt *time.Time                `json:"approved_at,omitempty"`
	DeniedAt   *time.Time                `json:"denied_at,omitempty"`
}
