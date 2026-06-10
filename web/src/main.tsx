import { getDefaultIntegrations, globalHandlersIntegration } from "@sentry/browser";
import * as Sentry from "@sentry/react";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { startPrefetch } from "./api/prefetch";
import { loadRuntimeConfig } from "./api/runtimeConfig";
import type { RuntimeConfigResponse } from "./api/types";
import { Toaster } from "./components/Toaster";
import { initBrowserTracing } from "./observability/browser";
import { initOpenPanel } from "./observability/openpanel";
import "./styles/global.css";

function initSentry(runtimeConfig: RuntimeConfigResponse) {
  const browserTracingEnabled = runtimeConfig.browser_tracing_enabled;
  const integrations = [
    ...getDefaultIntegrations({}),
    globalHandlersIntegration({ onerror: true, onunhandledrejection: true }),
    Sentry.replayIntegration(),
  ];
  if (!browserTracingEnabled) {
    integrations.push(Sentry.browserTracingIntegration());
  }

  Sentry.init({
    dsn: import.meta.env.VITE_SENTRY_DSN || "",
    environment: runtimeConfig.environment || import.meta.env.VITE_ENVIRONMENT || "development",
    integrations,
    tracesSampleRate: browserTracingEnabled ? 0 : 0.2,
    replaysSessionSampleRate: 0,
    replaysOnErrorSampleRate: 1.0,
    beforeSend(event) {
      // Drop errors from third-party scripts/fetches (e.g. Product Fruits, analytics).
      const msg = event.exception?.values?.[0]?.value ?? "";
      if (/\((?:.*\.productfruits\.com|.*\.analytics\.)|chrome-extension:/.test(msg)) {
        return null;
      }
      return event;
    },
  });
}

async function start() {
  const runtimeConfig = await loadRuntimeConfig();
  if (runtimeConfig.browser_tracing_enabled) {
    initBrowserTracing(runtimeConfig);
  }
  startPrefetch();
  initSentry(runtimeConfig);
  initOpenPanel(runtimeConfig);

  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App />
      <Toaster />
    </StrictMode>,
  );
}

void start();
