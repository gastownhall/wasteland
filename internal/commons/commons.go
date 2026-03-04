// Package commons provides wl-commons (Wasteland) database operations.
//
// The wl-commons database is the shared wanted board for the Wasteland federation.
package commons

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// DB abstracts SQL execution against a dolt database.
// Implemented by backend.LocalDB and backend.RemoteDB.
type DB interface {
	// Query runs a read-only SQL SELECT.
	// ref: "" = working copy / HEAD, "branch-name", "remote/main".
	Query(sql, ref string) (string, error)

	// Exec runs DML statements and auto-commits on the given branch.
	// branch: "" = main, "name" = named branch (created from main if needed).
	Exec(branch, commitMsg string, signed bool, stmts ...string) error

	// Branches returns branch names matching prefix.
	Branches(prefix string) ([]string, error)

	// DeleteBranch removes a branch.
	DeleteBranch(name string) error

	// PushBranch pushes a branch to origin.
	PushBranch(branch string, stdout io.Writer) error

	// PushMain pushes main to origin.
	PushMain(stdout io.Writer) error

	// Sync pulls latest from upstream.
	Sync() error

	// MergeBranch merges a branch into main.
	MergeBranch(branch string) error

	// DeleteRemoteBranch removes a branch on the origin remote.
	DeleteRemoteBranch(branch string) error

	// PushWithSync pushes to both upstream and origin with sync retry.
	PushWithSync(stdout io.Writer) error

	// CanWildWest returns nil if the backend supports wild-west mode.
	CanWildWest() error
}

// WLCommonsStore abstracts wl-commons database operations.
type WLCommonsStore interface {
	InsertWanted(item *WantedItem) error
	ClaimWanted(wantedID, rigHandle string) error
	UnclaimWanted(wantedID string) error
	SubmitCompletion(completionID, wantedID, rigHandle, evidence string) error
	QueryWanted(wantedID string) (*WantedItem, error)
	QueryWantedDetail(wantedID string) (*WantedItem, error)
	QueryCompletion(wantedID string) (*CompletionRecord, error)
	QueryStamp(stampID string) (*Stamp, error)
	AcceptCompletion(wantedID, completionID, rigHandle string, stamp *Stamp) error
	RejectCompletion(wantedID, rigHandle, reason string) error
	CloseWanted(wantedID string) error
	UpdateWanted(wantedID string, fields *WantedUpdate) error
	DeleteWanted(wantedID string) error
}

// WLCommons implements WLCommonsStore using a DB backend.
type WLCommons struct {
	db     DB
	signed bool
	hopURI string
}

// NewWLCommons creates a WLCommonsStore backed by the given DB.
func NewWLCommons(db DB) *WLCommons { return &WLCommons{db: db} }

// SetSigning enables or disables GPG-signed Dolt commits.
func (w *WLCommons) SetSigning(enabled bool) { w.signed = enabled }

// SetHopURI sets the rig's HOP protocol URI for completions and stamps.
func (w *WLCommons) SetHopURI(uri string) { w.hopURI = uri }

// InsertWanted inserts a new wanted item.
func (w *WLCommons) InsertWanted(item *WantedItem) error {
	return InsertWanted(w.db, item, w.signed)
}

// ClaimWanted claims a wanted item for a rig.
func (w *WLCommons) ClaimWanted(wantedID, rigHandle string) error {
	return ClaimWanted(w.db, wantedID, rigHandle, w.signed)
}

// UnclaimWanted reverts a claimed wanted item to open.
func (w *WLCommons) UnclaimWanted(wantedID string) error {
	return UnclaimWanted(w.db, wantedID, w.signed)
}

// SubmitCompletion records completion evidence for a claimed wanted item.
func (w *WLCommons) SubmitCompletion(completionID, wantedID, rigHandle, evidence string) error {
	return SubmitCompletion(w.db, completionID, wantedID, rigHandle, evidence, w.hopURI, w.signed)
}

// QueryWanted fetches a wanted item by ID.
func (w *WLCommons) QueryWanted(wantedID string) (*WantedItem, error) {
	return QueryWanted(w.db, wantedID)
}

// QueryWantedDetail fetches a wanted item with all display fields.
func (w *WLCommons) QueryWantedDetail(wantedID string) (*WantedItem, error) {
	return QueryWantedDetail(w.db, wantedID)
}

