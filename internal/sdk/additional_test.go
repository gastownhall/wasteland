package sdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/gastownhall/wasteland/internal/commons"
)

type sdkContextKey string

type queryOnlyDB struct {
	queryFunc func(sql, ref string) (string, error)
}

func (db *queryOnlyDB) Query(sql, ref string) (string, error) {
	if db.queryFunc != nil {
		return db.queryFunc(sql, ref)
	}
	return "", nil
}

func (db *queryOnlyDB) Exec(string, string, bool, ...string) error { return nil }
func (db *queryOnlyDB) Branches(string) ([]string, error)          { return nil, nil }
func (db *queryOnlyDB) DeleteBranch(string) error                  { return nil }
func (db *queryOnlyDB) PushBranch(string, io.Writer) error         { return nil }
func (db *queryOnlyDB) PushMain(io.Writer) error                   { return nil }
func (db *queryOnlyDB) Sync() error                                { return nil }
func (db *queryOnlyDB) MergeBranch(string) error                   { return nil }
func (db *queryOnlyDB) DeleteRemoteBranch(string) error            { return nil }
func (db *queryOnlyDB) PushWithSync(io.Writer) error               { return nil }
func (db *queryOnlyDB) CanWildWest() error                         { return nil }

type contextAwareQueryDB struct {
	*queryOnlyDB
	seen    *context.Context
	current context.Context
}

func (db *contextAwareQueryDB) WithContext(ctx context.Context) commons.DB {
	return &contextAwareQueryDB{
		queryOnlyDB: db.queryOnlyDB,
		seen:        db.seen,
		current:     ctx,
	}
}

func (db *contextAwareQueryDB) Query(sql, ref string) (string, error) {
	if db.seen != nil {
		*db.seen = db.current
	}
	return db.queryOnlyDB.Query(sql, ref)
}

func TestWithRigHandleReturnsShallowCopy(t *testing.T) {
	db := newFakeDB()
	c := New(ClientConfig{
		DB:                     db,
		RigHandle:              "alice",
		Mode:                   "pr",
		Signing:                true,
		HopURI:                 "hop://alice",
		BestEffortPendingReads: true,
		CloseUpstreamPR:        func(string) error { return nil },
		LoadPendingItem:        func(string, PendingItem) (*commons.WantedItem, error) { return nil, nil },
		ListPendingItems:       pendingItems(nil),
	})

	clone := c.WithRigHandle("bob")
	if clone == c {
		t.Fatal("WithRigHandle() should return a new client pointer")
	}
	if clone.RigHandle() != "bob" {
		t.Fatalf("RigHandle() = %q, want bob", clone.RigHandle())
	}
	if clone.Mode() != c.Mode() {
		t.Fatalf("Mode() = %q, want %q", clone.Mode(), c.Mode())
	}
	if clone.bestEffortPendingReads != c.bestEffortPendingReads {
		t.Fatalf("bestEffortPendingReads = %v, want %v", clone.bestEffortPendingReads, c.bestEffortPendingReads)
	}
	if clone.db != c.db || clone.CloseUpstreamPR == nil || clone.LoadPendingItem == nil || clone.ListPendingItems == nil {
		t.Fatal("WithRigHandle() should preserve DB and callbacks")
	}
	if c.RigHandle() != "alice" {
		t.Fatalf("original RigHandle() = %q, want alice", c.RigHandle())
	}
}

func TestBrowseContext_BindsDBContextAndUsesContextPendingCallback(t *testing.T) {
	key := sdkContextKey("browse-pending-callback")
	var seenDB context.Context
	var seenPending context.Context

	db := &contextAwareQueryDB{
		queryOnlyDB: &queryOnlyDB{
			queryFunc: func(string, string) (string, error) {
				return "id,title,project,type,priority,posted_by,claimed_by,status,effort_level\n", nil
			},
		},
		seen: &seenDB,
	}

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		ListPendingItemsContext: func(ctx context.Context) (map[string][]PendingItem, error) {
			seenPending = ctx
			return nil, nil
		},
		ListPendingItems: func() (map[string][]PendingItem, error) {
			t.Fatal("ListPendingItems() should not be called when ListPendingItemsContext is set")
			return nil, nil
		},
	})

	ctx := context.WithValue(context.Background(), key, "trace-bound")
	if _, err := c.BrowseContext(ctx, commons.BrowseFilter{View: "all", Priority: -1}); err != nil {
		t.Fatalf("BrowseContext() error = %v", err)
	}
	if got := seenDB.Value(key); got != "trace-bound" {
		t.Fatalf("db context value = %v, want trace-bound", got)
	}
	if got := seenPending.Value(key); got != "trace-bound" {
		t.Fatalf("pending callback context value = %v, want trace-bound", got)
	}
}

