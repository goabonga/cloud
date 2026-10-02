# Secrets and PKI

The control plane stores secret material encrypted at rest. Every private value
is sealed with the key-encryption key (KEK) before it is written and is only
recoverable through an explicit reveal call.

## Enabling encryption

The secret and PKI routes are served only when the API has a KEK. Provide it as
a base64-encoded 32-byte key in `GOA_KMS_KEY`:

```bash
GOA_KMS_KEY="$(head -c 32 /dev/urandom | base64)" infra-api
```

Without `GOA_KMS_KEY` the API logs `secret routes disabled` and the
`/secret`, `/secret_version`, `/ssl_ca` and `/ssl_cert` endpoints are absent.

## Secrets

A secret holds an opaque value encrypted with the KEK. Writes take the
plaintext; reads never return it. Plaintext is recoverable only through the
dedicated reveal endpoint.

| Method | Path | Purpose |
| --- | --- | --- |
| `PUT` | `/api/v1/secret/{uid}` | Store (encrypt) a value. |
| `GET` | `/api/v1/secret` | List secrets (redacted). |
| `GET` | `/api/v1/secret/{uid}` | Get a secret (redacted). |
| `GET` | `/api/v1/secret/{uid}/reveal` | Decrypt and return the value. |
| `DELETE` | `/api/v1/secret/{uid}` | Delete a secret. |

### Versions

A `secret_version` is an immutable, separately encrypted snapshot of a secret's
data, numbered sequentially per secret. Versions let you keep history or stage a
rotation before promoting it.

| Method | Path | Purpose |
| --- | --- | --- |
| `PUT` | `/api/v1/secret_version/{uid}` | Store a new version (body: `secretId`, `data`). |
| `GET` | `/api/v1/secret_version?secretId=...` | List versions of a secret. |
| `GET` | `/api/v1/secret_version/{uid}/reveal` | Decrypt and return a version. |
| `DELETE` | `/api/v1/secret_version/{uid}` | Delete a version. |

The assigned version number is reported in `status.version`.

## PKI

A certificate authority (`ssl_ca`) holds a self-signed CA whose private key is
KEK-encrypted; its public certificate is served in clear. A CA can sign leaf
certificates two ways:

- **Ephemeral** - `POST /api/v1/ssl_ca/{uid}/issue` returns a fresh certificate
  and key without storing them.
- **Persisted** - an `ssl_cert` is signed by a CA and stored, with its private
  key encrypted at rest and recoverable through reveal. A certificate can cover
  DNS names and IP addresses.

| Method | Path | Purpose |
| --- | --- | --- |
| `PUT` | `/api/v1/ssl_ca/{uid}` | Create a CA. |
| `POST` | `/api/v1/ssl_ca/{uid}/issue` | Issue an ephemeral leaf certificate. |
| `PUT` | `/api/v1/ssl_cert/{uid}` | Sign and store a leaf (body: `caId`, `commonName`, `dnsNames`, `ipAddresses`). |
| `GET` | `/api/v1/ssl_cert/{uid}/reveal` | Return the certificate and decrypted key. |
| `DELETE` | `/api/v1/ssl_cert/{uid}` | Delete a certificate. |

Private keys (CA and leaf) never appear in list or get responses and are never
written in clear on the platform's side. Two paths decrypt a leaf key: reveal,
and an agent rendering a disk file that holds the key for an instance.

The API creates the platform's public root, `public-root`, at its first start
when a KMS key is configured (common name `GOA_PUBLIC_ROOT_CN`, default
`infra public root`). It is global: every instance trusts it, and it signs the
certificates of public names. The API refuses to replace or delete it, and to
create another global CA. Any other CA is trusted by the instances of the VPCs
its `vpcIds` names.
