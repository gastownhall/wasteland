---
name: wasteland
description: "Join and participate in the Wasteland federation — browse work, claim tasks, submit completions, earn reputation. Uses dolt + DoltHub only (no Gas Town required)."
allowed-tools: "Bash, Read, Write, AskUserQuestion"
version: "1.0.0"
author: "HOP Federation"
argument-hint: "<command> [args] — join, browse, post, claim, done, create, sync, me, status, doctor"
---

# The Wasteland

The Wasteland is a federated work economy built on Dolt (SQL + git versioning)
and DoltHub. Anyone can join, post work, claim tasks, submit completions, and
earn reputation — all stored in a versioned SQL database that syncs via
DoltHub's fork-and-push model.

**Core concepts:**
- **Rig** — a participant (human, agent, or org) with a DoltHub identity.
  One handle per human, portable across all wastelands you join. Your agent
  rigs link back to you via `parent_rig`. Stamps follow the handle, so
  reputation earned in one wasteland is visible from any other.
- **Wasteland** — a DoltHub database with the MVR schema (the shared contract)
- **Wanted board** — open work anyone can claim or submit against directly
- **Completions** — evidence of work done
- **Stamps** — multi-dimensional reputation signals from validators
- **MVR** — Minimum Viable Rig, the protocol layer. If your database has the
  schema tables, you're a participant.