func TestBrowseContext_PropagatesPendingListCancellationByDefault(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Primary item", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	_, err := c.BrowseContext(context.Background(), commons.BrowseFilter{View: "all", Priority: -1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("BrowseContext() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestBrowseContext_BestEffortPendingReadsDegradesPendingListCancellation(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Primary item", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:                     db,
		RigHandle:              "alice",
		Mode:                   "wild-west",
		BestEffortPendingReads: true,
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	result, err := c.BrowseContext(context.Background(), commons.BrowseFilter{View: "all", Priority: -1})
	if err != nil {
		t.Fatalf("BrowseContext() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "w-1" {
		t.Fatalf("result.Items = %+v, want primary browse result", result.Items)
	}
}

func TestBrowseContext_StrictPendingReadsOverrideDisablesBestEffort(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Primary item", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:                     db,
		RigHandle:              "alice",
		Mode:                   "wild-west",
		BestEffortPendingReads: true,
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	_, err := c.BrowseContext(WithStrictPendingReads(context.Background()), commons.BrowseFilter{View: "all", Priority: -1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("BrowseContext() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestBrowseContext_PropagatesPendingItemCancellationByDefault(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Primary item", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "pr",
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-new": {{
					RigHandle: "alice",
					Status:    "open",
					Branch:    "wl/alice/w-new",
					ForkOwner: "alice",
				}},
			}, nil
		},
		LoadPendingItemContext: func(context.Context, string, PendingItem) (*commons.WantedItem, error) {
			return nil, context.Canceled
		},
	})

	_, err := c.BrowseContext(context.Background(), commons.BrowseFilter{View: "all", Priority: -1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BrowseContext() error = %v, want context.Canceled", err)
	}
}

func TestBrowseContext_BestEffortPendingReadsDegradesPendingItemCancellation(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Primary item", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:                     db,
		RigHandle:              "alice",
		Mode:                   "pr",
		BestEffortPendingReads: true,
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-new": {{
					RigHandle: "alice",
					Status:    "open",
					Branch:    "wl/alice/w-new",
					ForkOwner: "alice",
				}},
			}, nil
		},
		LoadPendingItemContext: func(context.Context, string, PendingItem) (*commons.WantedItem, error) {
			return nil, context.Canceled
		},
	})

	result, err := c.BrowseContext(context.Background(), commons.BrowseFilter{View: "all", Priority: -1})
	if err != nil {
		t.Fatalf("BrowseContext() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "w-1" {
		t.Fatalf("result.Items = %+v, want primary browse result only", result.Items)
	}
}

