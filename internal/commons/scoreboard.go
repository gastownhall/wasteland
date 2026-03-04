package commons

import (
	"fmt"
	"strconv"
	"strings"
)

// maxScoreboardLimit is the hard ceiling for scoreboard queries.
const maxScoreboardLimit = 100

// ScoreboardEntry holds aggregated stats for one rig on the public scoreboard.
type ScoreboardEntry struct {
	RigHandle     string
	DisplayName   string
	StampCount    int
	WeightedScore int
	UniqueTowns   int
	Completions   int
	AvgQuality    float64
	AvgReliab     float64
	TopSkills     []string
	TrustTier     string
}

// DeriveTrustTier returns a trust tier label based on weighted score.
func DeriveTrustTier(weightedScore int) string {
	switch {
	case weightedScore >= 50:
		return "imperator"
	case weightedScore >= 25:
		return "warrior"
	case weightedScore >= 10:
		return "settler"
	case weightedScore >= 3:
		return "scavenger"
	default:
		return "drifter"
	}
}

// QueryScoreboard aggregates stamps into a ranked public scoreboard.
// Rigs are ranked by weighted_score DESC.
func QueryScoreboard(db DB, limit int) ([]ScoreboardEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > maxScoreboardLimit {
		limit = maxScoreboardLimit
	}

	// 1. Stamp aggregates: count, weighted score, unique towns, avg quality/reliability.
	stampQuery := fmt.Sprintf(`SELECT
  s.subject,
  COUNT(*) AS stamp_count,
  SUM(CASE s.severity WHEN 'root' THEN 5 WHEN 'branch' THEN 3 WHEN 'leaf' THEN 1 ELSE 0 END) AS weighted_score,
  COUNT(DISTINCT s.author) AS unique_towns,
  COALESCE(AVG(JSON_EXTRACT(s.valence, '$.quality')), 0) AS avg_quality,
  COALESCE(AVG(JSON_EXTRACT(s.valence, '$.reliability')), 0) AS avg_reliability
FROM stamps s
GROUP BY s.subject
ORDER BY weighted_score DESC, stamp_count DESC, s.subject ASC
LIMIT %d`, limit)

	output, err := db.Query(stampQuery, "")
	if err != nil {
		return nil, fmt.Errorf("querying scoreboard stamps: %w", err)
	}

	rows := parseSimpleCSV(output)
	if len(rows) == 0 {
		return nil, nil
	}

	entries := make([]ScoreboardEntry, 0, len(rows))
	for _, row := range rows {
		stampCount, err := strconv.Atoi(row["stamp_count"])
		if err != nil {
			return nil, fmt.Errorf("parsing stamp_count for %q: %w", row["subject"], err)
		}
		weightedScore, err := strconv.Atoi(row["weighted_score"])
		if err != nil {
			return nil, fmt.Errorf("parsing weighted_score for %q: %w", row["subject"], err)
		}
		uniqueTowns, err := strconv.Atoi(row["unique_towns"])
		if err != nil {
			return nil, fmt.Errorf("parsing unique_towns for %q: %w", row["subject"], err)
		}
		avgQ, err := strconv.ParseFloat(row["avg_quality"], 64)
		if err != nil {
			return nil, fmt.Errorf("parsing avg_quality for %q: %w", row["subject"], err)
		}
		avgR, err := strconv.ParseFloat(row["avg_reliability"], 64)
		if err != nil {
			return nil, fmt.Errorf("parsing avg_reliability for %q: %w", row["subject"], err)
		}

		entries = append(entries, ScoreboardEntry{
			RigHandle:     row["subject"],
			StampCount:    stampCount,
			WeightedScore: weightedScore,
			UniqueTowns:   uniqueTowns,
			AvgQuality:    avgQ,
			AvgReliab:     avgR,
			TrustTier:     DeriveTrustTier(weightedScore),
		})
	}

	// 2. Validated completions per rig.
	if err := populateScoreboardCompletions(db, entries); err != nil {
		return nil, fmt.Errorf("querying scoreboard completions: %w", err)
	}

	// 3. Skill tags bulk fetch.
	if err := populateScoreboardSkills(db, entries); err != nil {
		return nil, fmt.Errorf("querying scoreboard skills: %w", err)
	}

	// 4. Display names from rigs table.
	if err := populateScoreboardDisplayNames(db, entries); err != nil {
		return nil, fmt.Errorf("querying scoreboard display names: %w", err)
	}

	return entries, nil
}

// populateScoreboardCompletions fetches validated completion counts per rig.
func populateScoreboardCompletions(db DB, entries []ScoreboardEntry) error {
	if len(entries) == 0 {
		return nil
	}

	handles := make([]string, len(entries))
	for i, e := range entries {
		handles[i] = fmt.Sprintf("'%s'", EscapeSQL(e.RigHandle))
	}

	query := fmt.Sprintf(`SELECT completed_by, COUNT(*) AS completions
FROM completions
WHERE stamp_id IS NOT NULL AND completed_by IN (%s)
GROUP BY completed_by`, strings.Join(handles, ","))

	output, err := db.Query(query, "")
	if err != nil {
		return err
	}

	rows := parseSimpleCSV(output)
	counts := make(map[string]int)
	for _, row := range rows {
		c, _ := strconv.Atoi(row["completions"])
		counts[row["completed_by"]] = c
	}

	for i := range entries {
		entries[i].Completions = counts[entries[i].RigHandle]
	}
	return nil
}

// populateScoreboardSkills fetches skill tags for all rigs from stamps.
func populateScoreboardSkills(db DB, entries []ScoreboardEntry) error {
	if len(entries) == 0 {
		return nil
	}

	handles := make([]string, len(entries))
	for i, e := range entries {
		handles[i] = fmt.Sprintf("'%s'", EscapeSQL(e.RigHandle))
	}

	query := fmt.Sprintf(`SELECT s.subject, s.skill_tags
FROM stamps s
WHERE s.subject IN (%s) AND s.skill_tags IS NOT NULL AND s.skill_tags != ''
ORDER BY s.subject`, strings.Join(handles, ","))

	output, err := db.Query(query, "")
	if err != nil {
		return err
	}

	rows := parseSimpleCSV(output)

	perRig := make(map[string]map[string]int)
	for _, row := range rows {
		rig := row["subject"]
		tags := parseTagsJSON(row["skill_tags"])
		if len(tags) == 0 {
			continue
		}
		if perRig[rig] == nil {
			perRig[rig] = make(map[string]int)
		}
		for _, tag := range tags {
			perRig[rig][strings.ToLower(tag)]++
		}
	}

	for i := range entries {
		entries[i].TopSkills = topNKeys(perRig[entries[i].RigHandle], 5)
	}
	return nil
}

// populateScoreboardDisplayNames fetches display names from the rigs table.
func populateScoreboardDisplayNames(db DB, entries []ScoreboardEntry) error {
	if len(entries) == 0 {
		return nil
	}

	handles := make([]string, len(entries))
	for i, e := range entries {
		handles[i] = fmt.Sprintf("'%s'", EscapeSQL(e.RigHandle))
	}

	query := fmt.Sprintf(`SELECT handle, COALESCE(display_name, '') AS display_name
FROM rigs
WHERE handle IN (%s)`, strings.Join(handles, ","))

	output, err := db.Query(query, "")
	if err != nil {
		return err
	}

	rows := parseSimpleCSV(output)
	names := make(map[string]string)
	for _, row := range rows {
		names[row["handle"]] = row["display_name"]
	}

	for i := range entries {
		entries[i].DisplayName = names[entries[i].RigHandle]
	}
	return nil
}
