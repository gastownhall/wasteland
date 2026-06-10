import { beforeEach, describe, expect, it, vi } from "vitest";

const mocked = vi.hoisted(() => ({
  init: vi.fn(),
  getDefaultIntegrations: vi.fn(() => ["default-integration"]),
  globalHandlersIntegration: vi.fn(() => "global-handlers"),
  loadRuntimeConfig: vi.fn(),
  initBrowserTracing: vi.fn(),
  initOpenPanel: vi.fn(),
  startPrefetch: vi.fn(),
  createRoot: vi.fn(),
  render: vi.fn(),
}));

vi.mock("./api/prefetch", () => ({
  startPrefetch: mocked.startPrefetch,
}));

vi.mock("./api/runtimeConfig", () => ({
  loadRuntimeConfig: mocked.loadRuntimeConfig,
}));

vi.mock("./observability/browser", () => ({
  initBrowserTracing: mocked.initBrowserTracing,
}));

vi.mock("./observability/openpanel", () => ({
  initOpenPanel: mocked.initOpenPanel,
}));

vi.mock("./App", () => ({
  App: () => <div>App Root</div>,
}));

vi.mock("./components/Toaster", () => ({
  Toaster: () => <div>Toast Root</div>,
}));

vi.mock("@sentry/browser", () => ({
  getDefaultIntegrations: mocked.getDefaultIntegrations,
  globalHandlersIntegration: mocked.globalHandlersIntegration,
}));

vi.mock("@sentry/react", () => ({
  init: mocked.init,
  browserTracingIntegration: () => "browser-tracing",
  replayIntegration: () => "replay",
}));

vi.mock("react-dom/client", () => ({
  createRoot: mocked.createRoot,
}));

describe("main", () => {
  beforeEach(() => {
    vi.resetModules();
    document.body.innerHTML = '<div id="root"></div>';
    mocked.init.mockReset();
    mocked.getDefaultIntegrations.mockClear();
    mocked.globalHandlersIntegration.mockClear();
    mocked.loadRuntimeConfig.mockReset();
    mocked.initBrowserTracing.mockReset();
    mocked.initOpenPanel.mockReset();
    mocked.startPrefetch.mockReset();
    mocked.createRoot.mockReset();
    mocked.render.mockReset();
    mocked.createRoot.mockReturnValue({ render: mocked.render });
    mocked.loadRuntimeConfig.mockResolvedValue({
      environment: "staging",
      browser_tracing_enabled: true,
      browser_trace_endpoint: "/api/telemetry/v1/traces",
      browser_trace_sample_ratio: 0.3,
    });
  });

  it("initializes browser otel before mounting the app", async () => {
    await import("./main");
    await vi.waitFor(() => expect(mocked.init).toHaveBeenCalled());

    expect(mocked.init).toHaveBeenCalledWith(
      expect.objectContaining({
        integrations: expect.arrayContaining(["default-integration", "global-handlers", "replay"]),
        tracesSampleRate: 0,
        replaysSessionSampleRate: 0,
        replaysOnErrorSampleRate: 1.0,
        environment: "staging",
      }),
    );
    expect(mocked.initBrowserTracing).toHaveBeenCalledWith({
      environment: "staging",
      browser_tracing_enabled: true,
      browser_trace_endpoint: "/api/telemetry/v1/traces",
      browser_trace_sample_ratio: 0.3,
    });
    expect(mocked.startPrefetch).toHaveBeenCalled();
    expect(mocked.initOpenPanel).toHaveBeenCalledWith({
      environment: "staging",
      browser_tracing_enabled: true,
      browser_trace_endpoint: "/api/telemetry/v1/traces",
      browser_trace_sample_ratio: 0.3,
    });
    expect(mocked.createRoot).toHaveBeenCalledWith(document.getElementById("root"));
    expect(mocked.render).toHaveBeenCalled();
  }, 30000);

  it("keeps sentry browser tracing when otel browser tracing is disabled", async () => {
    mocked.loadRuntimeConfig.mockResolvedValue({
      environment: "staging",
      browser_tracing_enabled: false,
      browser_trace_endpoint: "",
      browser_trace_sample_ratio: 0,
    });

    await import("./main");
    await vi.waitFor(() => expect(mocked.init).toHaveBeenCalled());

    expect(mocked.init).toHaveBeenCalledWith(
      expect.objectContaining({
        integrations: expect.arrayContaining(["default-integration", "global-handlers", "browser-tracing", "replay"]),
        tracesSampleRate: 0.2,
      }),
    );
    expect(mocked.initBrowserTracing).not.toHaveBeenCalled();
    expect(mocked.initOpenPanel).toHaveBeenCalledWith({
      environment: "staging",
      browser_tracing_enabled: false,
      browser_trace_endpoint: "",
      browser_trace_sample_ratio: 0,
    });
  });
});
