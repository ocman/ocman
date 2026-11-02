# Contributing to ocman

Ocman's primary repository and CI are on
[Forgejo](https://forgejo.nousefreak.be/dries/ocman). GitHub hosts a mirror.
Please submit pull requests to Forgejo and check its
[issue tracker](https://forgejo.nousefreak.be/dries/ocman/issues) before
opening a new issue on either host.

The [contributing guide](docs/other/contributing.md) covers reporting bugs,
local setup, commands, tests and the pull request checklist. Coding agents and
repository contributors should also read [AGENTS.md](AGENTS.md). The
[Makefile](Makefile) is the authoritative command reference.

Before starting `make dev`, configure disposable databases isolated to this
checkout and branch. Set `OCMAN_STATE_DB` to an absolute disposable path and
set `-conversations-db` alongside it. Native platform homes and managed agent
databases must also be isolated before backend development startup.
See the [database isolation prerequisite](docs/other/contributing.md#database-isolation-before-startup).
