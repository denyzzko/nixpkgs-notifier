# nixpkgs-notifier
 
A web application that tracks Nixpkgs packages and sends notifications when a new version is detected. Users log in via OIDC, subscribe to packages they care about, and receive notifications through email or webhook channels.

## Requirements
 
- [Nix](https://nixos.org/) - used to check package versions via `nix` CLI (must be available on the host)
- PostgreSQL - the application connects to an existing database and creates all tables (only on first startup)
- At least one OIDC identity provider (e.g. Google, Authentik, Keycloak)
- Email provider - either SMTP or [Resend](https://resend.com)

## Building
 
```bash
go build ./cmd/server
```

## Database setup
 
Application connects to existing PostgreSQL database. It does not create the database or the user - only tables. On first startup it runs `internal/database/sql/CREATE_TABLES.sql` automatically and logs the result. On next startups it detects that tables already exist and skips migration.

## Configuration
 
Configuration of the server is loaded from environment variables. File `.env` in the working directory is also supported for local development (variables can be injected directly into the process in production).
 
The application will refuse to start if any required variable is missing or invalid, and will print error message.

### Server
 
| Variable | Required | Default | Description |
|---|---|---|---|
| `SERVER_URL` | ✅ | - | Public-facing URL of the server, e.g. `https://example.com` |
| `SERVER_PORT` | - | `8080` (TLS off) / `443` (TLS on) | Port the process binds to. May differ from `SERVER_URL` when behind a reverse proxy. |
| `TLS_MODE` | - | `off` | Set to `on` to enable TLS |
| `TLS_CERT_FILE` | if `TLS_MODE=on` | - | Path to TLS certificate file |
| `TLS_KEY_FILE` | if `TLS_MODE=on` | - | Path to TLS private key file |

### Database
 
| Variable | Required | Default | Description |
|---|---|---|---|
| `DB_HOST` | ✅ | - | PostgreSQL host |
| `DB_PORT` | ✅ | - | PostgreSQL port |
| `DB_NAME` | ✅ | - | Database name |
| `DB_USER` | ✅ | - | Database user |
| `DB_PASS` | ✅ | - | Database password |
| `DB_SSLMODE` | ✅ | - | SSL mode: `disable`, `require`, `verify-ca`, `verify-full` |
| `DB_SSL_CA_CERT` | if `DB_SSLMODE=verify-full` or `verify-ca` | - | Path to CA certificate |

### Authentication (OIDC)
 
Authentication is handled entirely via OIDC. At least one provider must be configured.
 
`OIDC_PROVIDERS` is a JSON array. Each entry represents one identity provider login button on the login page.
 
| Field | Required | Description |
|---|---|---|
| `name` | ✅ | Short unique identifier used in URLs, e.g. `google` (must be URL-safe) |
| `display_name` | - | Human-readable label shown on the login button (`name` if empty) |
| `issuer` | ✅ | OIDC discovery URL, e.g. `https://accounts.google.com` |
| `client_id` | ✅ | OAuth2 client ID registered with the provider |
| `client_secret` | ✅ | OAuth2 client secret registered with the provider |
| `scopes` | - | OAuth2 scopes to request. Defaults to `["openid", "email", "profile"]` |
 
Example of single provider:
 
```json
[
  {
    "name": "authentik",
    "display_name": "School SSO",
    "issuer": "https://auth.example.com/application/o/notifier/",
    "client_id": "your-client-id",
    "client_secret": "your-client-secret"
  }
]
```
 
Set it as an environment variable (whole JSON as a single-line string):
 
```
OIDC_PROVIDERS=[{"name":"authentik","display_name":"School SSO","issuer":"https://auth.example.com/...","client_id":"...","client_secret":"..."}]
```
 
OIDC redirect URI that needs to be registered with provider:
 
```
{SERVER_URL}/auth/callback
```

### Email
 
| Variable | Required | Default | Description |
|---|---|---|---|
| `EMAIL_PROVIDER` | ✅ | - | `smtp` or `resend` |
| `SMTP_HOST` | if `EMAIL_PROVIDER=smtp` | - | SMTP server hostname |
| `SMTP_PORT` | if `EMAIL_PROVIDER=smtp` | - | SMTP server port |
| `SMTP_FROM` | if `EMAIL_PROVIDER=smtp` | - | From address used in sent emails |
| `SMTP_USER` | - | - | SMTP username. Leave empty for unauthenticated connection. |
| `SMTP_PASS` | - | - | SMTP password. Leave empty for unauthenticated connection. |
| `RESEND_API_KEY` | if `EMAIL_PROVIDER=resend` | - | Resend API key |
| `EMAIL_FROM_ADDR` | if `EMAIL_PROVIDER=resend` | - | From address used in sent emails |
 
SMTP uses STARTTLS if the server supports it. Authentication is only attempted when both `SMTP_USER` and `SMTP_PASS` are set.

### Notification dispatcher (overridable at runtime via admin config UI)
 
Controls how pending notifications are delivered in the background.
 
| Variable | Required | Default | Description |
|---|---|---|---|
| `NOTIFICATION_DISPATCH_INTERVAL` | - | `5m` | How often to poll for pending notifications from database, e.g. `30s`, `5m` |
| `NOTIFICATION_MAX_RETRIES` | - | `3` | Max delivery attempts before giving up |
| `NOTIFICATION_WORKER_COUNT` | - | `2` | Max concurrent deliveries |
| `NOTIFICATION_DISABLE_ON_MAX_RETRIES` | - | `true` | Automatically disable a channel after it reaches max retries |

### Package checker (overridable at runtime via admin config UI)
 
Controls how often Nixpkgs package versions are checked.
 
| Variable | Required | Default | Description |
|---|---|---|---|
| `PACKAGE_CHECK_INTERVAL` | - | `12h` | How often to check all tracked packages for new versions |
| `PACKAGE_CHECK_WORKER_COUNT` | - | `2` | Max concurrent package checks |
| `PACKAGE_CHECK_SKIP_THRESHOLD` | - | `5m` | Skip re-checking package that was already checked within this interval |

## Project structure
 
```
cmd/server/         - main entry point
internal/
  app/              - application-level business logic
  appError/         - typed app errors (used across packages to distinguish error kinds)
  auth/             - OIDC authentication setup
  checker/          - background package version checker
  database/         - PostgreSQL connection, queries, migrations
  dispatcher/       - background notification delivery loop
  env/              - configuration loading and validation
  middleware/       - HTTP middleware
  nix/              - Nix CLI integration
  notify/           - email and webhook senders (SMTP, Resend, webhook)
  session/          - session management
  ui/               - HTML templates
  web/              - HTTP handlers and routing
```

## Project status

The core functionality is complete and ready for deployment.

**What works:**
- OIDC login and session management
- Tracking Nixpkgs packages and checking them for new versions
- Adding notification channels
- Sending email notifications via SMTP and Resend
- Sending webhook notifications (generic JSON and Mattermost)
- Notification log with delivery status
- Background periodic package version check by the system 

**Not yet implemented:**
- Admin panel for configuration in UI - dispatcher/checker intervals and limits are configurable via env vars at startup but cannot be changed at runtime
- Admin user management in UI
- Notification history auto-cleanup
- Dropdown with common package branches when tracking a new package
- Highligh channel in Channels page when system automatically deactivates it because system failed to send notification to it in MaxRetries attempts
- Visual and other details