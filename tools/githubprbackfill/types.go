package main

import "time"

const (
	backfillVersion = "github-pr-backfill-v1"
	formulaVersion  = "v1"
	systemRig       = "gastownhall-backfill"
)

type Manifest struct {
	Version                string                 `json:"version"`
	GeneratedAt            string                 `json:"generated_at"`
	FormulaVersion         string                 `json:"formula_version"`
	Inputs                 ManifestInputs         `json:"inputs"`
	SourceState            SourceState            `json:"source_state"`
	IdentityMappings       []IdentityMapping      `json:"identity_mappings"`
	PRs                    []PRRecord             `json:"prs"`
	SyntheticRows          SyntheticRows          `json:"synthetic_rows"`
	ValidationExpectations ValidationExpectations `json:"validation_expectations"`
	Warnings               []string               `json:"warnings,omitempty"`
	Hash                   string                 `json:"hash,omitempty"`
}

type ManifestInputs struct {
	Repos                 []string `json:"repos"`
	IdentityOverridesPath string   `json:"identity_overrides_path"`
	ScoreOverridesPath    string   `json:"score_overrides_path,omitempty"`
	ExcludedLoginsPath    string   `json:"excluded_logins_path,omitempty"`
	ExcludedLogins        []string `json:"excluded_logins,omitempty"`
	GitHubMergedAfter     string   `json:"github_merged_after,omitempty"`
	GitHubParallelism     int      `json:"github_parallelism,omitempty"`
	GitHubCacheHash       string   `json:"github_cache_hash,omitempty"`
	GitHubFetchedAt       string   `json:"github_fetched_at,omitempty"`
}

type SourceState struct {
	CommonsDir    string `json:"commons_dir"`
	CommonsCommit string `json:"commons_commit"`
}

type IdentityMapping struct {
	GitHubLogin string `json:"github_login"`
	Handle      string `json:"handle"`
	Source      string `json:"source"`
	Reason      string `json:"reason,omitempty"`
}

