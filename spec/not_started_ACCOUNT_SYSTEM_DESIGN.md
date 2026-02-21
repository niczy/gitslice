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

## Execution Plan (PR by PR)

### Delivery Rules
- APIs are gRPC-first: define/extend `.proto` services and expose HTTP via grpc-gateway bindings.
- Do not add standalone `net/http` handlers for `/v1/*` account system routes.
- Complete one PR at a time: implement scope, run tests, push, wait for CI pass, merge, switch to `main`, `git pull --ff-only`, then start next PR.
- Keep each PR independently mergeable and production-safe.

### PR Sequence

1. **PR1 - Account service scaffolding**
   - Add `proto/account/account_service.proto` with route coverage for auth, users, orgs, memberships, teams, nodes, ACL, sharing, activity.
   - Add grpc-gateway HTTP annotations for all target endpoints.
   - Register account service in core server and gateway mux.
   - Add server skeleton/stub implementation (unimplemented where needed).
   - Exit criteria: route surface exists via gateway, build passes.

2. **PR2 - Auth core + sessions**
   - Implement signup/login/logout and password reset flow.
   - Implement session listing + session revocation.
   - Add persistent account/session storage fields and migrations (memory + postgres parity).
   - Keep `Authorization: User <username>` only as explicit dev fallback path.
   - Exit criteria: auth/session APIs functional end-to-end.

3. **PR3 - Users API**
   - Implement `GET/PATCH/DELETE /users/me` and `GET /users/{user_id}`.
   - Add validation, profile updates, and safe account deletion semantics.
   - Exit criteria: user APIs covered by unit/integration tests.

4. **PR4 - Organizations + namespace ownership**
   - Implement org create/list/get/update/delete.
   - Enforce root slug uniqueness across users and orgs.
   - Materialize ownership roots (`/{username}`, `/{org_slug}`) and role defaults.
   - Exit criteria: uniqueness and ownership rules enforced in storage/service layers.

5. **PR5 - Memberships + invites**
   - Implement invite create/accept/decline.
   - Implement member list/update/remove with admin safeguards (e.g. last-admin protection).
   - Exit criteria: invite lifecycle and membership transitions validated.

6. **PR6 - Teams**
   - Implement team CRUD and team membership add/remove.
   - Enforce org-scoped authorization for team management.
   - Exit criteria: teams usable as ACL principals.

7. **PR7 - Nodes model and APIs**
   - Implement file/folder node APIs (`GET/POST/PATCH/DELETE`, move/copy, children list).
   - Add path normalization and ownership boundary checks.
   - Exit criteria: namespace operations behave correctly for user/org roots.

8. **PR8 - ACL engine**
   - Implement ACL CRUD and effective access endpoint.
   - Enforce precedence: explicit node ACL > inherited ACL > org default > none.
   - Enforce admin/owner override and write-implies-read.
   - Exit criteria: central access evaluator integrated into node operations.

9. **PR9 - Sharing + shared view + activity**
   - Implement share create/delete and shared listing.
   - Implement activity query by path.
   - Add audit/event records for permission-relevant operations.
   - Exit criteria: shared and activity experiences are test-covered and consistent with ACLs.

10. **PR10 - OAuth/SSO linking + web migration**
   - Implement auth method linking/unlinking and OAuth/SAML callback integration in account service.
   - Migrate web app account calls to account gRPC-gateway endpoints.
   - Remove legacy standalone account HTTP API paths from runtime wiring.
   - Exit criteria: web login/profile/org workflows operate through gRPC+gateway only.

11. **PR11 - E2E hardening + spec completion**
   - Extend integration and web e2e coverage for auth/org/team/ACL/share paths.
   - Update docs (`README`, operational notes) for new account behavior.
   - Rename status to done and move this spec to finished state.
   - Exit criteria: CI green, tests stable, spec marked finished.

### Per-PR Verification Gate
1. Run local checks:
   - `export PATH=$HOME/.local/go/bin:$HOME/.local/protoc/bin:$PATH && make test`
   - `RUN_INTEGRATION_TESTS=1 make test` (for auth/account semantics changes)
   - `cd web && npm run test:e2e` (for web/auth UX changes)
2. Push branch and open PR.
3. Wait for GitHub Actions checks to pass.
4. Merge PR.
5. Switch to `main` and pull latest before next PR.
