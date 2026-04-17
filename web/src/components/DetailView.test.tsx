import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveUpstream } from "../api/client";
import { CommandsContext } from "../hooks/useCommands";
import { makeConfigResponse, makeDetailResponse, makeItem, mockFetch } from "../test-utils";

const mocked = vi.hoisted(() => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  wastelandState: {
    active: null as string | null,
    authenticated: false,
    environment: undefined as string | undefined,
    ready: true,
    viewerRigHandle: "alice" as string | undefined,
    wastelands: [] as Array<{ upstream: string }>,
  },
}));

vi.mock("sonner", () => ({
  toast: {
    success: mocked.toastSuccess,
    error: mocked.toastError,
  },
}));

vi.mock("../context/WastelandContext", () => ({
  useWasteland: () => ({
    ...mocked.wastelandState,
    switchTo: () => {},
    refresh: async () => {},
  }),
}));

import { DetailView } from "./DetailView";

const defaultCommands = { commands: [], register: () => () => {} };

function renderDetail(id = "item-1") {
  return render(
    <MemoryRouter initialEntries={[`/wanted/${id}`]}>
      <CommandsContext.Provider value={defaultCommands}>
        <Routes>
          <Route path="/wanted/:id" element={<DetailView />} />
          <Route path="/" element={<div data-testid="home">Home</div>} />
        </Routes>
      </CommandsContext.Provider>
    </MemoryRouter>,
  );
}

let cleanupFetch: () => void;

afterEach(() => {
  cleanup();
  setActiveUpstream(null);
  localStorage.removeItem("wl_active");
  cleanupFetch?.();
  vi.clearAllMocks();
  mocked.wastelandState = {
    active: null,
    authenticated: false,
    environment: undefined,
    ready: true,
    viewerRigHandle: "alice",
    wastelands: [],
  };
});

