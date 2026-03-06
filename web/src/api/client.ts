import * as Sentry from "@sentry/react";
import type {
  AuthStatusResponse,
  BrowseFilter,
  BrowseResponse,
  ConfigResponse,
  ConnectInput,
  ConnectResponse,
  ConnectSessionResponse,
  DashboardResponse,
  DetailResponse,
  ErrorResponse,
  JoinInput,
  JoinResponse,
  MutationResponse,
  PostInput,
  ProfileResponse,
  ProfileSummary,
  ScoreboardResponse,
  SettingsInput,
  UpdateInput,
} from "./types";

// --- Active upstream tracking (seeded from localStorage to avoid race with context) ---

const UPSTREAM_KEY = "wl_active";

function loadActiveUpstream(): string | null {
  try {
    return localStorage.getItem(UPSTREAM_KEY) || null;
  } catch {
    return null;
  }
}

let _activeUpstream: string | null = loadActiveUpstream();

export function setActiveUpstream(upstream: string | null) {
  _activeUpstream = upstream;
}

export function getActiveUpstream(): string | null {
  return _activeUpstream;
}

// --- Staging impersonation (persisted in sessionStorage to survive reloads) ---

const IMPERSONATE_KEY = "wl_impersonate";

function loadImpersonation(): string | null {
  try {
    return sessionStorage.getItem(IMPERSONATE_KEY) || null;
  } catch {
    return null;
  }
}

let _impersonateHandle: string | null = loadImpersonation();

export function setImpersonation(handle: string | null) {
  _impersonateHandle = handle;
  try {
    if (handle) {
      sessionStorage.setItem(IMPERSONATE_KEY, handle);
    } else {
      sessionStorage.removeItem(IMPERSONATE_KEY);
    }
  } catch {
    // sessionStorage unavailable — in-memory only
  }
}

export function getImpersonation(): string | null {
  return _impersonateHandle;
}

// --- API client ---

class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  // Inject X-Wasteland and X-Impersonate headers on non-auth API calls.
  let fetchInit = init;
  if ((_activeUpstream || _impersonateHandle) && !path.startsWith("/api/auth/")) {
    const headers = new Headers(init?.headers);
    if (_activeUpstream) {
      headers.set("X-Wasteland", _activeUpstream);
    }
    if (_impersonateHandle) {
      headers.set("X-Impersonate", _impersonateHandle);
    }
    fetchInit = { ...init, headers };
  }

  let resp: Response;
  try {
    resp = await fetch(path, fetchInit);
  } catch {
    throw new ApiError(0, "Network error — is the server running?");
  }

  // Redirect to /connect on auth failures in hosted mode.
  if (resp.status === 401 || resp.status === 412) {
    if (typeof window !== "undefined" && !window.location.pathname.startsWith("/connect")) {
      const returnTo = window.location.pathname + window.location.search;
      const reason = resp.status === 401 ? "&reason=expired" : "";
      window.location.href = `/connect?return_to=${encodeURIComponent(returnTo)}${reason}`;
      // Return a never-resolving promise to prevent callers from processing stale data.
      return new Promise<T>(() => {});
    }
  }

  let body: unknown;
  try {
    body = await resp.json();
  } catch {
    throw new ApiError(resp.status, resp.statusText || "Invalid response");
  }
  if (!resp.ok) {
    const err = new ApiError(resp.status, (body as ErrorResponse).error || resp.statusText);
    if (resp.status >= 500 && resp.status !== 503) {
      Sentry.captureException(err);
    }
    throw err;
  }
  return body as T;
}

function buildQuery(filter: BrowseFilter): string {
  const params = new URLSearchParams();
  if (filter.status) params.set("status", filter.status);
  if (filter.type) params.set("type", filter.type);
  if (filter.priority !== undefined && filter.priority >= 0) params.set("priority", String(filter.priority));
  if (filter.project) params.set("project", filter.project);
  if (filter.search) params.set("search", filter.search);
  if (filter.sort) params.set("sort", filter.sort);
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.view && filter.view !== "mine") params.set("view", filter.view);
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

export async function browse(filter: BrowseFilter = {}): Promise<BrowseResponse> {
  return request<BrowseResponse>(`/api/wanted${buildQuery(filter)}`);
}