func TestDetailContext_UsesContextAwarePendingCallbacks(t *testing.T) {
	key := sdkContextKey("detail-pending-loader")
	var seenPending context.Context
	var seenDetail context.Context

	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "alice",
		Mode:      "pr",
		ListPendingItemsContext: func(ctx context.Context) (map[string][]PendingItem, error) {
			seenPending = ctx
			return map[string][]PendingItem{
				"w-1": {{
					RigHandle:   "bob",
					Status:      "in_review",
					Branch:      "wl/bob/w-1",
					ForkOwner:   "bob",
					CompletedBy: "bob",
					Evidence:    "proof",
				}},
			}, nil
		},
		ListPendingItems: func() (map[string][]PendingItem, error) {
			t.Fatal("ListPendingItems() should not be called when ListPendingItemsContext is set")
			return nil, nil
		},
		LoadPendingDetailContext: func(ctx context.Context, wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			seenDetail = ctx
			return &commons.WantedItem{
					ID:          wantedID,
					Title:       "Pending detail",
					PostedBy:    "alice",
					Status:      "in_review",
					EffortLevel: "small",
				}, &commons.CompletionRecord{
					ID:          "c-1",
					WantedID:    wantedID,
					CompletedBy: pending.CompletedBy,
					Evidence:    pending.Evidence,
				}, nil, nil
		},
		LoadPendingDetail: func(string, PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			t.Fatal("LoadPendingDetail() should not be called when LoadPendingDetailContext is set")
			return nil, nil, nil, nil
		},
	})

	ctx := context.WithValue(context.Background(), key, "trace-bound")
	result, err := c.DetailContext(ctx, "w-1")
	if err != nil {
		t.Fatalf("DetailContext() error = %v", err)
	}
	if result.Item == nil || result.Item.ID != "w-1" {
		t.Fatalf("result.Item = %+v", result.Item)
	}
	if got := seenPending.Value(key); got != "trace-bound" {
		t.Fatalf("pending list context value = %v, want trace-bound", got)
	}
	if got := seenDetail.Value(key); got != "trace-bound" {
		t.Fatalf("pending detail context value = %v, want trace-bound", got)
	}
}

func TestDetailContext_PropagatesContextPendingListCancellationForPendingOnlyItem(t *testing.T) {
	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "alice",
		Mode:      "pr",
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	_, err := c.DetailContext(context.Background(), "w-missing")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DetailContext() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestDetailContext_FailsPendingListCancellationWhenMainItemExistsWithoutBestEffort(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Primary item", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "pr",
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	_, err := c.DetailContext(context.Background(), "w-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DetailContext() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestDetailContext_BestEffortPendingReadsDegradesPendingListCancellationWhenMainItemExists(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Primary item", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:                     db,
		RigHandle:              "alice",
		Mode:                   "pr",
		BestEffortPendingReads: true,
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	result, err := c.DetailContext(context.Background(), "w-1")
	if err != nil {
		t.Fatalf("DetailContext() error = %v", err)
	}
	if result.Item == nil || result.Item.ID != "w-1" {
		t.Fatalf("result.Item = %+v, want primary detail result", result.Item)
	}
}

func TestDetailContext_StrictPendingReadsOverrideStillFailsForPendingOnlyItem(t *testing.T) {
	c := New(ClientConfig{
		DB:                     newFakeDB(),
		RigHandle:              "alice",
		Mode:                   "pr",
		BestEffortPendingReads: true,
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	_, err := c.DetailContext(WithStrictPendingReads(context.Background()), "w-missing")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DetailContext() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestDetailContext_BestEffortPendingReadsStillFailsForPendingOnlyItem(t *testing.T) {
	c := New(ClientConfig{
		DB:                     newFakeDB(),
		RigHandle:              "alice",
		Mode:                   "pr",
		BestEffortPendingReads: true,
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	_, err := c.DetailContext(context.Background(), "w-missing")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DetailContext() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestDetailContext_WildWestDegradesPendingListCancellationWhenMainItemExists(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Primary item", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return nil, context.DeadlineExceeded
		},
	})

	result, err := c.DetailContext(WithStrictPendingReads(context.Background()), "w-1")
	if err != nil {
		t.Fatalf("DetailContext() error = %v", err)
	}
	if result.Item == nil || result.Item.ID != "w-1" {
		t.Fatalf("result.Item = %+v, want primary detail result", result.Item)
	}
	if !result.PendingReadIncomplete {
		t.Fatal("result.PendingReadIncomplete = false, want true")
	}
}

func TestDetailContext_FallsBackToConfiguredDBAfterContextLoaderError(t *testing.T) {
	db := newFakeDB()
	db.branches["wl/bob/w-1"] = true
	db.branchItems["wl/bob/w-1"] = map[string]*fakeItem{
		"w-1": {
			ID:          "w-1",
			Title:       "Branch fallback item",
			Status:      "in_review",
			PostedBy:    "alice",
			EffortLevel: "small",
		},
	}

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "pr",
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-1": {{
					RigHandle: "bob",
					Status:    "in_review",
					Branch:    "wl/bob/w-1",
					ForkOwner: "bob",
				}},
			}, nil
		},
		LoadPendingDetailContext: func(context.Context, string, PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			return nil, nil, nil, errors.New("upstream branch read failed")
		},
		LoadPendingDetail: func(string, PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			t.Fatal("LoadPendingDetail() should not be called after a context-aware loader error")
			return nil, nil, nil, nil
		},
	})

	result, err := c.DetailContext(context.Background(), "w-1")
	if err != nil {
		t.Fatalf("DetailContext() error = %v", err)
	}
	if result.Item == nil || result.Item.Title != "Branch fallback item" {
		t.Fatalf("result.Item = %+v", result.Item)
	}
}