// QueryCompletion fetches the completion record for a wanted item.
func (w *WLCommons) QueryCompletion(wantedID string) (*CompletionRecord, error) {
	return QueryCompletion(w.db, wantedID)
}

// QueryStamp fetches a stamp by ID.
func (w *WLCommons) QueryStamp(stampID string) (*Stamp, error) {
	return QueryStamp(w.db, stampID)
}

// AcceptCompletion validates a completion and creates a stamp.
func (w *WLCommons) AcceptCompletion(wantedID, completionID, rigHandle string, stamp *Stamp) error {
	return AcceptCompletion(w.db, wantedID, completionID, rigHandle, w.hopURI, stamp, w.signed)
}

// UpdateWanted updates mutable fields on an open wanted item.
func (w *WLCommons) UpdateWanted(wantedID string, fields *WantedUpdate) error {
	return UpdateWanted(w.db, wantedID, fields, w.signed)
}

// RejectCompletion reverts a wanted item from in_review to claimed.
func (w *WLCommons) RejectCompletion(wantedID, rigHandle, reason string) error {
	return RejectCompletion(w.db, wantedID, rigHandle, reason, w.signed)
}

// CloseWanted marks an in_review item as completed without a stamp.
func (w *WLCommons) CloseWanted(wantedID string) error {
	return CloseWanted(w.db, wantedID, w.signed)
}

// DeleteWanted soft-deletes a wanted item by setting status=withdrawn.
func (w *WLCommons) DeleteWanted(wantedID string) error {
	return DeleteWanted(w.db, wantedID, w.signed)
}

// WantedItem represents a row in the wanted table.
type WantedItem struct {
	ID              string
	Title           string
	Description     string
	Project         string
	Type            string
	Priority        int
	Tags            []string
	PostedBy        string
	ClaimedBy       string
	Status          string
	EffortLevel     string
	SandboxRequired bool
	CreatedAt       string
	UpdatedAt       string
}

// CompletionRecord represents a row in the completions table.
type CompletionRecord struct {
	ID          string
	WantedID    string
	CompletedBy string
	Evidence    string
	StampID     string
	ValidatedBy string
}

// Stamp represents a reputation stamp issued when accepting a completion.
type Stamp struct {
	ID          string
	Author      string
	Subject     string
	Quality     int
	Reliability int
	Severity    string
	ContextID   string
	ContextType string
	SkillTags   []string
	Message     string
}

// WantedUpdate holds the mutable fields for updating a wanted item.
// Zero-value fields are treated as "not set" and will not be updated.
// Priority uses -1 as "not set" since 0 is a valid priority.
type WantedUpdate struct {
	Title       string
	Description string
	Project     string
	Type        string
	Priority    int
	EffortLevel string
	Tags        []string
	TagsSet     bool // true if Tags was explicitly provided (even if empty)
}

// ConflictError indicates an optimistic concurrency conflict (e.g. item was
// already claimed or changed by another user). Mapped to HTTP 409 by the API.
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }

// isNothingToCommit returns true if the error indicates DOLT_COMMIT found no
// changes to commit.
func isNothingToCommit(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "nothing to commit")
}

// EscapeSQL escapes backslashes and single quotes for SQL string literals.
func EscapeSQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "'", "''")
}

