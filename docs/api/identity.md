# API Reference

## Packages

- [identity.miloapis.com/v1alpha1](#identitymiloapis.comv1alpha1)

## identity.miloapis.com/v1alpha1

Package v1alpha1 contains API types for identity-related resources.

### Resource Types

- [UserIdentity](#useridentity)
- [Session](#session)
- [ServiceAccountKey](#serviceaccountkey)

---

### UserIdentity

UserIdentity represents a user's linked identity within an external identity provider.

This resource describes the connection between a Milo user and their account in an external authentication provider (e.g., GitHub, Google, Microsoft). It is NOT the identity provider itself, but rather the user's specific identity within that provider.

**Use cases:**

- Display all authentication methods linked to a user account in the UI
- Show which external accounts a user has connected
- Provide visibility into federated identity mappings

**Important notes:**

- This is a read-only resource for display purposes only
- Identity management (linking/unlinking providers) is handled by the external authentication provider (e.g., Zitadel), not through this API
- No sensitive credentials or tokens are exposed through this resource

#### UserIdentityStatus

| Field | Type | Description |
| :--- | :--- | :--- |
| `userUID` | string | The unique identifier of the Milo user who owns this identity. |
| `providerID` | string | The unique identifier of the external identity provider instance. This is typically an internal ID from the authentication system. |
| `providerName` | string | The human-readable name of the identity provider. Examples: "GitHub", "Google", "Microsoft", "GitLab" |
| `username` | string | The user's username or identifier within the external identity provider. This is the name the user is known by in the external system (e.g., GitHub username). |

---

### Session

Session represents an active user session in the system.

This resource provides information about user authentication sessions, including the provider used for authentication and session metadata.

**Use cases:**

- Display active sessions for a user
- Monitor session activity
- Provide session management capabilities in the UI

**Important notes:**

- This is a read-only resource
- Session lifecycle is managed by the authentication provider
- No sensitive session tokens are exposed

#### SessionStatus

| Field | Type | Description |
| :--- | :--- | :--- |
| `userUID` | string | The unique identifier of the user who owns this session. |
| `provider` | string | The authentication provider used for this session. |
| `ip` | string | The IP address from which the session was created (optional). |
| `fingerprintID` | string | A fingerprint identifier for the session (optional). |
| `createdAt` | metav1.Time | The timestamp when the session was created. |
| `lastUpdatedAt` | *metav1.Time | The last time the provider updated this session, e.g. Zitadel `change_date` (optional). |
| `userAgent` | string | The client User-Agent string for this session, when the provider supplies it (optional). |

---

### ServiceAccountKey

ServiceAccountKey represents a credential for a ServiceAccount.

This resource allows users to manage API keys for machine-to-machine authentication. When a ServiceAccountKey is created, the system generates a private key that is returned in the status only once.

**Use cases:**

- Authenticating external services and automation scripts
- Managing key rotation and expiration
- Auditing service account activity

**Important notes:**

- The `privateKey` is ONLY available in the creation response and is NEVER persisted in the Milo API server.
- Keys can have an optional expiration date.
- Each key is associated with a specific `ServiceAccount` identified by its email.

#### ServiceAccountKeySpec

| Field | Type | Description |
| :--- | :--- | :--- |
| `serviceAccountUserName` | string | The email address of the ServiceAccount that owns this key. |
| `expirationDate` | metav1.Time | Optional date and time when the key will expire. |
| `publicKey` | string | Optional public key to be registered. If not provided, one will be auto-generated. |

#### ServiceAccountKeyStatus

| Field | Type | Description |
| :--- | :--- | :--- |
| `authProviderKeyID` | string | Unique identifier for the key in the authentication provider (e.g. Zitadel ID). |
| `privateKey` | string | PEM-encoded RSA private key. Only present in the response of a creation event. |
| `conditions` | []metav1.Condition | Standard Kubernetes conditions for resource status. |
