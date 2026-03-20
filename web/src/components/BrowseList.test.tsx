import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CommandsContext } from "../hooks/useCommands";
import { makeBrowseResponse, makeSummary, mockFetch } from "../test-utils";
import { BrowseList } from "./BrowseList";

const defaultCommands = { commands: [], register: () => () => {} };

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
afterEach(() => cleanupFetch?.());

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

  it("j/k keyboard navigation moves selection", async () => {
    cleanupFetch = mockFetch(() =>
      makeBrowseResponse([makeSummary({ id: "1", title: "First" }), makeSummary({ id: "2", title: "Second" })]),
    );
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("First").length).toBeGreaterThan(0));

    fireEvent.keyDown(window, { key: "j" });
    // Table rows include header + data rows
    const rows = screen.getAllByRole("row");
    // First data row (index 1, since header is index 0)
    expect(rows[1]).toHaveAttribute("data-selected", "true");

    fireEvent.keyDown(window, { key: "j" });
    expect(rows[2]).toHaveAttribute("data-selected", "true");

    fireEvent.keyDown(window, { key: "k" });
    expect(rows[1]).toHaveAttribute("data-selected", "true");
  });

  it("Enter navigates to detail", async () => {
    cleanupFetch = mockFetch(() => makeBrowseResponse([makeSummary({ id: "abc", title: "Item" })]));
    renderBrowse();
    await waitFor(() => expect(screen.getAllByText("Item").length).toBeGreaterThan(0));

    fireEvent.keyDown(window, { key: "j" });
    fireEvent.keyDown(window, { key: "Enter" });
    await waitFor(() => expect(screen.getByTestId("detail")).toBeInTheDocument());
  });

  it("starts background polling after the initial load", async () => {
    let pollCallback: (() => void) | null = null;
    const hiddenSpy = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval").mockImplementation(((
      fn: TimerHandler,
      delay?: number,
    ) => {
      if (delay === 30_000) pollCallback = fn as () => void;
      return 1 as ReturnType<typeof setInterval>;
    }) as typeof setInterval);
    const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval").mockImplementation(() => {});
    cleanupFetch = mockFetch(() =>
      makeBrowseResponse([makeSummary({ id: "1", title: "First" }), makeSummary({ id: "2", title: "Second" })]),
    );

    try {
      renderBrowse();
      await waitFor(() => expect(screen.getAllByText("First").length).toBeGreaterThan(0));
      await waitFor(() => expect(pollCallback).not.toBeNull());
    } finally {
      hiddenSpy.mockRestore();
      setIntervalSpy.mockRestore();
      clearIntervalSpy.mockRestore();
    }
  });

  it("preserves the selected item when polling reorders results", async () => {
    let fetchCount = 0;
    let pollCallback: (() => void) | null = null;
    const hiddenSpy = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    const setIntervalSpy = vi.spyOn(globalThis, "setInterval").mockImplementation(((
      fn: TimerHandler,
      delay?: number,
    ) => {
      if (delay === 30_000) pollCallback = fn as () => void;
      return 1 as ReturnType<typeof setInterval>;
    }) as typeof setInterval);
    const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval").mockImplementation(() => {});
    cleanupFetch = mockFetch(() => {
      fetchCount += 1;
      return fetchCount === 1
        ? makeBrowseResponse([
            makeSummary({ id: "1", title: "Alpha" }),
            makeSummary({ id: "2", title: "Bravo" }),
            makeSummary({ id: "3", title: "Charlie" }),
          ])
        : makeBrowseResponse([
            makeSummary({ id: "3", title: "Charlie" }),
            makeSummary({ id: "1", title: "Alpha" }),
            makeSummary({ id: "2", title: "Bravo" }),
          ]);
    });

    try {
      renderBrowse();
      await waitFor(() => expect(screen.getAllByText("Alpha").length).toBeGreaterThan(0));
      await waitFor(() => expect(pollCallback).not.toBeNull());

      fireEvent.keyDown(window, { key: "j" });
      fireEvent.keyDown(window, { key: "j" });
      expect(screen.getAllByRole("row")[2]).toHaveAttribute("data-selected", "true");

      await act(async () => {
        pollCallback?.();
      });
      await waitFor(() => expect(fetchCount).toBe(2));

      const selectedRow = screen.getAllByRole("row")[3];
      expect(selectedRow).toHaveAttribute("data-selected", "true");
      expect(selectedRow).toHaveTextContent("Bravo");
    } finally {
      hiddenSpy.mockRestore();
      setIntervalSpy.mockRestore();
      clearIntervalSpy.mockRestore();
    }
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
});