describe("DetailView", () => {
  it("shows skeleton while loading", () => {
    cleanupFetch = mockFetch(() => new Promise(() => {}));
    renderDetail();
    // While loading, the component renders skeletons (SkeletonLine etc)
    // No title text should be present yet
    expect(screen.queryByText("Fix the thing")).not.toBeInTheDocument();
  });

  it("renders item title and badges", async () => {
    cleanupFetch = mockFetch(() => {
      return makeDetailResponse({
        item: makeItem({
          title: "My Task",
          priority: 1,
          status: "open",
          type: "feature",
        }),
      });
    });
    renderDetail();
    await waitFor(() => expect(screen.getByText("My Task")).toBeInTheDocument());
    expect(screen.getByText("P1")).toBeInTheDocument();
    expect(screen.getByText("open")).toBeInTheDocument();
    expect(screen.getByText("feature")).toBeInTheDocument();
  });

  it("shows error on fetch failure", async () => {
    cleanupFetch = mockFetch(() => {
      return new Response(JSON.stringify({ error: "not found" }), {
        status: 404,
      });
    });
    renderDetail();
    await waitFor(() => expect(screen.getByText("not found")).toBeInTheDocument());
  });

  it("shows Edit button when canEdit is true", async () => {
    mocked.wastelandState.viewerRigHandle = "alice";
    cleanupFetch = mockFetch(() => makeDetailResponse({ item: makeItem({ posted_by: "alice" }) }));
    renderDetail();
    await waitFor(() => expect(screen.getByText("Edit")).toBeInTheDocument());
  });

  it("hides Edit button when poster is different", async () => {
    mocked.wastelandState.viewerRigHandle = "bob";
    cleanupFetch = mockFetch(() => makeDetailResponse({ item: makeItem({ posted_by: "alice" }) }));
    renderDetail();
    await waitFor(() => expect(screen.getByText("Fix the thing")).toBeInTheDocument());
    expect(screen.queryByText("Edit")).not.toBeInTheDocument();
  });

  it("shows submission actions for admins when backend allows them", async () => {
    mocked.wastelandState.viewerRigHandle = "admin";
    cleanupFetch = mockFetch(() =>
      makeDetailResponse({
        item: makeItem({ posted_by: "alice", status: "claimed" }),
        actions: ["accept", "reject", "close"],
        upstream_prs: [
          {
            is_upstream: true,
            rig_handle: "charlie",
            status: "in_review",
            evidence: "https://github.com/org/repo/pull/1",
            pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
          },
        ],
      }),
    );
    renderDetail();
    await waitFor(() => expect(screen.getByText("charlie")).toBeInTheDocument(), { timeout: 5000 });
    expect(screen.getByRole("button", { name: "accept" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "reject" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "close" })).toBeInTheDocument();
  }, 10000);

  it("shows submission actions for the poster when backend allows them", async () => {
    mocked.wastelandState.viewerRigHandle = "alice";
    cleanupFetch = mockFetch(() =>
      makeDetailResponse({
        item: makeItem({ posted_by: "alice", status: "claimed" }),
        actions: ["accept", "reject", "close"],
        upstream_prs: [
          {
            is_upstream: true,
            rig_handle: "charlie",
            status: "in_review",
            evidence: "https://github.com/org/repo/pull/1",
            pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
          },
        ],
      }),
    );
    renderDetail();
    await waitFor(() => expect(screen.getByText("charlie")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "accept" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "reject" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "close" })).toBeInTheDocument();
  });

  it("hides submission actions when backend does not allow them", async () => {
    mocked.wastelandState.viewerRigHandle = "alice";
    cleanupFetch = mockFetch(() =>
      makeDetailResponse({
        item: makeItem({ posted_by: "alice", status: "claimed" }),
        actions: [],
        upstream_prs: [
          {
            is_upstream: true,
            rig_handle: "charlie",
            status: "in_review",
            evidence: "https://github.com/org/repo/pull/1",
            pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
          },
        ],
      }),
    );
    renderDetail();
    await waitFor(() => expect(screen.getByText("charlie")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "accept" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "reject" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "close" })).not.toBeInTheDocument();
  });

  it("does not fetch config on detail load", async () => {
    const fetchFn = vi.fn((url: string) => {
      if (url.includes("/api/config")) {
        throw new Error("config request should not happen");
      }
      return makeDetailResponse();
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();
    await waitFor(() => expect(screen.getByText("Fix the thing")).toBeInTheDocument());
    expect(fetchFn).not.toHaveBeenCalledWith("/api/config", expect.anything());
  });

  it("action buttons trigger API calls", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.endsWith("/claim") && init?.method === "POST") return { detail: makeItem({ status: "claimed" }) };
      return makeDetailResponse({ actions: ["claim"] });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();
    await waitFor(() => expect(screen.getByText("claim")).toBeInTheDocument());
    await userEvent.click(screen.getByText("claim"));
    await waitFor(() => {
      const claimCalls = fetchFn.mock.calls.filter(([u]) => u.includes("/claim"));
      expect(claimCalls.length).toBeGreaterThan(0);
    });
  });

  it("destructive action shows confirm dialog", async () => {
    cleanupFetch = mockFetch((url) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      return makeDetailResponse({ actions: ["close"] });
    });
    renderDetail();
    await waitFor(() => expect(screen.getByText("close")).toBeInTheDocument());
    await userEvent.click(screen.getByText("close"));
    expect(screen.getByText(/Are you sure/)).toBeInTheDocument();
  });

  it("delete navigates to /", async () => {
    setActiveUpstream("hop/wl-commons");
    cleanupFetch = mockFetch((url, init) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.endsWith("/api/wanted/item-1") && init?.method === "DELETE") return { detail: null };
      return makeDetailResponse({ actions: ["delete"] });
    });
    renderDetail();
    await waitFor(() => expect(screen.getByText("delete")).toBeInTheDocument());
    await userEvent.click(screen.getByText("delete"));
    // Confirm dialog appears
    await waitFor(() => expect(screen.getByText("Confirm")).toBeInTheDocument());
    await userEvent.click(screen.getByText("Confirm"));
    await waitFor(() => expect(screen.getByTestId("home")).toBeInTheDocument());
  });

  it("done form submits evidence", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.endsWith("/done") && init?.method === "POST") return { detail: makeItem({ status: "claimed" }) };
      return makeDetailResponse({ actions: ["done"] });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();
    await waitFor(() => expect(screen.getByText("done")).toBeInTheDocument());
    await userEvent.click(screen.getByText("done"));
    const input = screen.getByPlaceholderText("https://github.com/...");
    await userEvent.type(input, "https://example.com/pr/1");
    await userEvent.click(screen.getByText("Submit"));
    await waitFor(() => {
      const doneCalls = fetchFn.mock.calls.filter(([u]) => u.includes("/done"));
      expect(doneCalls.length).toBeGreaterThan(0);
    });
  });

  it("view diff button loads diff content", async () => {
    cleanupFetch = mockFetch((url) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.includes("/diff/")) return { diff: "+added line" };
      return makeDetailResponse({
        branch: "wl/fix",
        delta: "1 table changed",
      });
    });
    renderDetail();
    await waitFor(() => expect(screen.getByText("View diff")).toBeInTheDocument());
    await userEvent.click(screen.getByText("View diff"));
    await waitFor(() => expect(screen.getByText("+added line")).toBeInTheDocument());
  });

  it("shows diff load failures inline", async () => {
    cleanupFetch = mockFetch((url) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.includes("/diff/")) {
        return new Response(JSON.stringify({ error: "diff exploded" }), {
          status: 500,
        });
      }
      return makeDetailResponse({
        branch: "wl/fix",
        delta: "1 table changed",
      });
    });
    renderDetail();
    await waitFor(() => expect(screen.getByText("View diff")).toBeInTheDocument());
    await userEvent.click(screen.getByText("View diff"));
    await waitFor(() => expect(screen.getByText("Error loading diff: diff exploded")).toBeInTheDocument());
  });

  it("renders branch metadata and links", async () => {
    cleanupFetch = mockFetch((url) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      return makeDetailResponse({
        item: makeItem({ status: "claimed" }),
        branch: "wl/alice/item-1",
        branch_url: "https://example.com/branch/wl/alice/item-1",
        main_status: "open",
        pr_url: "https://example.com/pr/1",
      });
    });
    renderDetail();

    await waitFor(() => expect(screen.getByRole("link", { name: "wl/alice/item-1" })).toBeInTheDocument());
    expect(screen.getByText("open → claimed")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "wl/alice/item-1" })).toHaveAttribute(
      "href",
      "https://example.com/branch/wl/alice/item-1",
    );
    expect(screen.getByRole("link", { name: "https://example.com/pr/1" })).toHaveAttribute(
      "href",
      "https://example.com/pr/1",
    );
  });

  it("renders branch metadata without a link when branch_url is missing", async () => {
    cleanupFetch = mockFetch((url) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      return makeDetailResponse({
        item: makeItem({ status: "claimed" }),
        branch: "wl/alice/item-1",
        delta: "1 file changed",
      });
    });
    renderDetail();

    await waitFor(() => expect(screen.getByText("wl/alice/item-1")).toBeInTheDocument());
    expect(screen.queryByRole("link", { name: "wl/alice/item-1" })).not.toBeInTheDocument();
  });

  it("renders completion and stamp details for completed items", async () => {
    cleanupFetch = mockFetch((url) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      return makeDetailResponse({
        item: makeItem({ status: "completed" }),
        completion: {
          id: "c-1",
          wanted_id: "item-1",
          completed_by: "bob",
          evidence: "https://example.com/pr/1",
          validated_by: "alice",
        },
        stamp: {
          id: "s-1",
          author: "alice",
          subject: "bob",
          quality: 5,
          reliability: 4,
          severity: "branch",
          message: "Strong finish",
        },
      });
    });
    renderDetail();

    await waitFor(() => expect(screen.getByText("Completion")).toBeInTheDocument());
    expect(screen.getByText("bob")).toBeInTheDocument();
    expect(screen.getByText(/Evidence: https:\/\/example.com\/pr\/1/)).toBeInTheDocument();
    expect(screen.getByText(/Validated by:/)).toBeInTheDocument();
    expect(screen.getByText("Stamp")).toBeInTheDocument();
    expect(screen.getByText(/Quality: 5 \/ Reliability: 4/)).toBeInTheDocument();
    expect(screen.getByText("Strong finish")).toBeInTheDocument();
  });

  it("treats submission rows without upstream links as mainline submissions", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.endsWith("/reject") && init?.method === "POST") return { detail: makeDetailResponse({ actions: [] }) };
      return makeDetailResponse({
        item: makeItem({ posted_by: "alice", status: "claimed" }),
        actions: ["reject"],
        upstream_prs: [{ is_upstream: false, rig_handle: "bob", status: "claimed" }],
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByText("bob")).toBeInTheDocument());
    expect(screen.getByText(/\(main\)/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "reject" }));
    await waitFor(() => {
      const rejectCalls = fetchFn.mock.calls.filter(([u]) => u.endsWith("/reject"));
      expect(rejectCalls.length).toBeGreaterThan(0);
    });
  });

  it("hides accept and close for submissions that are not in review", async () => {
    cleanupFetch = mockFetch((url) => {
      if (url.includes("/api/config")) return makeConfigResponse({ rig_handle: "alice" });
      return makeDetailResponse({
        item: makeItem({ posted_by: "alice", status: "claimed" }),
        actions: ["accept", "reject", "close"],
        upstream_prs: [
          {
            is_upstream: true,
            rig_handle: "charlie",
            status: "claimed",
            pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
          },
        ],
      });
    });
    renderDetail();

    await waitFor(() => expect(screen.getByText("charlie")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "reject" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "accept" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "close" })).not.toBeInTheDocument();
  });

  it("accepts upstream submissions via the upstream endpoint", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse({ rig_handle: "alice" });
      if (url.endsWith("/accept-upstream") && init?.method === "POST") {
        return { detail: makeDetailResponse({ actions: [] }) };
      }
      return makeDetailResponse({
        item: makeItem({ posted_by: "alice", status: "claimed" }),
        actions: ["accept", "reject", "close"],
        upstream_prs: [
          {
            is_upstream: true,
            rig_handle: "charlie",
            status: "in_review",
            evidence: "https://github.com/org/repo/pull/1",
            pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
          },
        ],
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "accept" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "accept" }));
    await waitFor(() => expect(screen.getByRole("dialog", { name: "Accept Submission" })).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText("Quality"), {
      target: { value: "4" },
    });
    fireEvent.change(screen.getByLabelText("Reliability"), {
      target: { value: "3" },
    });
    fireEvent.change(screen.getByLabelText("Severity"), {
      target: { value: "branch" },
    });
    fireEvent.change(screen.getByLabelText("Tags"), {
      target: { value: "go, sql" },
    });
    fireEvent.change(screen.getByLabelText("Message"), {
      target: { value: "Solid review" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Accept" }));
    await waitFor(() => {
      const acceptCalls = fetchFn.mock.calls.filter(([u]) => u.endsWith("/accept-upstream"));
      expect(acceptCalls.length).toBeGreaterThan(0);
      expect(acceptCalls[0]?.[1]?.body).toBe(
        JSON.stringify({
          rig_handle: "charlie",
          pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
          quality: 4,
          reliability: 3,
          severity: "branch",
          skill_tags: ["go", "sql"],
          message: "Solid review",
        }),
      );
    });
  }, 10000);

  it("opens an accept dialog for mainline acceptance and lets reliability default to quality", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith("/accept") && init?.method === "POST") {
        return { detail: makeDetailResponse({ actions: [] }) };
      }
      return makeDetailResponse({
        item: makeItem({ status: "in_review", claimed_by: "bob" }),
        actions: ["accept"],
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "accept" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "accept" }));
    expect(screen.getByRole("dialog", { name: "Accept Submission" })).toBeInTheDocument();
    expect(fetchFn.mock.calls.filter(([u]) => u.endsWith("/accept"))).toHaveLength(0);

    fireEvent.change(screen.getByLabelText("Quality"), {
      target: { value: "5" },
    });
    fireEvent.change(screen.getByLabelText("Tags"), {
      target: { value: "ops, review" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Accept" }));

    await waitFor(() => {
      const acceptCalls = fetchFn.mock.calls.filter(([u]) => u.endsWith("/accept"));
      expect(acceptCalls.length).toBeGreaterThan(0);
      expect(acceptCalls[0]?.[1]?.body).toBe(
        JSON.stringify({
          quality: 5,
          severity: "leaf",
          skill_tags: ["ops", "review"],
        }),
      );
    });
  }, 10000);

  it("reloads detail after a conflicting mainline accept", async () => {
    setActiveUpstream("hop/wl-commons");
    let detailCalls = 0;
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith("/accept") && init?.method === "POST") {
        return new Response(JSON.stringify({ error: "already completed" }), {
          status: 409,
        });
      }
      detailCalls += 1;
      return makeDetailResponse({
        item: makeItem({ status: detailCalls > 1 ? "completed" : "in_review" }),
        actions: detailCalls > 1 ? [] : ["accept"],
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "accept" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "accept" }));
    await userEvent.click(screen.getByRole("button", { name: "Accept" }));

    await waitFor(() => expect(detailCalls).toBe(2));
    expect(mocked.toastError).toHaveBeenCalledWith("This item was already claimed or changed by someone else");
    expect(screen.queryByRole("dialog", { name: "Accept Submission" })).not.toBeInTheDocument();
    expect(screen.getByText("completed")).toBeInTheDocument();
  });

  it("reloads detail after a conflicting upstream accept", async () => {
    setActiveUpstream("hop/wl-commons");
    let detailCalls = 0;
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.endsWith("/accept-upstream") && init?.method === "POST") {
        return new Response(JSON.stringify({ error: "already completed" }), {
          status: 409,
        });
      }
      detailCalls += 1;
      return makeDetailResponse({
        item: makeItem({
          posted_by: "alice",
          status: detailCalls > 1 ? "completed" : "claimed",
        }),
        actions: detailCalls > 1 ? [] : ["accept", "reject", "close"],
        upstream_prs:
          detailCalls > 1
            ? []
            : [
                {
                  is_upstream: true,
                  rig_handle: "charlie",
                  status: "in_review",
                  evidence: "https://github.com/org/repo/pull/1",
                  pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
                },
              ],
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "accept" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "accept" }));
    await userEvent.click(screen.getByRole("button", { name: "Accept" }));

    await waitFor(() => expect(detailCalls).toBe(2));
    expect(mocked.toastError).toHaveBeenCalledWith("This item was already claimed or changed by someone else");
    expect(screen.queryByRole("dialog", { name: "Accept Submission" })).not.toBeInTheDocument();
    expect(screen.getByText("completed")).toBeInTheDocument();
  });

  it("closes upstream submissions via the upstream endpoint", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse({ rig_handle: "alice" });
      if (url.endsWith("/close-upstream") && init?.method === "POST") {
        return { detail: makeDetailResponse({ actions: [] }) };
      }
      return makeDetailResponse({
        item: makeItem({ posted_by: "alice", status: "claimed" }),
        actions: ["accept", "reject", "close"],
        upstream_prs: [
          {
            is_upstream: true,
            rig_handle: "charlie",
            status: "in_review",
            evidence: "https://github.com/org/repo/pull/1",
            pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
          },
        ],
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "close" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "close" }));
    await waitFor(() => {
      const closeCalls = fetchFn.mock.calls.filter(([u]) => u.endsWith("/close-upstream"));
      expect(closeCalls.length).toBeGreaterThan(0);
      expect(closeCalls[0]?.[1]?.body).toBe(
        JSON.stringify({
          rig_handle: "charlie",
          pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
        }),
      );
    });
  });

  it("re-fetches detail after conflicting actions", async () => {
    setActiveUpstream("hop/wl-commons");
    let detailCalls = 0;
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.endsWith("/claim") && init?.method === "POST") {
        return new Response(JSON.stringify({ error: "already claimed" }), {
          status: 409,
        });
      }
      detailCalls += 1;
      return makeDetailResponse({
        item: makeItem({
          status: detailCalls > 1 ? "claimed" : "open",
          claimed_by: detailCalls > 1 ? "bob" : "",
        }),
        actions: detailCalls > 1 ? [] : ["claim"],
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "claim" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "claim" }));
    await waitFor(() => expect(detailCalls).toBe(2));
    expect(mocked.toastError).toHaveBeenCalledWith("This item was already claimed or changed by someone else");
    expect(screen.getByText("claimed")).toBeInTheDocument();
    expect(screen.getByText("bob")).toBeInTheDocument();
  });

  it("submits done evidence on Enter and clears the form", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.endsWith("/done") && init?.method === "POST") {
        return {
          detail: makeDetailResponse({
            item: makeItem({ status: "in_review", claimed_by: "alice" }),
            actions: [],
          }),
        };
      }
      return makeDetailResponse({ actions: ["done"] });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "done" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "done" }));
    const input = screen.getByPlaceholderText("https://github.com/...");
    fireEvent.change(input, { target: { value: "https://example.com/pr/2" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => {
      const doneCalls = fetchFn.mock.calls.filter(([u]) => u.endsWith("/done"));
      expect(doneCalls.length).toBeGreaterThan(0);
      expect(doneCalls[0]?.[1]?.body).toBe(JSON.stringify({ evidence: "https://example.com/pr/2" }));
    });
    await waitFor(() => expect(screen.queryByText("Submit for Review")).not.toBeInTheDocument());
  });

  it("cancels the done form and resets evidence", async () => {
    cleanupFetch = mockFetch((url) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      return makeDetailResponse({ actions: ["done"] });
    });
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "done" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "done" }));
    const input = screen.getByPlaceholderText("https://github.com/...");
    fireEvent.change(input, { target: { value: "https://example.com/pr/3" } });
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByText("Submit for Review")).not.toBeInTheDocument());

    await userEvent.click(screen.getByRole("button", { name: "done" }));
    expect(screen.getByPlaceholderText("https://github.com/...")).toHaveValue("");
  });

  it("discards branch changes after confirmation and navigates home", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.includes("/api/branches/wl/fix") && init?.method === "DELETE") return { status: "discarded" };
      return makeDetailResponse({
        branch: "wl/fix",
        branch_actions: ["discard"],
        delta: "1 file changed",
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "discard" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "discard" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Confirm" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(screen.getByTestId("home")).toBeInTheDocument());
    expect(fetchFn.mock.calls.some(([u]) => u.includes("/api/branches/wl/fix"))).toBe(true);
  });

  it("renders branch actions and calls their API endpoints", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn((url: string, init?: RequestInit) => {
      if (url.includes("/api/config")) return makeConfigResponse();
      if (url.includes("/api/branches/pr/wl/fix") && init?.method === "POST")
        return { url: "https://example.com/pr/1" };
      if (url.includes("/api/branches/apply/wl/fix") && init?.method === "POST") return { status: "applied" };
      return makeDetailResponse({
        branch: "wl/fix",
        branch_actions: ["submit_pr", "apply"],
        delta: "1 file changed",
      });
    });
    cleanupFetch = mockFetch(fetchFn);
    renderDetail();

    await waitFor(() => expect(screen.getByRole("button", { name: "submit pr" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "submit pr" }));
    await waitFor(() => {
      const submitCalls = fetchFn.mock.calls.filter(([u]) => u.includes("/api/branches/pr/wl/fix"));
      expect(submitCalls.length).toBeGreaterThan(0);
    });

    await userEvent.click(screen.getByRole("button", { name: "apply" }));
    await waitFor(() => {
      const applyCalls = fetchFn.mock.calls.filter(([u]) => u.includes("/api/branches/apply/wl/fix"));
      expect(applyCalls.length).toBeGreaterThan(0);
    });
  });
});
