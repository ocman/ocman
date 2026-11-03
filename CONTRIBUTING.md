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

In a linked worktree, `make dev` keeps ocman's databases in that checkout
(`.dev/state.db` and `state_conv.db`). The main checkout keeps conversation
history in `state_conv.db` but uses the live `~/.local/share/ocman/state.db`. Native platform data, such as OpenCode's database, is not
isolated; see [database isolation](docs/other/contributing.md#database-isolation-before-startup).
