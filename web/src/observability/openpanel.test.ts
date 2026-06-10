import { beforeEach, describe, expect, it, vi } from "vitest";

const mocked = vi.hoisted(() => ({
  openPanelConstructor: vi.fn(),
  setGlobalProperties: vi.fn(),
}));

vi.mock("@openpanel/web", () => ({
  OpenPanel: class {
    setGlobalProperties = mocked.setGlobalProperties;

    constructor(...args: unknown[]) {
      mocked.openPanelConstructor(...args);
    }
  },
}));

describe("OpenPanel analytics", () => {
  beforeEach(() => {
    vi.resetModules();
    window.localStorage.clear();
    mocked.openPanelConstructor.mockClear();
    mocked.setGlobalProperties.mockClear();
  });

  it("initializes against the self-hosted ingest endpoint in production", async () => {
    const { initOpenPanel, resetOpenPanelForTests } = await import("./openpanel");
    resetOpenPanelForTests();

    expect(
      initOpenPanel({
        environment: "production",
        browser_tracing_enabled: false,
      }),
    ).not.toBeNull();

    expect(mocked.openPanelConstructor).toHaveBeenCalledWith(
      expect.objectContaining({
        apiUrl: "https://events.gascity.com/api",
        clientId: "eb727ecb-d336-51ef-a590-49897d042825",
        trackScreenViews: true,
        trackOutgoingLinks: true,
        trackAttributes: true,
      }),
    );
    expect(mocked.setGlobalProperties).toHaveBeenCalledWith({
      app: "wasteland",
      environment: "production",
    });

    const options = mocked.openPanelConstructor.mock.calls[0]?.[0] as { filter: () => boolean };
    expect(options.filter()).toBe(true);
    window.localStorage.setItem("disable_tracking", "1");
    expect(options.filter()).toBe(false);
  });

  it("stays disabled outside production", async () => {
    const { initOpenPanel, resetOpenPanelForTests } = await import("./openpanel");
    resetOpenPanelForTests();

    expect(
      initOpenPanel({
        environment: "staging",
        browser_tracing_enabled: false,
      }),
    ).toBeNull();

    expect(mocked.openPanelConstructor).not.toHaveBeenCalled();
  });
});
