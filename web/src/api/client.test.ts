import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ApiError,
  accept,
  acceptUpstream,
  applyBranch,
  authStatus,
  bootstrap,
  branchDiff,
  browse,
  claim,
  close,
  closeUpstream,
  config,
  createItem,
  dashboard,
  deleteItem,
  detail,
  discardBranch,
  done,
  leaveWasteland,
  reject,
  rejectUpstream,
  saveSettings,
  setActiveUpstream,
  setImpersonation,
  submitPR,
  sync,
  unclaim,
  updateItem,
} from "./client";

let cleanup: () => void;

function mockFetch(handler: (url: string, init?: RequestInit) => Response | object) {
  const original = globalThis.fetch;
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const result = handler(url, init);
    if (result instanceof Response) return result;
    return new Response(JSON.stringify(result), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  cleanup = () => {
    globalThis.fetch = original;
  };
}

afterEach(() => {
  setActiveUpstream(null);
  setImpersonation(null);
  localStorage.removeItem("wl_active");
  cleanup?.();
});

describe("request()", () => {
  it("throws ApiError(0) on network error", async () => {
    mockFetch(() => {
      throw new TypeError("fetch failed");
    });
    await expect(browse()).rejects.toThrow(ApiError);
    await expect(browse()).rejects.toMatchObject({
      status: 0,
      message: "Network error — is the server running?",
    });
  });

  it("throws ApiError with status and parsed error message on 400", async () => {
    mockFetch(() => new Response(JSON.stringify({ error: "bad request" }), { status: 400 }));
    await expect(browse()).rejects.toMatchObject({
      status: 400,
      message: "bad request",
    });
  });

  it("throws ApiError with statusText on non-JSON 500", async () => {
    mockFetch(
      () =>
        new Response("Internal Server Error", {
          status: 500,
          statusText: "Internal Server Error",
        }),
    );
    await expect(browse()).rejects.toMatchObject({
      status: 500,
      message: "Internal Server Error",
    });
  });

  it("returns parsed JSON on 200", async () => {
    mockFetch(() => ({ items: [] }));
    const result = await browse();
    expect(result).toEqual({ items: [] });
  });

  it("fails fast on writes when no active upstream is selected", async () => {
    mockFetch(() => ({ ok: true }));
    await expect(claim("abc")).rejects.toMatchObject({
      status: 0,
      message: "Not connected to a wasteland — please refresh the page",
    });
    expect(vi.mocked(globalThis.fetch)).not.toHaveBeenCalled();
  });
});

describe("buildQuery()", () => {
  beforeEach(() => {
    mockFetch((_url) => ({ items: [] }));
  });

  it("sends empty query for empty filter", async () => {
    await browse({});
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted", {
      headers: new Headers(),
    });
  });

  it("builds correct query string for full filter", async () => {
    await browse({
      status: "open",
      type: "bug",
      priority: 1,
      project: "x",
      search: "foo",
      sort: "alpha",
      limit: 10,
    });
    const url = vi.mocked(globalThis.fetch).mock.calls[0][0] as string;
    expect(url).toContain("status=open");
    expect(url).toContain("type=bug");
    expect(url).toContain("priority=1");
    expect(url).toContain("project=x");
    expect(url).toContain("search=foo");
    expect(url).toContain("sort=alpha");
    expect(url).toContain("limit=10");
  });

  it("includes priority: 0", async () => {
    await browse({ priority: 0 });
    const url = vi.mocked(globalThis.fetch).mock.calls[0][0] as string;
    expect(url).toContain("priority=0");
  });

  it("omits priority: -1", async () => {
    await browse({ priority: -1 });
    const url = vi.mocked(globalThis.fetch).mock.calls[0][0] as string;
    expect(url).not.toContain("priority");
  });

  it("includes view=all in query string", async () => {
    await browse({ view: "all" });
    const url = vi.mocked(globalThis.fetch).mock.calls[0][0] as string;
    expect(url).toContain("view=all");
  });

  it("omits view=mine (default)", async () => {
    await browse({ view: "mine" });
    const url = vi.mocked(globalThis.fetch).mock.calls[0][0] as string;
    expect(url).not.toContain("view");
  });

  it("includes view=upstream in query string", async () => {
    await browse({ view: "upstream" });
    const url = vi.mocked(globalThis.fetch).mock.calls[0][0] as string;
    expect(url).toContain("view=upstream");
  });
});