// EscapeLIKE escapes SQL LIKE wildcards (% and _) in addition to standard
// SQL escaping. Use this when interpolating user input into LIKE patterns.
func EscapeLIKE(s string) string {
	s = EscapeSQL(s)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// CommitSQL returns the DOLT_COMMIT SQL statement, optionally with -S for GPG signing.
func CommitSQL(msg string, signed bool) string {
	if signed {
		return fmt.Sprintf("CALL DOLT_COMMIT('-S', '-m', '%s');\n", EscapeSQL(msg))
	}
	return fmt.Sprintf("CALL DOLT_COMMIT('-m', '%s');\n", EscapeSQL(msg))
}

// GenerateWantedID generates a unique wanted item ID in the format w-<10-char-hash>.
func GenerateWantedID(title string) string {
	randomBytes := make([]byte, 8)
	_, _ = rand.Read(randomBytes)

	input := fmt.Sprintf("%s:%d:%x", title, time.Now().UnixNano(), randomBytes)
	hash := sha256.Sum256([]byte(input))
	hashStr := hex.EncodeToString(hash[:])[:10]

	return fmt.Sprintf("w-%s", hashStr)
}

// GeneratePrefixedID generates a unique ID in the format <prefix>-<16 hex chars>
// from a SHA-256 hash of the inputs joined by "|" plus a timestamp.
func GeneratePrefixedID(prefix string, inputs ...string) string {
	now := time.Now().UTC().Format(time.RFC3339)
	data := strings.Join(inputs, "|") + "|" + now
	h := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%s-%x", prefix, h[:8])
}

// InsertWantedDML returns the pure DML for inserting a wanted item.
func InsertWantedDML(item *WantedItem) (string, error) {
	if item.ID == "" {
		return "", fmt.Errorf("wanted item ID cannot be empty")
	}
	if item.Title == "" {
		return "", fmt.Errorf("wanted item title cannot be empty")
	}

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	tagsJSON := formatTagsJSON(item.Tags)

	descField := "NULL"
	if item.Description != "" {
		descField = fmt.Sprintf("'%s'", EscapeSQL(item.Description))
	}
	projectField := "NULL"
	if item.Project != "" {
		projectField = fmt.Sprintf("'%s'", EscapeSQL(item.Project))
	}
	typeField := "NULL"
	if item.Type != "" {
		typeField = fmt.Sprintf("'%s'", EscapeSQL(item.Type))
	}
	postedByField := "NULL"
	if item.PostedBy != "" {
		postedByField = fmt.Sprintf("'%s'", EscapeSQL(item.PostedBy))
	}
	effortField := "'medium'"
	if item.EffortLevel != "" {
		effortField = fmt.Sprintf("'%s'", EscapeSQL(item.EffortLevel))
	}
	status := "'open'"
	if item.Status != "" {
		status = fmt.Sprintf("'%s'", EscapeSQL(item.Status))
	}

	return fmt.Sprintf(`INSERT INTO wanted (id, title, description, project, type, priority, tags, posted_by, status, effort_level, created_at, updated_at)
VALUES ('%s', '%s', %s, %s, %s, %d, %s, %s, %s, %s, '%s', '%s')`,
		EscapeSQL(item.ID), EscapeSQL(item.Title), descField, projectField, typeField,
		item.Priority, tagsJSON, postedByField, status, effortField,
		now, now), nil
}

// InsertWanted inserts a new wanted item using the given DB.
func InsertWanted(db DB, item *WantedItem, signed bool) error {
	dml, err := InsertWantedDML(item)
	if err != nil {
		return err
	}
	return db.Exec("", "wl post: "+item.Title, signed, dml)
}

// ClaimWantedDML returns the pure DML for claiming a wanted item.
func ClaimWantedDML(wantedID, rigHandle string) string {
	return fmt.Sprintf("UPDATE wanted SET claimed_by='%s', status='claimed', updated_at=NOW() WHERE id='%s' AND status='open'",
		EscapeSQL(rigHandle), EscapeSQL(wantedID))
}

// ClaimWanted updates a wanted item's status to claimed.
func ClaimWanted(db DB, wantedID, rigHandle string, signed bool) error {
	err := db.Exec("", "wl claim: "+wantedID, signed, ClaimWantedDML(wantedID, rigHandle))
	if err == nil {
		return nil
	}
	if isNothingToCommit(err) {
		return &ConflictError{Message: fmt.Sprintf("wanted item %q is not open or does not exist", wantedID)}
	}
	return fmt.Errorf("claim failed: %w", err)
}

// UnclaimWantedDML returns the pure DML for unclaiming a wanted item.
func UnclaimWantedDML(wantedID string) string {
	return fmt.Sprintf("UPDATE wanted SET claimed_by=NULL, status='open', updated_at=NOW() WHERE id='%s' AND status='claimed'",
		EscapeSQL(wantedID))
}

// UnclaimWanted reverts a claimed wanted item to open.
func UnclaimWanted(db DB, wantedID string, signed bool) error {
	err := db.Exec("", "wl unclaim: "+wantedID, signed, UnclaimWantedDML(wantedID))
	if err == nil {
		return nil
	}
	if isNothingToCommit(err) {
		return &ConflictError{Message: fmt.Sprintf("wanted item %q is not claimed or does not exist", wantedID)}
	}
	return fmt.Errorf("unclaim failed: %w", err)
}

// SubmitCompletionDML returns the pure DML statements for submitting a completion.
func SubmitCompletionDML(completionID, wantedID, rigHandle, evidence, hopURI string) []string {
	hopField := "NULL"
	if hopURI != "" {
		hopField = fmt.Sprintf("'%s'", EscapeSQL(hopURI))
	}

	update := fmt.Sprintf(`UPDATE wanted SET status='in_review', evidence_url='%s', updated_at=NOW() WHERE id='%s' AND status='claimed' AND claimed_by='%s'`,
		EscapeSQL(evidence), EscapeSQL(wantedID), EscapeSQL(rigHandle))

	insert := fmt.Sprintf(`INSERT IGNORE INTO completions (id, wanted_id, completed_by, evidence, hop_uri, completed_at) SELECT '%s', '%s', '%s', '%s', %s, NOW() FROM wanted WHERE id='%s' AND status='in_review' AND claimed_by='%s' AND NOT EXISTS (SELECT 1 FROM completions WHERE wanted_id='%s')`,
		EscapeSQL(completionID), EscapeSQL(wantedID), EscapeSQL(rigHandle), EscapeSQL(evidence),
		hopField,
		EscapeSQL(wantedID), EscapeSQL(rigHandle), EscapeSQL(wantedID))

	return []string{update, insert}
}

// SubmitCompletion inserts a completion record and updates the wanted status.
func SubmitCompletion(db DB, completionID, wantedID, rigHandle, evidence, hopURI string, signed bool) error {
	stmts := SubmitCompletionDML(completionID, wantedID, rigHandle, evidence, hopURI)
	err := db.Exec("", "wl done: "+wantedID, signed, stmts...)
	if err == nil {
		return nil
	}
	if isNothingToCommit(err) {
		return &ConflictError{Message: fmt.Sprintf("wanted item %q is not claimed by %q or does not exist", wantedID, rigHandle)}
	}
	return fmt.Errorf("completion failed: %w", err)
}

// QueryWanted fetches a wanted item by ID. Returns an error if not found.
func QueryWanted(db DB, wantedID string) (*WantedItem, error) {
	query := fmt.Sprintf(`SELECT id, title, status, COALESCE(claimed_by, '') as claimed_by, COALESCE(posted_by, '') as posted_by FROM wanted WHERE id='%s'`,
		EscapeSQL(wantedID))

	output, err := db.Query(query, "")
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	if len(rows) == 0 {
		return nil, fmt.Errorf("wanted item %q not found", wantedID)
	}

	row := rows[0]
	item := &WantedItem{
		ID:        row["id"],
		Title:     row["title"],
		Status:    row["status"],
		ClaimedBy: row["claimed_by"],
		PostedBy:  row["posted_by"],
	}
	return item, nil
}

// parseSimpleCSV parses CSV output from dolt sql into a slice of maps.
func parseSimpleCSV(data string) []map[string]string {
	lines := strings.Split(strings.TrimSpace(data), "\n")
	if len(lines) < 2 {
		return nil
	}

	headers := parseCSVLine(lines[0])
	var result []map[string]string

	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		fields := parseCSVLine(line)
		row := make(map[string]string)
		for i, h := range headers {
			if i < len(fields) {
				row[strings.TrimSpace(h)] = strings.TrimSpace(fields[i])
			}
		}
		result = append(result, row)
	}
	return result
}

