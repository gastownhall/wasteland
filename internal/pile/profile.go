package pile

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"github.com/gastownhall/wasteland/internal/commons"
)

// Profile is a developer's character sheet assembled from the-pile data.
type Profile struct {
	Handle      string  `json:"handle"`
	DisplayName string  `json:"display_name"`
	Bio         string  `json:"bio,omitempty"`
	Location    string  `json:"location,omitempty"`
	Company     string  `json:"company,omitempty"`
	AvatarURL   string  `json:"avatar_url,omitempty"`
	Source      string  `json:"source"`
	Confidence  float64 `json:"confidence"`
	CreatedAt   string  `json:"created_at"`

	// Activity stats (from sheet_json.activity_profile)
	TotalRepos int     `json:"total_repos,omitempty"`
	TotalStars int     `json:"total_stars,omitempty"`
	Followers  int     `json:"followers,omitempty"`
	AccountAge float64 `json:"account_age,omitempty"`

	// Value dimensions (normalized to 0-5 stamp scale)
	Quality     float64 `json:"quality"`
	Reliability float64 `json:"reliability"`
	Creativity  float64 `json:"creativity"`

	// Aggregated from the-pile stamps (GitHub analysis, NOT wasteland reputation)
	AssessmentCount int `json:"assessment_count"`

	Languages    []SkillEntry `json:"languages,omitempty"`
	Domains      []SkillEntry `json:"domains,omitempty"`
	Capabilities []SkillEntry `json:"capabilities,omitempty"`

	// From sheet_json
	NotableProjects []Project `json:"notable_projects,omitempty"`
}

// SkillEntry represents a skill with valence scores and evidence.
type SkillEntry struct {
	Name        string  `json:"name"`
	Quality     int     `json:"quality"`     // 0-5
	Reliability int     `json:"reliability"` // 0-5
	Creativity  int     `json:"creativity"`  // 0-5
	Confidence  float64 `json:"confidence"`
	Message     string  `json:"message"`
}

// Project is a notable open-source project.
type Project struct {
	Name       string   `json:"name"`
	Stars      int      `json:"stars"`
	Languages  []string `json:"languages,omitempty"`
	Role       string   `json:"role,omitempty"`
	ImpactTier string   `json:"impact_tier,omitempty"`
}

// ErrProfileNotFound is returned when a profile handle has no matching record.
var ErrProfileNotFound = fmt.Errorf("profile not found")

// ProfileResponseKind identifies the discriminated variant of a profile
// response returned by QueryProfileResponse.
type ProfileResponseKind string

// Recognized ProfileResponseKind variants.
const (
	KindCharacterSheet ProfileResponseKind = "character_sheet"
	KindStampFeed      ProfileResponseKind = "stamp_feed"
)

// ProfileResponse is a discriminated union. Exactly one of CharacterSheet
// or StampFeed is non-nil, matching Kind.
type ProfileResponse struct {
	Kind           ProfileResponseKind
	CharacterSheet *Profile
	StampFeed      *StampFeed
}

// characterSheetWire and stampFeedWire are marshal-only helpers so the wire
// format is a flat object with a leading "kind" field regardless of variant.
// The embedded pointer types intentionally do NOT implement json.Marshaler;
// if they ever do, Go's promotion rule would silently drop the outer Kind
// field. Do not add MarshalJSON to *Profile or *StampFeed without reworking
// this marshaller.
type characterSheetWire struct {
	Kind string `json:"kind"`
	*Profile
}

type stampFeedWire struct {
	Kind string `json:"kind"`
	*StampFeed
}

// MarshalJSON emits the wrapped variant with an injected kind field.
func (r *ProfileResponse) MarshalJSON() ([]byte, error) {
	switch r.Kind {
	case KindCharacterSheet:
		if r.CharacterSheet == nil {
			return nil, fmt.Errorf("ProfileResponse: character_sheet kind with nil body")
		}
		return json.Marshal(characterSheetWire{Kind: string(r.Kind), Profile: r.CharacterSheet})
	case KindStampFeed:
		if r.StampFeed == nil {
			return nil, fmt.Errorf("ProfileResponse: stamp_feed kind with nil body")
		}
		return json.Marshal(stampFeedWire{Kind: string(r.Kind), StampFeed: r.StampFeed})
	default:
		return nil, fmt.Errorf("ProfileResponse: unknown kind %q", r.Kind)
	}
}

