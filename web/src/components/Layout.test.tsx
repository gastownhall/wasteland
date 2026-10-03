import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocked = vi.hoisted(() => ({
  switchTo: vi.fn(),
  getImpersonation: vi.fn(),
  setImpersonation: vi.fn(),
  shortcutHandlers: null as null | { onTogglePalette: () => void; onToggleHelp: () => void },
  wastelandState: {
    wastelands: [] as Array<{ upstream: string }>,
    active: null as string | null,
    authenticated: false,
    connected: false,
    environment: undefined as string | undefined,
  },
}));

vi.mock("../api/client", () => ({
  getImpersonation: mocked.getImpersonation,
  setImpersonation: mocked.setImpersonation,
}));

vi.mock("../context/WastelandContext", () => ({
  useWasteland: () => ({
    ...mocked.wastelandState,
    switchTo: mocked.switchTo,
    refresh: async () => {},
  }),
}));

vi.mock("../hooks/useGlobalShortcuts", () => ({
  useGlobalShortcuts: (handlers: { onTogglePalette: () => void; onToggleHelp: () => void }) => {
    mocked.shortcutHandlers = handlers;
  },
}));

vi.mock("./CommandPalette", () => ({
  CommandPalette: ({ open }: { open: boolean }) => <div data-testid="palette-state">{open ? "open" : "closed"}</div>,
}));

vi.mock("./ShortcutHelp", () => ({
  ShortcutHelp: ({ open }: { open: boolean }) => <div data-testid="help-state">{open ? "open" : "closed"}</div>,
}));

import { Layout } from "./Layout";

function LocationProbe() {
  const { pathname, search, hash } = useLocation();
  return <div data-testid="location">{pathname + search + hash}</div>;
}

function renderLayout(initialEntry = "/") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <LocationProbe />
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<div data-testid="outlet">Board</div>} />
          <Route path="/connect" element={<div data-testid="outlet">Connect</div>} />
          <Route path="/join" element={<div data-testid="outlet">Join</div>} />
          <Route path="/wanted/:id" element={<div data-testid="outlet">Wanted</div>} />
          <Route path="/settings" element={<div data-testid="outlet">Settings</div>} />
          <Route path="/me" element={<div data-testid="outlet">Dashboard</div>} />
          <Route path="/profile" element={<div data-testid="outlet">Profiles</div>} />
          <Route path="/scoreboard" element={<div data-testid="outlet">Scoreboard</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  mocked.getImpersonation.mockReturnValue(null);
});

afterEach(() => {
  mocked.switchTo.mockReset();
  mocked.getImpersonation.mockReset();
  mocked.setImpersonation.mockReset();
  mocked.shortcutHandlers = null;
  mocked.wastelandState = {
    wastelands: [],
    active: null,
    authenticated: false,
    connected: false,
    environment: undefined,
  };
});