// parseCSVLine parses a single CSV line, handling quoted fields.
func parseCSVLine(line string) []string {
	var fields []string
	var field strings.Builder
	inQuote := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '"' && !inQuote:
			inQuote = true
		case ch == '"' && inQuote:
			if i+1 < len(line) && line[i+1] == '"' {
				field.WriteByte('"')
				i++
			} else {
				inQuote = false
			}
		case ch == ',' && !inQuote:
			fields = append(fields, field.String())
			field.Reset()
		default:
			field.WriteByte(ch)
		}
	}
	fields = append(fields, field.String())
	return fields
}

// QueryCompletion fetches the completion record for a wanted item.
func QueryCompletion(db DB, wantedID string) (*CompletionRecord, error) {
	return queryCompletionRef(db, wantedID, "")
}

// QueryCompletionAsOf fetches the completion record for a wanted item from a specific ref.
func QueryCompletionAsOf(db DB, wantedID, ref string) (*CompletionRecord, error) {
	return queryCompletionRef(db, wantedID, ref)
}

func queryCompletionRef(db DB, wantedID, ref string) (*CompletionRecord, error) {
	query := fmt.Sprintf(`SELECT id, wanted_id, completed_by, COALESCE(evidence, '') as evidence, COALESCE(stamp_id, '') as stamp_id, COALESCE(validated_by, '') as validated_by FROM completions WHERE wanted_id='%s'`,
		EscapeSQL(wantedID))

	output, err := db.Query(query, ref)
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	if len(rows) == 0 {
		if ref != "" {
			return nil, fmt.Errorf("no completion found for wanted item %q on ref %s", wantedID, ref)
		}
		return nil, fmt.Errorf("no completion found for wanted item %q", wantedID)
	}

	row := rows[0]
	return &CompletionRecord{
		ID:          row["id"],
		WantedID:    row["wanted_id"],
		CompletedBy: row["completed_by"],
		Evidence:    row["evidence"],
		StampID:     row["stamp_id"],
		ValidatedBy: row["validated_by"],
	}, nil
}