// StampFeed is the fallback view for a handle with no boot_block but at
// least one stamp in hop/wl-commons.
type StampFeed struct {
	Handle      string           `json:"handle"`
	GithubURL   string           `json:"github_url"`
	Stamps      []StampFeedEntry `json:"stamps"`
	StampsError *string          `json:"stamps_error"`
}

// StampFeedEntry is one card in the stamp feed.
type StampFeedEntry struct {
	ID            string   `json:"id"`
	SkillTags     []string `json:"skill_tags"`
	Quality       int      `json:"quality"`
	Reliability   int      `json:"reliability"`
	Validator     string   `json:"validator"`
	Message       string   `json:"message,omitempty"`
	EvidenceURL   string   `json:"evidence_url,omitempty"`
	EvidenceLabel string   `json:"evidence_label,omitempty"`
	EvidenceText  string   `json:"evidence_text,omitempty"`
	CreatedAt     string   `json:"created_at"`
}

// GithubHandleLookup resolves a rig handle to a verified GitHub username.
// It returns the cached entry and true when the handle has been recorded
// (whether resolution succeeded or was tried-and-failed), or a zero Entry
// and false when the handle has never been resolved. Implementations must
// be safe for concurrent use. Pass nil to skip the lookup entirely — the
// stamp feed then emits an empty GithubURL and the UI renders no link.
type GithubHandleLookup func(handle string) (github string, ok bool)

// QueryProfileResponse returns a character sheet if hop/the-pile has a
// boot_block for the handle, otherwise falls back to a stamp feed assembled
// from hop/wl-commons. Returns ErrProfileNotFound only when both sources
// are empty — a truly unknown handle. If commonsReader is nil, the fallback
// is skipped and ErrProfileNotFound propagates from the pile lookup.
//
// cacheGet, when non-nil, is consulted for the stamp-feed variant to
// populate StampFeed.GithubURL from a persistent rig-handle → GitHub
// username cache. Cache miss, absent handle, or empty GitHub username all
// yield an empty GithubURL. The character-sheet path never consults it.
func QueryProfileResponse(pileReader, commonsReader RowQuerier, cacheGet GithubHandleLookup, handle string) (*ProfileResponse, error) {
	profile, err := QueryProfile(pileReader, handle)
	if err == nil {
		return &ProfileResponse{Kind: KindCharacterSheet, CharacterSheet: profile}, nil
	}
	if !errors.Is(err, ErrProfileNotFound) {
		return nil, err
	}
	if commonsReader == nil {
		return nil, err
	}

	githubURL := ""
	if cacheGet != nil {
		if gh, ok := cacheGet(handle); ok && gh != "" {
			githubURL = "https://github.com/" + url.PathEscape(gh)
		}
	}

	feed := &StampFeed{
		Handle:    handle,
		GithubURL: githubURL,
		Stamps:    []StampFeedEntry{},
	}

	stamps, ferr := queryStampFeed(commonsReader, handle)
	if ferr != nil {
		slog.Warn("stamp feed fallback unavailable", "handle", handle, "error", ferr)
		code := "stamps_unavailable"
		feed.StampsError = &code
		return &ProfileResponse{Kind: KindStampFeed, StampFeed: feed}, nil
	}

	if len(stamps) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrProfileNotFound, handle)
	}

	feed.Stamps = stamps
	return &ProfileResponse{Kind: KindStampFeed, StampFeed: feed}, nil
}

// queryStampFeed fetches the last 10 stamps for a handle from hop/wl-commons,
// joined with their completion evidence.
func queryStampFeed(reader RowQuerier, handle string) ([]StampFeedEntry, error) {
	sql := fmt.Sprintf(
		`SELECT s.id, s.skill_tags, s.valence, s.message, s.author, s.created_at, c.evidence `+
			`FROM stamps s LEFT JOIN completions c ON s.context_id = c.id `+
			`WHERE s.subject = '%s' `+
			`ORDER BY s.created_at DESC, s.id DESC LIMIT 10`,
		commons.EscapeSQL(handle))
	rows, err := reader.QueryRows(sql)
	if err != nil {
		return nil, fmt.Errorf("querying stamp feed: %w", err)
	}
	feed := make([]StampFeedEntry, 0, len(rows))
	for _, row := range rows {
		feed = append(feed, parseStampFeedRow(row))
	}
	return feed, nil
}

