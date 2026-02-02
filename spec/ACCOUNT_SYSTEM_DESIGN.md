# Account System + Files/Folders Model

## Overview
GitSlice has no "projects." Everything is represented as files and folders in a single namespace. Each user owns a root at `/{user_name}`, and each organization owns a root at `/{org_name}`. Access is path-based with inheritance and explicit overrides.

## Goals
- Support individual developers and org admins/teams.
- Provide all auth methods (email/password, OAuth, SSO/SAML).
- Enforce user/admin roles with read/write permissions.

## Non-Goals
- Billing and compliance features.
- Project-level entities.

## Core Concepts
- **User:** Individual account with a personal root folder.
- **Organization:** Shared account space with its own root folder.
- **Team:** Org-scoped grouping for access control.
- **Node:** File or folder in the namespace.
- **ACL Entry:** Permission grant (read/write) for a principal (user/team/org).
- **Share:** Explicit access grant to an external principal.
- **Session:** Active login session and device metadata.

## Information Architecture
- **Global:** Auth, Home, Search, Activity, Settings.
- **Spaces:** User space (`/{user_name}`), org spaces (`/{org_name}`).
- **Virtual Views:** Shared with me, Recent.

## Web App Layout
- **Top Bar:** Logo, global search, org/user switcher, create (+).
- **Left Rail:** Spaces list, Shared, Recent, Settings.
- **Main Pane:** File browser (list/columns), breadcrumbs, sort/filter.
- **Right Drawer:** Metadata, access list, activity, actions.
- **Status Bar:** Background operations (optional).

## Key Screens
- **Auth:** Email/password + OAuth + SSO/SAML, account linking.
- **Home:** Quick access to My Space, Orgs, Shared.
- **File Browser:** Inline rename, drag/move, bulk select, permissions badges.
- **Access Panel:** ACL editor showing inherited vs explicit grants.
- **Org Admin:** Members, teams, invites, default access.
- **Settings:** Profile, security (sessions), organizations.

## Permissions Model
- **Roles:** user, admin.
- **Permissions:** read, write (write implies read).
- **Inheritance:** Folder ACLs apply to descendants unless overridden.
- **Priority:** Explicit node ACL > inherited ACL > org default > no access.
- **Admin Override:** Org admins can always read/write within org root.
- **Owner Override:** Users can always read/write within their own root.

## Access Rules
- Share any owned folder with read/write to another user or team.
- Org admin can set default ACL on `/{org_name}` root.
- Explicit subfolder ACLs can narrow or expand access.
- Slug uniqueness required across users and orgs to avoid root collisions.

## API Design

### Auth
```
POST /auth/signup
POST /auth/login
POST /auth/logout
POST /auth/password/reset
POST /auth/oauth/{provider}/callback
POST /auth/sso/saml/callback
GET  /auth/methods
POST /auth/methods/link
DELETE /auth/methods/{method_id}
GET  /auth/sessions
DELETE /auth/sessions/{session_id}
```

### Users
```
GET  /users/me
PATCH /users/me
DELETE /users/me
GET  /users/{user_id}
```

### Organizations
```
POST /orgs
GET  /orgs
GET  /orgs/{org_id}
PATCH /orgs/{org_id}
DELETE /orgs/{org_id}
```

### Memberships & Teams
```
POST /orgs/{org_id}/invites
POST /orgs/{org_id}/invites/{invite_id}/accept
POST /orgs/{org_id}/invites/{invite_id}/decline
GET  /orgs/{org_id}/members
PATCH /orgs/{org_id}/members/{member_id}
DELETE /orgs/{org_id}/members/{member_id}

POST /orgs/{org_id}/teams
GET  /orgs/{org_id}/teams
PATCH /orgs/{org_id}/teams/{team_id}
DELETE /orgs/{org_id}/teams/{team_id}
POST /orgs/{org_id}/teams/{team_id}/members
DELETE /orgs/{org_id}/teams/{team_id}/members/{member_id}
```

### Nodes (Files/Folders)
```
GET  /nodes?path=/{owner}/{path}
POST /nodes                  (create file/folder)
PATCH /nodes/{node_id}       (rename/move)
DELETE /nodes/{node_id}
POST /nodes/{node_id}/copy
POST /nodes/{node_id}/move
GET  /nodes/{node_id}/children
```

### ACLs
```
GET  /nodes/{node_id}/acl
POST /nodes/{node_id}/acl
DELETE /nodes/{node_id}/acl/{entry_id}
GET  /nodes/{node_id}/access (effective access for caller)
```

### Sharing
```
POST /nodes/{node_id}/share
GET  /shared
DELETE /shares/{share_id}
```

### Activity
```
GET /activity?path=/{owner}/{path}
```

## Data Model (Minimal)
- **User:** id, username, name, primary_email, created_at.
- **Org:** id, slug, name, owner_user_id.
- **Membership:** org_id, user_id, role (user/admin).
- **Team:** id, org_id, name.
- **TeamMember:** team_id, user_id.
- **Node:** id, owner_type (user/org), owner_id, path, name, type (file/folder), parent_id.
- **AclEntry:** id, node_id, principal_type (user/team/org), principal_id, permission (read/write), inherited (bool).
- **Share:** id, node_id, target_principal, permission.
- **Session:** id, user_id, last_seen_at, device_info.