export async function detail(id: string): Promise<DetailResponse> {
  return request<DetailResponse>(`/api/wanted/${id}`);
}

export async function dashboard(): Promise<DashboardResponse> {
  return request<DashboardResponse>("/api/dashboard");
}

export async function config(): Promise<ConfigResponse> {
  return request<ConfigResponse>("/api/config");
}

export async function scoreboard(): Promise<ScoreboardResponse> {
  return request<ScoreboardResponse>("/api/scoreboard");
}

export async function claim(id: string): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}/claim`, { method: "POST" });
}

export async function unclaim(id: string): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}/unclaim`, { method: "POST" });
}

export async function reject(id: string, reason?: string): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}/reject`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ reason: reason || "" }),
  });
}

export async function close(id: string): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}/close`, { method: "POST" });
}

export async function done(id: string, evidence: string): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}/done`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ evidence }),
  });
}

export async function accept(
  id: string,
  stamp?: {
    quality?: number;
    reliability?: number;
    severity?: string;
    skill_tags?: string[];
    message?: string;
  },
): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}/accept`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(stamp || {}),
  });
}

export async function acceptUpstream(
  id: string,
  rigHandle: string,
  stamp?: {
    quality?: number;
    reliability?: number;
    severity?: string;
    skill_tags?: string[];
    message?: string;
  },
): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}/accept-upstream`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ rig_handle: rigHandle, ...stamp }),
  });
}

export async function rejectUpstream(id: string, rigHandle: string): Promise<void> {
  await request<Record<string, string>>(`/api/wanted/${id}/reject-upstream`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ rig_handle: rigHandle }),
  });
}

export async function closeUpstream(id: string, rigHandle: string): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}/close-upstream`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ rig_handle: rigHandle }),
  });
}

export async function deleteItem(id: string): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}`, { method: "DELETE" });
}

export async function submitPR(branch: string): Promise<{ url: string }> {
  return request<{ url: string }>(`/api/branches/pr/${branch}`, { method: "POST" });
}

export async function applyBranch(branch: string): Promise<void> {
  await request<Record<string, string>>(`/api/branches/apply/${branch}`, { method: "POST" });
}

export async function discardBranch(branch: string): Promise<void> {
  await request<Record<string, string>>(`/api/branches/${branch}`, { method: "DELETE" });
}

export async function branchDiff(branch: string): Promise<{ diff: string }> {
  return request<{ diff: string }>(`/api/branches/diff/${branch}`);
}

export async function sync(): Promise<void> {
  await request<Record<string, string>>("/api/sync", { method: "POST" });
}

export async function createItem(input: PostInput): Promise<MutationResponse> {
  return request<MutationResponse>("/api/wanted", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function updateItem(id: string, input: UpdateInput): Promise<MutationResponse> {
  return request<MutationResponse>(`/api/wanted/${id}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function saveSettings(input: SettingsInput): Promise<void> {
  await request<Record<string, string>>("/api/settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

// --- Hosted auth functions ---

export async function authStatus(): Promise<AuthStatusResponse> {
  return request<AuthStatusResponse>("/api/auth/status");
}

export async function connectSession(endUserId: string): Promise<ConnectSessionResponse> {
  return request<ConnectSessionResponse>("/api/auth/connect-session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ end_user_id: endUserId }),
  });
}

export async function notifyConnect(input: ConnectInput): Promise<ConnectResponse> {
  return request<ConnectResponse>("/api/auth/connect", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function joinWasteland(input: JoinInput): Promise<JoinResponse> {
  return request<JoinResponse>("/api/auth/join", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function leaveWasteland(upstream: string): Promise<void> {
  await request<Record<string, string>>(`/api/auth/wastelands/${upstream}`, {
    method: "DELETE",
  });
}

export async function logout(): Promise<void> {
  await request<Record<string, string>>("/api/auth/logout", { method: "POST" });
}

export async function profile(handle: string): Promise<ProfileResponse> {
  return request<ProfileResponse>(`/api/profile/${encodeURIComponent(handle)}`);
}

export async function profileSearch(q: string): Promise<ProfileSummary[]> {
  return request<ProfileSummary[]>(`/api/profile?q=${encodeURIComponent(q)}`);
}

export function isConflictError(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409;
}

export { ApiError };
