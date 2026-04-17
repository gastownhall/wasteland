// Package dolthubdouble provides an in-process HTTP double for the DoltHub
// API, used by the e2e test harness to exercise remote-facing code paths
// without depending on dolthub.com.
package dolthubdouble

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/schema"
)

const schemaCommitMessage = "Initialize wl-commons schema v1.0"

// RequestLog captures a single HTTP request observed by the double.
type RequestLog struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// RepoRef identifies a DoltHub repository by owner/db.
type RepoRef struct {
	Owner string `json:"owner"`
	DB    string `json:"db"`
}

// BranchSeed describes a branch to create during Seed, optionally forked
// from an existing branch and populated with SQL statements.
type BranchSeed struct {
	Name string   `json:"name"`
	From string   `json:"from,omitempty"`
	SQL  []string `json:"sql,omitempty"`
}

// RepositorySeed describes a repository to create during Seed, including
// any fork relationship, initial main-branch SQL, and additional branches.
type RepositorySeed struct {
	Owner    string       `json:"owner"`
	DB       string       `json:"db"`
	ForkOf   *RepoRef     `json:"fork_of,omitempty"`
	MainSQL  []string     `json:"main_sql,omitempty"`
	Branches []BranchSeed `json:"branches,omitempty"`
}

// PRSeed describes a pull request to create during Seed.
type PRSeed struct {
	ID            string `json:"id,omitempty"`
	UpstreamOwner string `json:"upstream_owner"`
	UpstreamDB    string `json:"upstream_db"`
	FromOwner     string `json:"from_owner"`
	FromDB        string `json:"from_db,omitempty"`
	FromBranch    string `json:"from_branch"`
	Author        string `json:"author,omitempty"`
	State         string `json:"state,omitempty"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
}

// SeedRequest is the JSON body accepted by the double's seed endpoint.
type SeedRequest struct {
	Repositories []RepositorySeed `json:"repositories,omitempty"`
	PRs          []PRSeed         `json:"prs,omitempty"`
}

// RepositorySnapshot captures a point-in-time view of a repository's
// branches and pull requests.
type RepositorySnapshot struct {
	Owner       string                    `json:"owner"`
	DB          string                    `json:"db"`
	Branches    map[string]BranchSnapshot `json:"branches"`
	PullRequest []PullRequestSnapshot     `json:"pull_requests,omitempty"`
}

// BranchSnapshot captures the row contents of each well-known table on
// a single branch.
type BranchSnapshot struct {
	Wanted      []map[string]string `json:"wanted,omitempty"`
	Completions []map[string]string `json:"completions,omitempty"`
	Stamps      []map[string]string `json:"stamps,omitempty"`
	Rigs        []map[string]string `json:"rigs,omitempty"`
}

// PullRequestSnapshot captures a single pull request's state for test
// assertions.
type PullRequestSnapshot struct {
	ID              string `json:"id"`
	UpstreamOwner   string `json:"upstream_owner"`
	UpstreamDB      string `json:"upstream_db"`
	FromBranchOwner string `json:"from_branch_owner"`
	FromBranchRepo  string `json:"from_branch_repo"`
	FromBranch      string `json:"from_branch"`
	Author          string `json:"author"`
	State           string `json:"state"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	URL             string `json:"url"`
}

// Snapshot is the aggregate of observed requests, repository state, and
// pull request state returned by the double's snapshot endpoint.
type Snapshot struct {
	Requests     []RequestLog          `json:"requests"`
	Repositories []RepositorySnapshot  `json:"repositories"`
	PullRequests []PullRequestSnapshot `json:"pull_requests"`
}

// Server is an in-process HTTP double for DoltHub's REST and SQL APIs.
type Server struct {
	root string

	mu       sync.Mutex
	repos    map[string]*repository
	requests []RequestLog
	prs      map[string]*pullRequest
	nextPRID int
}

type repository struct {
	owner string
	db    string
	dir   string
	mu    sync.Mutex
}

type pullRequest struct {
	ID              string
	UpstreamOwner   string
	UpstreamDB      string
	FromBranchOwner string
	FromBranchRepo  string
	FromBranch      string
	Author          string
	State           string
	Title           string
	Description     string
}