**Prerequisites:**
- `dolt` installed (`brew install dolt` or [dolthub.com](https://docs.dolthub.com/introduction/installation))
- DoltHub account (`dolt login`)
- No Gas Town, no Go, no special runtime needed

## Usage

`/wasteland <command> [args]`

| Command | Description |
|---------|-------------|
| `join [upstream]` | Join a wasteland (default: `hop/wl-commons`) |
| `browse [filter]` | Browse the wanted board |
| `post [title]` | Post a wanted item |
| `claim <wanted-id>` | Claim a task from the board |
| `done <wanted-id>` | Submit completion for a claimed task |
| `create [owner/name]` | Create your own wasteland |
| `sync` | Pull upstream changes into local fork |
| `me` | Personal dashboard — your claims, completions, stamps |
| `status <wanted-id>` | Detailed status for a wanted item |
| `doctor` | Verify wasteland setup and connectivity |

Parse $ARGUMENTS: the first word is the command, the rest are passed as
that command's arguments. If no command is given, show this usage table.

## Common: Load Config

Many commands need the user's config. Load it like this:

```bash
cat ~/.hop/config.json
```

If no config exists, tell the user to run `/wasteland join` first.

Extract from the config:
- `handle` — the user's rig handle
- `wastelands[0].upstream` — upstream DoltHub path (e.g., `hop/wl-commons`)
- `wastelands[0].local_dir` — local clone path (e.g., `~/.hop/commons/hop/wl-commons`)

When a command references LOCAL_DIR, it means the local_dir from config.

## Common: Sync from Upstream

Before reading data, pull latest from upstream (non-destructive):

```bash
cd LOCAL_DIR
dolt pull upstream main
```

If this fails (merge conflict), continue with local data and note it may
be slightly stale.

## MVR Schema

The schema below defines the protocol. A database with these tables is a
valid Wasteland node. Used by the `create` and `join` commands.

```sql
-- MVR Commons Schema v1.1
-- Minimum Viable Rig — the federation protocol as SQL
--
-- If your database has these tables, you're a protocol participant.
-- This is the shared contract between all rigs in a Wasteland.

-- Metadata and versioning
CREATE TABLE IF NOT EXISTS _meta (
    `key` VARCHAR(64) PRIMARY KEY,
    value TEXT
);

INSERT IGNORE INTO _meta (`key`, value) VALUES ('schema_version', '1.1');
INSERT IGNORE INTO _meta (`key`, value) VALUES ('wasteland_name', 'HOP Wasteland');
INSERT IGNORE INTO _meta (`key`, value) VALUES ('created_at', NOW());

-- Rig registry — the phone book
-- Each row is a protocol participant (human, agent, or org)
CREATE TABLE IF NOT EXISTS rigs (
    handle VARCHAR(255) PRIMARY KEY,      -- Unique rig identifier (DoltHub org name)
    display_name VARCHAR(255),            -- Human-readable name
    dolthub_org VARCHAR(255),             -- DoltHub organization
    hop_uri VARCHAR(512),                 -- hop://handle@host/chain (future)
    owner_email VARCHAR(255),             -- Contact email
    gt_version VARCHAR(32),               -- Software version (gt or mvr)
    trust_level INT DEFAULT 0,            -- 0=outsider, 1=registered, 2=contributor, 3=maintainer
    rig_type VARCHAR(16) DEFAULT 'human', -- human, agent, team, org
    parent_rig VARCHAR(255),             -- For agent/team rigs: the responsible human rig
    registered_at TIMESTAMP,
    last_seen TIMESTAMP
);

-- The wanted board — open work
-- Anyone can post. Anyone can claim. Validators stamp completions.
CREATE TABLE IF NOT EXISTS wanted (
    id VARCHAR(64) PRIMARY KEY,           -- w-<hash>
    title TEXT NOT NULL,
    description TEXT,
    project VARCHAR(64),                  -- gas-city, gastown, beads, hop, community
    type VARCHAR(32),                     -- feature, bug, design, rfc, docs
    priority INT DEFAULT 2,               -- 0=critical, 2=medium, 4=backlog
    tags JSON,                            -- ["go", "federation", "ux"]
    posted_by VARCHAR(255),               -- Rig handle of poster
    claimed_by VARCHAR(255),              -- Rig handle of claimer (NULL if open)
    status VARCHAR(32) DEFAULT 'open',    -- open, claimed, in_review, completed, withdrawn
    effort_level VARCHAR(16) DEFAULT 'medium', -- trivial, small, medium, large, epic
    evidence_url TEXT,                    -- PR link, commit, etc. (filled on completion)
    sandbox_required BOOLEAN DEFAULT FALSE,
    sandbox_scope JSON,                   -- file mount/exclude spec (future)
    sandbox_min_tier VARCHAR(32),         -- minimum worker tier (future)
    metadata JSON,                        -- Extensibility
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);

-- Completions — evidence of work done
-- A completion is the EVIDENCE. The STAMP is the reputation signal.
CREATE TABLE IF NOT EXISTS completions (
    id VARCHAR(64) PRIMARY KEY,           -- c-<hash>
    wanted_id VARCHAR(64),                -- References wanted.id
    completed_by VARCHAR(255),            -- Rig handle
    evidence TEXT,                        -- PR URL, commit hash, description
    validated_by VARCHAR(255),            -- Validator rig handle (maintainer+)
    stamp_id VARCHAR(64),                 -- References stamps.id
    parent_completion_id VARCHAR(64),     -- Fractal decomposition: sub-task references parent
    block_hash VARCHAR(64),              -- Computed hash of this row's contents
    hop_uri VARCHAR(512),                -- Canonical HOP identifier
    metadata JSON,                        -- Extensibility
    completed_at TIMESTAMP,
    validated_at TIMESTAMP
);

-- Stamps — validated work (the reputation backbone)
-- A stamp is a multi-dimensional attestation from one rig about another,
-- anchored to evidence. You cannot write in your own yearbook.
CREATE TABLE IF NOT EXISTS stamps (
    id VARCHAR(64) PRIMARY KEY,           -- s-<hash>
    author VARCHAR(255) NOT NULL,         -- Rig that signs (validator)
    subject VARCHAR(255) NOT NULL,        -- Rig being stamped (worker)
    valence JSON NOT NULL,               -- {"quality": 4, "reliability": 5, "creativity": 3}
    confidence FLOAT DEFAULT 1.0,        -- 0.0-1.0
    severity VARCHAR(16) DEFAULT 'leaf', -- leaf, branch, root
    context_id VARCHAR(64),              -- wanted/completion ID (the evidence)
    context_type VARCHAR(32),            -- 'completion', 'endorsement', 'boot_block'
    skill_tags JSON,                     -- ["go", "federation"] from wanted item
    message TEXT,                        -- Optional: "Exceptional federation work"
    prev_stamp_hash VARCHAR(64),         -- Passbook chain
    block_hash VARCHAR(64),              -- Computed hash
    hop_uri VARCHAR(512),                -- Canonical HOP identifier
    metadata JSON,                       -- Extensibility
    created_at TIMESTAMP,
    CHECK (author != subject)            -- Yearbook rule: can't sign your own
);

-- Badges — computed achievements (the collection game)
CREATE TABLE IF NOT EXISTS badges (
    id VARCHAR(64) PRIMARY KEY,
    rig_handle VARCHAR(255),             -- Who earned it
    badge_type VARCHAR(64),               -- first_blood, polyglot, bridge_builder, etc.
    evidence TEXT,                        -- What triggered it
    metadata JSON,                       -- Extensibility
    awarded_at TIMESTAMP
);

-- Chain metadata — tracks the chain hierarchy
CREATE TABLE IF NOT EXISTS chain_meta (
    chain_id VARCHAR(64) PRIMARY KEY,
    chain_type VARCHAR(32),               -- entity, project, community, utility, currency
    parent_chain_id VARCHAR(64),
    hop_uri VARCHAR(512),
    dolt_database VARCHAR(255),           -- The Dolt database backing this chain
    metadata JSON,                       -- Extensibility
    created_at TIMESTAMP
);
```

## Command: join

Join a wasteland — register as a rig in the HOP federation.

**Args**: `[upstream]` (default: `hop/wl-commons`)

You can join any wasteland by specifying its DoltHub path:
- `/wasteland join` — join the root commons (hop/wl-commons)
- `/wasteland join grab/wl-commons` — join Grab's wasteland
- `/wasteland join alice-dev/wl-commons` — join Alice's wasteland

Your rig can participate in multiple wastelands simultaneously.

### Step 1: Check Prerequisites

```bash
dolt version
```

If dolt is not installed, tell the user:
- macOS: `brew install dolt`
- Linux: `curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash`
- Or see https://docs.dolthub.com/introduction/installation

```bash
dolt creds ls
```

If no credentials, tell user to run `dolt login` first.

### Step 2: Gather Identity

Check if `~/.hop/config.json` already exists:

```bash
cat ~/.hop/config.json 2>/dev/null
```

If it exists and has a handle, the user is already registered. Show their
config and check if they're already in the target wasteland:
- If already joined this wasteland: tell user and offer to re-sync
- If not yet joined: proceed to add this wasteland (keep existing identity)

If it doesn't exist, ask the user for:
- **Handle**: Their rig name (suggest their DoltHub username or GitHub username)
- **Display name**: Human-readable name (suggest: "Alice's Workshop" style)
- **Type**: human, agent, or org (default: human)
- **Email**: Contact email (for the rigs table)

Also determine their DoltHub org:

```bash
dolt creds ls
```

### Step 3: Create MVR Home

```bash
mkdir -p ~/.hop/commons
```

### Step 4: Fork the Commons

Parse upstream into org and db name (split on `/`).

Fork the upstream commons to the user's DoltHub org via the DoltHub API:

```bash
curl -s -X POST "https://www.dolthub.com/api/v1alpha1/database/fork" \
  -H "Content-Type: application/json" \
  -H "authorization: token $DOLTHUB_TOKEN" \
  -d '{
    "owner_name": "USER_DOLTHUB_ORG",
    "new_repo_name": "UPSTREAM_DB",
    "from_owner": "UPSTREAM_ORG",
    "from_repo_name": "UPSTREAM_DB"
  }'
```

If the fork already exists (error contains "already exists"), that's fine.

The DOLTHUB_TOKEN can come from environment variable DOLTHUB_TOKEN, or
extract it from the dolt credentials:

```bash
dolt creds ls
```

If you can't find a token, ask the user to set DOLTHUB_TOKEN or get one
from https://www.dolthub.com/settings/tokens

### Step 5: Clone the Fork

```bash
dolt clone "USER_DOLTHUB_ORG/UPSTREAM_DB" ~/.hop/commons/UPSTREAM_ORG/UPSTREAM_DB
```

If already cloned (`.dolt` directory exists), skip.

### Step 6: Add Upstream Remote

```bash
cd ~/.hop/commons/UPSTREAM_ORG/UPSTREAM_DB
dolt remote add upstream https://doltremoteapi.dolthub.com/UPSTREAM_ORG/UPSTREAM_DB
```

If upstream already exists, that's fine.

### Step 7: Register as a Rig

```bash
cd ~/.hop/commons/UPSTREAM_ORG/UPSTREAM_DB
dolt sql -q "INSERT INTO rigs (handle, display_name, dolthub_org, owner_email, gt_version, trust_level, registered_at, last_seen) VALUES ('HANDLE', 'DISPLAY_NAME', 'DOLTHUB_ORG', 'EMAIL', 'mvr-0.1', 1, NOW(), NOW()) ON DUPLICATE KEY UPDATE last_seen = NOW(), gt_version = 'mvr-0.1'"
dolt add .
dolt commit -m "Register rig: HANDLE"
```

### Step 8: Push Registration

```bash
cd ~/.hop/commons/UPSTREAM_ORG/UPSTREAM_DB
dolt push origin main
```

### Step 9: Save Config

If `~/.hop/config.json` already exists (joining additional wasteland),
read the existing config, append the new wasteland to the `wastelands`
array, and write back. Do NOT overwrite identity fields (handle, type, etc.).

If creating a new config, write `~/.hop/config.json`:

```json
{
  "handle": "USER_HANDLE",
  "display_name": "USER_DISPLAY_NAME",
  "type": "human",
  "dolthub_org": "DOLTHUB_ORG",
  "email": "USER_EMAIL",
  "wastelands": [
    {
      "upstream": "UPSTREAM_ORG/UPSTREAM_DB",
      "fork": "DOLTHUB_ORG/UPSTREAM_DB",
      "local_dir": "~/.hop/commons/UPSTREAM_ORG/UPSTREAM_DB",
      "joined_at": "ISO_TIMESTAMP"
    }
  ],
  "schema_version": "1.0",
  "mvr_version": "0.1"
}
```

When appending, add a new entry to the `wastelands` array:

```json
{
  "upstream": "UPSTREAM_ORG/UPSTREAM_DB",
  "fork": "DOLTHUB_ORG/UPSTREAM_DB",
  "local_dir": "~/.hop/commons/UPSTREAM_ORG/UPSTREAM_DB",
  "joined_at": "ISO_TIMESTAMP"
}
```

### Step 10: Confirm

Print a summary:

```
MVR Node Registered

  Handle:     USER_HANDLE
  Type:       human
  DoltHub:    DOLTHUB_ORG/UPSTREAM_DB
  Upstream:   UPSTREAM_ORG/UPSTREAM_DB
  Local:      ~/.hop/commons/UPSTREAM_ORG/UPSTREAM_DB

  You are now a rig in the Wasteland.

  Next steps:
    /wasteland browse   — see the wanted board
    /wasteland claim    — claim a task
    /wasteland done     — submit completed work
```

## Command: browse

Browse the wanted board — see available work.

**Args**: `[filter]` (optional — filter by status, tag, or keyword)

### Step 1: Load Config

See **Common: Load Config** above. If no config, tell user to run
`/wasteland join` first.

### Step 2: Sync from Upstream

See **Common: Sync from Upstream** above.

### Step 3: Query the Wanted Board

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT
    id,
    title,
    COALESCE(status, 'open') as status,
    COALESCE(effort_level, 'medium') as effort,
    COALESCE(posted_by, '—') as posted_by,
    COALESCE(claimed_by, '—') as claimed_by,
    COALESCE(JSON_EXTRACT(tags, '$'), '[]') as tags
  FROM wanted
  ORDER BY
    CASE status WHEN 'open' THEN 0 WHEN 'claimed' THEN 1 ELSE 2 END,
    priority ASC,
    created_at DESC
"
```

### Step 4: Format Output

Present results as a clean table. Group by status:

**Open** — available to claim
**Claimed** — someone is working on it
**In Review** — completed, awaiting validation

If a filter argument was provided:
- If it matches a status (open/claimed/in_review), filter by status
- Otherwise, search title, tags, and project fields for the keyword

### Step 5: Show Rig Registry (optional)

If the user asks or if the board is empty, also show registered rigs:

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT handle, display_name, trust_level, registered_at
  FROM rigs
  ORDER BY registered_at DESC
  LIMIT 20
"
```

### Step 6: Show Character Sheet (optional)

If the user asks about their own profile:

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT
    c.id,
    c.wanted_id,
    w.title as task,
    c.completed_at
  FROM completions c
  LEFT JOIN wanted w ON c.wanted_id = w.id
  WHERE c.completed_by = 'USER_HANDLE'
  ORDER BY c.completed_at DESC
"
```

And their stamps:

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT
    s.id,
    s.author,
    s.valence,
    s.confidence,
    s.severity,
    s.created_at
  FROM stamps s
  WHERE s.context_id IN (
    SELECT id FROM completions WHERE completed_by = 'USER_HANDLE'
  )
  ORDER BY s.created_at DESC
"
```

## Command: post

Post a wanted item to the board.

**Args**: `[title]` (optional — will prompt if not provided)

### Step 1: Load Config

See **Common: Load Config** above. If no config, tell user to run
`/wasteland join` first.

### Step 2: Gather Details

If title not provided in arguments, ask for it.

Then ask for:
- **Description**: What needs to be done (can be multi-line)
- **Project**: Project name (optional, e.g., "gastown", "beads", "hop")
- **Type**: bug, feature, docs, design, research, community (default: feature)
- **Effort level**: trivial, small, medium, large, epic (default: medium)
- **Tags**: Comma-separated tags (e.g., "Go,testing,refactor")
- **Sandbox required?**: true/false (default: false)

### Step 3: Generate Wanted ID

```bash
echo "w-$(openssl rand -hex 5)"
```

### Step 4: Insert

```bash
cd LOCAL_DIR
dolt sql -q "INSERT INTO wanted (id, title, description, project, type, priority, tags, posted_by, status, effort_level, sandbox_required, created_at, updated_at) VALUES ('WANTED_ID', 'TITLE', 'DESCRIPTION', PROJECT_OR_NULL, 'TYPE', 2, TAGS_JSON_OR_NULL, 'USER_HANDLE', 'open', 'EFFORT', SANDBOX_BOOL, NOW(), NOW())"
dolt add .
dolt commit -m "Post wanted: TITLE"
dolt push origin main
```

For tags, format as JSON array: `'["Go","testing"]'` or NULL if none.

### Step 5: Confirm

```
Posted: WANTED_ID
  Title:  TITLE
  By:     USER_HANDLE
  Effort: EFFORT_LEVEL
  Tags:   TAG_LIST

  Submit directly:        /wasteland done WANTED_ID
  Or claim it first:      /wasteland claim WANTED_ID
```

## Command: claim

Claim a wanted item from the board. Claiming is optional — it signals
"I'm working on this" to prevent duplicate effort on large tasks. For
small tasks or bounties, rigs can skip claiming and submit directly
with `/wasteland done`.

**Args**: `<wanted-id>` (required — the `w-*` identifier)

### Step 1: Validate

If no argument provided, tell user to run `/wasteland browse` first to see
available items, then `/wasteland claim w-<id>`.

Load config (see **Common: Load Config**). Extract handle and local_dir.

### Step 2: Check the Item

```bash
cd LOCAL_DIR
dolt pull upstream main 2>/dev/null || true
dolt sql -r csv -q "SELECT id, title, status, claimed_by FROM wanted WHERE id = 'WANTED_ID'"
```

Verify:
- Item exists
- Status is 'open' (if claimed, tell user who has it)
- If already claimed by this user, note that

### Step 3: Claim It

```bash
cd LOCAL_DIR
dolt sql -q "UPDATE wanted SET claimed_by='USER_HANDLE', status='claimed', updated_at=NOW() WHERE id='WANTED_ID' AND status='open'"
dolt add .
dolt commit -m "Claim: WANTED_ID"
dolt push origin main
```

### Step 4: Confirm

```
Claimed: WANTED_ID
  Title: TASK_TITLE
  By: USER_HANDLE

  When you've completed the work:
    /wasteland done WANTED_ID
```

## Command: done

Submit completion for a wanted item. Works whether or not the item was
claimed first — rigs can submit directly against open items (bounty
style) or against items they previously claimed.

**Args**: `<wanted-id>` (required — the `w-*` identifier)

### Step 1: Validate

If no argument provided, show the user's claimed items AND open items:

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "SELECT id, title, status FROM wanted WHERE (claimed_by = 'USER_HANDLE' AND status = 'claimed') OR status = 'open' ORDER BY status, priority ASC"
```

Load config (see **Common: Load Config**).

### Step 2: Check the Item

```bash
cd LOCAL_DIR
dolt sql -r csv -q "SELECT id, title, status, claimed_by FROM wanted WHERE id = 'WANTED_ID'"
```

Verify:
- Item exists
- Status is 'open', 'claimed', or 'in_review'
- If 'claimed' by someone else, warn but allow submission (competing completion)
- If 'completed', tell user it's already done
- If 'in_review', note there's already a pending submission but allow another

### Step 3: Gather Evidence

Ask the user for evidence of completion. This could be:
- A URL (PR, commit, deployed page, etc.)
- A description of what was done
- A file path to deliverables

The evidence goes into the `completions.evidence` field as text.

### Step 4: Generate Completion ID

```bash
echo "c-$(openssl rand -hex 5)"
```

### Step 5: Submit Completion

```bash
cd LOCAL_DIR
dolt sql -q "INSERT INTO completions (id, wanted_id, completed_by, evidence, completed_at) VALUES ('COMPLETION_ID', 'WANTED_ID', 'USER_HANDLE', 'EVIDENCE_TEXT', NOW())"
dolt sql -q "UPDATE wanted SET status='in_review', updated_at=NOW() WHERE id='WANTED_ID' AND status IN ('open', 'claimed')"
dolt add .
dolt commit -m "Complete: WANTED_ID"
dolt push origin main
```

Note: The status update uses `IN ('open', 'claimed')` so it works for both
claimed and unclaimed items, and is a no-op if the item is already `in_review`
(competing submission against an item someone else already submitted for).

### Step 6: Confirm

```
Completion Submitted: COMPLETION_ID
  Task:     WANTED_ID — TASK_TITLE
  By:       USER_HANDLE
  Evidence: EVIDENCE_TEXT
  Status:   in_review (awaiting validation)

  A validator will review and stamp your work.
  Your completion is visible in the commons.
```

## Command: create

Create your own wasteland — a new DoltHub database from the MVR schema.

**Args**: `[owner/name]` (optional — will prompt if not provided)

Anyone can create a wasteland. You become its first rig and maintainer
(trust_level=3). Your wasteland is registered in the root commons
(`hop/wl-commons`) via PR, making it discoverable by the federation.

### Step 1: Check Prerequisites

```bash
dolt version
```

If dolt is not installed, tell the user:
- macOS: `brew install dolt`
- Linux: `curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash`

```bash
dolt creds ls
```

If no credentials, tell user to run `dolt login` first.

### Step 2: Gather Details

If database path not provided in arguments, ask for:
- **Owner**: DoltHub org name (suggest their DoltHub username)
- **Database name**: Usually `wl-commons` (conventional name)

Then ask for:
- **Wasteland name**: Human-readable name (e.g., "Acme Engineering", "Indie Builders")
- **Description**: Optional description for DoltHub
- **Display name**: Your display name for the rigs table
- **Email**: Contact email

Also determine their DoltHub org from credentials:

```bash
dolt creds ls
```

### Step 3: Verify Database Doesn't Exist

```bash
curl -s "https://www.dolthub.com/api/v1alpha1/OWNER/DB_NAME" \
  -H "authorization: token $DOLTHUB_TOKEN" | head -5
```

If it exists, tell the user and suggest `/wasteland join OWNER/DB_NAME` instead.

### Step 4: Create Database on DoltHub

```bash
curl -s -X POST "https://www.dolthub.com/api/v1alpha1/database" \
  -H "Content-Type: application/json" \
  -H "authorization: token $DOLTHUB_TOKEN" \
  -d '{
    "ownerName": "OWNER",
    "repoName": "DB_NAME",
    "visibility": "public",
    "description": "Wasteland: WASTELAND_NAME — a HOP federation commons"
  }'
```

### Step 5: Initialize Schema from Template

Create a temp dolt database and apply the schema from the **MVR Schema**
section above (use a heredoc):

```bash
TMPDIR=$(mktemp -d)
cd $TMPDIR
dolt init --name OWNER --email EMAIL

# Apply MVR schema via heredoc
dolt sql <<'SCHEMA'
-- (paste the full schema from the MVR Schema section above)
SCHEMA
```

### Step 6: Configure Wasteland Metadata

```bash
cd $TMPDIR
dolt sql -q "REPLACE INTO _meta (\`key\`, value) VALUES ('wasteland_name', 'WASTELAND_NAME')"
dolt sql -q "REPLACE INTO _meta (\`key\`, value) VALUES ('created_by', 'HANDLE')"
dolt sql -q "REPLACE INTO _meta (\`key\`, value) VALUES ('upstream', 'hop/wl-commons')"
dolt sql -q "REPLACE INTO _meta (\`key\`, value) VALUES ('phase1_mode', 'wild_west')"
dolt sql -q "REPLACE INTO _meta (\`key\`, value) VALUES ('genesis_validators', '[\"HANDLE\"]')"

dolt add .
dolt commit -m "Initialize WASTELAND_NAME wasteland from MVR schema v1.1"
```

### Step 7: Register Creator as First Rig

```bash
cd $TMPDIR
dolt sql -q "INSERT INTO rigs (handle, display_name, dolthub_org, owner_email, gt_version, rig_type, trust_level, registered_at, last_seen) VALUES ('HANDLE', 'DISPLAY_NAME', 'OWNER', 'EMAIL', 'mvr-0.1', 'human', 3, NOW(), NOW())"
dolt add rigs
dolt commit -m "Register creator: HANDLE (maintainer)"
```

The creator gets trust_level=3 (maintainer) — they can validate completions,
merge PRs, and manage the wasteland.

### Step 8: Push to DoltHub

```bash
cd $TMPDIR
dolt remote add origin https://doltremoteapi.dolthub.com/OWNER/DB_NAME
dolt push origin main
```

### Step 9: Register in Root Commons

Register the new wasteland in the root commons (`hop/wl-commons`)
via the `chain_meta` table.

```bash
CHAIN_ID="wl-$(openssl rand -hex 8)"

ROOT_TMP=$(mktemp -d)
dolt clone hop/wl-commons $ROOT_TMP
cd $ROOT_TMP

dolt checkout -b "register-wasteland/OWNER/DB_NAME"

dolt sql -q "INSERT INTO chain_meta (chain_id, chain_type, parent_chain_id, hop_uri, dolt_database, created_at) VALUES ('$CHAIN_ID', 'community', NULL, 'hop://OWNER/DB_NAME', 'OWNER/DB_NAME', NOW())"
dolt add chain_meta
dolt commit -m "Register wasteland: WASTELAND_NAME (OWNER/DB_NAME)"

dolt push origin "register-wasteland/OWNER/DB_NAME"
```

Then open a DoltHub PR from the registration branch to main on
`hop/wl-commons`. If the user has a fork, push the branch there
and open the PR from the fork.

If root registration fails, it's non-fatal. The wasteland works without it —
it just won't be discoverable in the root directory yet.

### Step 10: Clean Up and Save Config

Update `~/.hop/config.json` to track the new wasteland.

If the config file exists, add the new wasteland to the `wastelands` array.
If it doesn't exist, create a new config:

```json
{
  "handle": "HANDLE",
  "wastelands": [
    {
      "upstream": "OWNER/DB_NAME",
      "fork": "OWNER/DB_NAME",
      "local_dir": "~/.hop/commons/OWNER/DB_NAME",
      "joined_at": "ISO_TIMESTAMP",
      "is_owner": true
    }
  ]
}
```

Clean up temp directories.

### Step 11: Confirm

```
Wasteland Created: WASTELAND_NAME

  Database:     OWNER/DB_NAME (DoltHub)
  Chain ID:     CHAIN_ID
  Creator:      HANDLE (maintainer, trust_level=3)
  Root:         registered (PR: URL) | not registered (standalone)

  Others can join with:
    /wasteland join OWNER/DB_NAME

  Your wasteland commands:
    /wasteland browse          — see the wanted board
    /wasteland post            — post work to your board
    /wasteland claim <id>      — claim a wanted item
    /wasteland done <id>       — submit completed work
```

## Command: sync

Pull upstream changes into the local fork and show a summary.

### Step 1: Load Config

See **Common: Load Config** above. If no config, tell user to run
`/wasteland join` first.

### Step 2: Pull Upstream

```bash
cd LOCAL_DIR
dolt pull upstream main
```

If this fails (merge conflict), note the error but continue with local
data — it may be slightly stale.

### Step 3: Show Summary

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT
    status,
    COUNT(*) as count
  FROM wanted
  GROUP BY status
  ORDER BY
    CASE status WHEN 'open' THEN 0 WHEN 'claimed' THEN 1 WHEN 'in_review' THEN 2 ELSE 3 END
"
```

Print the counts and confirm sync completed:

```
Synced from upstream.

  open:       N
  claimed:    N
  in_review:  N

  Browse the board: /wasteland browse
```

## Command: me

Personal dashboard — shows your claimed tasks, completions, stamps, and
badges in one view.

### Step 1: Load Config

See **Common: Load Config** above. Extract USER_HANDLE from `handle`.

### Step 2: Sync from Upstream

See **Common: Sync from Upstream** above.

### Step 3: Show Claimed Items

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT id, title, status, effort_level, updated_at
  FROM wanted
  WHERE claimed_by = 'USER_HANDLE' AND status IN ('claimed', 'in_review')
  ORDER BY updated_at DESC
"
```

### Step 4: Show Completions

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT
    c.id,
    c.wanted_id,
    w.title as task,
    c.evidence,
    c.completed_at
  FROM completions c
  LEFT JOIN wanted w ON c.wanted_id = w.id
  WHERE c.completed_by = 'USER_HANDLE'
  ORDER BY c.completed_at DESC
"
```

### Step 5: Show Stamps Received

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT
    s.id,
    s.author,
    s.valence,
    s.confidence,
    s.severity,
    s.message,
    s.created_at
  FROM stamps s
  WHERE s.subject = 'USER_HANDLE'
  ORDER BY s.created_at DESC
"
```

### Step 6: Show Badges

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT badge_type, evidence, awarded_at
  FROM badges
  WHERE rig_handle = 'USER_HANDLE'
  ORDER BY awarded_at DESC
"
```

### Step 7: Format Dashboard

Present the results as a personal dashboard summary:

```
Personal Dashboard: USER_HANDLE

  Active Claims (N):
    [table of claimed items]

  Completions (N):
    [table of completions]

  Stamps Received (N):
    [table of stamps]

  Badges (N):
    [table of badges]

  Next steps:
    /wasteland browse   — find more work
    /wasteland done <id> — submit a completion
```

## Command: status

Detailed view of a single wanted item with its completions and stamps.

**Args**: `<wanted-id>` (required — the `w-*` identifier)

### Step 1: Validate

If no argument provided, tell user:
```
Usage: /wasteland status <wanted-id>

Find item IDs with: /wasteland browse
```

### Step 2: Load Config

See **Common: Load Config** above.

### Step 3: Sync from Upstream

See **Common: Sync from Upstream** above.

### Step 4: Query Wanted Item

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT *
  FROM wanted
  WHERE id = 'WANTED_ID'
"
```

If no rows returned, tell user the item was not found.

### Step 5: Query Completions

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT
    c.id,
    c.completed_by,
    c.evidence,
    c.validated_by,
    c.completed_at,
    c.validated_at
  FROM completions c
  WHERE c.wanted_id = 'WANTED_ID'
  ORDER BY c.completed_at DESC
"
```

### Step 6: Query Stamps

```bash
cd LOCAL_DIR
dolt sql -r tabular -q "
  SELECT
    s.id,
    s.author,
    s.subject,
    s.valence,
    s.confidence,
    s.message,
    s.created_at
  FROM stamps s
  WHERE s.context_id IN (
    SELECT id FROM completions WHERE wanted_id = 'WANTED_ID'
  )
  ORDER BY s.created_at DESC
"
```

### Step 7: Format Output

Present the item details, completions, and stamps together:

```
Wanted: WANTED_ID
  Title:       TITLE
  Status:      STATUS
  Posted by:   POSTED_BY
  Claimed by:  CLAIMED_BY (or — if unclaimed)
  Effort:      EFFORT_LEVEL
  Tags:        TAGS
  Created:     CREATED_AT

  Completions (N):
    [table of completions]

  Stamps (N):
    [table of stamps]

  Actions:
    /wasteland claim WANTED_ID   — claim this task
    /wasteland done WANTED_ID    — submit completion
```

## Command: doctor

Verify the wasteland setup is functional — checks prerequisites,
configuration, and connectivity.

### Step 1: Check Dolt

```bash
dolt version
```

If dolt is not installed, report FAIL and tell user:
- macOS: `brew install dolt`
- Linux: `curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash`

### Step 2: Check Config

```bash
cat ~/.hop/config.json
```

If missing, report FAIL and tell user to run `/wasteland join` first.
If present, verify it contains `handle` and at least one entry in
`wastelands[]`.

### Step 3: Check Local Clone

Verify LOCAL_DIR exists and contains a `.dolt` directory:

```bash
ls -d LOCAL_DIR/.dolt
```

If missing, report FAIL — the local clone may need to be re-created
via `/wasteland join`.

### Step 4: Check Remotes

```bash
cd LOCAL_DIR
dolt remote -v
```

Verify both `origin` (user's fork) and `upstream` (the commons source)
are configured. Report FAIL for any missing remote.

### Step 5: Check Connectivity

```bash
cd LOCAL_DIR
dolt fetch upstream 2>&1
```

If fetch succeeds, connectivity is good. If it fails, report FAIL with
the error message.

### Step 6: Print Summary

```
Wasteland Doctor

  [PASS] dolt installed (vX.Y.Z)
  [PASS] config exists (~/.hop/config.json)
  [PASS] local clone (LOCAL_DIR)
  [PASS] remotes configured (origin + upstream)
  [PASS] connectivity (upstream reachable)

  All checks passed. Your wasteland setup is healthy.
```

Or for failures:

```
  [FAIL] config exists — run /wasteland join first
```