// QueryStampAsOf fetches a stamp by ID from a specific ref.
func QueryStampAsOf(db DB, stampID, ref string) (*Stamp, error) {
	return queryStampRef(db, stampID, ref)
}

// QueryWantedDetail fetches a wanted item with all display fields.
func QueryWantedDetail(db DB, wantedID string) (*WantedItem, error) {
	return queryWantedDetailRef(db, wantedID, "")
}

// QueryWantedDetailAsOf fetches a wanted item from a specific branch/ref.
func QueryWantedDetailAsOf(db DB, wantedID, ref string) (*WantedItem, error) {
	return queryWantedDetailRef(db, wantedID, ref)
}

func queryWantedDetailRef(db DB, wantedID, ref string) (*WantedItem, error) {
	query := fmt.Sprintf(`SELECT id, title, COALESCE(description,'') as description, COALESCE(project,'') as project, COALESCE(type,'') as type, priority, COALESCE(tags,'') as tags, COALESCE(posted_by,'') as posted_by, COALESCE(claimed_by,'') as claimed_by, status, COALESCE(effort_level,'medium') as effort_level, COALESCE(created_at,'') as created_at, COALESCE(updated_at,'') as updated_at FROM wanted WHERE id='%s'`,
		EscapeSQL(wantedID))

	output, err := db.Query(query, ref)
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	if len(rows) == 0 {
		if ref != "" {
			return nil, fmt.Errorf("wanted item %q not found on ref %s", wantedID, ref)
		}
		return nil, fmt.Errorf("wanted item %q not found", wantedID)
	}

	row := rows[0]
	priority, _ := strconv.Atoi(row["priority"])

	return &WantedItem{
		ID:          row["id"],
		Title:       row["title"],
		Description: row["description"],
		Project:     row["project"],
		Type:        row["type"],
		Priority:    priority,
		Tags:        parseTagsJSON(row["tags"]),
		PostedBy:    row["posted_by"],
		ClaimedBy:   row["claimed_by"],
		Status:      row["status"],
		EffortLevel: row["effort_level"],
		CreatedAt:   row["created_at"],
		UpdatedAt:   row["updated_at"],
	}, nil
}

// QueryStamp fetches a stamp by ID.
func QueryStamp(db DB, stampID string) (*Stamp, error) {
	return queryStampRef(db, stampID, "")
}

func queryStampRef(db DB, stampID, ref string) (*Stamp, error) {
	query := fmt.Sprintf(`SELECT id, author, subject, valence, severity, COALESCE(context_id,'') as context_id, COALESCE(context_type,'') as context_type, COALESCE(skill_tags,'') as skill_tags, COALESCE(message,'') as message FROM stamps WHERE id='%s'`,
		EscapeSQL(stampID))

	output, err := db.Query(query, ref)
	if err != nil {
		return nil, err
	}

	rows := parseSimpleCSV(output)
	if len(rows) == 0 {
		if ref != "" {
			return nil, fmt.Errorf("stamp %q not found on ref %s", stampID, ref)
		}
		return nil, fmt.Errorf("stamp %q not found", stampID)
	}

	row := rows[0]

	var valence struct {
		Quality     int `json:"quality"`
		Reliability int `json:"reliability"`
	}
	if v := row["valence"]; v != "" {
		_ = json.Unmarshal([]byte(v), &valence)
	}

	return &Stamp{
		ID:          row["id"],
		Author:      row["author"],
		Subject:     row["subject"],
		Quality:     valence.Quality,
		Reliability: valence.Reliability,
		Severity:    row["severity"],
		ContextID:   row["context_id"],
		ContextType: row["context_type"],
		SkillTags:   parseTagsJSON(row["skill_tags"]),
		Message:     row["message"],
	}, nil
}