func parseStampFeedRow(row map[string]any) StampFeedEntry {
	id := toString(row["id"])
	tags := []string{}
	if raw := toString(row["skill_tags"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			slog.Warn("malformed stamp skill_tags", "id", id, "value", raw, "error", err)
			tags = []string{}
		}
	}
	var valence struct {
		Quality     int `json:"quality"`
		Reliability int `json:"reliability"`
	}
	if raw := toString(row["valence"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &valence); err != nil {
			slog.Warn("malformed stamp valence", "id", id, "value", raw, "error", err)
		}
	}
	eURL, label, text := parseEvidence(toString(row["evidence"]))
	return StampFeedEntry{
		ID:            id,
		SkillTags:     tags,
		Quality:       valence.Quality,
		Reliability:   valence.Reliability,
		Validator:     toString(row["author"]),
		Message:       toString(row["message"]),
		EvidenceURL:   eURL,
		EvidenceLabel: label,
		EvidenceText:  text,
		CreatedAt:     toString(row["created_at"]),
	}
}

// GitHub URL regexes anchored so the owner/repo/pull segments cannot be
// followed by trailing whitespace or free text that would turn the evidence
// URL into a broken link. Valid GitHub URLs end at the matched segment, a
// path separator, a query, or a fragment. Repo and owner captures exclude
// query/fragment/whitespace chars so a URL like
// "https://github.com/foo/bar?tab=readme" yields label "foo/bar", not the
// noisy "foo/bar?tab=readme".
var (
	githubPRRegex   = regexp.MustCompile(`^https?://github\.com/([^/?#\s]+)/([^/?#\s]+)/pull/(\d+)(?:$|[/?#])`)
	githubRepoRegex = regexp.MustCompile(`^https?://github\.com/([^/?#\s]+)/([^/?#\s]+)(?:$|[/?#])`)
)

// parseEvidence splits raw evidence text into (url, label, text).
//
//	GitHub PR URL      → url unchanged, label "owner/repo#N", text ""
//	Other GitHub URL   → url unchanged, label "owner/repo",   text ""
//	Other http(s) URL  → url unchanged, label "",             text ""
//	Non-URL text       → url "",        label "",             text raw
//	Empty              → all ""
//
// Any embedded whitespace (after leading/trailing trim) causes the value to
// be treated as free text rather than a URL, so prose that starts with a
// URL prefix cannot produce a broken clickable link.
func parseEvidence(raw string) (eURL, label, text string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", ""
	}
	if strings.ContainsAny(raw, " \t\n\r") {
		return "", "", raw
	}
	if m := githubPRRegex.FindStringSubmatch(raw); m != nil {
		return raw, fmt.Sprintf("%s/%s#%s", m[1], m[2], m[3]), ""
	}
	if m := githubRepoRegex.FindStringSubmatch(raw); m != nil {
		return raw, fmt.Sprintf("%s/%s", m[1], m[2]), ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
		return raw, "", ""
	}
	return "", "", raw
}

// ProfileSummary is a lightweight profile for search results.
type ProfileSummary struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
}

// RowQuerier is the interface needed for profile queries (QueryRows only).
type RowQuerier interface {
	QueryRows(sql string) ([]map[string]any, error)
}

