# Identity and access management

This page is the reference for how `infra-api` decides *who* is calling and
*what* they may do. The [resource model](resource-model.md#organization-folder-and-project-hierarchy)
introduces the organization/folder/project hierarchy that access is scoped
to; this page covers the full picture: permissions, roles, the authorization
decision itself, and the authentication mechanisms that feed it an identity.

## Permissions

A fixed, Go-defined vocabulary of four permissions (`internal/iam`), generic
across every resource kind - there is no per-kind action list:

| Permission | Grants                                              |
| ---------- | ---------------------------------------------------- |
| `read`     | `list`/`get` a resource                              |
| `write`    | create or update a resource                          |
| `delete`   | delete a resource                                    |
| `admin`    | manage `iam_binding`s at or under the granted scope  |

## Roles

A role bundles a fixed set of permissions. Custom roles are a deliberate
non-goal: a role name is one of three Go constants, each resolving to the
same permission set everywhere it's granted.

| Role           | Permissions                        |
| -------------- | ----------------------------------- |
| `roles/viewer` | `read`                              |
| `roles/editor` | `read`, `write`, `delete`           |
| `roles/owner`  | `read`, `write`, `delete`, `admin`  |

A role is granted to members via an **IAM binding** (`iam_binding`), scoped
to a resource (see below).

## The global `admin` role

Separate from the three `roles/*` above is a single global role, the string
`"admin"`. It lives in its own namespace:

- stored on `User.spec.roles` (free-text list; only the literal `"admin"` is
  ever interpreted - any other string is inert);
- carried on an issued JWT's `roles` claim, or returned by access-token
  introspection;
- checked directly via `Identity.HasRole("admin")`, bypassing the
  organization/folder/project scope chain entirely.

`admin` is the *only* gate on three resource kinds that never go through the
scope chain at all: `user`, `access_token`, and `iam_binding` - see
[Kinds with no self-service path](#kinds-with-no-self-service-path).

The first admin is seeded by `infra-idp` from the `GOA_IDP_BOOTSTRAP_ADMIN`
environment variable (`username:password`), and only while the user store is
still empty - `user` management is itself admin-gated, so this is the only
way to create the first one.

## Scope: the organization/folder/project hierarchy

Every resource that participates in IAM carries a `metadata.projectId`. A
**project** attaches to an **organization** or to a **folder**, and a folder
may itself nest under an organization or another folder; an organization is
always the root.

An **IAM binding** (`iam_binding`) grants one role to a set of members on a
target resource - an organization, a folder, a project, or any other
resource for one-off sharing:

```json
{
  "spec": {
    "resource": { "kind": "project", "uid": "proj-1" },
    "role": "roles/editor",
    "members": ["user:alice"]
  }
}
```

- `members` entries are `"<kind>:<id>"` (today only the `user:` prefix is
  used - group and service-account principals are a possible future kind,
  which is why the prefix is required from the start).
- A binding on an organization or folder is inherited by every project (and
  its resources) underneath it.

To resolve what a project inherits, `internal/iam.ScopeChain` walks
`project -> folder... -> organization`, following each folder's or
project's `parentKind`/`parentId` up to 64 hops before failing loudly
(a guard against a parent cycle). `EffectivePermissions` then unions the
permissions of every binding whose target is anywhere in that chain and
whose `members` include the caller.

## How a request is authorized

Every generic CRUD handler (`internal/handler.Handler`) funnels through one
`authorize()` call per request, which defers to `iam.Authorizer.Allowed`:

1. **Admin bypass** - `isAdmin` (the caller's `admin` role) always allows.
2. **Owner fast path** - if the resource's `metadata.ownerUid` (stamped by
   the API at creation, never client-settable) equals the caller, always
   allow. This is why *creating* a resource never needs a binding: the
   creator becomes its owner.
3. **No project, no chain** - a resource with an empty `metadata.projectId`
   has nothing to inherit from, so only its owner or an admin can reach it.
4. Otherwise, walk the resource's project's `ScopeChain` and check whether
   any binding there grants the caller the needed permission.

| Verb (HTTP)    | Permission required                                      |
| -------------- | ---------------------------------------------------------- |
| `GET` (list)   | `read`, filtered per item - callers only see what they can read |
| `GET` (item)   | `read`                                                      |
| `PUT` (create) | none if unscoped (`projectId` empty); `write` on the target project if scoped |
| `PUT` (update) | `write`, checked against the *existing* owner/project       |
| `DELETE`       | `delete`, checked against the existing owner/project        |

`metadata.projectId` is immutable once set - a `PUT` that tries to change it
is rejected with `400`, so a resource can't be moved out of the scope it was
authorized into.

With neither `WithAuthorization` nor `WithAdminOnly` configured on a handler
(i.e. the API started without any authenticator wired up), every request is
served regardless of identity - the same open posture the API had before
this model existed.

### Kinds with no self-service path

Three kinds skip the scope chain entirely and require the global `admin`
role for every verb:

- **`user`** and **`access_token`** - managing another principal's identity
  or credentials is kept out of the ownership/binding model on purpose. A
  user may still `GET` their own `user` record without being an admin.
- **`iam_binding`** - a binding is what *grants* access, so letting its own
  creator manage it via the normal owner fast path would let any caller
  grant themselves a role on a project they otherwise can't touch.

### Not yet covered

`secret` and `ssl` resources are not wired to any authorization check yet,
regardless of whether authentication is enabled - tracked as a gap in
[the resource model](resource-model.md#organization-folder-and-project-hierarchy).

## Authentication: resolving the caller's identity

Authorization needs an `Identity{Subject, Roles}` to check against; that
comes from one of three `auth.Authenticator` implementations, tried in order
by `auth.Chain` until one succeeds:

| Authenticator            | Credential                          | Roles source                                  |
| ------------------------- | ------------------------------------ | ---------------------------------------------- |
| `TokenAuthenticator`      | static bearer token                  | none - bootstrap mechanism ahead of OIDC, owner fast path only |
| `JWTAuthenticator`        | ES256 JWT signed by `infra-idp`      | baked into the token's `roles` claim at issuance; trusted until the token expires |
| `IntrospectionAuthenticator` | opaque `access_token` (`infra_...`) | resolved *live* on every call via `infra-idp`'s `/introspect` |

`infra-api` enables whichever of these it has configuration for, via these
environment variables:

| Variable                        | Enables                                   |
| -------------------------------- | ------------------------------------------ |
| `GOA_API_TOKENS`                 | static tokens (`token:subject,...`)        |
| `GOA_API_JWT_PUBKEY`, `GOA_API_JWT_ISSUER` | JWT verification               |
| `GOA_API_IDP_INTROSPECT_URL`, `GOA_API_IDP_CLIENT_ID`, `GOA_API_IDP_CLIENT_SECRET` | access-token introspection against `infra-idp` |

None set at all means `infra-api` runs fully unauthenticated.

The live-introspection path matters operationally: because it re-resolves
the token owner's *current* `User.spec.roles` on every request, disabling
or demoting a user invalidates every access token they hold immediately -
unlike a JWT, which stays valid with its original roles until it expires.

## `infra-idp`: issuing credentials

`infra-idp` is the only thing that mints JWTs and validates access tokens.
Its HTTP surface:

| Route                                  | Purpose                                                        |
| ---------------------------------------- | --------------------------------------------------------------- |
| `POST /login`                            | username/password (bcrypt-verified) -> short-lived JWT          |
| `POST /token`                            | OAuth2 `client_credentials` or device-grant token exchange      |
| `POST /device_authorization`             | start an RFC 8628 device grant (`infra login` CLI flow)         |
| `POST /device/verify`                    | an already-authenticated human approves a pending device code   |
| `POST /introspect`                       | RFC 7662 introspection, called by `infra-api`                   |
| `GET /userinfo`                          | the caller's own `{subject, roles}` - used by the `www` console |
| `GET /jwks.json`, `GET /.well-known/openid-configuration` | standard OIDC discovery/keys endpoints |

Three ways a caller ends up with a credential:

- **Password login** (human, via the `www` console) - `POST /login` against
  `internal/identity.Service`, which stores only a bcrypt hash
  (`User.status.passwordHash`).
- **Client credentials** (machine-to-machine, e.g. the Terraform provider) -
  `POST /token` with `grant_type=client_credentials`, against an
  operator-configured `client_id:client_secret` map (`GOA_IDP_CLIENTS`); the
  issued JWT carries no roles.
- **Device authorization grant** (the `infra` CLI) - the CLI starts a device
  code, a human approves it in the `www` console while authenticated as
  themselves, and the CLI polls `/token` until it receives a JWT for the
  approving human's own identity. Credentials are cached locally in
  `~/.config/infra/credentials.json` (mode `0600`).

## Access tokens

An `access_token` is a long-lived, revocable credential that always acts as
its creator (`internal/accesstoken`):

- the plaintext (`infra_<32 random bytes, base64url>`) is generated
  server-side and returned exactly once, at creation;
- only its SHA-256 hash is ever stored (`access_token.status.tokenHash`);
- `Introspect` looks up the hash, rejects an expired token, and returns the
  *current* roles of the token's owner - not a snapshot taken at creation.

Managing any `access_token` - including one's own - requires the `admin`
role; see [Kinds with no self-service path](#kinds-with-no-self-service-path).

## Console (`www`)

The console fetches the signed-in user's identity once, via `/userinfo`
(`www/src/auth-context.tsx`'s `AuthProvider`/`useAuth()`), and gates its own
navigation on it: the "Users" and "Access tokens" entries under `/iam` only
render when `roles` includes `"admin"` (`www/src/components/Layout.tsx`).
Those two pages are the UI for the admin-only `user` and `access_token`
kinds described above - the console itself enforces no additional policy
beyond what the API already requires.