describe("Layout", () => {
  it("shows sign in navigation for unauthenticated users", () => {
    renderLayout("/");

    expect(screen.getByRole("link", { name: "board" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "profiles" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "scoreboard" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "sign in" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "me" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "settings" })).not.toBeInTheDocument();
    expect(screen.getByTestId("palette-state")).toHaveTextContent("closed");
    expect(screen.getByTestId("help-state")).toHaveTextContent("closed");
  }, 10000);

  it("shows connect navigation for authenticated users without a joined wasteland", () => {
    mocked.wastelandState = {
      wastelands: [],
      active: null,
      authenticated: true,
      connected: false,
      environment: undefined,
    };

    renderLayout("/");

    expect(screen.getByRole("link", { name: "connect" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "settings" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "me" })).not.toBeInTheDocument();
  });

  it.each([
    { authenticated: false, label: "sign in" },
    { authenticated: true, label: "connect" },
  ])("preserves the wanted item URL when clicking $label", async ({ authenticated, label }) => {
    mocked.wastelandState.authenticated = authenticated;
    const returnTo = "/wanted/item-1?tab=activity&filter=needs%20review#comment-2";
    renderLayout(returnTo);

    await userEvent.click(screen.getByRole("link", { name: label }));

    const destination = new URL(screen.getByTestId("location").textContent ?? "", "https://wasteland.test");
    expect(destination.pathname).toBe("/connect");
    expect(destination.searchParams.get("return_to")).toBe(returnTo);
    expect(destination.hash).toBe("");
  });

  it.each([
    "/connect",
    "/join",
    "/connect/",
    "/join/",
  ])("keeps the existing return destination when signing in from %s", async (path) => {
    const queryAndHash = "?return_to=%2Fwanted%2Fitem-1%3Ftab%3Dactivity%23comment-2&reason=expired#credentials";
    renderLayout(path + queryAndHash);

    await userEvent.click(screen.getByRole("link", { name: "sign in" }));

    expect(screen.getByTestId("location").textContent).toBe(`/connect${queryAndHash}`);
  });

  it("keeps a direct connect page standalone", async () => {
    renderLayout("/connect");

    await userEvent.click(screen.getByRole("link", { name: "sign in" }));

    expect(screen.getByTestId("location").textContent).toBe("/connect");
  });

  it("uses the current page after navigating before signing in", async () => {
    renderLayout("/wanted/item-1?tab=activity#comment-2");

    await userEvent.click(screen.getByRole("link", { name: "profiles" }));
    await userEvent.click(screen.getByRole("link", { name: "sign in" }));

    const destination = new URL(screen.getByTestId("location").textContent ?? "", "https://wasteland.test");
    expect(destination.searchParams.get("return_to")).toBe("/profile");
  });

  it("shows authenticated navigation and switches wastelands", async () => {
    mocked.wastelandState = {
      wastelands: [{ upstream: "org/wl-one" }, { upstream: "org/wl-two" }],
      active: "org/wl-one",
      authenticated: true,
      connected: true,
      environment: undefined,
    };

    renderLayout("/settings");

    expect(screen.getByRole("link", { name: "me" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "settings" })).toBeInTheDocument();

    const switcher = screen.getByRole("combobox", { name: "Active wasteland" });
    await userEvent.selectOptions(switcher, "org/wl-two");
    expect(mocked.switchTo).toHaveBeenCalledWith("org/wl-two");
  });

  it("shows a fixed upstream label when only one wasteland is available", () => {
    mocked.wastelandState = {
      wastelands: [{ upstream: "org/wl-one" }],
      active: "org/wl-one",
      authenticated: true,
      connected: true,
      environment: undefined,
    };

    renderLayout("/");

    expect(screen.getByText("org/wl-one")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Active wasteland" })).not.toBeInTheDocument();
  });

  it("shows staging impersonation controls", () => {
    mocked.wastelandState = {
      wastelands: [],
      active: null,
      authenticated: false,
      connected: false,
      environment: "staging",
    };

    renderLayout("/");

    expect(screen.getByText("staging")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("impersonate rig handle...")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "go" })).toBeDisabled();
  });

  it("shows the active impersonation state in staging", () => {
    mocked.getImpersonation.mockReturnValue("bob");
    mocked.wastelandState = {
      wastelands: [],
      active: null,
      authenticated: false,
      connected: false,
      environment: "staging",
    };

    renderLayout("/");

    expect(screen.getByText("acting as bob")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "stop" })).toBeInTheDocument();
  });

  it("applies impersonation from the staging banner", async () => {
    mocked.wastelandState = {
      wastelands: [],
      active: null,
      authenticated: false,
      connected: false,
      environment: "staging",
    };

    renderLayout("/");

    await userEvent.type(screen.getByPlaceholderText("impersonate rig handle..."), "bob");
    await userEvent.click(screen.getByRole("button", { name: "go" }));

    expect(mocked.setImpersonation).toHaveBeenCalledWith("bob");
  });

  it("clears impersonation from the staging banner", async () => {
    mocked.getImpersonation.mockReturnValue("bob");
    mocked.wastelandState = {
      wastelands: [],
      active: null,
      authenticated: false,
      connected: false,
      environment: "staging",
    };

    renderLayout("/");

    await userEvent.click(screen.getByRole("button", { name: "stop" }));

    expect(mocked.setImpersonation).toHaveBeenCalledWith(null);
  });

  it("opens palette and shortcut help when global shortcuts fire", () => {
    renderLayout("/");

    act(() => {
      mocked.shortcutHandlers?.onTogglePalette();
    });
    expect(screen.getByTestId("palette-state")).toHaveTextContent("open");

    act(() => {
      mocked.shortcutHandlers?.onToggleHelp();
    });
    expect(screen.getByTestId("help-state")).toHaveTextContent("open");
  });
});