// QueryProfile fetches a full developer profile from the-pile.
func QueryProfile(p RowQuerier, handle string) (*Profile, error) {
	// Query 1: boot_block for identity, sheet_json, confidence
	bbRows, err := p.QueryRows(fmt.Sprintf(
		"SELECT handle, source, sheet_json, confidence, created_at FROM boot_blocks WHERE handle = '%s' LIMIT 1",
		commons.EscapeSQL(handle)))
	if err != nil {
		return nil, fmt.Errorf("querying boot_block: %w", err)
	}
	if len(bbRows) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrProfileNotFound, handle)
	}

	row := bbRows[0]
	profile := &Profile{
		Handle:    toString(row["handle"]),
		Source:    toString(row["source"]),
		CreatedAt: toString(row["created_at"]),
	}

	if conf, ok := row["confidence"].(string); ok {
		if _, err := fmt.Sscanf(conf, "%f", &profile.Confidence); err != nil {
			slog.Warn("malformed confidence value", "handle", handle, "value", conf)
		}
	} else if conf, ok := row["confidence"].(float64); ok {
		profile.Confidence = conf
	}

	// Parse sheet_json
	sheetStr := toString(row["sheet_json"])
	if sheetStr != "" {
		if err := parseSheetJSON(sheetStr, profile); err != nil {
			slog.Warn("failed to parse sheet_json", "handle", handle, "error", err)
		}
	}

	// Query 2: stamps for skill evidence
	stampRows, err := p.QueryRows(fmt.Sprintf(
		"SELECT skill_tags, valence, confidence, message FROM stamps WHERE subject = '%s' ORDER BY confidence DESC",
		commons.EscapeSQL(handle)))
	if err != nil {
		return nil, fmt.Errorf("querying stamps: %w", err)
	}

	profile.AssessmentCount = len(stampRows)
	parseStamps(stampRows, profile)

	return profile, nil
}

// SearchProfiles searches for profiles matching a query string.
func SearchProfiles(p RowQuerier, query string, limit int) ([]ProfileSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	escaped := commons.EscapeSQL(escapeLIKE(query))
	rows, err := p.QueryRows(fmt.Sprintf(
		"SELECT handle, display_name FROM rigs WHERE handle LIKE '%%%s%%' OR display_name LIKE '%%%s%%' LIMIT %d",
		escaped, escaped, limit))
	if err != nil {
		return nil, fmt.Errorf("searching profiles: %w", err)
	}

	results := make([]ProfileSummary, 0, len(rows))
	for _, row := range rows {
		results = append(results, ProfileSummary{
			Handle:      toString(row["handle"]),
			DisplayName: toString(row["display_name"]),
		})
	}
	return results, nil
}

// parseSheetJSON extracts profile fields from the boot_block sheet_json.
func parseSheetJSON(raw string, profile *Profile) error {
	var sheet struct {
		Identity struct {
			DisplayName string  `json:"display_name"`
			Bio         *string `json:"bio"`
			Location    string  `json:"location"`
			GithubLogin string  `json:"github_login"`
			AccountAge  float64 `json:"account_age_years"`
			SocialProof struct {
				Followers int `json:"followers"`
			} `json:"social_proof"`
		} `json:"identity"`
		ValueDimensions struct {
			Quality     float64 `json:"quality"`
			Reliability float64 `json:"reliability"`
			Creativity  float64 `json:"creativity"`
		} `json:"value_dimensions"`
		ActivityProfile struct {
			TotalCommits int `json:"total_commits"`
			TotalPRs     int `json:"total_prs"`
		} `json:"activity_profile"`
		NotableProjects []struct {
			Name       string   `json:"name"`
			Stars      int      `json:"stars"`
			Languages  []string `json:"languages"`
			Role       string   `json:"role"`
			ImpactTier string   `json:"impact_tier"`
		} `json:"notable_projects"`
		Skills struct {
			PrimaryLanguages []struct {
				Language      string  `json:"language"`
				EvidenceScore float64 `json:"evidence_score"`
				Summary       string  `json:"evidence_summary"`
				Proficiency   string  `json:"proficiency_tier"`
			} `json:"primary_languages"`
			Domains []struct {
				Domain        string  `json:"domain"`
				EvidenceScore float64 `json:"evidence_score"`
				Summary       string  `json:"evidence_summary"`
			} `json:"domains"`
			Capabilities []struct {
				Capability    string  `json:"capability"`
				EvidenceScore float64 `json:"evidence_score"`
				Summary       string  `json:"evidence_summary"`
			} `json:"capabilities"`
		} `json:"skills"`
	}

	if err := json.Unmarshal([]byte(raw), &sheet); err != nil {
		return fmt.Errorf("unmarshal sheet_json: %w", err)
	}

	profile.DisplayName = sheet.Identity.DisplayName
	if sheet.Identity.Bio != nil {
		profile.Bio = *sheet.Identity.Bio
	}
	profile.Location = sheet.Identity.Location
	profile.AccountAge = sheet.Identity.AccountAge
	profile.Followers = sheet.Identity.SocialProof.Followers

	// Boot block value_dimensions are 0-1 scale; normalize to 0-5 stamp scale.
	profile.Quality = sheet.ValueDimensions.Quality * 5
	profile.Reliability = sheet.ValueDimensions.Reliability * 5
	profile.Creativity = sheet.ValueDimensions.Creativity * 5

	// Notable projects
	for _, np := range sheet.NotableProjects {
		profile.NotableProjects = append(profile.NotableProjects, Project{
			Name:       np.Name,
			Stars:      np.Stars,
			Languages:  np.Languages,
			Role:       np.Role,
			ImpactTier: np.ImpactTier,
		})
	}

	// Total stars from notable projects
	for _, np := range sheet.NotableProjects {
		profile.TotalStars += np.Stars
	}
	profile.TotalRepos = len(sheet.NotableProjects)
	return nil
}

