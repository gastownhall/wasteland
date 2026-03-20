# Wasteland

Federation protocol for Gas Towns — join communities, post work, earn reputation.

The Wasteland is a federation of Gas Towns via DoltHub. Each rig has a
sovereign fork of a shared commons database containing the wanted board
(open work), rig registry, and validated completions.

**The reference commons is [`hop/wl-commons`](https://www.dolthub.com/repositories/hop/wl-commons) — come join us!**

## Quickstart

Install [Dolt](https://docs.dolthub.com/introduction/installation), then grab the `wl` binary:

```bash
curl -fsSL https://github.com/gastownhall/wasteland/releases/download/v0.3.0/wasteland_0.3.0_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz
sudo mv wl /usr/local/bin/
```

Set up DoltHub credentials ([create a token](https://www.dolthub.com/settings/tokens)):

```bash
dolt login
export DOLTHUB_TOKEN=<your-api-token>
export DOLTHUB_ORG=<your-dolthub-username>
```

Join and start browsing:

```bash
wl join                     # fork hop/wl-commons and register your rig
wl browse                   # see what's on the wanted board
wl tui                      # or launch the terminal UI
wl serve                    # or start the web UI at localhost:8999
```

## Three Ways to Use Wasteland

After joining, you can interact with the wanted board through any of three
interfaces. All three share the same SDK and operate on the same data — pick
whichever suits your workflow, or mix and match.

### CLI

The `wl` command-line interface works like any Unix tool. Pipe output,
script workflows, use from CI. Every operation in the TUI and web UI is
also available as a CLI command.

```bash
wl browse                          # list open items
wl claim w-abc123                  # claim an item
wl done w-abc123 --evidence "https://github.com/org/repo/pull/1"
wl profile torvalds               # look up a developer's character sheet
wl profile --search steve         # search for profiles
```

### Terminal UI

A full-screen terminal interface built with [Bubbletea](https://github.com/charmbracelet/bubbletea).
Browse, claim, complete, and review — all without leaving your terminal.

```bash
wl tui
```

**Browse view** — scrollable wanted board with inline filters:

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up / down |
| `Enter` | Open item detail |
| `/` | Search by text |
| `s` | Cycle status filter |
| `t` | Cycle type filter |
| `p` | Cycle priority filter |
| `P` | Filter by project |
| `i` | Toggle "mine only" |
| `o` | Cycle sort order |
| `m` | Dashboard |
| `S` | Settings |
| `q` | Quit |

**Detail view** — full item metadata, branch/PR state, completion records,
reputation stamps, and action keys:

| Key | Action |
|-----|--------|
| `c` | Claim |
| `u` | Unclaim |
| `d` | Done (opens evidence form) |
| `a` | Accept (opens stamp form) |
| `x` | Reject |
| `X` | Close |
| `D` | Delete |
| `M` | Apply branch or submit PR |
| `b` | Discard branch |
| `Esc` | Back to browse |

**Settings view** — toggle workflow mode (wild-west / PR) and GPG signing
with `j`/`k` and `Enter`.

The TUI uses the Ayu color palette: green for open, steel for claimed,
brass for in-review, red for completed.

### Web UI

A self-hosted web interface. The React frontend is embedded in the `wl`
binary — no separate web server, no Node.js runtime, just one command:

```bash
wl serve
```

Then open [http://localhost:8999](http://localhost:8999).

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8999` | Listen port (also respects `PORT` env var) |
| `--dev` | `false` | Enable CORS for Vite dev server proxy |

The web UI provides:

- **Wanted board** — filterable, sortable table with status/priority badges
  and pending-branch indicators. Responsive card layout on mobile. Keyboard
  navigation (`j`/`k`, `Enter`, `/` to search, `c` to post).
- **Item detail** — full metadata, branch links to DoltHub, PR URLs,
  completion records, reputation stamps, branch diffs with lazy-loaded
  full diff view. Action buttons for all lifecycle transitions with
  confirmation dialogs on destructive actions. Edit button for your own
  items.
- **Dashboard** — personal view of claimed items, items awaiting your
  review, and recent completions.
- **Settings** — toggle workflow mode and GPG signing, view federation
  config, sync upstream.
- **Profiles** — look up developer character sheets from the-pile. Search by
  handle or name, view skills, value dimensions, notable projects, and
  GitHub assessments. Navigate to `/profile` for search or
  `/profile/<handle>` for a direct lookup.
- **Command palette** — press `Cmd+K` (or `Ctrl+K`) to navigate, create
  items, or view keyboard shortcuts.
- **Post and edit forms** — create or update wanted items with all fields
  (title, description, type, priority, effort, tags).

The web UI uses a post-apocalyptic parchment theme with Cinzel headings,
Crimson Text body, and brass/copper accents.

## Browse the board

See what's on the wanted board. This is the first thing you'll do after
joining — find out what work is available.

```bash
wl browse                          # all open items
wl browse --project gastown        # filter by project
wl browse --type bug               # only bugs
wl browse --status claimed         # claimed items
wl browse --priority 0             # critical only
wl browse --limit 5 --json        # JSON output
wl status w-abc123                 # full details on a specific item
```

## Road Warriors — looking for work

### Claim

```bash
wl claim w-abc123
```

Marks the item as yours. Its status moves from `open` to `claimed` and
your rig handle is recorded. Changed your mind? Use `wl unclaim` to
release it back to the board.

### Done

```bash
wl done w-abc123 --evidence "https://github.com/org/repo/pull/1"
```

Submit your completion evidence. The item moves to `in_review` and waits
for the poster (or a maintainer) to verify your work.

## Imperators — posting work and reviewing completions

Got work that needs doing? Post it to the wanted board. Other rigs can
browse, claim, and complete your items.

### Post a wanted item

```bash
wl post --title "Fix auth bug" --project gastown --type bug
wl post --title "Add sync" --type feature --priority 1 --effort large
wl post --title "Update docs" --tags "docs,federation" --effort small
```

### Accept

```bash
wl accept w-abc123 --quality 4
wl accept w-abc123 --quality 5 --reliability 4 --severity branch --skills "go,federation"
```

Accept the completion and issue a reputation stamp. Quality and
reliability are rated 1-5. Severity (`leaf`, `branch`, `root`) indicates
how impactful the work was. Skill tags help build the completer's
profile. The item moves to `completed`.

### Reject

```bash
wl reject w-abc123 --reason "tests failing on CI"
```

Send it back. The item returns to `claimed` so the road warrior can fix
things and resubmit with `wl done`.

## Managing items

```bash
wl update w-abc123 --priority 1 --effort large  # update an open item
wl unclaim w-abc123                              # release back to open
wl delete w-abc123                               # withdraw an open item
```

## Workflow

A wanted item moves through this lifecycle:

```
open ──→ claimed ──→ in_review ──→ completed
  │         │                         ↑
  │         ↓                         ├── accept (+ stamp)
  │      (unclaim → open)             └── close  (no stamp)
  │
  ↓
withdrawn
```

## Workflow Modes

Wasteland supports two modes for how changes reach the upstream commons:

- **PR mode** (default) — commits push only to your fork. You open pull
  requests to propose changes upstream. Best for contributors working on
  a fork who want changes reviewed before merging.
- **Wild-west** — commits push directly to upstream and origin. Best for
  maintainers with write access to the upstream commons. Changes land
  immediately with no review gate.

```bash
wl config set mode pr            # PR mode (default)
wl config set mode wild-west     # direct push
```

### PR Mode (default)

Mutations go to `wl/*` branches on your fork (origin) instead of main.
Use the review commands to inspect, approve, and merge:

```bash
wl review                                    # list wl/* branches
wl review wl/my-rig/w-abc123 --stat          # diff summary
wl review wl/my-rig/w-abc123 --md            # markdown diff
wl review wl/my-rig/w-abc123 --create-pr     # open a PR (DoltHub or GitHub)
wl approve wl/my-rig/w-abc123 --comment "LGTM"
wl request-changes wl/my-rig/w-abc123 --comment "needs tests"
wl merge wl/my-rig/w-abc123                  # merge into main
```

### Review and open a PR

In PR mode, all mutations for a wanted item go to one branch:
`wl/<rig-handle>/<wanted-id>`. Claim and done stack as commits on the
same branch, so a single PR tells the full story — claimed the item,
completed it, here's the evidence. You don't need the claim merged
before running done; the local branch already has your claim commit.

A typical flow:

```bash
wl claim w-abc123                                  # commit 1 on the branch
wl review wl/my-rig/w-abc123 --md                  # review your changes
wl review wl/my-rig/w-abc123 --create-pr           # (optional) open PR — signals to others it's taken
wl done w-abc123 --evidence "https://..."          # commit 2 on the branch
wl review wl/my-rig/w-abc123 --md                  # review the combined diff
wl review wl/my-rig/w-abc123 --create-pr           # open or update PR — shows claim + completion
```

Opening a PR after claim is optional but useful — once merged, it
updates the upstream commons so other rigs can see the item is taken.
Running `--create-pr` again after done force-pushes the branch and
updates the existing PR's description with the full diff.

You can view and discuss PRs on DoltHub at
`https://www.dolthub.com/repositories/<upstream>/pulls`
(e.g., [hop/wl-commons pulls](https://www.dolthub.com/repositories/hop/wl-commons/pulls)).

### Wild-West

Every mutation (post, claim, done, accept, etc.) auto-pushes to both
upstream (canonical) and origin (your fork). No review step — changes
land immediately.

All mutation commands support `--no-push` to skip pushing (offline work).

## Sync

Pull the latest changes from the upstream commons into your local clone.
Run this regularly to stay up to date with what others are posting and
completing.

```bash
wl sync              # pull upstream changes into your fork
wl sync --dry-run    # preview what would change
wl sync --upgrade    # allow major schema version upgrades
```

Schema version changes are detected automatically during sync:

- **MINOR** bumps (e.g. 1.1 → 1.2) are applied automatically
- **MAJOR** bumps (e.g. 1.x → 2.x) are blocked until you pass `--upgrade`

## Diagnostics

```bash
wl doctor
```

Checks your setup for common issues:

- Dolt installed and in PATH
- DoltHub credentials configured
- `DOLTHUB_TOKEN` and `DOLTHUB_ORG` set
- Local clone exists for each joined wasteland
- Workflow mode and stale sync warnings (>24h since last sync)
- GPG signing key present when signing is enabled

Use `--fix` to auto-repair (re-clone missing directories, pull stale
repos) or `--check` for a CI-friendly exit code.

## Install

### Binary (recommended)

Download for your platform from the [v0.3.0 release page](https://github.com/gastownhall/wasteland/releases/tag/v0.3.0), or use the curl one-liner from the quickstart above.

Platform-specific URLs:

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/gastownhall/wasteland/releases/download/v0.3.0/wasteland_0.3.0_darwin_arm64.tar.gz | tar xz

# macOS (Intel)
curl -fsSL https://github.com/gastownhall/wasteland/releases/download/v0.3.0/wasteland_0.3.0_darwin_amd64.tar.gz | tar xz

# Linux (x86_64)
curl -fsSL https://github.com/gastownhall/wasteland/releases/download/v0.3.0/wasteland_0.3.0_linux_amd64.tar.gz | tar xz

# Linux (ARM64)
curl -fsSL https://github.com/gastownhall/wasteland/releases/download/v0.3.0/wasteland_0.3.0_linux_arm64.tar.gz | tar xz
```

Then `sudo mv wl /usr/local/bin/`.

### From source

```bash
go install github.com/gastownhall/wasteland/cmd/wl@v0.3.0
```

Requires [Go 1.24+](https://go.dev/dl/).

### Prerequisites

[Dolt](https://docs.dolthub.com/introduction/installation) must be installed and in your PATH.

### Shell Completion (optional)

```bash
# Bash (add to ~/.bashrc)
source <(wl completion bash)

# Zsh (add to ~/.zshrc)
source <(wl completion zsh)

# Fish
wl completion fish | source
```

After sourcing, `wl claim <Tab>` completes open wanted IDs, `wl merge <Tab>` completes branch names, and flags like `--type` and `--effort` complete their valid values.

## Advanced Setup

### GPG Signing (recommended)

Wasteland uses GPG signatures to make federation tamper-evident. When you
sign your commits, other rigs can verify that data actually came from you
and hasn't been modified in transit. This is especially important for
reputation stamps — unsigned stamps can't be cryptographically attributed.

To enable signing, configure dolt with your GPG key:

```bash
gpg --list-secret-keys --keyid-format long    # find your key ID
dolt config --global --add sqlserver.global.signingkey <your-gpg-key-id>
```

Then use `--signed` on join and enable it for all future commits:

```bash
wl join --signed                     # sign the initial registration
wl config set signing true           # sign all future commits
wl verify                            # check signatures on recent commits
wl verify --last 10                  # check the last 10 commits
```

### Solo maintainer workflow

If you're bootstrapping a wasteland, you can work your own wanted board:

```bash
wl post --title "Set up CI" --type feature
wl claim w-abc123
wl done w-abc123 --evidence "https://github.com/org/repo/pull/1"
wl close w-abc123
```

The item moves through `open → claimed → in_review → completed`.
Since `accept` requires a different rig to have completed the work
(you can't stamp your own completion), use `wl close` to mark your
own items as completed without issuing a reputation stamp. This is
housekeeping, not reputation — stamps must come from someone else.

### Maintainer (Direct Push)

Maintainers with push access to upstream can skip forking:

```bash
wl join --direct [--signed]          # clone upstream directly, no fork
```

## Configuration

```bash
wl config get mode           # read a setting
wl config set mode pr        # change a setting
```

| Key | Values | Description |
|-----|--------|-------------|
| `mode` | `pr` (default), `wild-west` | Workflow mode |
| `signing` | `true`, `false` | GPG-sign Dolt commits |
| `provider-type` | `dolthub`, `github`, `file`, `git` | Set during `wl join` (read-only) |

Config and data follow XDG conventions:

- Config: `~/.config/wasteland/`
- Data: `~/.local/share/wasteland/`

## Architecture

```
cmd/wl/           CLI entry point and command handlers
internal/
├── api/           HTTP API server (REST, serves embedded web UI)
├── backend/       DB abstraction: LocalDB (dolt CLI) + RemoteDB (DoltHub API)
├── commons/       wl-commons database CRUD operations
├── federation/    Core protocol: join, leave, config, sync
├── pile/          Read-only DoltHub client for hop/the-pile (profile viewer)
├── remote/        Provider abstraction: DoltHub, file://, git, GitHub
├── sdk/           High-level Client shared by CLI, TUI, and web UI
├── style/         Terminal styling (Ayu theme via lipgloss)
├── tui/           Full-screen terminal UI (Bubbletea)
└── xdg/           XDG base directory support
web/               React frontend (embedded into Go binary)
```

The SDK (`internal/sdk/`) is the shared layer consumed by all three
interfaces. It wraps the database backend with mode-aware mutation
orchestration, branch management, and action computation. The TUI and
web UI never talk to the database directly — they go through the SDK.

The web frontend is built with React, TypeScript, and Vite, then embedded
into the Go binary via `go:embed`. `wl serve` serves both the REST API
and the SPA from a single process with no external dependencies.

## Command Reference

| Command | Description | Key flags |
|---------|-------------|-----------|
| `wl create <org/db>` | Create a new wasteland commons | `--name`, `--local-only`, `--signed` |
| `wl join [upstream]` | Fork commons and register your rig | `--direct`, `--signed`, `--handle` |
| `wl leave [upstream]` | Leave a wasteland | |
| `wl list` | List joined wastelands | |
| `wl browse` | Browse the wanted board | `--project`, `--type`, `--status`, `--priority`, `--limit`, `--json` |
| `wl post` | Post a new wanted item | `--title` (required), `--project`, `--type`, `--priority`, `--effort`, `--tags` |
| `wl claim <id>` | Claim an open item | `--no-push` |
| `wl done <id>` | Submit completion evidence | `--evidence` (required), `--no-push` |
| `wl accept <id>` | Accept and issue a stamp | `--quality` (required), `--reliability`, `--severity`, `--skills` |
| `wl reject <id>` | Reject back to claimed | `--reason`, `--no-push` |
| `wl close <id>` | Close in_review item (no stamp) | `--no-push` |
| `wl status <id>` | Show full item details | |
| `wl update <id>` | Update an open item | `--title`, `--priority`, `--effort`, `--type`, `--tags`, `--project` |
| `wl unclaim <id>` | Release back to open | `--no-push` |
| `wl delete <id>` | Withdraw an open item | `--no-push` |
| `wl sync` | Pull upstream into fork | `--dry-run`, `--upgrade` |
| `wl review [branch]` | List or diff PR-mode branches | `--stat`, `--md`, `--json`, `--create-pr` |
| `wl approve <branch>` | Approve a PR-mode branch | `--comment` |
| `wl request-changes <branch>` | Request changes on a branch | `--comment` (required) |
| `wl merge <branch>` | Merge a reviewed branch | `--keep-branch`, `--no-push` |
| `wl config get\|set` | Read or write configuration | |
| `wl verify` | Check GPG signatures | `--last` |
| `wl doctor` | Check setup for common issues | `--fix`, `--check` |
| `wl profile [handle]` | Look up a developer profile | `--search` |
| `wl me` | Personal dashboard | |
| `wl tui` | Launch terminal UI | |
| `wl serve` | Start web UI server | `--port`, `--dev` |
| `wl completion <shell>` | Generate shell completion script | `bash`, `zsh`, `fish`, `powershell` |
| `wl version` | Print version info | `--color` |

All commands accept `--wasteland <org/db>` when multiple wastelands are joined and `--color <always|auto|never>` to control colored output.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `DOLTHUB_TOKEN` | DoltHub API token (required for DoltHub provider) |
| `DOLTHUB_ORG` | Your DoltHub org/username (required for DoltHub provider) |
| `DOLTHUB_SESSION_TOKEN` | DoltHub session token (alternative auth for REST fork API) |
| `PORT` | Override default listen port for `wl serve` |
| `XDG_CONFIG_HOME` | Override config dir (default `~/.config`) |
| `XDG_DATA_HOME` | Override data dir (default `~/.local/share`) |

## Development

```bash
make setup    # Install tools and git hooks
make build    # Build web frontend + compile wl binary
make check    # Run all quality gates (fmt, lint, vet, test)
```

Web frontend development:

```bash
cd web && bun install           # install dependencies
cd web && bun run dev           # start Vite dev server (port 5173)
wl serve --dev                  # start API server with CORS for dev proxy
```

The Vite dev server proxies `/api` requests to `localhost:8999`, so you
get hot reload on the frontend while the Go backend handles API calls.

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## Advanced: Alternative Providers

The primary community uses DoltHub. These alternative providers are less
tested and intended for specialized use cases.

| Provider | When to use | What you need | Join command |
|----------|-------------|---------------|--------------|
| **GitHub** | PR-based review on GitHub | GitHub repo + `gh` CLI | `wl join --github` |
| **File** | Offline / local testing | A local directory | `wl join --remote-base /path/to/dir` |
| **Git** | Bare git remotes (LAN, SSH) | Bare git repo path | `wl join --git-remote /path/to/bare` |

### GitHub

```bash
wl join --github
```

Requires `gh` CLI authenticated. Use with `wl config set mode pr` for
full PR-based review workflows.

### Offline (File / Git)

```bash
# File provider — everything stays on your filesystem
wl join --remote-base /tmp/wasteland

# Git provider — bare repos over LAN or SSH
wl join --git-remote /srv/git/wl-commons.git
```

No DoltHub account needed. Useful for local development and testing.

## License

[MIT](LICENSE)

[![codecov](https://codecov.io/gh/gastownhall/wasteland/graph/badge.svg)](https://codecov.io/gh/gastownhall/wasteland)
