# meow

A package manager for **Meow Linux**, a small Void-based distribution.
Written in Go. It works with real XBPS binary packages (`.xbps`) and the official
Void repositories, but installs, removes and upgrades packages itself instead of
calling xbps.

```
$ meow sync
$ sudo meow install neovim
the following packages will be installed:
  libgcc-14.2.0_3
  luajit-2.1.1736781742_1
  neovim-0.10.2_1
total: 3. proceed? [Y/n]
```

Русская версия: [README-ru.md](README-ru.md)

## Commands

| Command | What it does |
|---|---|
| `meow sync` | Downloads `<arch>-repodata` from the configured repositories, parses `index.plist`, refreshes the cache |
| `meow install <pkg>...` | Installs a package with all dependencies (`-y` — no prompt, `-n` — dry run) |
| `meow remove <pkg>...` | Removes a package (`-R` — cascade, also drops orphaned auto-dependencies; `-n`, `-y`) |
| `meow upgrade` | Upgrades everything installed to the versions from the index (`-n`) |
| `meow repair` | Finishes an interrupted transaction using the on-disk journal |
| `meow list` | Lists installed packages |
| `meow search <query>` | Searches names and descriptions in the cached index |
| `meow info <pkg>` | Name, version, architecture and description from the index |
| `meow enable` / `meow disable <svc>` | Enables or disables a runit service (`/etc/sv` ↔ `/var/service`) |
| `meow etc-update` | Merges configuration files updated by packages (`*.meow-new`) |
| `meow clean` | Clears the package cache and logs older than 30 days |
| `meow log [txid]` | Lists transactions, or prints the log of one |

Additionally, `meow --root <path> <cmd>` works on an alternative root (a prepared
OS directory), which is handy for testing without a chroot.

## How it works

**Dependencies.** Resolution walks `run_depends` from the index: package names,
`provides` (including virtual ones such as `cmd:awk`) and version constraints
(`awk>=0`, `foo-1.2_1`). The walk is a three-colour iterative DFS, so the install
order is topological and cycles are reported as `circular dependency: a -> b`.
When several packages satisfy a dependency and there is no explicit choice,
installation stops and lists the candidates instead of guessing.

**Journal and `repair`.** Before every transaction the queue of packages, with its
phases, is written to `/var/db/meow/journal.json`. Ctrl+C does not kill an
operation mid-package: the current package is finished and the journal is
preserved. The next `meow repair` removes partially unpacked files, replays
post-install scripts and installs whatever is left.

**Configs in `/etc`.** When a package ships a new version of a config the user has
edited, the old file is left untouched and the new one is unpacked next to it with
a `.meow-new` suffix; `meow etc-update` then offers
`[r]eplace [k]eep [d]elete`. On package removal an edited config is kept as
`<file>.meow-save`.

**Integrity.** Downloaded packages are verified against `filename-sha256` from the
index; the cache (`/var/cache/meow`) is reused only when the hash matches.

**Versions.** `CompareVersions` is a port of `xbps_cmpver`: dewey-style segment
comparison plus a separate revision after `_` (`1.2.3_4` > `1.2.3_1`), with
`alpha < beta < pre < rc < pl`.

**Output discipline.** Data (`list`, `search`, `info`, `log`) goes to stdout and
pipes cleanly; progress, warnings and hints go to stderr. After an install meow
reminds you about `dracut --force` (kernel), `ldconfig` (libraries),
`fc-cache -fv` (fonts), `update-ca-certificates`, `udevadm control --reload-rules`
and the rest.

## Building

Requires Go 1.27+ and `git`. The only dependency is `howett.net/plist`.

```sh
git clone https://github.com/meowllluz/meowpm.git
cd meowpm
go build -o meow .
go test ./...
sudo install -Dm755 meow /usr/bin/meow
```

At runtime meow needs `curl`, `tar`, `zstd` (Void's tar extracts `index.plist`
from a `.tar.zst` through the external zstd binary), `shadow`, and optionally
`runit` for `enable`/`disable`.

## Configuration

`/etc/meow/pm.conf`:

```ini
# required: main repository
REPO_MAIN=https://repo-default.voidlinux.org/current
# optional: nonfree
REPO_NONFREE=https://repo-default.voidlinux.org/current/nonfree

# architecture, x86_64 by default
ARCH=x86_64

# what to use for the hints' privilege escalation (doas/sudo), doas by default
ESCALATOR=doas

# packages meow must never touch (comma separated).
# On Meow Linux base-files carries the distribution branding.
IGNORE_PACKAGES=base-files
```

First run:

```sh
sudo meow sync
meow search nvim
sudo meow install neovim
sudo meow upgrade -n     # see what would be upgraded
```

`IGNORE_PACKAGES` is a hard block: installation, upgrade and dependency
resolution all fail if such a package is requested directly or needed as a
dependency.

## Data layout

```
/var/db/meow/
  installed.list        name|version|date|auto  (one line per package)
  <pkg>.json            files with sha256, dependencies, provides, conflicts, status
  <pkg>.install         the package INSTALL script, if any
  <pkg>.remove          the package REMOVE script, if any
  journal.json          unfinished transaction (exists only during an operation)
  log/                  sync/install/upgrade/repair logs
/var/cache/meow/
  repo_<repo>.json      index cache
  repo_<repo>.plist     extracted index.plist (for debugging)
  search_<repo>.txt     flat index used by search
  <pkg>-<ver>.<arch>.tar.zst  downloaded packages
```

Package scripts are invoked as `sh <script> <phase> <update>`, where the phase is
`pre`/`post` for INSTALL and `pre-remove`/`post-remove` for REMOVE, and `update`
is `yes`/`no`. Environment: `PKGNAME`, `VERSION`, `UPDATE`, `PKG_DESTDIR`.

## Tests

```sh
go test ./...                        # unit tests: versions, resolve, files.plist parsing, DB
sudo ./scripts/test-chroot.sh        # integration run in a chroot (fresh Void rootfs)
ROOT=/tmp/void sudo ./scripts/test-chroot.sh   # same, into a custom directory
```

The integration script downloads the current Void glibc rootfs, bootstraps
`curl/tar/shadow/runit/zstd` in it with the native xbps, builds meow inside and
runs `sync → install → list/info/search → remove`.

## Installing on plain Void

meow can be tried on an existing Void installation without reinstalling the
system: the migrator [xbps2meow](https://github.com/meowllluz/xbps2meow) converts
the `/var/db/xbps` database into meow's format, after which xbps is removed and
meow keeps upgrading the same packages from the same repositories. Packages that
came from xbps are recorded with `Auto=false`, so `remove -R` does not treat them
as orphans.

## Not implemented (on purpose)

To save you the search: downgrades are intentionally unsupported; there is no
version hold/pinning, no installing of local `.xbps` files, no repository
signature verification (only the package sha256), no hooks, and no separate
`meow autoremove` command (the cascade lives in `remove -R`).

## Project

- The `.xbps` and `index.plist` formats are documented in `void-packages`
  `Manual.md` and the [void-linux/xbps](https://github.com/void-linux/xbps)
  sources (`lib/package_unpack.c`).
- The distribution it is written for: Meow Linux (Void + runit, systemd-boot, TTY).
- System build scripts: `scripts/build-rootfs.sh`, `live/build-iso.sh`,
  `live/meow-install`.

License: GNU GPL v2 — see [LICENSE](LICENSE).