// parseStamps categorizes stamps into languages, domains, and capabilities.
func parseStamps(rows []map[string]any, profile *Profile) {
	// Known programming languages for classification
	langSet := map[string]bool{
		"c": true, "c++": true, "go": true, "rust": true, "python": true,
		"javascript": true, "typescript": true, "java": true, "ruby": true,
		"shell": true, "assembly": true, "makefile": true, "openscad": true,
		"kotlin": true, "swift": true, "scala": true, "haskell": true,
		"perl": true, "php": true, "lua": true, "r": true, "dart": true,
		"elixir": true, "erlang": true, "clojure": true, "zig": true,
		"nim": true, "ocaml": true, "f#": true, "c#": true, "html": true,
		"css": true, "sql": true, "matlab": true, "julia": true,
	}

	for _, row := range rows {
		var tags []string
		tagsRaw := toString(row["skill_tags"])
		if tagsRaw != "" {
			_ = json.Unmarshal([]byte(tagsRaw), &tags)
		}
		if len(tags) == 0 {
			continue
		}

		var valence struct {
			Quality     int `json:"quality"`
			Reliability int `json:"reliability"`
			Creativity  int `json:"creativity"`
		}
		valenceRaw := toString(row["valence"])
		if valenceRaw != "" {
			_ = json.Unmarshal([]byte(valenceRaw), &valence)
		}

		var conf float64
		if c, ok := row["confidence"].(string); ok {
			if _, err := fmt.Sscanf(c, "%f", &conf); err != nil {
				slog.Warn("malformed stamp confidence", "value", c)
			}
		} else if c, ok := row["confidence"].(float64); ok {
			conf = c
		}

		msg := toString(row["message"])
		primaryTag := tags[0]

		entry := SkillEntry{
			Name:        primaryTag,
			Quality:     valence.Quality,
			Reliability: valence.Reliability,
			Creativity:  valence.Creativity,
			Confidence:  conf,
			Message:     msg,
		}

		// Classify: language, domain, or capability stamp.
		switch {
		case langSet[strings.ToLower(primaryTag)]:
			profile.Languages = append(profile.Languages, entry)
		case isDomainTag(primaryTag):
			profile.Domains = append(profile.Domains, entry)
		default:
			profile.Capabilities = append(profile.Capabilities, entry)
		}
	}
}

func isDomainTag(tag string) bool {
	domainPrefixes := []string{
		"operating-systems", "systems-programming", "audio-processing",
		"text-processing", "hardware-design", "web-development",
		"machine-learning", "data-engineering", "mobile-development",
		"devops", "security", "database", "networking", "cloud",
		"frontend", "backend", "fullstack", "game-development",
		"embedded", "blockchain", "ai", "ml", "infrastructure",
	}
	lower := strings.ToLower(tag)
	for _, prefix := range domainPrefixes {
		if lower == prefix || strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// escapeLIKE escapes SQL LIKE metacharacters so they match literally.
// Must be called before commons.EscapeSQL so the backslash escapes survive
// the string-literal layer (EscapeSQL doubles backslashes for MySQL/Dolt).
func escapeLIKE(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}
