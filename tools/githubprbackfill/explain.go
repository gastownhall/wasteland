package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type explainPROptions struct {
	Repo             string
	Number           int
	URL              string
	Subject          string
	IdentitySource   string
	GitHubCachePath  string
	GitHubPageSize   int
	GitHubDelay      time.Duration
	Format           string
	IncludeRawBodies bool
	OutPath          string
}

type explainPRReport struct {
	Version         string              `json:"version"`
	FormulaVersion  string              `json:"formula_version"`
	Repo            string              `json:"repo"`
	PR              int                 `json:"pr"`
	EvidenceURL     string              `json:"evidence_url"`
	CacheHash       string              `json:"cache_hash,omitempty"`
	Reproducibility string              `json:"reproducibility"`
	Subject         string              `json:"subject"`
	IdentitySource  string              `json:"identity_source"`
	RawInputs       explainRawInputs    `json:"raw_inputs"`
	DerivedSignals  Signals             `json:"derived_signals"`
	Score           Score               `json:"score"`
	ScoreRules      explainScoreRules   `json:"score_rules"`
	StampPreview    explainStampPreview `json:"stamp_preview"`
}

type explainRawInputs struct {
	Title          string                 `json:"title"`
	Author         explainUser            `json:"author"`
	BaseRef        string                 `json:"base_ref"`
	CreatedAt      time.Time              `json:"created_at"`
	MergedAt       *time.Time             `json:"merged_at,omitempty"`
	Additions      int                    `json:"additions"`
	Deletions      int                    `json:"deletions"`
	ChangedFiles   int                    `json:"changed_files"`
	Labels         []string               `json:"labels,omitempty"`
	Files          []explainFile          `json:"files"`
	Commits        []explainCommit        `json:"commits"`
	Reviews        []explainReview        `json:"reviews"`
	IssueComments  []explainComment       `json:"issue_comments"`
	ReviewComments []explainComment       `json:"review_comments"`
	Counts         map[string]int         `json:"counts"`
	Notes          map[string]interface{} `json:"notes,omitempty"`
}

type explainUser struct {
	Login string `json:"login,omitempty"`
	Type  string `json:"type,omitempty"`
}

