import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveUpstream } from "../api/client";
import type { WantedItem } from "../api/types";
import { makeDetailResponse, mockFetch, renderWithRouter } from "../test-utils";
import { WantedForm } from "./WantedForm";

const onClose = vi.fn();
const onSaved = vi.fn();
let cleanupFetch: () => void;

afterEach(() => {
  setActiveUpstream(null);
  localStorage.removeItem("wl_active");
  cleanupFetch?.();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

function makeEditItem(): WantedItem {
  return {
    id: "item-1",
    title: "Existing Title",
    description: "Existing desc",
    project: "myproj",
    type: "bug",
    priority: 1,
    effort_level: "small",
    tags: ["tag1", "tag2"],
    status: "open",
    posted_by: "alice",
  };
}

type FetchFn = (url: string, init?: RequestInit) => object;

describe("WantedForm", () => {
  it("create mode: renders empty form", () => {
    cleanupFetch = mockFetch(() => ({}));
    renderWithRouter(<WantedForm onClose={onClose} onSaved={onSaved} />);
    expect(screen.getByText("Post New Item")).toBeInTheDocument();
    const titleInput = screen.getByPlaceholderText("What needs to be done?");
    expect(titleInput).toHaveValue("");
  });

  it("create mode: calls createItem on submit", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn<FetchFn>(() => ({ detail: null }));
    cleanupFetch = mockFetch(fetchFn);
    renderWithRouter(<WantedForm onClose={onClose} onSaved={onSaved} />);

    fireEvent.change(screen.getByPlaceholderText("What needs to be done?"), {
      target: { value: "New task" },
    });
    fireEvent.click(screen.getByText("Post"));
    await waitFor(() => {
      const postCalls = fetchFn.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(postCalls.length).toBeGreaterThan(0);
      expect(postCalls[0][0]).toBe("/api/wanted");
    });
  }, 10000);

  it("edit mode: pre-populates fields", () => {
    cleanupFetch = mockFetch(() => ({}));
    renderWithRouter(<WantedForm item={makeEditItem()} onClose={onClose} onSaved={onSaved} />);
    expect(screen.getByText("Edit Item")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("What needs to be done?")).toHaveValue("Existing Title");
    expect(screen.getByPlaceholderText("Details, context, acceptance criteria...")).toHaveValue("Existing desc");
  });

  it("edit mode: calls updateItem on submit", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn<FetchFn>(() => ({ detail: null }));
    cleanupFetch = mockFetch(fetchFn);
    renderWithRouter(<WantedForm item={makeEditItem()} onClose={onClose} onSaved={onSaved} />);

    fireEvent.click(screen.getByText("Update"));
    await waitFor(() => {
      const patchCalls = fetchFn.mock.calls.filter(([, init]) => init?.method === "PATCH");
      expect(patchCalls.length).toBeGreaterThan(0);
      expect(patchCalls[0][0]).toBe("/api/wanted/item-1");
    });
  });

  it("delivers a completed edit after unmount without stale close callbacks or toasts", async () => {
    setActiveUpstream("hop/wl-commons");
    let resolveSave!: (response: object) => void;
    const pendingSave = new Promise<object>((resolve) => {
      resolveSave = resolve;
    });
    const toastSuccess = vi.spyOn(toast, "success");
    const toastError = vi.spyOn(toast, "error");
    cleanupFetch = mockFetch(() => pendingSave);
    const view = renderWithRouter(<WantedForm item={makeEditItem()} onClose={onClose} onSaved={onSaved} />);
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    view.unmount();
    const savedDetail = makeDetailResponse({ pr_url: "https://example.com/pr/1" });

    await act(async () => resolveSave({ detail: savedDetail }));

    expect(onSaved).toHaveBeenCalledExactlyOnceWith(savedDetail);
    expect(onClose).not.toHaveBeenCalled();
    expect(toastSuccess).not.toHaveBeenCalled();
    expect(toastError).not.toHaveBeenCalled();
  });

  it("parses tags from comma-separated input", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn<FetchFn>(() => ({ detail: null }));
    cleanupFetch = mockFetch(fetchFn);
    renderWithRouter(<WantedForm onClose={onClose} onSaved={onSaved} />);

    fireEvent.change(screen.getByPlaceholderText("What needs to be done?"), {
      target: { value: "Tagged task" },
    });
    fireEvent.change(screen.getByPlaceholderText("tag1, tag2, ..."), {
      target: { value: " foo , bar , , baz " },
    });
    fireEvent.click(screen.getByText("Post"));
    await waitFor(() => {
      const postCalls = fetchFn.mock.calls.filter(([, init]) => init?.method === "POST");
      const body = JSON.parse(postCalls[0][1]?.body as string);
      expect(body.tags).toEqual(["foo", "bar", "baz"]);
    });
  });

  it("empty title disables submit", () => {
    cleanupFetch = mockFetch(() => ({}));
    renderWithRouter(<WantedForm onClose={onClose} onSaved={onSaved} />);
    expect(screen.getByText("Post")).toBeDisabled();
  });

  it("Escape closes the form", () => {
    cleanupFetch = mockFetch(() => ({}));
    renderWithRouter(<WantedForm onClose={onClose} onSaved={onSaved} />);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  it("Cmd+Enter submits", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn<FetchFn>(() => ({ detail: null }));
    cleanupFetch = mockFetch(fetchFn);
    renderWithRouter(<WantedForm onClose={onClose} onSaved={onSaved} />);
    fireEvent.change(screen.getByPlaceholderText("What needs to be done?"), {
      target: { value: "Task via shortcut" },
    });
    fireEvent.keyDown(window, { key: "Enter", metaKey: true });
    await waitFor(() => {
      const postCalls = fetchFn.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(postCalls.length).toBeGreaterThan(0);
    });
  });
});

