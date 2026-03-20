import type { WantedSummary } from "../api/types";

export function findSelectedIndex(items: WantedSummary[], selectedId: string | null): number {
  if (!selectedId) return -1;
  return items.findIndex((item) => item.id === selectedId);
}

export function resolveSelectedIdAfterRefresh(selectedId: string | null, nextItems: WantedSummary[]): string | null {
  if (!selectedId) return null;
  return nextItems.some((item) => item.id === selectedId) ? selectedId : null;
}
