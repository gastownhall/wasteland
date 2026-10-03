import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ConnectSessionInput } from "../api/types";
import { ConnectPage } from "./ConnectPage";

const mocked = vi.hoisted(() => ({
  authStatus: vi.fn(),
  connectSession: vi.fn(),
  notifyConnect: vi.fn(),
  joinWasteland: vi.fn(),
  redeemConnectToken: vi.fn(),
  refresh: vi.fn(),
  toastSuccess: vi.fn(),
  toastWarning: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("../api/client", () => ({
  authStatus: mocked.authStatus,
  connectSession: mocked.connectSession,
  notifyConnect: mocked.notifyConnect,
  joinWasteland: mocked.joinWasteland,
}));

vi.mock("../api/directAuthService", () => ({
  buildConnectTokenMetadata: vi.fn((input: ConnectSessionInput) => ({
    rig_handle: input.rig_handle,
    wastelands: [
      {
        upstream: input.upstream,
        fork_org: input.fork_org,
        fork_db: input.fork_db,
        mode: input.mode ?? "pr",
        signing: input.signing ?? true,
      },
    ],
  })),
  redeemConnectToken: mocked.redeemConnectToken,
}));

vi.mock("../context/WastelandContext", () => ({
  useWasteland: () => ({
    wastelands: [],
    active: null,
    authenticated: false,
    environment: undefined,
    switchTo: () => {},
    refresh: mocked.refresh,
  }),
}));

vi.mock("sonner", () => ({
  toast: {
    success: mocked.toastSuccess,
    warning: mocked.toastWarning,
    error: mocked.toastError,
  },
}));

function LocationProbe() {
  const { pathname, search, hash } = useLocation();
  return <div data-testid="location">{pathname + search + hash}</div>;
}

function renderConnect(initialEntry: string) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <LocationProbe />
      <Routes>
        <Route path="/connect" element={<ConnectPage />} />
        <Route path="/join" element={<ConnectPage />} />
        <Route path="/" element={<div data-testid="home">Home</div>} />
        <Route path="/wanted/:id" element={<div data-testid="wanted">Wanted</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  mocked.authStatus.mockReset();
  mocked.connectSession.mockReset();
  mocked.notifyConnect.mockReset();
  mocked.joinWasteland.mockReset();
  mocked.redeemConnectToken.mockReset();
  mocked.refresh.mockReset();
  mocked.toastSuccess.mockReset();
  mocked.toastWarning.mockReset();
  mocked.toastError.mockReset();
  localStorage.clear();
  sessionStorage.clear();
});

describe("ConnectPage", () => {
  it("redirects invalid external return_to values back to the board", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: true,
      connected: true,
      wastelands: [],
    });

    renderConnect("/connect?return_to=https://evil.example");

    await waitFor(() => expect(screen.getByTestId("home")).toBeInTheDocument(), { timeout: 10000 });
  }, 15000);

  it("redirects connected users to return_to when entering /connect", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: true,
      connected: true,
      wastelands: [],
    });

    renderConnect("/connect?return_to=/wanted/item-1");

    await waitFor(() => expect(screen.getByTestId("wanted")).toBeInTheDocument(), { timeout: 10000 });
  }, 15000);

  it("stays on the join view when already connected and entering /join", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: true,
      connected: true,
      wastelands: [],
    });

    renderConnect("/join");

    await waitFor(() => expect(screen.getByRole("heading", { name: "Join a Wasteland" })).toBeInTheDocument(), {
      timeout: 10000,
    });
    expect(screen.getByRole("button", { name: "Join" })).toBeInTheDocument();
  }, 15000);

  it("shows the expired-token hint when reason=expired", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: false,
      connected: false,
      wastelands: [],
    });

    renderConnect("/connect?reason=expired");

    await waitFor(() => expect(screen.getByText(/token has expired or is invalid/i)).toBeInTheDocument());
  });

  it("validates that a username is present before connecting", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: false,
      connected: false,
      wastelands: [],
    });

    renderConnect("/connect");

    await waitFor(() => expect(screen.getByRole("button", { name: "Connect" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));

    expect(mocked.toastError).toHaveBeenCalledWith("DoltHub username is required");
    expect(mocked.connectSession).not.toHaveBeenCalled();
  });

  it("validates that an API token is present before connecting", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: false,
      connected: false,
      wastelands: [],
    });

    renderConnect("/connect");

    await waitFor(() => expect(screen.getByPlaceholderText("alice-dev")).toBeInTheDocument());
    fireEvent.change(screen.getByPlaceholderText("alice-dev"), { target: { value: "alice-dev" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));

    expect(mocked.toastError).toHaveBeenCalledWith("DoltHub API token is required");
    expect(mocked.connectSession).not.toHaveBeenCalled();
  }, 10000);

  it("connects with default identity values and surfaces setup warnings", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: false,
      connected: false,
      wastelands: [],
    });
    mocked.connectSession.mockResolvedValue({
      auth_service_base_url: "https://auth.example.test",
      connect_token: "connect-token",
      redeem_secret: "redeem-secret",
      expires_at: "2026-04-07T19:00:00Z",
    });
    mocked.redeemConnectToken.mockResolvedValue({ connection_id: "conn-1", status: "active" });
    mocked.notifyConnect.mockResolvedValue({ setup_warning: "Grant SQL permissions" });
    mocked.refresh.mockResolvedValue(undefined);

    const returnTo = "/wanted/item-1?tab=activity&filter=needs%20review#comment-2";
    renderConnect(`/connect?return_to=${encodeURIComponent(returnTo)}`);

    await waitFor(() => expect(screen.getByPlaceholderText("alice-dev")).toBeInTheDocument(), { timeout: 5000 });
    fireEvent.change(screen.getByPlaceholderText("alice-dev"), { target: { value: "alice-dev" } });
    fireEvent.change(screen.getByPlaceholderText("your-dolthub-api-token"), { target: { value: "secret-token" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() =>
      expect(mocked.connectSession).toHaveBeenCalledWith({
        rig_handle: "alice-dev",
        fork_org: "alice-dev",
        fork_db: "wl-commons",
        upstream: "hop/wl-commons",
        mode: "pr",
        signing: true,
        display_name: "alice-dev",
      }),
    );
    expect(mocked.redeemConnectToken).toHaveBeenCalledWith({
      auth_service_base_url: "https://auth.example.test",
      connect_token: "connect-token",
      redeem_secret: "redeem-secret",
      api_key: "secret-token",
      metadata: {
        rig_handle: "alice-dev",
        wastelands: [
          {
            upstream: "hop/wl-commons",
            fork_org: "alice-dev",
            fork_db: "wl-commons",
            mode: "pr",
            signing: true,
          },
        ],
      },
    });
    expect(mocked.notifyConnect).toHaveBeenCalledWith({
      connection_id: "conn-1",
      upstream: "hop/wl-commons",
      display_name: "alice-dev",
    });
    expect(mocked.refresh).toHaveBeenCalled();
    expect(mocked.toastWarning).toHaveBeenCalledWith("Grant SQL permissions");
    expect(mocked.toastSuccess).toHaveBeenCalledWith("Connected to DoltHub");
    await waitFor(() => expect(screen.getByTestId("wanted")).toBeInTheDocument());
    expect(screen.getByTestId("location").textContent).toBe(returnTo);
  }, 10000);

  it("connects with advanced overrides", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: false,
      connected: false,
      wastelands: [],
    });
    mocked.connectSession.mockResolvedValue({
      auth_service_base_url: "https://auth.example.test",
      connect_token: "connect-token",
      redeem_secret: "redeem-secret",
      expires_at: "2026-04-07T19:00:00Z",
    });
    mocked.redeemConnectToken.mockResolvedValue({ connection_id: "conn-1", status: "active" });
    mocked.notifyConnect.mockResolvedValue({});
    mocked.refresh.mockResolvedValue(undefined);

    renderConnect("/connect");

    await waitFor(() => expect(screen.getByRole("button", { name: /\+ Advanced/i })).toBeInTheDocument(), {
      timeout: 5000,
    });
    const usernameInput = screen.getAllByRole("textbox")[0];
    fireEvent.change(usernameInput, { target: { value: "alice-dev" } });
    fireEvent.change(screen.getByPlaceholderText("your-dolthub-api-token"), { target: { value: "secret-token" } });
    fireEvent.click(screen.getByRole("button", { name: /\+ Advanced/i }));
    const [, rigHandleInput, forkOrgInput, forkDBInput, upstreamInput] = screen.getAllByRole("textbox");
    expect((usernameInput as HTMLInputElement).value).toBe("alice-dev");
    fireEvent.change(rigHandleInput, { target: { value: "alice" } });
    fireEvent.change(forkOrgInput, { target: { value: "alice-org" } });
    fireEvent.change(forkDBInput, { target: { value: "wl-custom" } });
    fireEvent.change(upstreamInput, { target: { value: "org/wl-custom" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() =>
      expect(mocked.connectSession).toHaveBeenCalledWith({
        rig_handle: "alice",
        fork_org: "alice-org",
        fork_db: "wl-custom",
        upstream: "org/wl-custom",
        mode: "pr",
        signing: true,
        display_name: "alice-dev",
      }),
    );
  }, 10000);

  it("surfaces connect failures to the user", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: false,
      connected: false,
      wastelands: [],
    });
    mocked.connectSession.mockRejectedValue(new Error("session failed"));

    renderConnect("/connect");

    await waitFor(() => expect(screen.getByPlaceholderText("alice-dev")).toBeInTheDocument(), { timeout: 5000 });
    fireEvent.change(screen.getByPlaceholderText("alice-dev"), { target: { value: "alice-dev" } });
    fireEvent.change(screen.getByPlaceholderText("your-dolthub-api-token"), { target: { value: "secret-token" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() => expect(mocked.toastError).toHaveBeenCalledWith("session failed"));
  }, 10000);

  it("validates required join fields", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: true,
      connected: true,
      wastelands: [],
    });

    renderConnect("/join");

    await waitFor(() => expect(screen.getByRole("heading", { name: "Join a Wasteland" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Join" }));

    expect(mocked.toastError).toHaveBeenCalledWith("Fork org, fork DB, and upstream are required");
    expect(mocked.joinWasteland).not.toHaveBeenCalled();
  });

  it("joins a wasteland and surfaces setup warnings", async () => {
    mocked.authStatus.mockResolvedValue({
      authenticated: true,
      connected: true,
      wastelands: [],
    });
    mocked.joinWasteland.mockResolvedValue({ setup_warning: "Fork created without branch perms" });
    mocked.refresh.mockResolvedValue(undefined);

    renderConnect("/join?return_to=/wanted/item-1");

    await waitFor(() => expect(screen.getByPlaceholderText("your-dolthub-org")).toBeInTheDocument(), {
      timeout: 5000,
    });
    fireEvent.change(screen.getByPlaceholderText("your-dolthub-org"), { target: { value: "alice-org" } });
    fireEvent.change(screen.getByPlaceholderText("wl-commons"), { target: { value: "wl-other" } });
    fireEvent.change(screen.getByPlaceholderText("org/wl-commons"), { target: { value: "org/wl-other" } });
    fireEvent.click(screen.getByRole("button", { name: "Join" }));

    await waitFor(() =>
      expect(mocked.joinWasteland).toHaveBeenCalledWith({
        fork_org: "alice-org",
        fork_db: "wl-other",
        upstream: "org/wl-other",
        signing: true,
      }),
    );
    expect(mocked.refresh).toHaveBeenCalled();
    expect(mocked.toastWarning).toHaveBeenCalledWith("Fork created without branch perms");
    expect(mocked.toastSuccess).toHaveBeenCalledWith("Joined wasteland");
    await waitFor(() => expect(screen.getByTestId("wanted")).toBeInTheDocument(), { timeout: 10000 });
  }, 10000);
});
