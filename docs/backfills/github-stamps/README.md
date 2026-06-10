# GitHub PR Stamp Backfill

This directory holds the reviewed inputs for the GitHub PR stamp backfill.

The backfill credits merged human PRs to `main` in:

- `gastownhall/gascity`
- `gastownhall/gastown`
- `gastownhall/beads`
- `gastownhall/wasteland`

Each eligible PR is represented as a full synthetic Wasteland completion:

- `wanted.id = w-gh-<repo>-<pr>`
- `completions.id = c-gh-<repo>-<pr>`
- `stamps.id = s-gh-<repo>-<pr>`

Synthetic rows are authored/validated by the system rig
`gastownhall-backfill`. Existing real Wasteland completions with the same
canonical GitHub PR evidence URL are authoritative and must not be duplicated.

## Tool

The operator tool lives at `tools/githubprbackfill`.

Initial workflow:

```bash
BACKFILL_COMMONS_DIR=/path/to/wl-commons \
BACKFILL_OUTPUT_DIR=.tmp/github-pr-backfill \
BACKFILL_LIMIT_PER_REPO=10 \
scripts/github-pr-backfill.sh
```

Use `--fetch-only` to warm the GitHub cache without reading or writing a
manifest. Use `--limit-per-repo <n>` for small-batch dry runs. Use
`--merged-after <YYYY-MM-DD|RFC3339>` or `--since-days <n>` to constrain a
scheduled run to recently merged PRs.

The default checked-in inputs exclude reviewed admin/maintainer accounts in
`excluded-logins.json`. This keeps the backfill focused on external
contributor incentives rather than crediting maintainers with outsized
repository context.

The checked-in script creates the manifest, validates the hash, renders SQL,
applies it to a Dolt branch, and can optionally push/create a DoltHub PR:

```bash
DOLTHUB_FORK_ORG=julianknutsen \
BACKFILL_COMMONS_DIR=/path/to/wl-commons \
BACKFILL_BRANCH=wl/julianknutsen/github-pr-backfill-$(date -u +%Y%m%d) \
BACKFILL_PUSH=1 \
BACKFILL_CREATE_PR=1 \
scripts/github-pr-backfill.sh
```

Wendy's `github-pr-backfill` exec order runs the same script with
`BACKFILL_SINCE_DAYS=14`. It needs `DOLTHUB_TOKEN` for DoltHub PR creation and
DoltHub credentials configured for `dolt push`.

To show how a single PR score was calculated:

```bash
go run ./tools/githubprbackfill explain-pr \
  --url https://github.com/gastownhall/gascity/pull/1723 \
  --subject julianknutsen \
  --identity-source exact \
  --github-cache .tmp/github-pr-backfill/github-cache.json \
  --format json
```

`--github-cache` is optional. Without it, `explain-pr` fetches the PR metadata
live from GitHub into an in-memory cache. With it, the report is reproducible
for the given formula version and cache hash. The report separates raw GitHub
inputs, derived signals, score rules, and the synthetic stamp preview.

## Formula v1

Eligibility:

- include merged PRs whose base branch is `main`
- include human GitHub users, including maintainers
- skip reviewed admin/maintainer accounts in `excluded-logins.json`
- skip GitHub App/Bot actors and null/deleted authors
- skip existing stamped PR evidence
- explicit adoption PRs may credit the original contributor instead of the
  maintainer PR author

Quality and reliability:

- start at `4`
- cap at `3` for later reverted, generated-only, dependency-only,
  mechanical-only, or no-effective-authored-file PRs
- subtract `1` for strong blocking review signals
- subtract `1` for maintainer commits
- subtract `1` for post-review author changes when strong review/comment
  signals exist
- floor at `3`
- ceiling at `4`
- reliability equals quality

Creativity:

- default `3`
- `4` for meaningful feature/design/API/UI/runtime changes
- `2` for dependency, generated-only, mechanical, formatting, revert-only,
  docs-only, or CI-only changes
- never `5` in v1

Severity:

- `root` if effective changed files >= 20 or effective churn >= 1500
- `branch` if effective changed files >= 5, effective churn >= 300, or
  title/labels indicate feature/performance/refactor
- `leaf` otherwise

Confidence uses the-pile-like range:

- `0.55` exact existing rig handle
- `0.53` explicit reviewed identity override
- `0.51` provisional GitHub-derived rig
- `0.49` maintainer commits or strong review intervention
- `0.46` later reverted PR, subject conflict, or manually questionable
  attribution