describe("hosted request headers", () => {
  beforeEach(() => {
    mockFetch(() => ({ items: [] }));
  });

  it("injects X-Wasteland on non-auth requests", async () => {
    setActiveUpstream("hop/wl-commons");
    await browse();
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted", {
      headers: new Headers({ "X-Wasteland": "hop/wl-commons" }),
    });
  });

  it("injects X-Impersonate on non-auth requests", async () => {
    setImpersonation("bob");
    await detail("abc");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted/abc", {
      headers: new Headers({ "X-Impersonate": "bob" }),
    });
  });

  it("injects both hosted headers on mutations", async () => {
    setActiveUpstream("hop/wl-commons");
    setImpersonation("bob");
    await claim("abc");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted/abc/claim", {
      method: "POST",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
        "X-Impersonate": "bob",
      }),
    });
  });

  it("reloads X-Wasteland from localStorage for writes", async () => {
    localStorage.setItem("wl_active", "hop/wl-commons");
    await claim("abc");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted/abc/claim", {
      method: "POST",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
  });

  it("does not inject hosted headers on auth endpoints", async () => {
    setActiveUpstream("hop/wl-commons");
    setImpersonation("bob");
    await authStatus();
    await leaveWasteland("hop/wl-commons");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenNthCalledWith(1, "/api/auth/status", undefined);
    expect(vi.mocked(globalThis.fetch)).toHaveBeenNthCalledWith(2, "/api/auth/wastelands/hop/wl-commons", {
      method: "DELETE",
    });
  });

  it("injects hosted headers on bootstrap", async () => {
    setActiveUpstream("hop/wl-commons");
    await bootstrap();
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/bootstrap", {
      headers: new Headers({ "X-Wasteland": "hop/wl-commons" }),
    });
  });
});