// parseTagsJSON parses a JSON array string like `["go","auth"]` into a string slice.
func parseTagsJSON(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "NULL" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(s), &tags); err != nil {
		return nil
	}
	return tags
}

// AcceptCompletionDML returns the pure DML statements for accepting a completion.
func AcceptCompletionDML(wantedID, completionID, rigHandle, hopURI string, stamp *Stamp) []string {
	tagsField := formatTagsJSON(stamp.SkillTags)

	msgField := "NULL"
	if stamp.Message != "" {
		msgField = fmt.Sprintf("'%s'", EscapeSQL(stamp.Message))
	}

	hopField := "NULL"
	if hopURI != "" {
		hopField = fmt.Sprintf("'%s'", EscapeSQL(hopURI))
	}

	valence := fmt.Sprintf(`{"quality": %d, "reliability": %d}`, stamp.Quality, stamp.Reliability)

	insertStamp := fmt.Sprintf(`INSERT INTO stamps (id, author, subject, valence, confidence, severity, context_id, context_type, skill_tags, message, hop_uri, created_at) VALUES ('%s', '%s', '%s', '%s', 1.0, '%s', '%s', 'completion', %s, %s, %s, NOW())`,
		EscapeSQL(stamp.ID), EscapeSQL(rigHandle), EscapeSQL(stamp.Subject),
		EscapeSQL(valence), EscapeSQL(stamp.Severity),
		EscapeSQL(completionID), tagsField, msgField, hopField)

	updateCompletion := fmt.Sprintf(`UPDATE completions SET validated_by='%s', stamp_id='%s', validated_at=NOW() WHERE id='%s'`,
		EscapeSQL(rigHandle), EscapeSQL(stamp.ID), EscapeSQL(completionID))

	updateWanted := fmt.Sprintf(`UPDATE wanted SET status='completed', updated_at=NOW() WHERE id='%s' AND status='in_review'`,
		EscapeSQL(wantedID))

	return []string{insertStamp, updateCompletion, updateWanted}
}

// AcceptCompletion validates a completion, creates a stamp, and marks the item completed.
func AcceptCompletion(db DB, wantedID, completionID, rigHandle, hopURI string, stamp *Stamp, signed bool) error {
	stmts := AcceptCompletionDML(wantedID, completionID, rigHandle, hopURI, stamp)
	err := db.Exec("", "wl accept: "+wantedID, signed, stmts...)
	if err == nil {
		return nil
	}
	if isNothingToCommit(err) {
		return &ConflictError{Message: fmt.Sprintf("wanted item %q is not in_review or does not exist", wantedID)}
	}
	return fmt.Errorf("accept failed: %w", err)
}

// UpdateWantedDML returns the pure DML for updating a wanted item.
func UpdateWantedDML(wantedID string, fields *WantedUpdate) (string, error) {
	var setClauses []string

	if fields.Title != "" {
		setClauses = append(setClauses, fmt.Sprintf("title='%s'", EscapeSQL(fields.Title)))
	}
	if fields.Description != "" {
		setClauses = append(setClauses, fmt.Sprintf("description='%s'", EscapeSQL(fields.Description)))
	}
	if fields.Project != "" {
		setClauses = append(setClauses, fmt.Sprintf("project='%s'", EscapeSQL(fields.Project)))
	}
	if fields.Type != "" {
		setClauses = append(setClauses, fmt.Sprintf("type='%s'", EscapeSQL(fields.Type)))
	}
	if fields.Priority >= 0 {
		setClauses = append(setClauses, fmt.Sprintf("priority=%d", fields.Priority))
	}
	if fields.EffortLevel != "" {
		setClauses = append(setClauses, fmt.Sprintf("effort_level='%s'", EscapeSQL(fields.EffortLevel)))
	}
	if fields.TagsSet {
		setClauses = append(setClauses, fmt.Sprintf("tags=%s", formatTagsJSON(fields.Tags)))
	}

	if len(setClauses) == 0 {
		return "", fmt.Errorf("no fields to update")
	}

	setClauses = append(setClauses, "updated_at=NOW()")

	return fmt.Sprintf("UPDATE wanted SET %s WHERE id='%s' AND status='open'",
		strings.Join(setClauses, ", "), EscapeSQL(wantedID)), nil
}