type explainFile struct {
	Filename  string `json:"filename"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
	LowSignal bool   `json:"low_signal"`
}

type explainCommit struct {
	AuthorLogin    string    `json:"author_login,omitempty"`
	CommitterLogin string    `json:"committer_login,omitempty"`
	AuthorType     string    `json:"author_type,omitempty"`
	CommitterType  string    `json:"committer_type,omitempty"`
	AuthoredAt     time.Time `json:"authored_at"`
	Message        string    `json:"message"`
}

type explainReview struct {
	User    explainUser `json:"user"`
	State   string      `json:"state"`
	At      *time.Time  `json:"at,omitempty"`
	Body    string      `json:"body,omitempty"`
	Signals []string    `json:"signals,omitempty"`
}

type explainComment struct {
	User    explainUser `json:"user"`
	At      time.Time   `json:"at"`
	Body    string      `json:"body,omitempty"`
	Signals []string    `json:"signals,omitempty"`
}

type explainScoreRules struct {
	QualityReliability []string `json:"quality_reliability"`
	Creativity         []string `json:"creativity"`
	Severity           []string `json:"severity"`
	Confidence         []string `json:"confidence"`
}

type explainStampPreview struct {
	Author      string   `json:"author"`
	Subject     string   `json:"subject"`
	Valence     Score    `json:"valence"`
	Severity    string   `json:"severity"`
	Confidence  float64  `json:"confidence"`
	SkillTags   []string `json:"skill_tags"`
	Message     string   `json:"message"`
	EvidenceURL string   `json:"evidence_url"`
}

func runExplainPR(args []string) error {
	fs := flag.NewFlagSet("explain-pr", flag.ContinueOnError)
	opts := explainPROptions{}
	fs.StringVar(&opts.Repo, "repo", "", "GitHub repo, e.g. gastownhall/gascity")
	fs.IntVar(&opts.Number, "pr", 0, "GitHub PR number")
	fs.StringVar(&opts.URL, "url", "", "GitHub PR URL, e.g. https://github.com/org/repo/pull/123")
	fs.StringVar(&opts.Subject, "subject", "", "Wasteland subject handle; defaults to GitHub login")
	fs.StringVar(&opts.IdentitySource, "identity-source", "provisional", "identity source: exact, override, or provisional")
	fs.StringVar(&opts.GitHubCachePath, "github-cache", "", "optional resumable GitHub cache")
	fs.IntVar(&opts.GitHubPageSize, "github-page-size", 100, "GitHub page size")
	fs.DurationVar(&opts.GitHubDelay, "github-request-delay", 0, "delay between uncached GitHub requests")
	fs.StringVar(&opts.Format, "format", "json", "output format: json or markdown")
	fs.BoolVar(&opts.IncludeRawBodies, "include-raw-bodies", true, "include review/comment bodies used by text signal rules")
	fs.StringVar(&opts.OutPath, "out", "", "optional output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.URL != "" {
		repo, number, err := parseGitHubPRURL(opts.URL)
		if err != nil {
			return err
		}
		if opts.Repo == "" {
			opts.Repo = repo
		}
		if opts.Number == 0 {
			opts.Number = number
		}
	}
	if opts.Repo == "" || opts.Number <= 0 {
		return fmt.Errorf("explain-pr requires --url or both --repo and --pr")
	}
	if opts.IdentitySource == "" {
		opts.IdentitySource = "provisional"
	}
	switch opts.IdentitySource {
	case "exact", "override", "provisional":
	default:
		return fmt.Errorf("invalid --identity-source %q", opts.IdentitySource)
	}
	report, err := explainPR(context.Background(), opts)
	if err != nil {
		return err
	}
	var output []byte
	switch opts.Format {
	case "json":
		output, err = json.MarshalIndent(report, "", "  ")
	case "markdown":
		output = []byte(renderExplainMarkdown(report))
	default:
		return fmt.Errorf("invalid --format %q", opts.Format)
	}
	if err != nil {
		return err
	}
	output = append(output, '\n')
	if opts.OutPath != "" {
		return os.WriteFile(opts.OutPath, output, 0o644)
	}
	_, err = os.Stdout.Write(output)
	return err
}

func explainPR(ctx context.Context, opts explainPROptions) (explainPRReport, error) {
	cache, err := loadGitHubCache(opts.GitHubCachePath)
	if err != nil {
		return explainPRReport{}, err
	}
	client := newGitHubClient(os.Getenv("GITHUB_TOKEN"), cache, opts.GitHubCachePath, opts.GitHubPageSize, opts.GitHubDelay)
	fp, err := client.fetchPR(ctx, opts.Repo, opts.Number)
	if err != nil {
		return explainPRReport{}, err
	}
	if err := cache.save(opts.GitHubCachePath); err != nil {
		return explainPRReport{}, err
	}
	cacheHash, err := cache.hash()
	if err != nil {
		return explainPRReport{}, err
	}
	pr := prRecordFromFetched(fp)
	if pr.AuthorLogin == "" {
		return explainPRReport{}, fmt.Errorf("PR %s#%d has no author login", opts.Repo, opts.Number)
	}
	if opts.Subject == "" {
		opts.Subject = githubHandle(pr.AuthorLogin)
	}
	pr.Subject = opts.Subject
	pr.Signals.IdentitySource = opts.IdentitySource
	pr.Decision = "stamp"
	pr.Score = scorePR(pr)
	if err := validateScore(pr.Score); err != nil {
		return explainPRReport{}, err
	}
	return explainPRReport{
		Version:         backfillVersion,
		FormulaVersion:  formulaVersion,
		Repo:            opts.Repo,
		PR:              opts.Number,
		EvidenceURL:     pr.URL,
		CacheHash:       cacheHash,
		Reproducibility: reproducibilityNote(opts.GitHubCachePath),
		Subject:         opts.Subject,
		IdentitySource:  opts.IdentitySource,
		RawInputs:       rawInputsFromFetched(fp, opts.IncludeRawBodies),
		DerivedSignals:  pr.Signals,
		Score:           pr.Score,
		ScoreRules:      explainRules(pr),
		StampPreview:    stampPreview(pr),
	}, nil
}

func parseGitHubPRURL(raw string) (string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, fmt.Errorf("parse PR URL: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return "", 0, fmt.Errorf("not a GitHub PR URL: %s", raw)
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("invalid PR number in URL: %s", raw)
	}
	return parts[0] + "/" + parts[1], n, nil
}

func rawInputsFromFetched(fp fetchedPR, includeBodies bool) explainRawInputs {
	out := explainRawInputs{
		Title:          fp.Pull.Title,
		Author:         userForExplain(fp.Pull.User),
		BaseRef:        fp.Pull.Base.Ref,
		CreatedAt:      fp.Pull.CreatedAt,
		MergedAt:       fp.Pull.MergedAt,
		Additions:      fp.Pull.Additions,
		Deletions:      fp.Pull.Deletions,
		ChangedFiles:   fp.Pull.ChangedFiles,
		Labels:         labelsFromIssue(fp.Issue),
		Files:          make([]explainFile, 0, len(fp.Files)),
		Commits:        make([]explainCommit, 0, len(fp.Commits)),
		Reviews:        make([]explainReview, 0, len(fp.Reviews)),
		IssueComments:  make([]explainComment, 0, len(fp.IssueComments)),
		ReviewComments: make([]explainComment, 0, len(fp.ReviewComments)),
		Counts: map[string]int{
			"files":           len(fp.Files),
			"commits":         len(fp.Commits),
			"reviews":         len(fp.Reviews),
			"issue_comments":  len(fp.IssueComments),
			"review_comments": len(fp.ReviewComments),
		},
	}
	for _, f := range fp.Files {
		out.Files = append(out.Files, explainFile{
			Filename:  f.Filename,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Changes:   f.Changes,
			LowSignal: generatedOrLowSignal(f.Filename),
		})
	}
	for _, c := range fp.Commits {
		out.Commits = append(out.Commits, explainCommit{
			AuthorLogin:    login(c.Author),
			CommitterLogin: login(c.Committer),
			AuthorType:     userType(c.Author),
			CommitterType:  userType(c.Committer),
			AuthoredAt:     c.Commit.Author.Date,
			Message:        c.Commit.Message,
		})
	}
	for _, r := range fp.Reviews {
		row := explainReview{User: userForExplain(r.User), State: r.State, At: r.SubmittedAt, Signals: textSignals(r.Body)}
		if includeBodies {
			row.Body = r.Body
		}
		out.Reviews = append(out.Reviews, row)
	}
	for _, c := range fp.IssueComments {
		out.IssueComments = append(out.IssueComments, commentForExplain(c, includeBodies))
	}
	for _, c := range fp.ReviewComments {
		out.ReviewComments = append(out.ReviewComments, commentForExplain(c, includeBodies))
	}
	return out
}

func commentForExplain(c ghComment, includeBody bool) explainComment {
	row := explainComment{User: userForExplain(c.User), At: c.CreatedAt, Signals: textSignals(c.Body)}
	if includeBody {
		row.Body = c.Body
	}
	return row
}

func userForExplain(u *ghUser) explainUser {
	if u == nil {
		return explainUser{}
	}
	return explainUser{Login: u.Login, Type: u.Type}
}

func login(u *ghUser) string {
	if u == nil {
		return ""
	}
	return u.Login
}

func userType(u *ghUser) string {
	if u == nil {
		return ""
	}
	return u.Type
}

var blockingExplainRegex = regexp.MustCompile(`(?i)\b(blocker|blocking|must fix|unsafe|incorrect|broken|regression|data loss|security|changes requested)\b`)

func textSignals(body string) []string {
	if blockingExplainRegex.MatchString(body) {
		return []string{"blocking_text_match"}
	}
	return nil
}

func explainRules(pr PRRecord) explainScoreRules {
	s := pr.Signals
	rules := explainScoreRules{
		QualityReliability: []string{"start quality=4 reliability=4", "clamp final quality/reliability to 3..4"},
		Creativity:         []string{"start creativity=3"},
		Severity:           []string{"start severity=leaf"},
		Confidence:         []string{"start confidence from identity source"},
	}
	if s.LaterReverted || s.NoEffectiveAuthoredFiles || s.GeneratedOnly || s.DependencyOnly || s.MechanicalOnly {
		rules.QualityReliability = append(rules.QualityReliability, "low-signal cap: max quality/reliability 3")
	}
	if s.StrongBlockingReview {
		rules.QualityReliability = append(rules.QualityReliability, "strong blocking review: -1")
	}
	if s.MaintainerCommits > 0 {
		rules.QualityReliability = append(rules.QualityReliability, "maintainer commits: -1")
	}
	if s.PostReviewAuthorChanges > 0 && s.StrongBlockingReview {
		rules.QualityReliability = append(rules.QualityReliability, "post-review author changes after strong signal: -1")
	}
	if s.DependencyOnly || s.GeneratedOnly || s.MechanicalOnly || s.DocsOnly || s.CIOnly || s.RevertOnly || s.NoEffectiveAuthoredFiles {
		rules.Creativity = append(rules.Creativity, "low-creativity work type: creativity=2")
	} else if s.FeatureLike || s.RuntimeOrAPILike {
		rules.Creativity = append(rules.Creativity, "feature/runtime/API-like: creativity=4")
	}
	churn := s.EffectiveAdditions + s.EffectiveDeletions
	if s.EffectiveChangedFiles >= 20 || churn >= 1500 {
		rules.Severity = append(rules.Severity, "effective files >=20 or churn >=1500: severity=root")
	} else if s.EffectiveChangedFiles >= 5 || churn >= 300 || containsAny(strings.ToLower(pr.Title+" "+strings.Join(s.Labels, " ")), "feat", "feature", "perf", "performance", "refactor") {
		rules.Severity = append(rules.Severity, "effective files >=5, churn >=300, or feature/perf/refactor text: severity=branch")
	}
	if s.NoEffectiveAuthoredFiles {
		rules.Severity = append(rules.Severity, "no effective authored files: severity=leaf")
	}
	switch s.IdentitySource {
	case "exact":
		rules.Confidence = append(rules.Confidence, "exact identity: confidence=0.55")
	case "override":
		rules.Confidence = append(rules.Confidence, "override identity: confidence=0.53")
	default:
		rules.Confidence = append(rules.Confidence, "provisional identity: confidence=0.51")
	}
	if s.MaintainerCommits > 0 || s.StrongBlockingReview {
		rules.Confidence = append(rules.Confidence, "maintainer commits or blocking review: confidence cap 0.49")
	}
	if s.LaterReverted || s.SubjectConflict || s.QuestionableAttribution {
		rules.Confidence = append(rules.Confidence, "revert/conflict/questionable attribution: confidence cap 0.46")
	}
	return rules
}

func stampPreview(pr PRRecord) explainStampPreview {
	return explainStampPreview{
		Author:      systemRig,
		Subject:     pr.Subject,
		Valence:     pr.Score,
		Severity:    pr.Score.Severity,
		Confidence:  pr.Score.Confidence,
		SkillTags:   pr.Signals.SkillTags,
		Message:     fmt.Sprintf("github-pr-backfill formula=%s severity=%s quality=%d reliability=%d creativity=%d confidence=%.2f signals=%s", formulaVersion, pr.Score.Severity, pr.Score.Quality, pr.Score.Reliability, pr.Score.Creativity, pr.Score.Confidence, strings.Join(pr.Score.Rationale, "; ")),
		EvidenceURL: pr.URL,
	}
}

func reproducibilityNote(cachePath string) string {
	if cachePath == "" {
		return "live GitHub fetch into in-memory cache; rerun with --github-cache to pin fetched inputs"
	}
	return "deterministic for this formula version and GitHub cache contents"
}

func renderExplainMarkdown(r explainPRReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# GitHub PR Score Explanation\n\n")
	fmt.Fprintf(&b, "- PR: %s#%d\n", r.Repo, r.PR)
	fmt.Fprintf(&b, "- Evidence: %s\n", r.EvidenceURL)
	fmt.Fprintf(&b, "- Formula: %s / %s\n", r.Version, r.FormulaVersion)
	fmt.Fprintf(&b, "- Cache hash: %s\n", r.CacheHash)
	fmt.Fprintf(&b, "- Subject: %s (%s)\n\n", r.Subject, r.IdentitySource)
	fmt.Fprintf(&b, "## Score\n\n")
	fmt.Fprintf(&b, "- Severity: %s\n", r.Score.Severity)
	fmt.Fprintf(&b, "- Quality: %d\n", r.Score.Quality)
	fmt.Fprintf(&b, "- Reliability: %d\n", r.Score.Reliability)
	fmt.Fprintf(&b, "- Creativity: %d\n", r.Score.Creativity)
	fmt.Fprintf(&b, "- Confidence: %.2f\n", r.Score.Confidence)
	fmt.Fprintf(&b, "- Rationale: %s\n\n", strings.Join(r.Score.Rationale, "; "))
	fmt.Fprintf(&b, "## Derived Signals\n\n```json\n")
	_ = json.NewEncoder(&b).Encode(r.DerivedSignals)
	fmt.Fprintf(&b, "```\n")
	return b.String()
}

var _ io.Writer = (*strings.Builder)(nil)