func TestDetailContext_PendingOnlyLoaderFailureDoesNotFallbackToMain(t *testing.T) {
	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "alice",
		Mode:      "pr",
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-missing": {{
					RigHandle: "bob",
					Status:    "in_review",
					Branch:    "wl/bob/w-missing",
					ForkOwner: "bob",
				}},
			}, nil
		},
		LoadPendingDetailContext: func(context.Context, string, PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			return nil, nil, nil, errors.New("upstream branch read failed")
		},
	})

	_, err := c.DetailContext(context.Background(), "w-missing")
	if err == nil || !strings.Contains(err.Error(), `wanted item "w-missing" not found on ref wl/bob/w-missing`) {
		t.Fatalf("DetailContext() error = %v, want branch-ref not found", err)
	}
	if strings.Contains(err.Error(), `wanted item "w-missing" not found`) && !strings.Contains(err.Error(), "on ref") {
		t.Fatalf("DetailContext() error = %v, want branch-ref failure not main fallback", err)
	}
}

func TestDetailContext_PropagatesContextLoaderCancellation(t *testing.T) {
	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "alice",
		Mode:      "pr",
		ListPendingItemsContext: func(context.Context) (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-1": {{
					RigHandle: "bob",
					Status:    "in_review",
					Branch:    "wl/bob/w-1",
					ForkOwner: "bob",
				}},
			}, nil
		},
		LoadPendingDetailContext: func(context.Context, string, PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			return nil, nil, nil, context.Canceled
		},
		LoadPendingDetail: func(string, PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			t.Fatal("LoadPendingDetail() should not be called after a context-aware loader cancellation")
			return nil, nil, nil, nil
		},
	})

	_, err := c.DetailContext(context.Background(), "w-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DetailContext() error = %v, want context.Canceled", err)
	}
}

func TestDetailContext_UsesContextAwareCheckPR(t *testing.T) {
	key := sdkContextKey("check-pr-callback")
	var seenPR context.Context

	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})
	db.branches["wl/alice/w-1"] = true
	db.branchItems["wl/alice/w-1"] = map[string]*fakeItem{
		"w-1": {
			ID:          "w-1",
			Title:       "Fix bug",
			Status:      "claimed",
			Priority:    1,
			PostedBy:    "alice",
			ClaimedBy:   "alice",
			EffortLevel: "medium",
		},
	}

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "pr",
		CheckPRContext: func(ctx context.Context, branch string) string {
			seenPR = ctx
			if branch != "wl/alice/w-1" {
				t.Fatalf("branch = %q, want wl/alice/w-1", branch)
			}
			return "https://example.com/pr/1"
		},
		CheckPR: func(string) string {
			t.Fatal("CheckPR() should not be called when CheckPRContext is set")
			return ""
		},
	})

	ctx := context.WithValue(context.Background(), key, "trace-bound")
	result, err := c.DetailContext(ctx, "w-1")
	if err != nil {
		t.Fatalf("DetailContext() error = %v", err)
	}
	if result.PRURL != "https://example.com/pr/1" {
		t.Fatalf("PRURL = %q, want https://example.com/pr/1", result.PRURL)
	}
	if got := seenPR.Value(key); got != "trace-bound" {
		t.Fatalf("check PR context value = %v, want trace-bound", got)
	}
}