type PRRecord struct {
	Repo        string    `json:"repo"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	AuthorLogin string    `json:"author_login"`
	Subject     string    `json:"subject,omitempty"`
	BaseRef     string    `json:"base_ref"`
	CreatedAt   time.Time `json:"created_at"`
	MergedAt    time.Time `json:"merged_at"`
	Decision    string    `json:"decision"`
	Reason      string    `json:"reason,omitempty"`
	Signals     Signals   `json:"signals"`
	Score       Score     `json:"score,omitempty"`
	RowIDs      RowIDs    `json:"row_ids,omitempty"`
	Warnings    []string  `json:"warnings,omitempty"`
}

type Signals struct {
	EffectiveAdditions       int      `json:"effective_additions"`
	EffectiveDeletions       int      `json:"effective_deletions"`
	EffectiveChangedFiles    int      `json:"effective_changed_files"`
	RawAdditions             int      `json:"raw_additions"`
	RawDeletions             int      `json:"raw_deletions"`
	RawChangedFiles          int      `json:"raw_changed_files"`
	Labels                   []string `json:"labels,omitempty"`
	SkillTags                []string `json:"skill_tags,omitempty"`
	IdentitySource           string   `json:"identity_source,omitempty"`
	StrongBlockingReview     bool     `json:"strong_blocking_review,omitempty"`
	MaintainerCommits        int      `json:"maintainer_commits,omitempty"`
	PostReviewAuthorChanges  int      `json:"post_review_author_changes,omitempty"`
	ReviewFixupCommit        bool     `json:"review_fixup_commit,omitempty"`
	LaterReverted            bool     `json:"later_reverted,omitempty"`
	NoEffectiveAuthoredFiles bool     `json:"no_effective_authored_files,omitempty"`
	GeneratedOnly            bool     `json:"generated_only,omitempty"`
	DependencyOnly           bool     `json:"dependency_only,omitempty"`
	MechanicalOnly           bool     `json:"mechanical_only,omitempty"`
	DocsOnly                 bool     `json:"docs_only,omitempty"`
	CIOnly                   bool     `json:"ci_only,omitempty"`
	RevertOnly               bool     `json:"revert_only,omitempty"`
	FeatureLike              bool     `json:"feature_like,omitempty"`
	RuntimeOrAPILike         bool     `json:"runtime_or_api_like,omitempty"`
	SubjectConflict          bool     `json:"subject_conflict,omitempty"`
	QuestionableAttribution  bool     `json:"questionable_attribution,omitempty"`
	AdoptedOriginalPR        string   `json:"adopted_original_pr,omitempty"`
	UnknownCommitAuthorCount int      `json:"unknown_commit_author_count,omitempty"`
	LargeGeneratedDiff       bool     `json:"large_generated_diff,omitempty"`
}

type Score struct {
	Quality     int      `json:"quality"`
	Reliability int      `json:"reliability"`
	Creativity  int      `json:"creativity"`
	Severity    string   `json:"severity"`
	Confidence  float64  `json:"confidence"`
	Source      string   `json:"source"`
	Rationale   []string `json:"rationale,omitempty"`
}

type RowIDs struct {
	Wanted     string `json:"wanted"`
	Completion string `json:"completion"`
	Stamp      string `json:"stamp"`
}

type SyntheticRows struct {
	Rigs        []RigRow        `json:"rigs"`
	Wanted      []WantedRow     `json:"wanted"`
	Completions []CompletionRow `json:"completions"`
	Stamps      []StampRow      `json:"stamps"`
}

type RigRow struct {
	Handle       string `json:"handle"`
	DisplayName  string `json:"display_name"`
	GTVersion    string `json:"gt_version"`
	TrustLevel   int    `json:"trust_level"`
	RigType      string `json:"rig_type"`
	RegisteredAt string `json:"registered_at"`
	LastSeen     string `json:"last_seen"`
}

type WantedRow struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Type        string   `json:"type"`
	Priority    int      `json:"priority"`
	Tags        []string `json:"tags"`
	PostedBy    string   `json:"posted_by"`
	ClaimedBy   string   `json:"claimed_by"`
	Status      string   `json:"status"`
	EffortLevel string   `json:"effort_level"`
	EvidenceURL string   `json:"evidence_url"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type CompletionRow struct {
	ID          string `json:"id"`
	WantedID    string `json:"wanted_id"`
	CompletedBy string `json:"completed_by"`
	Evidence    string `json:"evidence"`
	ValidatedBy string `json:"validated_by"`
	StampID     string `json:"stamp_id"`
	CompletedAt string `json:"completed_at"`
	ValidatedAt string `json:"validated_at"`
}

type StampRow struct {
	ID          string         `json:"id"`
	Author      string         `json:"author"`
	Subject     string         `json:"subject"`
	Valence     map[string]int `json:"valence"`
	Confidence  float64        `json:"confidence"`
	Severity    string         `json:"severity"`
	ContextID   string         `json:"context_id"`
	ContextType string         `json:"context_type"`
	SkillTags   []string       `json:"skill_tags"`
	Message     string         `json:"message"`
	CreatedAt   string         `json:"created_at"`
}

type ValidationExpectations struct {
	ExpectedRigs        int                        `json:"expected_rigs"`
	ExpectedWanted      int                        `json:"expected_wanted"`
	ExpectedCompletions int                        `json:"expected_completions"`
	ExpectedStamps      int                        `json:"expected_stamps"`
	SkippedByReason     map[string]int             `json:"skipped_by_reason,omitempty"`
	ScoreboardDeltas    map[string]ScoreboardDelta `json:"scoreboard_deltas,omitempty"`
}

type ScoreboardDelta struct {
	StampCount    int      `json:"stamp_count"`
	WeightedScore int      `json:"weighted_score"`
	Completions   int      `json:"completions"`
	SkillTags     []string `json:"skill_tags,omitempty"`
}
