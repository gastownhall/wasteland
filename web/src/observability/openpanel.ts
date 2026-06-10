import { OpenPanel } from "@openpanel/web";
import type { RuntimeConfigResponse } from "../api/types";

const defaultApiUrl = "https://events.gascity.com/api";
const defaultClientId = "eb727ecb-d336-51ef-a590-49897d042825";
const productionHosts = new Set(["wasteland.gastownhall.ai"]);

let openPanel: OpenPanel | null = null;

function hasTrackingOptOut(): boolean {
  try {
    return window.localStorage.getItem("disable_tracking") === "1";
  } catch {
    return false;
  }
}

function isOpenPanelEnabled(config: RuntimeConfigResponse): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  const environment = config.environment?.toLowerCase();
  return environment === "production" || productionHosts.has(window.location.hostname);
}

export function initOpenPanel(config: RuntimeConfigResponse): OpenPanel | null {
  if (openPanel) {
    return openPanel;
  }
  if (!isOpenPanelEnabled(config) || hasTrackingOptOut()) {
    return null;
  }

  openPanel = new OpenPanel({
    apiUrl: import.meta.env.VITE_OPENPANEL_API_URL || defaultApiUrl,
    clientId: import.meta.env.VITE_OPENPANEL_CLIENT_ID || defaultClientId,
    trackScreenViews: true,
    trackOutgoingLinks: true,
    trackAttributes: true,
    filter: () => !hasTrackingOptOut(),
  });
  openPanel.setGlobalProperties({
    app: "wasteland",
    environment: config.environment || import.meta.env.VITE_ENVIRONMENT || "production",
  });

  return openPanel;
}

export function resetOpenPanelForTests() {
  openPanel = null;
}
