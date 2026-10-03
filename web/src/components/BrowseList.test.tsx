import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { toast } from "sonner";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveUpstream } from "../api/client";
import { CommandsContext } from "../hooks/useCommands";
import { makeBrowseResponse, makeSummary, mockFetch } from "../test-utils";
import { BrowseList } from "./BrowseList";

const defaultCommands = { commands: [], register: () => () => {} };

function deferredResponse() {
  let resolve!: (response: ReturnType<typeof makeBrowseResponse> | Response) => void;
  const promise = new Promise<ReturnType<typeof makeBrowseResponse> | Response>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

vi.mock("../context/WastelandContext", () => ({
  useWasteland: () => ({
    active: null,
    authenticated: false,
    environment: undefined,
    ready: true,
    viewerRigHandle: undefined,
    wastelands: [],
    switchTo: () => {},
    refresh: async () => {},
  }),
}));

function renderBrowse() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <CommandsContext.Provider value={defaultCommands}>
        <Routes>
          <Route path="/" element={<BrowseList />} />
          <Route path="/wanted/:id" element={<div data-testid="detail">Detail</div>} />
        </Routes>
      </CommandsContext.Provider>
    </MemoryRouter>,
  );
}

let cleanupFetch: () => void;
afterEach(() => {
  cleanupFetch?.();
  setActiveUpstream(null);
  localStorage.removeItem("wl_active");
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("BrowseList", () => {
  it("renders table from browse() response", async () => {
    cleanupFetch = mockFetch(() =>
      makeBrowseResponse([
        makeSummary({ id: "1", title: "Task One", status: "open", priority: 1 }),
        makeSummary({ id: "2", title: "Task Two", status: "claimed", priority: 2 }),
      ]),
    );
    renderBrowse();
    // Items appear in both table and card views, so use getAllByText
    await waitFor(() => expect(screen.getAllByText("Task One").length).toBeGreaterThan(0));
    expect(screen.getAllByText("Task Two").length).toBeGreaterThan(0);
  });

  it("shows skeleton while loading", () => {
    cleanupFetch = mockFetch(() => new Promise(() => {}));
    renderBrowse();
    expect(screen.queryByText("Wanted Board")).toBeInTheDocument();
    expect(screen.queryByText("Task One")).not.toBeInTheDocument();
  });

  it("shows empty state when no items", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([]));
    renderBrowse();
    await waitFor(() => expect(screen.getByText("No items found")).toBeInTheDocument());
  });

  it("shows error on fetch failure", async () => {
    cleanupFetch = mockFetch(() => new Response(JSON.stringify({ error: "server error" }), { status: 500 }));
    renderBrowse();
    await waitFor(() => expect(screen.getByText("server error")).toBeInTheDocument());
  });

  it("keeps the latest search results when an older search finishes later", async () => {
    const older = deferredResponse();
    cleanupFetch = mockFetch((url) => {
      const search = new URL(url, "https://wasteland.test").searchParams.get("search");
      if (search === "older") return older.promise;
      if (search === "latest") return makeBrowseResponse([makeSummary({ id: "latest", title: "Latest result" })]);
      return makeBrowseResponse([makeSummary({ id: "initial", title: "Initial result" })]);
    });
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Initial result").length).toBeGreaterThan(0));

    fireEvent.change(screen.getByLabelText("Search items"), { target: { value: "older" } });
    fireEvent.change(screen.getByLabelText("Search items"), { target: { value: "latest" } });
    await waitFor(() => expect(screen.getAllByText("Latest result").length).toBeGreaterThan(0));
    await act(async () => {
      older.resolve({
        items: [makeSummary({ id: "older", title: "Older result" })],
        warning: "Obsolete cached response",
      });
    });

    expect(screen.getByLabelText("Search items")).toHaveValue("latest");
    expect(screen.getAllByText("Latest result").length).toBeGreaterThan(0);
    expect(screen.queryByText("Older result")).not.toBeInTheDocument();
    expect(screen.queryByText("Obsolete cached response")).not.toBeInTheDocument();
  });

  it("ignores an older search failure after the latest search succeeds", async () => {
    const older = deferredResponse();
    const toastError = vi.spyOn(toast, "error");
    cleanupFetch = mockFetch((url) => {
      const search = new URL(url, "https://wasteland.test").searchParams.get("search");
      if (search === "older") return older.promise;
      if (search === "latest") return makeBrowseResponse([makeSummary({ id: "latest", title: "Latest result" })]);
      return makeBrowseResponse([makeSummary({ id: "initial", title: "Initial result" })]);
    });
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Initial result").length).toBeGreaterThan(0));

    fireEvent.change(screen.getByLabelText("Search items"), { target: { value: "older" } });
    fireEvent.change(screen.getByLabelText("Search items"), { target: { value: "latest" } });
    await waitFor(() => expect(screen.getAllByText("Latest result").length).toBeGreaterThan(0));
    await act(async () => {
      older.resolve(new Response(JSON.stringify({ error: "Obsolete search failure" }), { status: 500 }));
    });

    expect(screen.getAllByText("Latest result").length).toBeGreaterThan(0);
    expect(screen.queryByText("Obsolete search failure")).not.toBeInTheDocument();
    expect(toastError).not.toHaveBeenCalled();
  });

  it("shows a non-fatal warning when browse returns cached data", async () => {
    cleanupFetch = mockFetch(() => ({
      items: [makeSummary({ id: "1", title: "Cached Item", status: "open", priority: 1 })],
      warning: "Upstream database is temporarily unavailable. Showing cached data.",
    }));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Cached Item").length).toBeGreaterThan(0));
    expect(screen.getByText(/Showing cached data/)).toBeInTheDocument();
  });

  it("j/k keyboard navigation moves selection", async () => {
    cleanupFetch = mockFetch(() =>
      makeBrowseResponse([makeSummary({ id: "1", title: "First" }), makeSummary({ id: "2", title: "Second" })]),
    );
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("First").length).toBeGreaterThan(0));
    await act(async () => {
      await Promise.resolve();
    });

    fireEvent.keyDown(window, { key: "j" });
    await waitFor(() => {
      const rows = screen.getAllByRole("row");
      expect(rows[1]).toHaveAttribute("data-selected", "true");
    });

    fireEvent.keyDown(window, { key: "j" });
    await waitFor(() => {
      const rows = screen.getAllByRole("row");
      expect(rows[2]).toHaveAttribute("data-selected", "true");
    });

    fireEvent.keyDown(window, { key: "k" });
    await waitFor(() => {
      const rows = screen.getAllByRole("row");
      expect(rows[1]).toHaveAttribute("data-selected", "true");
    });
  }, 10000);

  it("Enter navigates to detail", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([makeSummary({ id: "abc", title: "Item" })]));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Item").length).toBeGreaterThan(0));
    await act(async () => {
      await Promise.resolve();
    });

    fireEvent.keyDown(window, { key: "j" });
    await waitFor(() => {
      const rows = screen.getAllByRole("row");
      expect(rows[1]).toHaveAttribute("data-selected", "true");
    });
    fireEvent.keyDown(window, { key: "Enter" });
    await waitFor(() => expect(screen.getByTestId("detail")).toBeInTheDocument(), { timeout: 5000 });
  }, 10000);

  it("slash focuses the search input", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([makeSummary({ id: "1", title: "Item" })]));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Item").length).toBeGreaterThan(0));

    fireEvent.keyDown(window, { key: "/" });

    expect(screen.getByLabelText("Search items")).toHaveFocus();
  });

  it("keyboard shortcuts are ignored while typing in the search input", async () => {
    cleanupFetch = mockFetch(() =>
      makeBrowseResponse([makeSummary({ id: "1", title: "First" }), makeSummary({ id: "2", title: "Second" })]),
    );
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("First").length).toBeGreaterThan(0));

    const search = screen.getByLabelText("Search items");
    search.focus();
    fireEvent.keyDown(search, { key: "j", bubbles: true });
    fireEvent.keyDown(search, { key: "c", bubbles: true });

    const rows = screen.getAllByRole("row");
    expect(rows[1]).not.toHaveAttribute("data-selected", "true");
    expect(screen.queryByText("Post New Item")).not.toBeInTheDocument();
  });

  it("c opens WantedForm", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([makeSummary()]));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Fix the thing").length).toBeGreaterThan(0));

    fireEvent.keyDown(window, { key: "c" });
    expect(screen.getByText("Post New Item")).toBeInTheDocument();
  });

  it("+ Post button opens WantedForm", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([]));
    renderBrowse();
    await waitFor(() => expect(screen.getByText("No items found")).toBeInTheDocument());
    fireEvent.click(screen.getAllByText("+ Post")[0]);
    expect(screen.getByText("Post New Item")).toBeInTheDocument();
  });

  it("shows pending tag when pending_count > 0", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([makeSummary({ id: "1", title: "PR Item", pending_count: 1 })]));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("pending").length).toBeGreaterThan(0));
  });

  it("shows pending count badge when pending_count > 1", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([makeSummary({ id: "1", title: "Multi PR", pending_count: 3 })]));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText(/pending/).length).toBeGreaterThan(0));
    expect(screen.getAllByText(/×3/).length).toBeGreaterThan(0);
  });

  it("renders pending submission details and links", async () => {
    cleanupFetch = mockFetch(() =>
      makeBrowseResponse([
        makeSummary({
          id: "1",
          title: "Competing PRs",
          pending_count: 2,
          pending_items: [
            { rig_handle: "alice", status: "in_review", pr_url: "https://example.com/pr/1" },
            { rig_handle: "bob", status: "claimed", branch_url: "https://example.com/branch/wl/bob/1" },
          ],
        }),
      ]),
    );
    renderBrowse();

    await waitFor(() => expect(screen.getAllByText("Competing submissions").length).toBeGreaterThan(0));
    expect(screen.getAllByText("alice").length).toBeGreaterThan(0);
    expect(screen.getAllByText("in review").length).toBeGreaterThan(0);
    expect(screen.getAllByRole("link", { name: "PR" })[0]).toHaveAttribute("href", "https://example.com/pr/1");
    expect(screen.getAllByText("bob").length).toBeGreaterThan(0);
    expect(screen.getAllByText("claimed").length).toBeGreaterThan(0);
    expect(screen.getAllByRole("link", { name: "branch" })[0]).toHaveAttribute(
      "href",
      "https://example.com/branch/wl/bob/1",
    );
  });

  it("does not show pending tag when pending_count is 0", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([makeSummary({ id: "1", title: "No PR", pending_count: 0 })]));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("No PR").length).toBeGreaterThan(0));
    expect(screen.queryByText("pending")).not.toBeInTheDocument();
  });

  it("i key opens inference form", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([makeSummary()]));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Fix the thing").length).toBeGreaterThan(0));

    fireEvent.keyDown(window, { key: "i" });
    expect(screen.getByText("Post Inference Job")).toBeInTheDocument();
  });

  it("saving a posted item closes the form and reloads the list", async () => {
    setActiveUpstream("hop/wl-commons");
    let browseCalls = 0;
    cleanupFetch = mockFetch((url, init) => {
      if (url === "/api/wanted" && !init?.method) {
        browseCalls += 1;
        if (browseCalls === 1) return makeBrowseResponse([]);
        return makeBrowseResponse([makeSummary({ id: "new-1", title: "Fresh Task" })]);
      }
      if (url === "/api/wanted" && init?.method === "POST") {
        return {
          detail: {
            item: {
              id: "new-1",
              title: "Fresh Task",
              status: "open",
              priority: 2,
              effort_level: "medium",
              posted_by: "alice",
              type: "feature",
            },
            actions: ["claim"],
            branch_actions: [],
            mode: "wild-west",
          },
        };
      }
      throw new Error(`unexpected request: ${url}`);
    });
    renderBrowse();

    await waitFor(() => expect(screen.getByText("No items found")).toBeInTheDocument());
    fireEvent.click(screen.getAllByText("+ Post")[0]);
    fireEvent.change(screen.getByPlaceholderText("What needs to be done?"), { target: { value: "Fresh Task" } });
    fireEvent.click(screen.getByRole("button", { name: "Post" }));

    await waitFor(() => expect(screen.getAllByText("Fresh Task").length).toBeGreaterThan(0));
    expect(screen.queryByText("Post New Item")).not.toBeInTheDocument();
    expect(browseCalls).toBe(2);
  });

  it("saving an inferred item closes the form and reloads the list", async () => {
    setActiveUpstream("hop/wl-commons");
    let browseCalls = 0;
    cleanupFetch = mockFetch((url, init) => {
      if (url === "/api/wanted" && !init?.method) {
        browseCalls += 1;
        if (browseCalls === 1) return makeBrowseResponse([]);
        return makeBrowseResponse([makeSummary({ id: "infer-1", title: "infer: Generate summary" })]);
      }
      if (url === "/api/wanted" && init?.method === "POST") {
        return {
          detail: {
            item: {
              id: "infer-1",
              title: "infer: Generate summary",
              status: "open",
              priority: 2,
              effort_level: "small",
              posted_by: "alice",
              type: "inference",
            },
            actions: ["claim"],
            branch_actions: [],
            mode: "wild-west",
          },
        };
      }
      throw new Error(`unexpected request: ${url}`);
    });
    renderBrowse();

    await waitFor(() => expect(screen.getByText("No items found")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "+ Infer" }));
    fireEvent.change(screen.getByPlaceholderText("What should the model generate?"), {
      target: { value: "Generate summary" },
    });
    fireEvent.change(screen.getByPlaceholderText("llama3.2:1b"), { target: { value: "gpt-test" } });
    fireEvent.click(screen.getByRole("button", { name: "Post Job" }));

    await waitFor(() => expect(screen.getAllByText("infer: Generate summary").length).toBeGreaterThan(0));
    expect(screen.queryByText("Post Inference Job")).not.toBeInTheDocument();
    expect(browseCalls).toBe(2);
  });

  it("background polling refreshes the item list after the first load", async () => {
    let calls = 0;
    let poll: (() => void) | undefined;
    vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    const realSetInterval = globalThis.setInterval;
    vi.spyOn(globalThis, "setInterval").mockImplementation(((callback: TimerHandler, delay?: number) => {
      if (delay === 30_000) {
        poll = callback as () => void;
        return 1 as unknown as ReturnType<typeof setInterval>;
      }
      return realSetInterval(callback, delay);
    }) as typeof setInterval);
    cleanupFetch = mockFetch((url, init) => {
      if (url === "/api/wanted" && !init?.method) {
        calls += 1;
        if (calls === 1) return makeBrowseResponse([makeSummary({ id: "1", title: "Old Task" })]);
        return makeBrowseResponse([makeSummary({ id: "1", title: "Polled Task" })]);
      }
      throw new Error(`unexpected request: ${url}`);
    });
    renderBrowse();

    await waitFor(() => expect(screen.getAllByText("Old Task").length).toBeGreaterThan(0));
    await waitFor(() => expect(poll).toBeTypeOf("function"), { timeout: 5000 });

    await act(async () => {
      poll?.();
      await Promise.resolve();
    });

    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    await waitFor(() => expect(screen.getAllByText("Polled Task").length).toBeGreaterThan(0));
    expect(calls).toBeGreaterThanOrEqual(2);
  });

  it("keeps new search results when an in-flight poll for old filters finishes", async () => {
    const olderPoll = deferredResponse();
    let initialFilterCalls = 0;
    let poll: (() => void) | undefined;
    vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    const realSetInterval = globalThis.setInterval;
    vi.spyOn(globalThis, "setInterval").mockImplementation(((callback: TimerHandler, delay?: number) => {
      if (delay === 30_000) {
        poll = callback as () => void;
        return 1 as unknown as ReturnType<typeof setInterval>;
      }
      return realSetInterval(callback, delay);
    }) as typeof setInterval);
    cleanupFetch = mockFetch((url) => {
      if (new URL(url, "https://wasteland.test").searchParams.get("search") === "latest") {
        return makeBrowseResponse([makeSummary({ id: "latest", title: "Latest result" })]);
      }
      initialFilterCalls += 1;
      if (initialFilterCalls === 1)
        return makeBrowseResponse([makeSummary({ id: "initial", title: "Initial result" })]);
      return olderPoll.promise;
    });
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Initial result").length).toBeGreaterThan(0));
    await waitFor(() => expect(poll).toBeTypeOf("function"));

    await act(async () => {
      poll?.();
    });
    expect(initialFilterCalls).toBe(2);
    fireEvent.change(screen.getByLabelText("Search items"), { target: { value: "latest" } });
    await waitFor(() => expect(screen.getAllByText("Latest result").length).toBeGreaterThan(0));
    await act(async () => {
      olderPoll.resolve(makeBrowseResponse([makeSummary({ id: "older", title: "Older polled result" })]));
    });

    expect(screen.getByLabelText("Search items")).toHaveValue("latest");
    expect(screen.getAllByText("Latest result").length).toBeGreaterThan(0);
    expect(screen.queryByText("Older polled result")).not.toBeInTheDocument();
  });
});