func TestPRMutations_DoNotRequireCheckPRContextForBranchDurability(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{
		ID:          "w-1",
		Title:       "Fix bug",
		Status:      "claimed",
		ClaimedBy:   "alice",
		PostedBy:    "bob",
		EffortLevel: "medium",
	})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "pr",
		CheckPRContext: func(context.Context, string) string {
			t.Fatal("CheckPRContext() should not be called on the mutation hot path")
			return ""
		},
		CheckPR: func(string) string {
			t.Fatal("CheckPR() should not be called on the mutation hot path")
			return ""
		},
	})

	result, err := c.Unclaim("w-1")
	if err != nil {
		t.Fatalf("Unclaim() error = %v", err)
	}
	if result.Detail == nil || result.Detail.Item == nil {
		t.Fatal("expected mutation detail item")
	}
	if result.Detail.Item.Status != "open" {
		t.Fatalf("status = %q, want open", result.Detail.Item.Status)
	}
	if result.Detail.Branch == "" {
		t.Fatal("expected branch to persist when branch diverges from main")
	}
	if result.Detail.Delta == "" {
		t.Fatal("expected non-empty delta for branch mutation")
	}
}

func TestDetail_UsesSingleJoinedQuery(t *testing.T) {
	var calls int
	db := &queryOnlyDB{
		queryFunc: func(sql, ref string) (string, error) {
			calls++
			if ref != "" {
				t.Fatalf("unexpected ref = %q", ref)
			}
			if strings.Contains(sql, "FROM completions") || strings.Contains(sql, "FROM stamps") {
				t.Fatalf("unexpected separate detail query: %s", sql)
			}
			if !strings.Contains(sql, "LEFT JOIN completions") || !strings.Contains(sql, "LEFT JOIN stamps") {
				t.Fatalf("expected joined detail query, got %s", sql)
			}
			return strings.Join([]string{
				"id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at,completion_id,completion_wanted_id,completed_by,evidence,completion_stamp_id,validated_by,stamp_record_id,stamp_author,stamp_subject,stamp_valence,stamp_severity,stamp_context_id,stamp_context_type,stamp_skill_tags,stamp_message",
				`w-1,Fix hosted detail,Trace the detail path,hop,bug,1,"[""go"",""perf""]",alice,bob,in_review,medium,2026-03-20,2026-03-21,c-1,w-1,bob,https://example.com/proof,s-1,alice,s-1,alice,bob,"{""quality"":4,""reliability"":5}",medium,w-1,completion,"[""go"",""perf""]",Looks good`,
				"",
			}, "\n"), nil
		},
	}

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})
	result, err := c.Detail("w-1")
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if result.Completion == nil || result.Completion.ID != "c-1" {
		t.Fatalf("completion = %+v, want joined completion", result.Completion)
	}
	if result.Stamp == nil || result.Stamp.ID != "s-1" {
		t.Fatalf("stamp = %+v, want joined stamp", result.Stamp)
	}
}

func TestBrowse_BranchOnlyPendingUsesItemLoader(t *testing.T) {
	db := newFakeDB()
	var itemLoads, detailLoads int
	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "pr",
		LoadPendingItem: func(wantedID string, _ PendingItem) (*commons.WantedItem, error) {
			itemLoads++
			return &commons.WantedItem{
				ID:          wantedID,
				Title:       "Pending branch-only item",
				Project:     "hop",
				Type:        "docs",
				Priority:    1,
				PostedBy:    "alice",
				Status:      "open",
				EffortLevel: "small",
			}, nil
		},
		LoadPendingDetail: func(string, PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			detailLoads++
			return nil, nil, nil, fmt.Errorf("should not be called")
		},
		ListPendingItems: pendingItems(map[string][]PendingItem{
			"w-new": {{
				RigHandle: "alice",
				Status:    "open",
				Branch:    "wl/alice/w-new",
				ForkOwner: "alice",
			}},
		}),
	})

	result, err := c.Browse(commons.BrowseFilter{View: "mine", Priority: -1})
	if err != nil {
		t.Fatalf("Browse() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != "w-new" {
		t.Fatalf("result.Items = %+v, want branch-only pending item", result.Items)
	}
	if itemLoads != 1 {
		t.Fatalf("itemLoads = %d, want 1", itemLoads)
	}
	if detailLoads != 0 {
		t.Fatalf("detailLoads = %d, want 0", detailLoads)
	}
}

