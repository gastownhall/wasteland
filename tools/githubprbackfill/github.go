package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type githubClient struct {
	token        string
	httpClient   *http.Client
	cache        *githubCache
	cachePath    string
	pageSize     int
	requestDelay time.Duration
	mergedAfter  *time.Time
	parallelism  int
	maxRetries   int
}

type ghUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type ghBaseRef struct {
	Ref string `json:"ref"`
}

type ghLabel struct {
	Name string `json:"name"`
}

type ghPull struct {
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	HTMLURL      string     `json:"html_url"`
	User         *ghUser    `json:"user"`
	Base         ghBaseRef  `json:"base"`
	CreatedAt    time.Time  `json:"created_at"`
	MergedAt     *time.Time `json:"merged_at"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changed_files"`
}

type ghIssue struct {
	Labels []ghLabel `json:"labels"`
}

type ghFile struct {
	Filename  string `json:"filename"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
}

type ghCommit struct {
	Author    *ghUser `json:"author"`
	Committer *ghUser `json:"committer"`
	Commit    struct {
		Message string `json:"message"`
		Author  struct {
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

type ghReview struct {
	User        *ghUser    `json:"user"`
	State       string     `json:"state"`
	Body        string     `json:"body"`
	SubmittedAt *time.Time `json:"submitted_at"`
}

type ghComment struct {
	User      *ghUser   `json:"user"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type fetchedPR struct {
	Repo           string
	Pull           ghPull
	Issue          ghIssue
	Files          []ghFile
	Commits        []ghCommit
	Reviews        []ghReview
	IssueComments  []ghComment
	ReviewComments []ghComment
}

func newGitHubClient(token string, cache *githubCache, cachePath string, pageSize int, delay time.Duration) *githubClient {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	return &githubClient{
		token:        token,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		cache:        cache,
		cachePath:    cachePath,
		pageSize:     pageSize,
		requestDelay: delay,
		parallelism:  4,
		maxRetries:   3,
	}
}

func (c *githubClient) fetchRepo(ctx context.Context, repo string, limit int) ([]fetchedPR, error) {
	var eligible []int
	for page := 1; ; page++ {
		var pulls []ghPull
		path := fmt.Sprintf("/repos/%s/pulls?state=closed&base=main&sort=created&direction=asc&per_page=%d&page=%d", repo, c.pageSize, page)
		if err := c.getJSON(ctx, "pulls:"+repo+":"+strconv.Itoa(page), path, &pulls); err != nil {
			return nil, err
		}
		if len(pulls) == 0 {
			break
		}
		for _, p := range pulls {
			if p.MergedAt == nil || p.Base.Ref != "main" {
				continue
			}
			if c.mergedAfter != nil && !p.MergedAt.After(*c.mergedAfter) {
				continue
			}
			eligible = append(eligible, p.Number)
			if limit > 0 && len(eligible) >= limit {
				break
			}
		}
		if limit > 0 && len(eligible) >= limit {
			break
		}
		if len(pulls) < c.pageSize {
			break
		}
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	fmt.Fprintf(os.Stderr, "%s: fetching details for %d eligible merged main PRs with parallelism=%d\n", repo, len(eligible), maxInt(1, c.parallelism))
	workers := c.parallelism
	if workers <= 0 {
		workers = 1
	}
	if workers > len(eligible) {
		workers = len(eligible)
	}
	out := make([]fetchedPR, len(eligible))
	jobs := make(chan int)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var done atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				fp, err := c.fetchPR(ctx, repo, eligible[idx])
				if err != nil {
					select {
					case errCh <- err:
						cancel()
					default:
					}
					return
				}
				out[idx] = fp
				n := done.Add(1)
				if n%25 == 0 || int(n) == len(eligible) {
					fmt.Fprintf(os.Stderr, "%s: fetched %d/%d PR detail sets\n", repo, n, len(eligible))
				}
			}
		}()
	}