// New returns a Server that stores repository state under root. If root
// is empty, a temporary directory is allocated.
func New(root string) (*Server, error) {
	if root == "" {
		tmp, err := os.MkdirTemp("", "wasteland-dolthub-double-*")
		if err != nil {
			return nil, fmt.Errorf("create temp root: %w", err)
		}
		root = tmp
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir: %w", err)
	}
	return &Server{
		root:     root,
		repos:    make(map[string]*repository),
		prs:      make(map[string]*pullRequest),
		nextPRID: 1,
	}, nil
}

// Close removes the server's root directory and any repository state
// underneath it.
func (s *Server) Close() error {
	return os.RemoveAll(s.root)
}

// Reset removes all repositories, pull requests, and request logs from
// the server while leaving the root directory in place.
func (s *Server) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, repo := range s.repos {
		if err := os.RemoveAll(repo.dir); err != nil {
			return fmt.Errorf("remove repo %s/%s: %w", repo.owner, repo.db, err)
		}
	}
	s.repos = make(map[string]*repository)
	s.requests = nil
	s.prs = make(map[string]*pullRequest)
	s.nextPRID = 1
	return nil
}

// Seed applies a SeedRequest: creating repositories, branches, and pull
// requests as described.
func (s *Server) Seed(req SeedRequest) error {
	for _, repoSeed := range req.Repositories {
		if _, err := s.ensureRepository(repoSeed); err != nil {
			return err
		}
	}
	for _, prSeed := range req.PRs {
		if _, err := s.addPR(prSeed); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot returns a point-in-time view of observed requests, repository
// contents, and pull-request state. baseURL is used to compose PR URLs.
func (s *Server) Snapshot(baseURL string) (Snapshot, error) {
	s.mu.Lock()
	requests := append([]RequestLog(nil), s.requests...)
	repoKeys := make([]string, 0, len(s.repos))
	for key := range s.repos {
		repoKeys = append(repoKeys, key)
	}
	slices.Sort(repoKeys)
	prs := make([]*pullRequest, 0, len(s.prs))
	for _, pr := range s.prs {
		clone := *pr
		prs = append(prs, &clone)
	}
	s.mu.Unlock()

	repositories := make([]RepositorySnapshot, 0, len(repoKeys))
	for _, key := range repoKeys {
		repo, ok := s.repositoryByKey(key)
		if !ok {
			continue
		}
		snap, err := repo.snapshot()
		if err != nil {
			return Snapshot{}, err
		}
		snap.PullRequest = pullRequestsForRepo(prs, repo.owner, repo.db, baseURL)
		repositories = append(repositories, snap)
	}

	pullSnapshots := make([]PullRequestSnapshot, 0, len(prs))
	for _, pr := range prs {
		pullSnapshots = append(pullSnapshots, pr.snapshot(baseURL))
	}
	slices.SortFunc(pullSnapshots, func(a, b PullRequestSnapshot) int {
		return strings.Compare(a.ID, b.ID)
	})

	return Snapshot{
		Requests:     requests,
		Repositories: repositories,
		PullRequests: pullSnapshots,
	}, nil
}

// MergePR merges the specified PR by copying changed rows from the source
// branch into the upstream repo's main branch, mirroring DoltHub's
// server-side merge behavior for the double's test contract.
func (s *Server) MergePR(prID string) error {
	s.mu.Lock()
	pr, ok := s.prs[prID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown PR %s", prID)
	}
	clone := *pr
	s.mu.Unlock()

	src, ok := s.repositoryByOwnerDB(clone.FromBranchOwner, clone.FromBranchRepo)
	if !ok {
		return fmt.Errorf("source repo %s/%s not found", clone.FromBranchOwner, clone.FromBranchRepo)
	}
	dest, ok := s.repositoryByOwnerDB(clone.UpstreamOwner, clone.UpstreamDB)
	if !ok {
		return fmt.Errorf("destination repo %s/%s not found", clone.UpstreamOwner, clone.UpstreamDB)
	}

	wantedRows, err := src.queryRows(clone.FromBranch, fmt.Sprintf(
		"SELECT COALESCE(to_id, from_id) AS id, diff_type FROM dolt_diff('main', '%s', 'wanted') ORDER BY COALESCE(to_id, from_id)",
		escapeSQL(clone.FromBranch),
	))
	if err != nil {
		return fmt.Errorf("query changed wanted rows for PR %s: %w", prID, err)
	}
	for _, row := range wantedRows {
		wantedID := row["id"]
		if wantedID == "" {
			continue
		}
		if row["diff_type"] == "removed" {
			if err := dest.execSQL("main", "main", []string{
				fmt.Sprintf("DELETE FROM completions WHERE wanted_id='%s'", escapeSQL(wantedID)),
				fmt.Sprintf("DELETE FROM wanted WHERE id='%s'", escapeSQL(wantedID)),
			}); err != nil {
				return fmt.Errorf("delete removed wanted %s: %w", wantedID, err)
			}
			continue
		}
		if err := copyWantedState(src, dest, clone.FromBranch, wantedID); err != nil {
			return fmt.Errorf("copy wanted state for %s: %w", wantedID, err)
		}
	}

	s.mu.Lock()
	if existing := s.prs[prID]; existing != nil {
		existing.State = "closed"
	}
	s.mu.Unlock()
	return nil
}

// Handler returns an http.Handler serving the double's emulated
// DoltHub REST and SQL endpoints.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.recordRequest(r)
		switch {
		case strings.HasPrefix(r.URL.Path, "/__dolthub/api/v1alpha1/"):
			s.handleAPI(w, r)
		case strings.HasPrefix(r.URL.Path, "/__dolthub/repositories/"):
			s.handleRepositoryURL(w, r)
		case r.URL.Path == "/__dolthub/graphql":
			http.Error(w, "graphql not implemented", http.StatusNotImplemented)
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *Server) handleRepositoryURL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"url": r.URL.String(),
	})
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/__dolthub/api/v1alpha1/")
	parts := strings.Split(path, "/")
	for i := range parts {
		value, err := url.PathUnescape(parts[i])
		if err == nil {
			parts[i] = value
		}
	}

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	if parts[0] == "fork" {
		http.Error(w, "fork not implemented", http.StatusNotImplemented)
		return
	}
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}

	owner := parts[0]
	db := parts[1]

	switch parts[2] {
	case "pulls":
		s.handlePulls(w, r, owner, db, parts[3:])
	case "write":
		s.handleWrite(w, r, owner, db, parts[3:])
	default:
		s.handleQuery(w, r, owner, db, strings.Join(parts[2:], "/"))
	}
}