func TestPostAndUpdate(t *testing.T) {
	db := newFakeDB()
	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})

	posted, err := c.Post(PostInput{
		Title:       "Fix hosted login",
		Description: "Stop stale auth state from leaking into hosted mode",
		Project:     "hop",
		Type:        "bug",
		Priority:    1,
		EffortLevel: "small",
		Tags:        []string{"go", "auth"},
	})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if posted.Detail == nil || posted.Detail.Item == nil {
		t.Fatal("Post() should return detail with item")
	}
	if !strings.HasPrefix(posted.Detail.Item.ID, "w-") {
		t.Fatalf("post ID = %q, want w- prefix", posted.Detail.Item.ID)
	}
	if posted.Detail.Item.PostedBy != "alice" {
		t.Fatalf("posted_by = %q, want alice", posted.Detail.Item.PostedBy)
	}
	if db.pushCalls != 1 {
		t.Fatalf("pushCalls = %d, want 1 after post", db.pushCalls)
	}

	updated, err := c.Update(posted.Detail.Item.ID, &commons.WantedUpdate{
		Title:       "Fix hosted auth state",
		Description: "Refresh hosted auth state after reconnect",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Detail == nil || updated.Detail.Item == nil {
		t.Fatal("Update() should return detail with item")
	}
	if updated.Detail.Item.Title != "Fix hosted auth state" {
		t.Fatalf("updated title = %q, want Fix hosted auth state", updated.Detail.Item.Title)
	}
	if updated.Detail.Item.Description != "Refresh hosted auth state after reconnect" {
		t.Fatalf("updated description = %q, want refreshed description", updated.Detail.Item.Description)
	}
	if db.pushCalls != 2 {
		t.Fatalf("pushCalls = %d, want 2 after update", db.pushCalls)
	}
	if updated.Detail.Item.Project != "hop" {
		t.Fatalf("omitted project = %q, want hop", updated.Detail.Item.Project)
	}
	cleared, err := c.Update(posted.Detail.Item.ID, &commons.WantedUpdate{
		Priority: -1, DescriptionSet: true, ProjectSet: true,
	})
	if err != nil {
		t.Fatalf("Update() clearing optional text error = %v", err)
	}
	if cleared.Detail.Item.Description != "" || cleared.Detail.Item.Project != "" {
		t.Fatalf("optional text was not cleared: %+v", cleared.Detail.Item)
	}
}

func TestClearBranchDataAndExtractWantedID(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", PostedBy: "alice", EffortLevel: "medium"})
	db.completions["w-1"] = &fakeCompletion{ID: "c-1", WantedID: "w-1", CompletedBy: "bob", Evidence: "proof"}

	branch := "wl/alice/w-1"
	db.branches[branch] = true
	db.branchItems[branch] = map[string]*fakeItem{
		"w-1": {ID: "w-1", Title: "Fix bug", Status: "in_review", PostedBy: "alice", EffortLevel: "medium"},
	}

	if err := clearBranchData(db, branch); err != nil {
		t.Fatalf("clearBranchData() error = %v", err)
	}
	if _, ok := db.branchItems[branch]["w-1"]; ok {
		t.Fatal("branch item should be deleted")
	}
	if _, ok := db.completions["w-1"]; ok {
		t.Fatal("completion should be deleted")
	}

	if got := extractWantedID(branch); got != "w-1" {
		t.Fatalf("extractWantedID(valid) = %q, want w-1", got)
	}
	if got := extractWantedID("feature/alice/w-1"); got != "" {
		t.Fatalf("extractWantedID(invalid) = %q, want empty", got)
	}
}

func TestRejectUpstream(t *testing.T) {
	var closedURL string
	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "alice",
		ListPendingItems: pendingItems(map[string][]PendingItem{
			"w-1": {{RigHandle: "charlie", Status: "in_review", PRURL: "https://dolthub.example/pulls/42"}},
		}),
		CloseUpstreamPR: func(prURL string) error {
			closedURL = prURL
			return nil
		},
	})

	if err := c.RejectUpstream("w-1", "charlie"); err != nil {
		t.Fatalf("RejectUpstream() error = %v", err)
	}
	if closedURL != "https://dolthub.example/pulls/42" {
		t.Fatalf("closed PR URL = %q, want pull URL", closedURL)
	}
}