sendLoop:
	for idx := range eligible {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- idx:
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}
	return out, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (c *githubClient) fetchPR(ctx context.Context, repo string, number int) (fetchedPR, error) {
	base := fmt.Sprintf("/repos/%s", repo)
	var fp fetchedPR
	fp.Repo = repo
	if err := c.getJSON(ctx, fmt.Sprintf("pull:%s:%d", repo, number), fmt.Sprintf("%s/pulls/%d", base, number), &fp.Pull); err != nil {
		return fp, err
	}
	if err := c.getJSON(ctx, fmt.Sprintf("issue:%s:%d", repo, number), fmt.Sprintf("%s/issues/%d", base, number), &fp.Issue); err != nil {
		return fp, err
	}
	if err := c.getPaged(ctx, fmt.Sprintf("files:%s:%d", repo, number), fmt.Sprintf("%s/pulls/%d/files", base, number), &fp.Files); err != nil {
		return fp, err
	}
	if err := c.getPaged(ctx, fmt.Sprintf("commits:%s:%d", repo, number), fmt.Sprintf("%s/pulls/%d/commits", base, number), &fp.Commits); err != nil {
		return fp, err
	}
	if err := c.getPaged(ctx, fmt.Sprintf("reviews:%s:%d", repo, number), fmt.Sprintf("%s/pulls/%d/reviews", base, number), &fp.Reviews); err != nil {
		return fp, err
	}
	if err := c.getPaged(ctx, fmt.Sprintf("issue-comments:%s:%d", repo, number), fmt.Sprintf("%s/issues/%d/comments", base, number), &fp.IssueComments); err != nil {
		return fp, err
	}
	if err := c.getPaged(ctx, fmt.Sprintf("review-comments:%s:%d", repo, number), fmt.Sprintf("%s/pulls/%d/comments", base, number), &fp.ReviewComments); err != nil {
		return fp, err
	}
	return fp, nil
}

func (c *githubClient) getPaged(ctx context.Context, keyPrefix, endpoint string, dest any) error {
	var rawPages []json.RawMessage
	for page := 1; ; page++ {
		var pageData json.RawMessage
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		path := fmt.Sprintf("%s%sper_page=%d&page=%d", endpoint, sep, c.pageSize, page)
		if err := c.getJSON(ctx, fmt.Sprintf("%s:%d", keyPrefix, page), path, &pageData); err != nil {
			return err
		}
		rawPages = append(rawPages, pageData)
		var pageItems []json.RawMessage
		if err := json.Unmarshal(pageData, &pageItems); err != nil {
			return fmt.Errorf("parse paged response %s: %w", keyPrefix, err)
		}
		if len(pageItems) < c.pageSize {
			break
		}
	}
	joined := []byte("[")
	first := true
	for _, pageData := range rawPages {
		var pageItems []json.RawMessage
		if err := json.Unmarshal(pageData, &pageItems); err != nil {
			return err
		}
		for _, item := range pageItems {
			if !first {
				joined = append(joined, ',')
			}
			joined = append(joined, item...)
			first = false
		}
	}
	joined = append(joined, ']')
	return json.Unmarshal(joined, dest)
}

func (c *githubClient) getJSON(ctx context.Context, key, endpoint string, dest any) error {
	if entry, ok := c.cache.get(key); ok && entry.Complete {
		return json.Unmarshal(entry.Data, dest)
	}
	body, err := c.fetch(ctx, endpoint)
	if err != nil {
		return err
	}
	dirty := c.cache.set(key, cacheEntry{
		Complete:  true,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Data:      json.RawMessage(body),
	})
	if dirty%500 == 0 {
		if err := c.cache.save(c.cachePath); err != nil {
			return err
		}
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return err
	}
	return nil
}

func (c *githubClient) fetch(ctx context.Context, endpoint string) ([]byte, error) {
	if c.requestDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.requestDelay):
		}
	}
	u := "https://api.github.com" + endpoint
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			if wait := rateLimitWait(resp); wait > 0 && attempt < c.maxRetries {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(wait):
					continue
				}
			}
		}
		if resp.StatusCode >= 500 && attempt < c.maxRetries {
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		return nil, fmt.Errorf("github GET %s: %s: %s", endpoint, resp.Status, string(body))
	}
	return nil, fmt.Errorf("github GET %s: retries exhausted", endpoint)
}

func rateLimitWait(resp *http.Response) time.Duration {
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	reset := resp.Header.Get("X-RateLimit-Reset")
	if remaining == "0" && reset != "" {
		sec, err := strconv.ParseInt(reset, 10, 64)
		if err == nil {
			wait := time.Until(time.Unix(sec, 0)) + 5*time.Second
			if wait > 0 {
				return wait
			}
		}
	}
	return 0
}