func (s *Server) handlePulls(w http.ResponseWriter, r *http.Request, owner, db string, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		pulls := s.listPRs(owner, db)
		writeJSON(w, map[string]any{"pulls": pulls})
	case len(rest) == 0 && r.Method == http.MethodPost:
		var req struct {
			Title           string `json:"title"`
			Description     string `json:"description"`
			FromBranchOwner string `json:"fromBranchOwnerName"`
			FromBranchRepo  string `json:"fromBranchRepoName"`
			FromBranchName  string `json:"fromBranchName"`
			ToBranchOwner   string `json:"toBranchOwnerName"`
			ToBranchRepo    string `json:"toBranchRepoName"`
			ToBranchName    string `json:"toBranchName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		pr, err := s.addPR(PRSeed{
			UpstreamOwner: owner,
			UpstreamDB:    db,
			FromOwner:     req.FromBranchOwner,
			FromDB:        req.FromBranchRepo,
			FromBranch:    req.FromBranchName,
			Author:        req.FromBranchOwner,
			State:         "open",
			Title:         req.Title,
			Description:   req.Description,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"_id": pr.ID, "status": pr.State})
	case len(rest) == 1 && r.Method == http.MethodGet:
		pr, ok := s.getPR(rest[0])
		if !ok || pr.UpstreamOwner != owner || pr.UpstreamDB != db {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"from_branch":       pr.FromBranch,
			"from_branch_owner": pr.FromBranchOwner,
			"author":            pr.Author,
		})
	case len(rest) == 1 && r.Method == http.MethodPatch:
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := s.patchPR(owner, db, rest[0], req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleWrite(w http.ResponseWriter, r *http.Request, owner, db string, rest []string) {
	if len(rest) == 0 && r.Method == http.MethodGet {
		writeJSON(w, map[string]any{
			"done": true,
			"res_details": map[string]string{
				"query_execution_status":  "Success",
				"query_execution_message": "",
			},
		})
		return
	}
	if len(rest) != 2 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	repo, ok := s.repositoryByOwnerDB(owner, db)
	if !ok {
		http.Error(w, "repository not found", http.StatusNotFound)
		return
	}
	sqlQuery := r.URL.Query().Get("q")
	if strings.TrimSpace(sqlQuery) == "" {
		http.Error(w, "missing q parameter", http.StatusBadRequest)
		return
	}

	if err := repo.execWrite(rest[0], rest[1], sqlQuery); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{
		"query_execution_status":  "Success",
		"query_execution_message": "",
	})
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request, owner, db, ref string) {
	repo, ok := s.repositoryByOwnerDB(owner, db)
	if !ok {
		http.Error(w, "repository not found", http.StatusNotFound)
		return
	}

	sqlQuery := r.URL.Query().Get("q")
	if strings.TrimSpace(sqlQuery) == "" {
		if err := repo.ensureRef(ref); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{
			"query_execution_status": "Success",
			"repository_owner":       owner,
			"repository_name":        db,
			"commit_ref":             ref,
		})
		return
	}

	csvData, err := repo.queryCSV(ref, sqlQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	response, err := csvToQueryResponse(owner, db, ref, sqlQuery, csvData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, response)
}

func (s *Server) recordRequest(r *http.Request) {
	headers := make(map[string]string)
	for key, values := range r.Header {
		headers[strings.ToLower(key)] = strings.Join(values, ", ")
	}
	var body string
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		r.Body = io.NopCloser(strings.NewReader(body))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, RequestLog{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.RawQuery,
		Headers: headers,
		Body:    body,
	})
}

func (s *Server) ensureRepository(seed RepositorySeed) (*repository, error) {
	if seed.Owner == "" || seed.DB == "" {
		return nil, fmt.Errorf("repository seed requires owner and db")
	}
	if repo, ok := s.repositoryByOwnerDB(seed.Owner, seed.DB); ok {
		if len(seed.MainSQL) > 0 {
			if err := repo.execSQL("main", "main", seed.MainSQL); err != nil {
				return nil, err
			}
		}
		for _, branch := range seed.Branches {
			from := branch.From
			if from == "" {
				from = "main"
			}
			if err := repo.execSQL(branch.Name, from, branch.SQL); err != nil {
				return nil, err
			}
		}
		return repo, nil
	}

	repoDir := filepath.Join(s.root, seed.Owner, seed.DB)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return nil, fmt.Errorf("create repo parent dir: %w", err)
	}

	if seed.ForkOf != nil {
		src, ok := s.repositoryByOwnerDB(seed.ForkOf.Owner, seed.ForkOf.DB)
		if !ok {
			return nil, fmt.Errorf("fork source %s/%s not found", seed.ForkOf.Owner, seed.ForkOf.DB)
		}
		if err := copyDir(src.dir, repoDir); err != nil {
			return nil, fmt.Errorf("copy fork source: %w", err)
		}
	} else {
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			return nil, fmt.Errorf("create repo dir: %w", err)
		}
		if err := initRepo(repoDir); err != nil {
			return nil, err
		}
	}

	repo := &repository{
		owner: seed.Owner,
		db:    seed.DB,
		dir:   repoDir,
	}
	s.mu.Lock()
	s.repos[repoKey(seed.Owner, seed.DB)] = repo
	s.mu.Unlock()

	if len(seed.MainSQL) > 0 {
		if err := repo.execSQL("main", "main", seed.MainSQL); err != nil {
			return nil, err
		}
	}
	for _, branch := range seed.Branches {
		from := branch.From
		if from == "" {
			from = "main"
		}
		if err := repo.execSQL(branch.Name, from, branch.SQL); err != nil {
			return nil, err
		}
	}
	return repo, nil
}

func (s *Server) addPR(seed PRSeed) (*pullRequest, error) {
	if seed.UpstreamOwner == "" || seed.UpstreamDB == "" || seed.FromOwner == "" || seed.FromBranch == "" {
		return nil, fmt.Errorf("PR seed requires upstream owner/db, from owner, and from branch")
	}
	if seed.FromDB == "" {
		seed.FromDB = seed.UpstreamDB
	}
	if _, ok := s.repositoryByOwnerDB(seed.UpstreamOwner, seed.UpstreamDB); !ok {
		return nil, fmt.Errorf("upstream repo %s/%s not found", seed.UpstreamOwner, seed.UpstreamDB)
	}
	if _, ok := s.repositoryByOwnerDB(seed.FromOwner, seed.FromDB); !ok {
		return nil, fmt.Errorf("source repo %s/%s not found", seed.FromOwner, seed.FromDB)
	}
	if seed.State == "" {
		seed.State = "open"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	id := seed.ID
	if id == "" {
		id = fmt.Sprintf("%d", s.nextPRID)
		s.nextPRID++
	}
	pr := &pullRequest{
		ID:              id,
		UpstreamOwner:   seed.UpstreamOwner,
		UpstreamDB:      seed.UpstreamDB,
		FromBranchOwner: seed.FromOwner,
		FromBranchRepo:  seed.FromDB,
		FromBranch:      seed.FromBranch,
		Author:          fallback(seed.Author, seed.FromOwner),
		State:           seed.State,
		Title:           seed.Title,
		Description:     seed.Description,
	}
	s.prs[id] = pr
	return pr, nil
}

func (s *Server) patchPR(owner, db, prID string, req map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.prs[prID]
	if pr == nil || pr.UpstreamOwner != owner || pr.UpstreamDB != db {
		return fmt.Errorf("PR not found")
	}
	if title, ok := req["title"]; ok {
		pr.Title = title
	}
	if desc, ok := req["description"]; ok {
		pr.Description = desc
	}
	if state, ok := req["state"]; ok {
		pr.State = state
	}
	return nil
}

func (s *Server) listPRs(owner, db string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	prs := make([]map[string]any, 0)
	for _, pr := range s.prs {
		if pr.UpstreamOwner == owner && pr.UpstreamDB == db {
			prs = append(prs, map[string]any{
				"pull_id": pr.ID,
				"state":   pr.State,
			})
		}
	}
	slices.SortFunc(prs, func(a, b map[string]any) int {
		return strings.Compare(fmt.Sprint(a["pull_id"]), fmt.Sprint(b["pull_id"]))
	})
	return prs
}

func (s *Server) getPR(prID string) (*pullRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pr, ok := s.prs[prID]
	if !ok {
		return nil, false
	}
	clone := *pr
	return &clone, true
}

func (s *Server) repositoryByKey(key string) (*repository, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.repos[key]
	return repo, ok
}

func (s *Server) repositoryByOwnerDB(owner, db string) (*repository, bool) {
	return s.repositoryByKey(repoKey(owner, db))
}

func pullRequestsForRepo(prs []*pullRequest, owner, db, baseURL string) []PullRequestSnapshot {
	rows := make([]PullRequestSnapshot, 0)
	for _, pr := range prs {
		if pr.UpstreamOwner == owner && pr.UpstreamDB == db {
			rows = append(rows, pr.snapshot(baseURL))
		}
	}
	slices.SortFunc(rows, func(a, b PullRequestSnapshot) int {
		return strings.Compare(a.ID, b.ID)
	})
	return rows
}

func (pr *pullRequest) snapshot(baseURL string) PullRequestSnapshot {
	return PullRequestSnapshot{
		ID:              pr.ID,
		UpstreamOwner:   pr.UpstreamOwner,
		UpstreamDB:      pr.UpstreamDB,
		FromBranchOwner: pr.FromBranchOwner,
		FromBranchRepo:  pr.FromBranchRepo,
		FromBranch:      pr.FromBranch,
		Author:          pr.Author,
		State:           pr.State,
		Title:           pr.Title,
		Description:     pr.Description,
		URL:             fmt.Sprintf("%s/__dolthub/repositories/%s/%s/pulls/%s", strings.TrimRight(baseURL, "/"), pr.UpstreamOwner, pr.UpstreamDB, pr.ID),
	}
}

func (r *repository) ensureRef(ref string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ref == "" || ref == "main" {
		return nil
	}
	exists, err := commons.BranchExists(r.dir, ref)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("ref %s not found", ref)
	}
	return nil
}

func (r *repository) queryCSV(ref, sqlQuery string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := checkoutRef(r.dir, ref); err != nil {
		return "", err
	}
	defer func() { _ = commons.CheckoutMain(r.dir) }()
	return commons.DoltSQLQuery(r.dir, sqlQuery)
}

func (r *repository) queryRows(ref, sqlQuery string) ([]map[string]string, error) {
	csvData, err := r.queryCSV(ref, sqlQuery)
	if err != nil {
		return nil, err
	}
	_, rows, err := parseCSV(csvData)
	return rows, err
}

func (r *repository) execSQL(branch, fromBranch string, stmts []string) error {
	if len(stmts) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if branch == "" {
		branch = "main"
	}
	if fromBranch == "" {
		fromBranch = "main"
	}
	if err := checkoutOrCreate(r.dir, branch, fromBranch); err != nil {
		return err
	}
	defer func() { _ = commons.CheckoutMain(r.dir) }()

	script := strings.Join(trimStatements(stmts), "\n") + "\n"
	script += "CALL DOLT_ADD('-A');\n"
	script += fmt.Sprintf("CALL DOLT_COMMIT('-m', '%s');\n", escapeSQL(commitMessage(branch)))
	return commons.DoltSQLScript(r.dir, script)
}

func (r *repository) execWrite(fromBranch, toBranch, sqlQuery string) error {
	if branch, ok := parseMergeBranch(sqlQuery); ok {
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := commons.CheckoutMain(r.dir); err != nil {
			return err
		}
		return commons.MergeBranch(r.dir, branch)
	}
	if branch, ok := parseDeleteBranch(sqlQuery); ok {
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := commons.CheckoutMain(r.dir); err != nil {
			return err
		}
		return commons.DeleteBranch(r.dir, branch)
	}
	err := r.execSQL(toBranch, fromBranch, []string{sqlQuery})
	if err != nil && tolerateNoopDelete(sqlQuery, err) {
		return nil
	}
	return err
}

func (r *repository) snapshot() (RepositorySnapshot, error) {
	branches, err := r.branchNames()
	if err != nil {
		return RepositorySnapshot{}, err
	}
	data := make(map[string]BranchSnapshot, len(branches))
	for _, branch := range branches {
		snap, err := r.branchSnapshot(branch)
		if err != nil {
			return RepositorySnapshot{}, err
		}
		data[branch] = snap
	}
	return RepositorySnapshot{
		Owner:    r.owner,
		DB:       r.db,
		Branches: data,
	}, nil
}

func (r *repository) branchNames() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := commons.CheckoutMain(r.dir); err != nil {
		return nil, err
	}
	branches, err := commons.ListBranches(r.dir, "")
	if err != nil {
		return nil, err
	}
	foundMain := false
	for _, branch := range branches {
		if branch == "main" {
			foundMain = true
			break
		}
	}
	if !foundMain {
		branches = append([]string{"main"}, branches...)
	}
	slices.Sort(branches)
	return branches, nil
}

func (r *repository) branchSnapshot(branch string) (BranchSnapshot, error) {
	wanted, err := r.queryRows(branch, "SELECT id, title, COALESCE(description,'') AS description, COALESCE(project,'') AS project, COALESCE(type,'') AS type, priority, COALESCE(tags,'') AS tags, COALESCE(posted_by,'') AS posted_by, COALESCE(claimed_by,'') AS claimed_by, status, COALESCE(effort_level,'medium') AS effort_level FROM wanted ORDER BY id")
	if err != nil {
		return BranchSnapshot{}, err
	}
	completions, err := r.queryRows(branch, "SELECT id, wanted_id, completed_by, COALESCE(evidence,'') AS evidence, COALESCE(validated_by,'') AS validated_by, COALESCE(stamp_id,'') AS stamp_id FROM completions ORDER BY id")
	if err != nil {
		return BranchSnapshot{}, err
	}
	stamps, err := r.queryRows(branch, "SELECT id, author, subject, valence, confidence, severity, COALESCE(context_id,'') AS context_id, COALESCE(context_type,'') AS context_type, COALESCE(skill_tags,'') AS skill_tags, COALESCE(message,'') AS message FROM stamps ORDER BY id")
	if err != nil {
		return BranchSnapshot{}, err
	}
	rigs, err := r.queryRows(branch, "SELECT handle, COALESCE(display_name,'') AS display_name, COALESCE(dolthub_org,'') AS dolthub_org FROM rigs ORDER BY handle")
	if err != nil {
		return BranchSnapshot{}, err
	}
	return BranchSnapshot{
		Wanted:      wanted,
		Completions: completions,
		Stamps:      stamps,
		Rigs:        rigs,
	}, nil
}

func initRepo(dir string) error {
	if err := runDolt(dir, "init", "-b", "main"); err != nil {
		return fmt.Errorf("init repo: %w", err)
	}
	script := schema.SQL +
		"INSERT IGNORE INTO _meta (`key`, value) VALUES ('wasteland_name', 'E2E Wasteland');\n" +
		"CALL DOLT_ADD('-A');\n" +
		fmt.Sprintf("CALL DOLT_COMMIT('--allow-empty', '-m', '%s');\n", schemaCommitMessage)
	if err := commons.DoltSQLScript(dir, script); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func copyWantedState(src, dest *repository, branch, wantedID string) error {
	wantedRows, err := src.queryRows(branch, fmt.Sprintf("SELECT * FROM wanted WHERE id='%s'", escapeSQL(wantedID)))
	if err != nil {
		return err
	}
	completionRows, err := src.queryRows(branch, fmt.Sprintf("SELECT * FROM completions WHERE wanted_id='%s'", escapeSQL(wantedID)))
	if err != nil {
		return err
	}
	stmts := []string{
		fmt.Sprintf("DELETE FROM completions WHERE wanted_id='%s'", escapeSQL(wantedID)),
		fmt.Sprintf("DELETE FROM wanted WHERE id='%s'", escapeSQL(wantedID)),
	}
	if len(wantedRows) > 0 {
		stmts = append(stmts, insertStatement("wanted", wantedRows[0]))
	}
	if len(completionRows) > 0 {
		completion := completionRows[0]
		if stampID := completion["stamp_id"]; stampID != "" {
			stampRows, err := src.queryRows(branch, fmt.Sprintf("SELECT * FROM stamps WHERE id='%s'", escapeSQL(stampID)))
			if err != nil {
				return err
			}
			stmts = append(stmts, fmt.Sprintf("DELETE FROM stamps WHERE id='%s'", escapeSQL(stampID)))
			if len(stampRows) > 0 {
				stmts = append(stmts, insertStatement("stamps", stampRows[0]))
			}
		}
		stmts = append(stmts, insertStatement("completions", completion))
	}
	return dest.execSQL("main", "main", stmts)
}

func csvToQueryResponse(owner, db, ref, sqlQuery, csvData string) (map[string]any, error) {
	headers, rows, err := parseCSV(csvData)
	if err != nil {
		return nil, err
	}
	schemaFragment := make([]map[string]string, 0, len(headers))
	for _, header := range headers {
		schemaFragment = append(schemaFragment, map[string]string{
			"columnName": header,
			"columnType": "text",
		})
	}
	rowObjects := make([]map[string]string, 0, len(rows))
	rowObjects = append(rowObjects, rows...)
	return map[string]any{
		"query_execution_status":  "Success",
		"query_execution_message": "",
		"repository_owner":        owner,
		"repository_name":         db,
		"commit_ref":              ref,
		"sql_query":               sqlQuery,
		"schema_fragment":         schemaFragment,
		"rows":                    rowObjects,
	}, nil
}

func parseCSV(csvData string) ([]string, []map[string]string, error) {
	csvData = strings.TrimSpace(csvData)
	if csvData == "" {
		return nil, nil, nil
	}
	reader := csv.NewReader(strings.NewReader(csvData))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(records) == 0 {
		return nil, nil, nil
	}
	headers := records[0]
	rows := make([]map[string]string, 0, max(len(records)-1, 0))
	for _, record := range records[1:] {
		row := make(map[string]string, len(headers))
		for i, header := range headers {
			if i < len(record) {
				row[header] = record[i]
			} else {
				row[header] = ""
			}
		}
		rows = append(rows, row)
	}
	return headers, rows, nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func repoKey(owner, db string) string {
	return owner + "/" + db
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

func trimStatements(stmts []string) []string {
	out := make([]string, 0, len(stmts))
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		stmt = strings.TrimRight(stmt, ";")
		out = append(out, stmt+";")
	}
	return out
}

func commitMessage(branch string) string {
	return "e2e write " + branch
}

func checkoutRef(dir, ref string) error {
	if ref == "" || ref == "main" {
		return commons.CheckoutMain(dir)
	}
	exists, err := commons.BranchExists(dir, ref)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("branch %s not found", ref)
	}
	return runDolt(dir, "checkout", ref)
}

func checkoutOrCreate(dir, branch, from string) error {
	if branch == "main" {
		return commons.CheckoutMain(dir)
	}
	return commons.CheckoutBranchFrom(dir, branch, from)
}

func runDolt(dir string, args ...string) error {
	return runCommand(dir, "dolt", args...)
}

func runCommand(dir, name string, args ...string) error {
	cmd := execCommand(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

var execCommand = func(name string, args ...string) commandRunner {
	return commandRunner{cmd: name, args: args}
}

type commandRunner struct {
	cmd  string
	args []string
	Dir  string
}

func (c commandRunner) CombinedOutput() ([]byte, error) {
	cmd := exec.Command(c.cmd, c.args...)
	cmd.Dir = c.Dir
	return cmd.CombinedOutput()
}

func parseMergeBranch(sqlQuery string) (string, bool) {
	prefix := "CALL DOLT_MERGE('"
	trimmed := strings.TrimSpace(sqlQuery)
	if !strings.HasPrefix(strings.ToUpper(trimmed), strings.ToUpper(prefix)) {
		return "", false
	}
	return parseSingleQuotedArg(trimmed)
}

func parseDeleteBranch(sqlQuery string) (string, bool) {
	prefix := "CALL DOLT_BRANCH('-D', '"
	trimmed := strings.TrimSpace(sqlQuery)
	if !strings.HasPrefix(strings.ToUpper(trimmed), strings.ToUpper(prefix)) {
		return "", false
	}
	idx := strings.Index(trimmed, "', '")
	if idx < 0 {
		return "", false
	}
	start := idx + len("', '")
	end := strings.LastIndex(trimmed, "')")
	if end <= start {
		return "", false
	}
	return trimmed[start:end], true
}

func tolerateNoopDelete(sqlQuery string, err error) bool {
	trimmed := strings.TrimSpace(strings.ToUpper(sqlQuery))
	return strings.HasPrefix(trimmed, "DELETE ") && strings.Contains(strings.ToLower(err.Error()), "nothing to commit")
}

func parseSingleQuotedArg(sqlQuery string) (string, bool) {
	start := strings.Index(sqlQuery, "('")
	end := strings.LastIndex(sqlQuery, "')")
	if start < 0 || end <= start+2 {
		return "", false
	}
	return sqlQuery[start+2 : end], true
}

func insertStatement(table string, row map[string]string) string {
	columns := make([]string, 0, len(row))
	for col := range row {
		columns = append(columns, col)
	}
	slices.Sort(columns)
	values := make([]string, 0, len(columns))
	for _, col := range columns {
		values = append(values, columnSQLValue(table, col, row[col]))
	}
	return fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		table,
		strings.Join(columns, ", "),
		strings.Join(values, ", "),
	)
}

func columnSQLValue(table, column, value string) string {
	if value == "" && isJSONColumn(table, column) {
		return "NULL"
	}
	return quoteSQL(value)
}

func isJSONColumn(table, column string) bool {
	switch table {
	case "wanted":
		return column == "tags" || column == "sandbox_scope"
	case "stamps":
		return column == "valence" || column == "skill_tags"
	case "boot_blocks":
		return column == "sheet_json"
	}
	return false
}

func quoteSQL(value string) string {
	if value == "" {
		return "''"
	}
	return fmt.Sprintf("'%s'", escapeSQL(value))
}

func escapeSQL(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func copyDir(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