func TestRejectUpstream_Errors(t *testing.T) {
	t.Run("missing pr url", func(t *testing.T) {
		c := New(ClientConfig{
			DB:        newFakeDB(),
			RigHandle: "alice",
			ListPendingItems: pendingItems(map[string][]PendingItem{
				"w-1": {{RigHandle: "charlie", Status: "in_review"}},
			}),
			CloseUpstreamPR: func(string) error { return nil },
		})

		err := c.RejectUpstream("w-1", "charlie")
		if err == nil || !strings.Contains(err.Error(), "submission has no upstream PR to close") {
			t.Fatalf("RejectUpstream() error = %v, want missing PR error", err)
		}
	})

	t.Run("missing submitter", func(t *testing.T) {
		c := New(ClientConfig{
			DB:               newFakeDB(),
			RigHandle:        "alice",
			ListPendingItems: pendingItems(map[string][]PendingItem{"w-1": {{RigHandle: "bob"}}}),
		})

		err := c.RejectUpstream("w-1", "charlie")
		if err == nil || !strings.Contains(err.Error(), "no pending submission from charlie") {
			t.Fatalf("RejectUpstream() error = %v, want submitter lookup error", err)
		}
	})
}

func TestRejectUpstreamSelected_PRURLDisambiguatesSameRig(t *testing.T) {
	var closedURL string
	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "alice",
		ListPendingItems: pendingItems(map[string][]PendingItem{
			"w-1": {
				{RigHandle: "charlie", Status: "in_review", PRURL: "https://dolthub.example/pulls/41"},
				{RigHandle: "charlie", Status: "in_review", PRURL: "https://dolthub.example/pulls/42"},
			},
		}),
		CloseUpstreamPR: func(prURL string) error {
			closedURL = prURL
			return nil
		},
	})

	if err := c.RejectUpstreamSelected("w-1", UpstreamSubmissionSelector{
		RigHandle: "charlie",
		PRURL:     "https://dolthub.example/pulls/42",
	}); err != nil {
		t.Fatalf("RejectUpstreamSelected() error = %v", err)
	}
	if closedURL != "https://dolthub.example/pulls/42" {
		t.Fatalf("closed PR URL = %q, want selected pull URL", closedURL)
	}
}

func TestCloseUpstream(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", PostedBy: "alice", EffortLevel: "medium"})

	var closedURL string
	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		HopURI:    "hop://alice",
		ListPendingItems: pendingItems(map[string][]PendingItem{
			"w-1": {{
				RigHandle:   "charlie",
				Status:      "in_review",
				CompletedBy: "charlie",
				Evidence:    "proof",
				PRURL:       "https://dolthub.example/pulls/9",
			}},
		}),
		CloseUpstreamPR: func(prURL string) error {
			closedURL = prURL
			return nil
		},
	})

	result, err := c.CloseUpstream("w-1", "charlie")
	if err != nil {
		t.Fatalf("CloseUpstream() error = %v", err)
	}
	if result.Detail == nil || result.Detail.Item == nil {
		t.Fatal("CloseUpstream() should return detail with item")
	}
	if result.Detail.Item.Status != "completed" {
		t.Fatalf("status = %q, want completed", result.Detail.Item.Status)
	}
	if closedURL != "https://dolthub.example/pulls/9" {
		t.Fatalf("closed PR URL = %q, want pull URL", closedURL)
	}
	if db.pushCalls != 1 {
		t.Fatalf("pushCalls = %d, want 1", db.pushCalls)
	}
}

func TestCloseUpstream_SelfAllowed(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "charlie",
		Mode:      "wild-west",
		ListPendingItems: pendingItems(map[string][]PendingItem{
			"w-1": {{
				RigHandle:   "charlie",
				Status:      "in_review",
				CompletedBy: "charlie",
				Evidence:    "proof",
			}},
		}),
	})

	result, err := c.CloseUpstream("w-1", "charlie")
	if err != nil {
		t.Fatalf("CloseUpstream() error = %v", err)
	}
	if result.Detail == nil || result.Detail.Item == nil {
		t.Fatal("CloseUpstream() should return detail with item")
	}
	if result.Detail.Item.Status != "completed" {
		t.Fatalf("status = %q, want completed", result.Detail.Item.Status)
	}
	if db.completions["w-1"].CompletedBy != "charlie" {
		t.Fatalf("completion = %q, want charlie", db.completions["w-1"].CompletedBy)
	}
	if db.completions["w-1"].StampID != "" {
		t.Fatalf("stamp_id = %q, want empty", db.completions["w-1"].StampID)
	}
}