// UpdateWanted updates mutable fields on an open wanted item.
func UpdateWanted(db DB, wantedID string, fields *WantedUpdate, signed bool) error {
	dml, err := UpdateWantedDML(wantedID, fields)
	if err != nil {
		return err
	}

	err = db.Exec("", "wl update: "+wantedID, signed, dml)
	if err == nil {
		return nil
	}
	if isNothingToCommit(err) {
		return &ConflictError{Message: fmt.Sprintf("wanted item %q is not open or does not exist", wantedID)}
	}
	return fmt.Errorf("update failed: %w", err)
}

// CloseWantedDML returns the pure DML for closing a wanted item.
func CloseWantedDML(wantedID string) string {
	return fmt.Sprintf("UPDATE wanted SET status='completed', updated_at=NOW() WHERE id='%s' AND status='in_review'",
		EscapeSQL(wantedID))
}

// CloseWanted marks an in_review wanted item as completed without a stamp.
func CloseWanted(db DB, wantedID string, signed bool) error {
	err := db.Exec("", "wl close: "+wantedID, signed, CloseWantedDML(wantedID))
	if err == nil {
		return nil
	}
	if isNothingToCommit(err) {
		return &ConflictError{Message: fmt.Sprintf("wanted item %q is not in_review or does not exist", wantedID)}
	}
	return fmt.Errorf("close failed: %w", err)
}

// formatTagsJSON formats a string slice as a JSON array SQL literal.
func formatTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "NULL"
	}
	escaped := make([]string, len(tags))
	for i, t := range tags {
		t = strings.ReplaceAll(t, `\`, `\\`)
		t = strings.ReplaceAll(t, `"`, `\"`)
		escaped[i] = t
	}
	jsonStr := fmt.Sprintf(`["%s"]`, strings.Join(escaped, `","`))
	return fmt.Sprintf("'%s'", strings.ReplaceAll(jsonStr, "'", "''"))
}

// DeleteWantedDML returns the pure DML for soft-deleting a wanted item.
func DeleteWantedDML(wantedID string) string {
	return fmt.Sprintf("UPDATE wanted SET status='withdrawn', updated_at=NOW() WHERE id='%s' AND status='open'",
		EscapeSQL(wantedID))
}

// DeleteWanted soft-deletes a wanted item by setting status=withdrawn.
func DeleteWanted(db DB, wantedID string, signed bool) error {
	err := db.Exec("", "wl delete: "+wantedID, signed, DeleteWantedDML(wantedID))
	if err == nil {
		return nil
	}
	if isNothingToCommit(err) {
		return &ConflictError{Message: fmt.Sprintf("wanted item %q is not open or does not exist", wantedID)}
	}
	return fmt.Errorf("delete failed: %w", err)
}

// RejectCompletionDML returns the pure DML statements for rejecting a completion.
func RejectCompletionDML(wantedID string) []string {
	return []string{
		fmt.Sprintf("DELETE FROM completions WHERE wanted_id='%s'", EscapeSQL(wantedID)),
		fmt.Sprintf("UPDATE wanted SET status='claimed', updated_at=NOW() WHERE id='%s' AND status='in_review'", EscapeSQL(wantedID)),
	}
}

// RejectCompletion reverts a wanted item from in_review to claimed.
func RejectCompletion(db DB, wantedID, _, reason string, signed bool) error {
	commitMsg := fmt.Sprintf("wl reject: %s", wantedID)
	if reason != "" {
		commitMsg += " — " + reason
	}

	stmts := RejectCompletionDML(wantedID)
	err := db.Exec("", commitMsg, signed, stmts...)
	if err == nil {
		return nil
	}
	if isNothingToCommit(err) {
		return &ConflictError{Message: fmt.Sprintf("wanted item %q is not in_review or does not exist", wantedID)}
	}
	return fmt.Errorf("reject failed: %w", err)
}
