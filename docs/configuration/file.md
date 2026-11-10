---
title: Configuration file
weight: 1
---

Ocman uses a versioned YAML file for startup options and supported application
settings. The UI saves supported settings back to this file. Configuration tools
can manage the same file; ocman does not attempt to reconcile changes with Git.

## Location and first-run migration

Select the file with `-config /path/to/config.yaml` or `OCMAN_CONFIG`. Otherwise
ocman uses `$XDG_CONFIG_HOME/ocman/config.yaml`, falling back to
`~/.config/ocman/config.yaml`. When `OCMAN_STATE_DB` explicitly isolates the state
database, the default config is `config.yaml` beside that database instead. This
keeps development and test instances away from the daily driver's config.

At startup, after database schema migrations and before serving application
requests, ocman creates a missing file from supported saved database settings.
This also works for a new installation with no saved settings. Existing files
are authoritative and are never overwritten by this migration.

The migration leaves original database settings untouched as a backup. Subsequent
UI saves update the file, not those old rows. Removing a setting from YAML uses
the application default, not the stale database backup. After a successful
cutover, a missing configuration file is an error: restore it rather than
silently importing old settings again.

Migration is performed outside the SQLite schema transaction. Secret slots are
committed before a complete configuration is atomically published; an interrupted
migration can safely retry. Retain both the config file and a SQLite-consistent
database backup. Old backup rows may still contain legacy plaintext credentials.

## Example

```yaml
version: 1

startup:
  addr: "0.0.0.0:8228"
  public-base-url: "https://ocman.example.com"
  auth-password: "secret://file/run/secrets/ocman_password"
  platforms: "claudecode"
  gui: false

settings:
  session.default_agent: build
  worktree.inherit_permissions: false
  judge_model: "provider/model"
  judge_delay: 5000
  judge_endpoint:
    format: openai
    endpoint: "https://reviewer.example.com/v1/chat/completions"
    model: reviewer
    apiKey: "secret://env/REVIEWER_API_KEY"
    minSafeProbability: 0.99
  prompt_sections:
    - title: Production changes
      content: Always ask before modifying production.
      enabled: true
```

`startup` keys use the existing flag names, without the leading dash. Values
must be scalars. `config` itself cannot be specified inside the file. Unknown
startup options, unknown settings, duplicate keys, multiple YAML documents,
anchors, aliases and unsupported schema versions are rejected. The file has a
4 MiB size limit.

Startup precedence is explicit CLI flags, supported environment overrides, file
values, then built-in defaults. Existing password precedence remains environment,
password-file flag, then password flag or its file-configured value. In YAML,
`startup.auth-password` must be a secret reference, never a plaintext password.
The original `-auth-password-file` option also remains available.

Environment overrides currently supported by the file loader are:

| Variable | Startup option |
| --- | --- |
| `OCMAN_ADDR` | `addr` |
| `OCMAN_GUI` | `gui` |
| `OCMAN_MCP_ADDR` | `mcp-addr` |
| `OCMAN_PLATFORMS` | `platforms` |
| `OCMAN_PUBLIC_BASE_URL` | `public-base-url` |
| `OCMAN_RELAY_URL` | `relay-url` |
| `OCMAN_CONVERSATIONS_DB` | `conversations-db` |
| `OCMAN_REMOTE_LISTEN` | `remote-listen` |

Other existing environment variables keep their documented behavior. A
`startup.platforms` value or `OCMAN_PLATFORMS` pins the platform selection just
like the corresponding flag; omit it to use editable `settings.platforms.enabled`.

Restart ocman after editing startup options. Supported application settings are
read from the file when their existing setting APIs are called. There is no file
watcher or general live-reload guarantee for services that cache configuration.
Local sidebar reads use the effective file-backed archive resurfacing setting,
not the retained SQLite backup. YAML boolean spellings such as `False` and
`FALSE` are normalized before reaching legacy setting interfaces.

## Secret references

References are whole values, not interpolation expressions:

| Reference | Source |
| --- | --- |
| `secret://env/VARIABLE_NAME` | An environment variable |
| `secret://file/run/secrets/key` | The absolute file `/run/secrets/key` |
| `secret://ocman/<key>` | An encrypted credential in this instance's database |

Provider references use percent-encoding for special characters such as `#`,
`?` and `%`; decoding happens exactly once. File secrets must be regular files,
are limited to 1 MiB, and have trailing CR/LF removed. Other whitespace remains.
File contents and environment values are never interpreted as another reference.
Mounted-secret symlinks are supported for secret reads.

Missing or empty required credentials, invalid references and unavailable
providers fail startup. At runtime, a failed reference returns an error rather
than falling back to an old database credential. Provider failures never include
the resolved secret. Only designated credential fields resolve references;
ordinary configuration strings remain literal.

The first providers are `env`, `file` and `ocman`. Other provider names, including
`1password`, are reserved for future implementations and currently fail. No
shell execution, secret-manager CLI invocation or recursive resolution occurs.

New UI credentials are encrypted and referenced with `secret://ocman/...`.
Existing ocman-managed credentials can be replaced or cleared through their
existing UI. External references cannot be replaced or removed by secret-update
API calls. Change the external source, or explicitly edit the configuration's
reference to transfer ownership. Ocman never changes environment variables or
externally referenced files.

Configuration credentials use AES-GCM with a dedicated stable key stored in the
owner-only database. Cookie-key rotation does not rotate that key. This protects
against accidental disclosure, not an attacker who can read the database and its
key. External master-key storage is not implemented in this stage. Externally
resolved values are not copied into the managed secret store.

## UI saves and scope

UI saves preserve document comments and unrelated fields, though YAML formatting
may change. Writes are atomic, use owner-only permissions, and serialize
cooperating ocman writers. An external content change detected during a save
rejects the save. External tools should use atomic file replacement; the lock is
not a transaction with editors that ignore it. Read-only configs can be used
headlessly but cannot be changed through the UI. Config symlinks are readable but
UI writes to them are refused rather than replacing the symlink.

This first migration covers the shared settings API: platform selection and
defaults, session defaults and resurfacing, worktree permission inheritance,
reviewer configuration and prompt sections, archive settings, model fallthrough,
notification badges, idle timeout, sharing, Factory's beta toggle, forge prompt
templates, link-preview rules, saved preview application bundles, webhook relay
settings, and per-project models and launch defaults.

It does not yet migrate dedicated feature tables such as remotes, installed
plugins, provider connections, preview OAuth grants, or routine definitions.
Generated identities, signing/encryption keys, per-session permissions and
runtime checkpoints remain database state. Fully declarative provisioning of
those remaining feature tables requires follow-up adapters.