func TestCloseUpstreamSelected_PRURLDisambiguatesSameRig(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", PostedBy: "alice", EffortLevel: "medium"})

	var closedURL string
	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		ListPendingItems: pendingItems(map[string][]PendingItem{
			"w-1": {
				{
					RigHandle:   "charlie",
					Status:      "in_review",
					CompletedBy: "charlie",
					Evidence:    "proof-a",
					PRURL:       "https://dolthub.example/pulls/41",
				},
				{
					RigHandle:   "charlie",
					Status:      "in_review",
					CompletedBy: "charlie",
					Evidence:    "proof-b",
					PRURL:       "https://dolthub.example/pulls/42",
				},
			},
		}),
		CloseUpstreamPR: func(prURL string) error {
			closedURL = prURL
			return nil
		},
	})

	result, err := c.CloseUpstreamSelected("w-1", UpstreamSubmissionSelector{
		RigHandle: "charlie",
		PRURL:     "https://dolthub.example/pulls/42",
	})
	if err != nil {
		t.Fatalf("CloseUpstreamSelected() error = %v", err)
	}
	if result.Detail == nil || result.Detail.Item == nil {
		t.Fatal("CloseUpstreamSelected() should return detail with item")
	}
	if db.completions["w-1"].Evidence != "proof-b" {
		t.Fatalf("accepted evidence = %q, want proof-b", db.completions["w-1"].Evidence)
	}
	if closedURL != "https://dolthub.example/pulls/42" {
		t.Fatalf("closed PR URL = %q, want selected pull URL", closedURL)
	}
}

func TestFindUpstreamSubmissionErrorsAndLeaderboard(t *testing.T) {
	t.Run("find upstream submission errors", func(t *testing.T) {
		c := New(ClientConfig{DB: newFakeDB(), RigHandle: "alice"})
		if _, err := c.findUpstreamSubmission("w-1", "charlie"); err == nil || !strings.Contains(err.Error(), "upstream PR listing not available") {
			t.Fatalf("findUpstreamSubmission(nil) error = %v", err)
		}

		c = New(ClientConfig{
			DB:        newFakeDB(),
			RigHandle: "alice",
			ListPendingItems: func() (map[string][]PendingItem, error) {
				return nil, fmt.Errorf("pending unavailable")
			},
		})
		if _, err := c.findUpstreamSubmission("w-1", "charlie"); err == nil || !strings.Contains(err.Error(), "pending unavailable") {
			t.Fatalf("findUpstreamSubmission(error) error = %v", err)
		}
	})

	t.Run("leaderboard parses aggregate rows", func(t *testing.T) {
		db := &queryOnlyDB{
			queryFunc: func(sql, _ string) (string, error) {
				switch {
				case strings.Contains(sql, "GROUP BY c.completed_by"):
					return "completed_by,completions,avg_quality,avg_reliability,avg_creativity\nalice,2,4.5,3.5,1.5\n", nil
				case strings.Contains(sql, "skill_tags"):
					return "completed_by,skill_tags\nalice,\"[\"\"go\"\",\"\"Go\"\",\"\"sql\"\"]\"\n", nil
				default:
					return "", fmt.Errorf("unexpected query: %s", sql)
				}
			},
		}

		c := New(ClientConfig{DB: db, RigHandle: "alice"})
		board, err := c.Leaderboard(0)
		if err != nil {
			t.Fatalf("Leaderboard() error = %v", err)
		}
		if len(board) != 1 {
			t.Fatalf("Leaderboard() entries = %d, want 1", len(board))
		}
		if board[0].RigHandle != "alice" || board[0].Completions != 2 {
			t.Fatalf("Leaderboard() entry = %+v", board[0])
		}
		if got := strings.Join(board[0].TopSkills, ","); got != "go,sql" {
			t.Fatalf("TopSkills = %q, want go,sql", got)
		}
	})
}
