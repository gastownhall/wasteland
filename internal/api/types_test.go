package api

import (
	"testing"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/sdk"
)

func TestToDetailResponse_SuppressesTerminalUpstreamSubmissions(t *testing.T) {
	resp := toDetailResponse(&sdk.DetailResult{
		Item: &commons.WantedItem{
			ID:        "w-1",
			Title:     "Done task",
			Status:    "completed",
			ClaimedBy: "winner",
		},
		UpstreamPRs: []sdk.PendingItem{{
			RigHandle: "charlie",
			Status:    "in_review",
			ClaimedBy: "charlie",
			PRURL:     "https://example.com/pr/1",
		}},
	}, "pr")

	if resp == nil {
		t.Fatal("expected response")
	}
	if len(resp.UpstreamPRs) != 0 {
		t.Fatalf("upstream PRs = %+v, want none for completed item", resp.UpstreamPRs)
	}
	if resp.Item == nil || resp.Item.ClaimedBy != "winner" {
		t.Fatalf("item = %+v, want completed winner preserved", resp.Item)
	}
}

func TestToDetailResponse_PreservesActiveUpstreamSubmissions(t *testing.T) {
	resp := toDetailResponse(&sdk.DetailResult{
		Item: &commons.WantedItem{
			ID:        "w-1",
			Title:     "Open task",
			Status:    "open",
			ClaimedBy: "alice",
		},
		UpstreamPRs: []sdk.PendingItem{{
			RigHandle: "charlie",
			Status:    "in_review",
			ClaimedBy: "charlie",
			PRURL:     "https://example.com/pr/1",
		}},
	}, "pr")

	if resp == nil {
		t.Fatal("expected response")
	}
	if len(resp.UpstreamPRs) != 1 {
		t.Fatalf("upstream PRs = %+v, want 1 active submission", resp.UpstreamPRs)
	}
	if resp.Item == nil || resp.Item.ClaimedBy != "Multiple (pending)" {
		t.Fatalf("item = %+v, want pending overlay for active item", resp.Item)
	}
}
