package commons

import (
	"fmt"
	"strconv"
)

// RigRow is a public-safe row from the rigs table.
type RigRow struct {
	Handle       string `json:"handle"`
	DisplayName  string `json:"display_name,omitempty"`
	DolthubOrg   string `json:"dolthub_org,omitempty"`
	TrustLevel   int    `json:"trust_level"`
	TrustTier    string `json:"trust_tier"`
	RegisteredAt string `json:"registered_at,omitempty"`
	LastSeen     string `json:"last_seen,omitempty"`
	RigType      string `json:"rig_type,omitempty"`
	ParentRig    string `json:"parent_rig,omitempty"`
}

// StampRow is a public-safe row from the stamps table.
type StampRow struct {
	ID          string  `json:"id"`
	Author      string  `json:"author"`
	Subject     string  `json:"subject"`
	Valence     string  `json:"valence"`
	Confidence  float64 `json:"confidence"`
	Severity    string  `json:"severity"`
	ContextID   string  `json:"context_id,omitempty"`
	ContextType string  `json:"context_type,omitempty"`
	SkillTags   string  `json:"skill_tags,omitempty"`
	Message     string  `json:"message,omitempty"`
	CreatedAt   string  `json:"created_at,omitempty"`
}

// CompletionRow is a public-safe row from the completions table.
type CompletionRow struct {
	ID          string `json:"id"`
	WantedID    string `json:"wanted_id"`
	CompletedBy string `json:"completed_by"`
	Evidence    string `json:"evidence,omitempty"`
	ValidatedBy string `json:"validated_by,omitempty"`
	StampID     string `json:"stamp_id,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	ValidatedAt string `json:"validated_at,omitempty"`
}

// WantedRow is a public-safe row from the wanted table.
type WantedRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Project     string `json:"project,omitempty"`
	Type        string `json:"type,omitempty"`
	Priority    int    `json:"priority"`
	Tags        string `json:"tags,omitempty"`
	PostedBy    string `json:"posted_by,omitempty"`
	ClaimedBy   string `json:"claimed_by,omitempty"`
	Status      string `json:"status"`
	EffortLevel string `json:"effort_level,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// BadgeRow is a public-safe row from the badges table.
type BadgeRow struct {
	ID        string `json:"id"`
	RigHandle string `json:"rig_handle"`
	BadgeType string `json:"badge_type"`
	AwardedAt string `json:"awarded_at,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
}

// ScoreboardDump holds flat arrays of all public tables.
type ScoreboardDump struct {
	Rigs        []RigRow        `json:"rigs"`
	Stamps      []StampRow      `json:"stamps"`
	Completions []CompletionRow `json:"completions"`
	Wanted      []WantedRow     `json:"wanted"`
	Badges      []BadgeRow      `json:"badges"`
}

// QueryScoreboardDump returns all tables as flat arrays, omitting sensitive columns.
func QueryScoreboardDump(db DB) (*ScoreboardDump, error) {
	dump := &ScoreboardDump{}

	var err error
	if dump.Rigs, err = queryDumpRigs(db); err != nil {
		return nil, fmt.Errorf("dumping rigs: %w", err)
	}
	if dump.Stamps, err = queryDumpStamps(db); err != nil {
		return nil, fmt.Errorf("dumping stamps: %w", err)
	}
	if dump.Completions, err = queryDumpCompletions(db); err != nil {
		return nil, fmt.Errorf("dumping completions: %w", err)
	}
	if dump.Wanted, err = queryDumpWanted(db); err != nil {
		return nil, fmt.Errorf("dumping wanted: %w", err)
	}
	if dump.Badges, err = queryDumpBadges(db); err != nil {
		return nil, fmt.Errorf("dumping badges: %w", err)
	}

	// Derive trust_tier for each rig from stamp weighted scores.
	populateDumpTrustTiers(dump)

	return dump, nil
}

// populateDumpTrustTiers computes weighted scores from stamps and sets
// TrustTier on each RigRow, ensuring the dump uses the same tier labels
// as the scoreboard API.
func populateDumpTrustTiers(dump *ScoreboardDump) {
	scores := make(map[string]int)
	for _, s := range dump.Stamps {
		weight := 0
		switch s.Severity {
		case "root":
			weight = 5
		case "branch":
			weight = 3
		case "leaf":
			weight = 1
		}
		scores[s.Subject] += weight
	}
	for i := range dump.Rigs {
		dump.Rigs[i].TrustTier = DeriveTrustTier(scores[dump.Rigs[i].Handle])
	}
}

func queryDumpRigs(db DB) ([]RigRow, error) {
	query := `SELECT handle, COALESCE(display_name,'') AS display_name, COALESCE(dolthub_org,'') AS dolthub_org,
  COALESCE(trust_level,0) AS trust_level, COALESCE(registered_at,'') AS registered_at,
  COALESCE(last_seen,'') AS last_seen, COALESCE(rig_type,'') AS rig_type, COALESCE(parent_rig,'') AS parent_rig
FROM rigs ORDER BY handle`

	output, err := db.Query(query, "")
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	result := make([]RigRow, 0, len(rows))
	for _, r := range rows {
		tl, err := strconv.Atoi(r["trust_level"])
		if err != nil && r["trust_level"] != "" && r["trust_level"] != "0" {
			return nil, fmt.Errorf("parsing trust_level for %q: %w", r["handle"], err)
		}
		result = append(result, RigRow{
			Handle:       r["handle"],
			DisplayName:  r["display_name"],
			DolthubOrg:   r["dolthub_org"],
			TrustLevel:   tl,
			RegisteredAt: r["registered_at"],
			LastSeen:     r["last_seen"],
			RigType:      r["rig_type"],
			ParentRig:    r["parent_rig"],
		})
	}
	return result, nil
}

func queryDumpStamps(db DB) ([]StampRow, error) {
	query := `SELECT id, author, subject, COALESCE(valence,'{}') AS valence, COALESCE(confidence,1) AS confidence,
  COALESCE(severity,'leaf') AS severity, COALESCE(context_id,'') AS context_id,
  COALESCE(context_type,'') AS context_type, COALESCE(skill_tags,'') AS skill_tags,
  COALESCE(message,'') AS message, COALESCE(created_at,'') AS created_at
FROM stamps ORDER BY created_at DESC`

	output, err := db.Query(query, "")
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	result := make([]StampRow, 0, len(rows))
	for _, r := range rows {
		conf, err := strconv.ParseFloat(r["confidence"], 64)
		if err != nil && r["confidence"] != "" {
			return nil, fmt.Errorf("parsing confidence for stamp %q: %w", r["id"], err)
		}
		result = append(result, StampRow{
			ID:          r["id"],
			Author:      r["author"],
			Subject:     r["subject"],
			Valence:     r["valence"],
			Confidence:  conf,
			Severity:    r["severity"],
			ContextID:   r["context_id"],
			ContextType: r["context_type"],
			SkillTags:   r["skill_tags"],
			Message:     r["message"],
			CreatedAt:   r["created_at"],
		})
	}
	return result, nil
}

func queryDumpCompletions(db DB) ([]CompletionRow, error) {
	query := `SELECT id, COALESCE(wanted_id,'') AS wanted_id, COALESCE(completed_by,'') AS completed_by,
  COALESCE(evidence,'') AS evidence, COALESCE(validated_by,'') AS validated_by,
  COALESCE(stamp_id,'') AS stamp_id, COALESCE(completed_at,'') AS completed_at,
  COALESCE(validated_at,'') AS validated_at
FROM completions ORDER BY completed_at DESC`

	output, err := db.Query(query, "")
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	result := make([]CompletionRow, 0, len(rows))
	for _, r := range rows {
		result = append(result, CompletionRow{
			ID:          r["id"],
			WantedID:    r["wanted_id"],
			CompletedBy: r["completed_by"],
			Evidence:    r["evidence"],
			ValidatedBy: r["validated_by"],
			StampID:     r["stamp_id"],
			CompletedAt: r["completed_at"],
			ValidatedAt: r["validated_at"],
		})
	}
	return result, nil
}

func queryDumpWanted(db DB) ([]WantedRow, error) {
	query := `SELECT id, COALESCE(title,'') AS title, COALESCE(description,'') AS description,
  COALESCE(project,'') AS project, COALESCE(type,'') AS type, COALESCE(priority,2) AS priority,
  COALESCE(tags,'') AS tags, COALESCE(posted_by,'') AS posted_by, COALESCE(claimed_by,'') AS claimed_by,
  COALESCE(status,'open') AS status, COALESCE(effort_level,'medium') AS effort_level,
  COALESCE(created_at,'') AS created_at, COALESCE(updated_at,'') AS updated_at
FROM wanted ORDER BY created_at DESC`

	output, err := db.Query(query, "")
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	result := make([]WantedRow, 0, len(rows))
	for _, r := range rows {
		p, err := strconv.Atoi(r["priority"])
		if err != nil && r["priority"] != "" && r["priority"] != "0" {
			return nil, fmt.Errorf("parsing priority for wanted %q: %w", r["id"], err)
		}
		result = append(result, WantedRow{
			ID:          r["id"],
			Title:       r["title"],
			Description: r["description"],
			Project:     r["project"],
			Type:        r["type"],
			Priority:    p,
			Tags:        r["tags"],
			PostedBy:    r["posted_by"],
			ClaimedBy:   r["claimed_by"],
			Status:      r["status"],
			EffortLevel: r["effort_level"],
			CreatedAt:   r["created_at"],
			UpdatedAt:   r["updated_at"],
		})
	}
	return result, nil
}

func queryDumpBadges(db DB) ([]BadgeRow, error) {
	query := `SELECT id, COALESCE(rig_handle,'') AS rig_handle, COALESCE(badge_type,'') AS badge_type,
  COALESCE(awarded_at,'') AS awarded_at, COALESCE(evidence,'') AS evidence
FROM badges ORDER BY awarded_at DESC`

	output, err := db.Query(query, "")
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	result := make([]BadgeRow, 0, len(rows))
	for _, r := range rows {
		result = append(result, BadgeRow{
			ID:        r["id"],
			RigHandle: r["rig_handle"],
			BadgeType: r["badge_type"],
			AwardedAt: r["awarded_at"],
			Evidence:  r["evidence"],
		})
	}
	return result, nil
}
