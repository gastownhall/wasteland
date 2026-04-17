# Profile Fallback View — Stamp Feed for Pre-Backfill Users

## Summary

When a viewer navigates to `/profile/{handle}` for a user who has
federation activity in `hop/wl-commons` but has not yet been backfilled
into `hop/the-pile`, render a distinct "stamp feed" view instead of
404-ing. The stamp feed shows the rig handle (as a GitHub link) and up
to 10 recent stamps with evidence links, so every active federation
participant has a useful profile page.

## Trigger Rule

The backend checks `hop/the-pile.boot_blocks` for a row matching
`handle`.

- **Row present** → character sheet (existing behavior, unchanged).
- **Row absent** → fall back to `hop/wl-commons` stamp query.
  - **≥1 stamp where `subject = handle`** → stamp feed (new).
  - **0 stamps** → 404 (true unknown handle).

The backend may run both queries concurrently; if `boot_blocks` hits,
discard the `wl-commons` result.

## Data Sources

| Mode             | Source DB        | Tables                              |
|------------------|------------------|-------------------------------------|
| character_sheet  | `hop/the-pile`   | `boot_blocks`, `stamps`             |
| stamp_feed       | `hop/wl-commons` | `stamps` ⟕ `completions`            |

Stamp feed query:

```sql
SELECT s.id, s.skill_tags, s.valence, s.message, s.author,
       s.created_at, c.evidence
FROM stamps s
LEFT JOIN completions c ON s.context_id = c.id
WHERE s.subject = ?
ORDER BY s.created_at DESC, s.id DESC
LIMIT 10
```

No cache — matches the existing profile handler's behavior. Volume is
trivial (`wl-commons` has 14 completions total at time of spec).

## API Contract

`GET /api/profile/{handle}` — **breaking change**: response becomes a
discriminated union.

**Character sheet (existing shape + `kind`):**

```json
{
  "kind": "character_sheet",
  "handle": "bmizerany",
  "display_name": "Blake Mizerany",
  "quality": 3.75,
  "reliability": 4.0
}
```

**Stamp feed (new):**

```json
{
  "kind": "stamp_feed",
  "handle": "rileywhite",
  "github_url": "https://github.com/rileywhite",
  "stamps": [
    {
      "id": "s-abc",
      "skill_tags": ["go", "backend"],
      "quality": 4,
      "reliability": 5,
      "validator": "julianknutsen",
      "message": "Added retry middleware",
      "evidence_url": "https://github.com/gastownhall/gascity/pull/548",
      "evidence_label": "gastownhall/gascity#548",
      "created_at": "2026-04-13T09:33:05Z"
    }
  ],
  "stamps_error": null
}
```

**Stamp feed, `wl-commons` unreachable:**

```json
{
  "kind": "stamp_feed",
  "handle": "rileywhite",
  "github_url": "https://github.com/rileywhite",
  "stamps": [],
  "stamps_error": "stamps_unavailable"
}
```

**404:** no boot_block and no `wl-commons` stamps.

Evidence parsing rules (server-side):

- `^https?://github\.com/([^/]+)/([^/]+)/pull/(\d+)` → `evidence_label`
  = `owner/repo#N`, `evidence_url` unchanged.
- `^https?://github\.com/([^/]+)/([^/]+)` → `evidence_label` =
  `owner/repo`, `evidence_url` unchanged.
- Else → `evidence_label` = null, `evidence_url` = null, raw text
  delivered as `evidence_text`.
- Missing / NULL evidence → all three fields null; card renders without
  an evidence line.

## UI — Stamp Feed View

New React component: `web/src/components/StampFeedView.tsx`.
`ProfileView` delegates to it when `data.kind === "stamp_feed"`.

**Header:**

```
{handle}                             [View on GitHub ↗]
──────────────────────────────────────────────────────
No character sheet yet — showing federation activity
```

- `handle` as `<h1>`.
- GitHub link → `github.com/{handle}`, opens in a new tab
  (`target="_blank" rel="noopener noreferrer"`).
- Banner styled in the existing `unverifiedNote` tone or similar.

**Stamp card (one per row, up to 10):**

```
[go] [backend]                                    Q4 R5
→ gastownhall/gascity#548
validated by julianknutsen · 2026-04-13
"Added retry middleware"
```

- Skill tags as pill badges.
- Q/R scores shown top-right when valence is present.
- Evidence line omitted when `evidence_url` is null.
- Message shown below, truncated at ~200 chars with a "show more"
  affordance.
- Cards ordered by `created_at DESC`.

**Loading state:** reuse the existing `SkeletonRows` component.

**Empty with `stamps_error` set:** render header / banner normally; the
card region shows "Couldn't load recent stamps — try again later" with
a retry affordance.

**Zero stamps without error:** impossible under the trigger rules
(would be a 404).

## GitHub Handle Resolution (Phase 4)

The stamp-feed view links to `github.com/{rig_handle}`, but wasteland
rig handles are not guaranteed to match their owner's GitHub username.
Phase 4 adds a persistent local cache mapping `rig_handle →
github_username` populated from GitHub PR authorship observed in
stamp evidence URLs.

**Scope.** Local-per-rig JSON file at
`~/.local/share/wasteland/github-handles.json`. Not federated. Each
operator builds their own view.

**Record shape.**

```json
{
  "rileywhite": {
    "github": "rileywhite",
    "source_pr": "https://github.com/gastownhall/gascity/pull/548",
    "resolved_at": "2026-04-17T12:00:00Z"
  },
  "rome": {
    "github": "",
    "source_pr": "",
    "resolved_at": "2026-04-17T12:00:00Z"
  }
}
```

