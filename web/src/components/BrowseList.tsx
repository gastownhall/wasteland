import { startTransition, useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { browse } from "../api/client";
import { consumePrefetch } from "../api/prefetch";
import type { PendingItemSummary, WantedSummary } from "../api/types";
import { useFilterParams } from "../hooks/useFilterParams";
import styles from "./BrowseList.module.css";
import { EmptyState } from "./EmptyState";
import { FilterBar } from "./FilterBar";
import { PriorityBadge } from "./PriorityBadge";
import { SkeletonRows } from "./Skeleton";
import { StatusBadge } from "./StatusBadge";
import { WantedForm } from "./WantedForm";

export function BrowseList() {
  const navigate = useNavigate();
  const [items, setItems] = useState<WantedSummary[]>([]);
  const [filter, setFilter] = useFilterParams();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [warning, setWarning] = useState("");
  const [showForm, setShowForm] = useState(false);
  const [showInferForm, setShowInferForm] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const selectedIndexRef = useRef(-1);
  const searchRef = useRef<HTMLInputElement>(null);
  const hasLoadedRef = useRef(false);

  const setSelection = useCallback((next: number) => {
    selectedIndexRef.current = next;
    setSelectedIndex(next);
  }, []);

  const load = useCallback(async () => {
    if (!hasLoadedRef.current) setLoading(true);
    setError("");
    setWarning("");
    try {
      // On first load with default filters, use prefetched data if available.
      const prefetched = !hasLoadedRef.current ? consumePrefetch() : null;
      const resp = (prefetched && (await prefetched)) || (await browse(filter));
      setItems(resp.items);
      if (resp.warning) setWarning(resp.warning);
      setSelection(-1);
      hasLoadedRef.current = true;
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Failed to load";
      setError(msg);
      toast.error(msg);
    } finally {
      setLoading(false);
    }
  }, [filter, setSelection]);

  useEffect(() => {
    load();
  }, [load]);

  // Silent background poll — no loading spinner, no error toasts.
  useEffect(() => {
    if (!hasLoadedRef.current) return;
    const id = setInterval(() => {
      if (document.hidden) return;
      browse(filter)
        .then((resp) => {
          setItems(resp.items);
          setSelection(-1);
        })
        .catch(() => {});
    }, 30_000);
    return () => clearInterval(id);
  }, [filter, setSelection]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      const inInput = target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT";
      if (inInput) return;

      switch (e.key) {
        case "j": {
          e.preventDefault();
          if (items.length === 0) break;
          setSelection(Math.min(selectedIndexRef.current + 1, items.length - 1));
          break;
        }
        case "k": {
          e.preventDefault();
          if (items.length === 0) break;
          setSelection(Math.max(selectedIndexRef.current - 1, 0));
          break;
        }
        case "Enter": {
          const index = selectedIndexRef.current;
          if (index >= 0 && index < items.length) {
            startTransition(() => navigate(`/wanted/${items[index].id}`));
          }
          break;
        }
        case "c":
          setShowForm(true);
          break;
        case "i":
          if (__INFER_ENABLED__) setShowInferForm(true);
          break;
        case "/":
          e.preventDefault();
          searchRef.current?.focus();
          break;
      }
    };

    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [items, navigate, setSelection]);

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h2 className={styles.heading}>Wanted Board</h2>
        <div className={styles.headerActions}>
          {__INFER_ENABLED__ && (
            <button type="button" className={styles.inferBtn} onClick={() => setShowInferForm(true)}>
              + Infer
            </button>
          )}
          <button type="button" className={styles.postBtn} onClick={() => setShowForm(true)}>
            + Post
          </button>
        </div>
      </div>

      <FilterBar filter={filter} onChange={setFilter} searchRef={searchRef} />

      {error && <p className={styles.error}>{error}</p>}
      {warning && <p className={styles.warning}>{warning}</p>}

      {loading ? (
        <SkeletonRows count={6} />
      ) : items.length === 0 ? (
        <EmptyState
          title="No items found"
          description="The wanted board is empty. Post the first item to get started."
          ctaLabel="+ Post"
          onCta={() => setShowForm(true)}
        />
      ) : (
        <>
          <table className={styles.table} aria-label="Wanted items">
            <thead>
              <tr className={styles.thead}>
                <th className={styles.th} aria-sort={filter.sort === "priority" ? "ascending" : undefined}>
                  Priority
                </th>
                <th className={styles.th} aria-sort={filter.sort === "alpha" ? "ascending" : undefined}>
                  Title
                </th>
                <th className={styles.th}>Status</th>
                <th className={styles.th}>Type</th>
                <th className={styles.th}>Posted By</th>
                <th className={styles.th}>Claimed By</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item, index) => (
                <tr
                  key={item.id}
                  className={styles.row}
                  data-selected={index === selectedIndex || undefined}
                  aria-selected={index === selectedIndex || undefined}
                >
                  <td className={styles.td}>
                    <PriorityBadge priority={item.priority} />
                  </td>
                  <td className={styles.td}>
                    <Link to={`/wanted/${item.id}`} className={styles.titleLink}>
                      {item.title}
                    </Link>
                  </td>
                  <td className={styles.td}>
                    <span className={styles.statusCell}>
                      <StatusBadge status={item.status} />
                      {item.pending_count != null && item.pending_count > 0 && (
                        <PendingIndicator count={item.pending_count} items={item.pending_items} />
                      )}
                    </span>
                  </td>
                  <td className={styles.tdMuted}>{item.type || "-"}</td>
                  <td className={styles.tdMuted}>{item.posted_by || "-"}</td>
                  <td className={styles.tdMuted}>{item.claimed_by || "-"}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <div className={styles.cardList}>
            {items.map((item, index) => (
              <div key={item.id} className={styles.card} data-selected={index === selectedIndex || undefined}>
                <div className={styles.cardTop}>
                  <PriorityBadge priority={item.priority} />
                  <StatusBadge status={item.status} />
                  {item.pending_count != null && item.pending_count > 0 && (
                    <PendingIndicator count={item.pending_count} items={item.pending_items} />
                  )}
                </div>
                <Link to={`/wanted/${item.id}`} className={styles.cardTitle}>
                  {item.title}
                </Link>
                <div className={styles.cardMeta}>
                  {item.type && <span>{item.type}</span>}
                  {item.posted_by && <span>{item.posted_by}</span>}
                </div>
              </div>
            ))}
          </div>
        </>
      )}

      {showForm && (
        <WantedForm
          onClose={() => setShowForm(false)}
          onSaved={() => {
            setShowForm(false);
            load();
          }}
        />
      )}

      {__INFER_ENABLED__ && showInferForm && (
        <WantedForm
          mode="inference"
          onClose={() => setShowInferForm(false)}
          onSaved={() => {
            setShowInferForm(false);
            load();
          }}
        />
      )}
    </div>
  );
}

function PendingIndicator({ count, items }: { count: number; items?: PendingItemSummary[] }) {
  return (
    <span className={styles.pendingIndicator}>
      pending
      {count > 1 && <span className={styles.pendingCount}>&times;{count}</span>}
      {items && items.length > 0 && (
        <span className={styles.pendingCard}>
          <span className={styles.pendingCardTitle}>Competing submissions</span>
          {items.map((p, i) => (
            <span key={i} className={styles.pendingCardRow}>
              <span className={styles.pendingCardHandle}>{p.rig_handle}</span>
              {p.status && <span className={styles.pendingCardStatus}>{p.status.replace("_", " ")}</span>}
              {p.pr_url && (
                <a href={p.pr_url} target="_blank" rel="noopener noreferrer" className={styles.pendingCardLink}>
                  PR
                </a>
              )}
              {p.branch_url && !p.pr_url && (
                <a href={p.branch_url} target="_blank" rel="noopener noreferrer" className={styles.pendingCardLink}>
                  branch
                </a>
              )}
            </span>
          ))}
        </span>
      )}
    </span>
  );
}