describe("API functions", () => {
  beforeEach(() => {
    mockFetch(() => ({ items: [], item: {}, ok: true }));
  });

  it("browse() calls GET /api/wanted", async () => {
    await browse();
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted", {
      headers: new Headers(),
    });
  });

  it("detail() calls GET /api/wanted/:id", async () => {
    await detail("abc");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted/abc", {
      headers: new Headers(),
    });
  });

  it("dashboard() calls GET /api/dashboard", async () => {
    await dashboard();
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/dashboard", {
      headers: new Headers(),
    });
  });

  it("config() calls GET /api/config", async () => {
    await config();
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/config", {
      headers: new Headers(),
    });
  });

  it("bootstrap() calls GET /api/bootstrap", async () => {
    await bootstrap();
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/bootstrap", {
      headers: new Headers(),
    });
  });

  it("claim() calls POST /api/wanted/:id/claim", async () => {
    setActiveUpstream("hop/wl-commons");
    await claim("abc");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted/abc/claim", {
      method: "POST",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
  });

  it("unclaim() calls POST /api/wanted/:id/unclaim", async () => {
    setActiveUpstream("hop/wl-commons");
    await unclaim("abc");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted/abc/unclaim", {
      method: "POST",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
  });

  it("reject() calls POST with reason", async () => {
    setActiveUpstream("hop/wl-commons");
    await reject("abc", "not good");
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/wanted/abc/reject");
    expect(call[1]?.method).toBe("POST");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({ reason: "not good" });
  });

  it("close() calls POST /api/wanted/:id/close", async () => {
    setActiveUpstream("hop/wl-commons");
    await close("abc");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted/abc/close", {
      method: "POST",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
  });

  it("done() calls POST with evidence", async () => {
    setActiveUpstream("hop/wl-commons");
    await done("abc", "http://evidence");
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/wanted/abc/done");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({
      evidence: "http://evidence",
    });
  });

  it("accept() calls POST with stamp data", async () => {
    setActiveUpstream("hop/wl-commons");
    await accept("abc", {
      quality: 5,
      reliability: 4,
      skill_tags: ["ops", "review"],
    });
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/wanted/abc/accept");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({
      quality: 5,
      reliability: 4,
      skill_tags: ["ops", "review"],
    });
  });

  it("acceptUpstream() calls POST with selector and stamp data", async () => {
    setActiveUpstream("hop/wl-commons");
    await acceptUpstream(
      "abc",
      {
        rig_handle: "charlie",
        pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
      },
      {
        quality: 5,
        reliability: 4,
        severity: "branch",
        skill_tags: ["go", "sql"],
      },
    );
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/wanted/abc/accept-upstream");
    expect(call[1]?.method).toBe("POST");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({
      rig_handle: "charlie",
      pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
      quality: 5,
      reliability: 4,
      severity: "branch",
      skill_tags: ["go", "sql"],
    });
  });

  it("rejectUpstream() calls POST with selector", async () => {
    setActiveUpstream("hop/wl-commons");
    await rejectUpstream("abc", {
      rig_handle: "charlie",
      pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
    });
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/wanted/abc/reject-upstream");
    expect(call[1]?.method).toBe("POST");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({
      rig_handle: "charlie",
      pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
    });
  });

  it("closeUpstream() calls POST with selector", async () => {
    setActiveUpstream("hop/wl-commons");
    await closeUpstream("abc", {
      rig_handle: "charlie",
      pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
    });
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/wanted/abc/close-upstream");
    expect(call[1]?.method).toBe("POST");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({
      rig_handle: "charlie",
      pr_url: "https://www.dolthub.com/repositories/org/db/pulls/1",
    });
  });

  it("deleteItem() calls DELETE /api/wanted/:id", async () => {
    setActiveUpstream("hop/wl-commons");
    await deleteItem("abc");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/wanted/abc", {
      method: "DELETE",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
  });

  it("submitPR() calls POST /api/branches/pr/:branch", async () => {
    setActiveUpstream("hop/wl-commons");
    mockFetch(() => ({ url: "https://dolthub.com/pr/1" }));
    const result = await submitPR("wl/fix");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/branches/pr/wl/fix", {
      method: "POST",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
    expect(result.url).toBe("https://dolthub.com/pr/1");
  });

  it("applyBranch() calls POST /api/branches/apply/:branch", async () => {
    setActiveUpstream("hop/wl-commons");
    await applyBranch("wl/fix");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/branches/apply/wl/fix", {
      method: "POST",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
  });

  it("discardBranch() calls DELETE /api/branches/:branch", async () => {
    setActiveUpstream("hop/wl-commons");
    await discardBranch("wl/fix");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/branches/wl/fix", {
      method: "DELETE",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
  });

  it("branchDiff() calls GET /api/branches/diff/:branch", async () => {
    mockFetch(() => ({ diff: "+line" }));
    const result = await branchDiff("wl/fix");
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/branches/diff/wl/fix", {
      headers: new Headers(),
    });
    expect(result.diff).toBe("+line");
  });

  it("sync() calls POST /api/sync", async () => {
    setActiveUpstream("hop/wl-commons");
    await sync();
    expect(vi.mocked(globalThis.fetch)).toHaveBeenCalledWith("/api/sync", {
      method: "POST",
      headers: new Headers({
        "X-Wasteland": "hop/wl-commons",
      }),
    });
  });

  it("createItem() calls POST /api/wanted with body", async () => {
    setActiveUpstream("hop/wl-commons");
    await createItem({ title: "New item" });
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/wanted");
    expect(call[1]?.method).toBe("POST");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({ title: "New item" });
  });

  it("updateItem() calls PATCH /api/wanted/:id with body", async () => {
    setActiveUpstream("hop/wl-commons");
    await updateItem("abc", { title: "Updated" });
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/wanted/abc");
    expect(call[1]?.method).toBe("PATCH");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({ title: "Updated" });
  });

  it("saveSettings() calls PUT /api/settings with body", async () => {
    setActiveUpstream("hop/wl-commons");
    await saveSettings({ mode: "pr", signing: true });
    const call = vi.mocked(globalThis.fetch).mock.calls[0];
    expect(call[0]).toBe("/api/settings");
    expect(call[1]?.method).toBe("PUT");
    expect(call[1]?.headers).toEqual(
      new Headers({
        "Content-Type": "application/json",
        "X-Wasteland": "hop/wl-commons",
      }),
    );
    expect(JSON.parse(call[1]?.body as string)).toEqual({
      mode: "pr",
      signing: true,
    });
  });
});
