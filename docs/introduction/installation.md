---
title: Installation
weight: 2
---

## Install a release

The installer supports macOS and Linux. It prefers GitHub release binaries
for amd64 and arm64 and builds the selected tag locally when the download
is unavailable or your architecture has no prebuilt binary.

```sh
# Latest stable, the default
curl -fsSL https://forgejo.nousefreak.be/dries/ocman/raw/branch/main/install.sh | bash -s -- install

# Latest nightly
curl -fsSL https://forgejo.nousefreak.be/dries/ocman/raw/branch/main/install.sh | bash -s -- install nightly

# Exact version, with or without the v prefix
curl -fsSL https://forgejo.nousefreak.be/dries/ocman/raw/branch/main/install.sh | bash -s -- install v0.274.0
```

To inspect the script before executing it, download it to a file and run
`bash install.sh install stable` after reviewing it.

Installation requires Bash, curl, tar and git. The binary goes to
`~/.local/bin/ocman`. Add `~/.local/bin` to your shell's PATH. By default,
the installer starts ocman in the background at <http://127.0.0.1:8228>.
It does not install agent CLIs or configure their accounts. See the
[runtime requirements](_index.md#requirements).

Add `--no-start` to install without starting or stopping ocman. Use this
when managing the process yourself, such as with systemd or launchd.
The installer itself does not arrange startup after a reboot.

## Channels and updates

```sh
ocman-ctl update                 # follow the previously selected channel
ocman-ctl update stable          # switch to stable
ocman-ctl update nightly         # switch to nightly
ocman-ctl update v0.274.0        # pin or downgrade to an exact tag
ocman-ctl update --no-start      # replace files without touching the process
```

Stable uses GitHub's latest full release. Nightly uses the first nightly tag
in GitHub's release listing, paging past non-nightly releases. Dev tags are
not selected for either channel. Exact versions stay pinned across updates.
An exact dev tag such as `v0.274.0-dev.3` builds locally because dev tags
have no release binaries.

If GitHub's channel lookup fails, the installer resolves the highest matching
version from source tags. It still tries that tag's GitHub binary before
building. It never substitutes `main` or another version when a build fails.

The selected channel or exact tag is saved in
`~/.local/share/ocman/install-version`. Updates restart the background process
only after installation succeeds. Restart a process you manage yourself
after replacing its binary.

`ocman-ctl update` keeps the currently installed controller script. To get
installer and service-management fixes too, rerun the bootstrap command from
the first section with your chosen channel or version. Add `--no-start` if
you manage the process yourself. Bootstrap downloads the current controller
from Forgejo's `main` branch independently of the selected binary version.

## Build fallback

Local builds need git, Go, Node.js and pnpm. Use the versions required by
the selected tag's `go.mod`, `mise.toml` and `frontend/package.json`.
The installer installs locked frontend dependencies, builds the frontend,
then builds Go with the selected version stamped into the binary.

```sh
ocman-ctl install nightly --source --no-start
```

Source checkouts live in `~/.local/share/ocman/src`. Existing tracked or
untracked changes stop the update instead of being discarded. Use a dedicated
checkout for the installer, not your working repository.

## Manage the background process

```sh
ocman-ctl status
ocman-ctl start
ocman-ctl stop
ocman-ctl restart
ocman-ctl logs
ocman-ctl doctor
ocman-ctl uninstall
```

Uninstall removes the installer checkout, binaries and logs. Application data
is kept unless you explicitly use `ocman-ctl uninstall --purge`.

## Overrides

Keep path overrides consistent when invoking `ocman-ctl` later.

| Variable | Default | Purpose |
| --- | --- | --- |
| `OCMAN_VERSION` | `stable` on first install, saved selection later | Channel or exact tag |
| `OCMAN_PREFIX` | `~/.local` | Install prefix |
| `OCMAN_BIN` | `$OCMAN_PREFIX/bin/ocman` | Binary destination |
| `OCMAN_SRC` | `$OCMAN_PREFIX/share/ocman/src` | Dedicated source checkout |
| `OCMAN_REPO` | `https://forgejo.nousefreak.be/dries/ocman.git` | Source repository, not binary downloads |
| `OCMAN_RUN` | `${XDG_STATE_HOME:-~/.local/state}/ocman` | PID file and logs |
| `OCMAN_ADDR` | `127.0.0.1:8228` | Background server address |

For a native macOS desktop app, download the DMG from the
[releases page](https://github.com/ocman/ocman/releases). The shell installer
installs the web server binary, not the desktop app.