describe("WantedForm inference mode", () => {
  it("renders inference-specific fields and hides generic fields", () => {
    cleanupFetch = mockFetch(() => ({}));
    renderWithRouter(<WantedForm mode="inference" onClose={onClose} onSaved={onSaved} />);
    expect(screen.getByText("Post Inference Job")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("What should the model generate?")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("llama3.2:1b")).toBeInTheDocument();
    expect(screen.queryByPlaceholderText("What needs to be done?")).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText("tag1, tag2, ...")).not.toBeInTheDocument();
  });

  it("submit disabled when prompt or model empty", () => {
    cleanupFetch = mockFetch(() => ({}));
    renderWithRouter(<WantedForm mode="inference" onClose={onClose} onSaved={onSaved} />);
    expect(screen.getByText("Post Job")).toBeDisabled();
  });

  it("submit disabled when only prompt filled", async () => {
    cleanupFetch = mockFetch(() => ({}));
    renderWithRouter(<WantedForm mode="inference" onClose={onClose} onSaved={onSaved} />);
    fireEvent.change(screen.getByPlaceholderText("What should the model generate?"), {
      target: { value: "test prompt" },
    });
    expect(screen.getByText("Post Job")).toBeDisabled();
  });

  it("sends correctly shaped PostInput", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn<FetchFn>(() => ({ detail: null }));
    cleanupFetch = mockFetch(fetchFn);
    renderWithRouter(<WantedForm mode="inference" onClose={onClose} onSaved={onSaved} />);

    fireEvent.change(screen.getByPlaceholderText("What should the model generate?"), {
      target: { value: "Summarize this" },
    });
    fireEvent.change(screen.getByPlaceholderText("llama3.2:1b"), {
      target: { value: "llama3.2:1b" },
    });
    fireEvent.click(screen.getByText("Post Job"));

    await waitFor(() => {
      const postCalls = fetchFn.mock.calls.filter(([, init]) => init?.method === "POST");
      expect(postCalls.length).toBeGreaterThan(0);
      const body = JSON.parse(postCalls[0][1]?.body as string);
      expect(body.title).toBe("infer: Summarize this");
      expect(body.type).toBe("inference");
      expect(body.priority).toBe(2);
      expect(body.effort_level).toBe("small");
      const desc = JSON.parse(body.description);
      expect(desc.prompt).toBe("Summarize this");
      expect(desc.model).toBe("llama3.2:1b");
      expect(desc.seed).toBe(42);
      expect(desc.max_tokens).toBe(0);
    });
  });

  it("advanced toggle shows seed and max tokens", async () => {
    cleanupFetch = mockFetch(() => ({}));
    renderWithRouter(<WantedForm mode="inference" onClose={onClose} onSaved={onSaved} />);

    expect(screen.queryByText("Seed")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("+ Advanced"));
    expect(screen.getByText("Seed")).toBeInTheDocument();
    expect(screen.getByText("Max Tokens")).toBeInTheDocument();
    expect(screen.getByDisplayValue("42")).toBeInTheDocument();
    expect(screen.getByDisplayValue("0")).toBeInTheDocument();
  });

  it("truncates long prompts in title to 60 chars", async () => {
    setActiveUpstream("hop/wl-commons");
    const fetchFn = vi.fn<FetchFn>(() => ({ detail: null }));
    cleanupFetch = mockFetch(fetchFn);
    renderWithRouter(<WantedForm mode="inference" onClose={onClose} onSaved={onSaved} />);

    const longPrompt = "a".repeat(80);
    fireEvent.change(screen.getByPlaceholderText("What should the model generate?"), {
      target: { value: longPrompt },
    });
    fireEvent.change(screen.getByPlaceholderText("llama3.2:1b"), {
      target: { value: "llama3.2:1b" },
    });
    fireEvent.click(screen.getByText("Post Job"));

    await waitFor(() => {
      const postCalls = fetchFn.mock.calls.filter(([, init]) => init?.method === "POST");
      const body = JSON.parse(postCalls[0][1]?.body as string);
      expect(body.title).toBe(`infer: ${"a".repeat(60)}...`);
    });
  });
});