- Absent key → never tried. Consumer shows no GitHub link.
- Non-empty `github` → resolved. Consumer links to `github.com/{github}`.
- Empty `github` → tried-and-failed (no parseable PR URL on any stamp).
  Consumer shows no GitHub link.

**Populate triggers.**

1. **Auto-populate on stamp approval.** The SDK's `Accept` and
   `AcceptUpstream` methods, after the DML succeeds and the mutex
   releases, synchronously call the resolver for the stamp's `subject`.
   5-second timeout. Errors are logged via `slog`; the Accept itself
   still returns success. Skipped when the subject already has a
   resolved cache entry.

2. **On-demand via CLI.** `wl resolve-github <handle>` resolves one
   handle. `wl resolve-github --all` resolves every handle observed in
   `hop/wl-commons.stamps` that is not already in the cache. Default
   semantics: single-handle always re-resolves; `--all` skips resolved
   entries and retries tried-and-failed ones. `--refresh` forces
   re-resolution of resolved entries too.

**Resolver behavior.**

Queries `hop/wl-commons` for the subject's stamps joined to their
completions, orders by `created_at DESC`, takes the first evidence URL
that matches `^https?://github\.com/[^/]+/[^/]+/pull/\d+`, and calls
`GET https://api.github.com/repos/{owner}/{repo}/pulls/{n}` with
`Authorization: Bearer $GITHUB_TOKEN`. Reads `.user.login` from the
response body.

**Auth.** A fine-grained PAT with "Public repositories (read-only)"
is enough. Set `GITHUB_TOKEN` in the environment. Missing/invalid
token → resolver logs and skips; cache does not get populated, but
stamp approval is unaffected.

**Trust model.** The cached `github` field is the author of the most
recent PR-shaped evidence URL attached to any stamp whose subject is
this rig handle. It is *not* a cryptographic attestation that the rig
owns the GitHub account. An evidence URL pointing at somebody else's
PR would cause the cache to map this rig to that unrelated GitHub
user. For a thin profile fallback link this is acceptable, but do
not treat entries as verified identity outside this context.

**Consumer change.** `internal/pile/profile.go` `QueryProfileResponse`
now reads the cache when building `StampFeed.GithubURL`:

- Cache hit with non-empty `github` → `https://github.com/{github}`.
- Cache miss or empty `github` → empty string; the web UI already
  gates the anchor on a non-empty URL, so no link renders.

**Concurrency.** Cache writes use atomic temp-file-then-rename (same
pattern as `internal/federation/federation.go:fileConfigStore`). No
advisory file lock; concurrent writers may lose one write but the
next `Accept` or `--all` repopulates. Corrupted JSON on read logs a
warning and treats the cache as empty.

## Out of Scope (v1)

- **Backfill triggering from the UI.** The-pile pipeline is external;
  there is no in-repo mechanism to request ingestion.
- **Profile search fallback.** `ProfileSearch` queries `the-pile.rigs`
  and will not surface fallback-eligible handles. Users reach the
  fallback only via direct URL or external link. Known gap.
- **Pagination / "all stamps" view.** Fixed 10, no load-more.
- **Non-PR evidence rendering beyond `owner/repo`.** Commit and blob
  URLs render as `owner/repo` linked to the full URL; we do not
  special-case them.
- **Federated handle map.** The cache is per-rig local. Later phases
  may promote verified mappings to `hop/wl-commons` for shared use.
- **Automatic cache refresh.** No TTL; stale entries persist until the
  operator runs `wl resolve-github --refresh` or the file is edited.

## Implementation Breakdown

Three phases, each ≤ 5 files, each independently verifiable.

### Phase 1 — Backend: wl-commons client + discriminated response

- Extend `internal/pile/` (or add a parallel package) to also query
  `hop/wl-commons`.
- New `QueryStampFeed(handle) ([]StampEntry, error)`.
- Refactor `pile.QueryProfile` to return a `ProfileResponse` union:
  `{Kind, CharacterSheet?, StampFeed?}`.
- Update `handleProfile` in `internal/api/pile_handlers.go` to marshal
  the union and branch on `boot_block` presence → `wl-commons`
  fallback.
- Update 404 semantics: 404 only when both sources are empty.
- Unit tests: fake `RowQuerier` for both clients; test all four
  branches (sheet present / sheet absent + stamps / both absent /
  wl-commons error).

### Phase 2 — Frontend types + component

- Update `web/src/api/types.ts` to a discriminated union keyed on
  `kind`.
- New `web/src/components/StampFeedView.tsx` + CSS module.
- `ProfileView.tsx` branches on `data.kind` and delegates.
- Reuse existing `SkeletonRows` for loading.

### Phase 3 — Polish + error-state handling

- `stamps_error` banner variant.
- Evidence-parsing correctness tests (PR, non-PR, free text, NULL).

### Phase 4 — GitHub handle resolution cache

- New `internal/githubcache/` package (Cache + Resolver).
- SDK `Accept`/`AcceptUpstream` hook after mutex release (sync, 5s).
- `internal/pile/profile.go` reads cache; drops unverified handle
  fallback.
- `cmd/wl/cmd_resolve_github.go` CLI: single-handle + `--all` +
  `--refresh`.
- Auth: `GITHUB_TOKEN` env var.
- Style pass to match the existing Ayu / parchment theme.
