import { useCallback, useEffect, useOptimistic, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { toast } from "sonner";
import {
  accept,
  applyBranch,
  branchDiff,
  claim,
  close,
  config,
  deleteItem,
  detail,
  discardBranch,
  done,
  isConflictError,
  reject,
  submitPR,
  unclaim,
} from "../api/client";
import type { DetailResponse, MutationResponse } from "../api/types";
import { ActionButton } from "./ActionButton";
import { ConfirmDialog } from "./ConfirmDialog";
import styles from "./DetailView.module.css";
import { PriorityBadge } from "./PriorityBadge";
import { SkeletonBadge, SkeletonBlock, SkeletonLine } from "./Skeleton";
import { StatusBadge } from "./StatusBadge";
import { WantedForm } from "./WantedForm";

const destructiveActions = new Set(["delete", "close", "reject", "discard"]);

const actionStatusMap: Record<string, string> = {
  claim: "claimed",
  unclaim: "open",
  close: "completed",
  accept: "completed",
};

export function DetailView() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [data, setData] = useState<DetailResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<string | null>(null);
  const [diffContent, setDiffContent] = useState<string | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);
  const [evidenceInput, setEvidenceInput] = useState("");
  const [showDoneForm, setShowDoneForm] = useState(false);
  const [showEditForm, setShowEditForm] = useState(false);
  const [doneSubmitting, setDoneSubmitting] = useState(false);
  const [rigHandle, setRigHandle] = useState("");

  const [optimisticStatus, setOptimisticStatus] = useOptimistic(
    data?.item.status ?? "",
    (_current: string, next: string) => next,
  );

  useEffect(() => {
    config()
      .then((c) => setRigHandle(c.rig_handle))
      .catch(() => {});
  }, []);

  const load = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    setError("");
    try {
      setData(await detail(id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load");
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  const handleAction = async (action: string) => {
    if (!id || !data) return;

    const newStatus = actionStatusMap[action];
    if (newStatus) {
      setOptimisticStatus(newStatus);
    }

    try {
      let result: MutationResponse | undefined;
      switch (action) {
        case "claim":
          result = await claim(id);
          break;
        case "unclaim":
          result = await unclaim(id);
          break;
        case "reject":
          result = await reject(id);
          break;
        case "close":
          result = await close(id);
          break;
        case "delete":
          await deleteItem(id);
          toast.success("Item deleted");
          navigate("/");
          return;
        case "accept":
          result = await accept(id);
          break;
        case "submit_pr":
          if (data.branch) {
            await submitPR(data.branch);
          }
          break;
        case "apply":
          if (data.branch) {
            await applyBranch(data.branch);
          }
          break;
        case "discard":
          if (data.branch) {
            await discardBranch(data.branch);
            toast.success("Changes discarded");
            navigate("/");
          }
          return;
        default:
          return;
      }
      toast.success(`${action} successful`);
      // Use the detail from the mutation response to avoid a full refetch
      // (which would flash a skeleton loader).
      if (result?.detail) {
        setData(result.detail);
      } else {
        await load();
      }
    } catch (e) {
      if (isConflictError(e)) {
        toast.error("This item was already claimed or changed by someone else");
        await load();
        return;
      }
      const msg = e instanceof Error ? e.message : `Failed to ${action}`;
      toast.error(msg);
    }
  };

  const handleDone = async () => {
    if (!id || !evidenceInput.trim() || doneSubmitting) return;
    setDoneSubmitting(true);
    try {
      const result = await done(id, evidenceInput.trim());
      setShowDoneForm(false);
      setEvidenceInput("");
      toast.success("Submitted for review");
      if (result?.detail) {
        setData(result.detail);
      } else {
        await load();
      }
    } catch (e) {
      if (isConflictError(e)) {
        toast.error("This item was already claimed or changed by someone else");
        await load();
        return;
      }
      toast.error(e instanceof Error ? e.message : "Failed to submit");
    } finally {
      setDoneSubmitting(false);
    }
  };

  const handleLoadDiff = async () => {
    if (!data?.branch) return;
    setDiffLoading(true);
    try {
      const resp = await branchDiff(data.branch);
      setDiffContent(resp.diff);
    } catch (e) {
      setDiffContent(`Error loading diff: ${e instanceof Error ? e.message : "unknown error"}`);
    } finally {
      setDiffLoading(false);
    }
  };

  const onActionClick = async (action: string) => {
    if (action === "done") {
      setShowDoneForm(true);
      return;
    }
    if (destructiveActions.has(action)) {
      setConfirm(action);
    } else {
      await handleAction(action);
    }
  };

  if (loading)
    return (
      <div className={styles.page}>
        <SkeletonLine width="60px" />
        <div style={{ marginTop: 16 }}>
          <SkeletonLine width="70%" />
          <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
            <SkeletonBadge />
            <SkeletonBadge />
          </div>
        </div>
        <div style={{ marginTop: 16 }}>
          <SkeletonBlock />
        </div>
      </div>
    );
  if (error) return <p className={styles.errorText}>{error}</p>;
  if (!data) return <p className={styles.notFound}>Not found.</p>;

  const { item, completion, stamp, branch, branch_url, main_status, pr_url, delta, actions, branch_actions } = data;
  const branchActions = branch_actions || [];
  const displayStatus = optimisticStatus || item.status;
  const canEdit = rigHandle && rigHandle === item.posted_by;

  return (
    <div className={styles.page}>
      <button type="button" className={styles.backBtn} onClick={() => navigate(-1)}>
        &larr; back
      </button>

      <div className={styles.header}>
        <div className={styles.titleRow}>
          <h2 className={styles.title}>{item.title}</h2>
          {canEdit && (
            <button type="button" className={styles.editBtn} onClick={() => setShowEditForm(true)}>
              Edit
            </button>
          )}
        </div>
        <div className={styles.badges}>
          <PriorityBadge priority={item.priority} />
          <StatusBadge status={displayStatus} />
          {item.type && <span className={styles.typeLabel}>{item.type}</span>}
        </div>
      </div>

      {item.description && <div className={styles.description}>{item.description}</div>}

      <div className={styles.metadata}>
        <span className={styles.metaLabel}>Posted by</span>
        <span className={styles.metaValue}>{item.posted_by || "-"}</span>
        <span className={styles.metaLabel}>Claimed by</span>
        <span className={styles.metaValue}>{item.claimed_by || "-"}</span>
        <span className={styles.metaLabel}>Effort</span>
        <span className={styles.metaValue}>{item.effort_level || "-"}</span>
        {item.tags && item.tags.length > 0 && (
          <>
            <span className={styles.metaLabel}>Tags</span>
            <span className={styles.metaValue}>{item.tags.join(", ")}</span>
          </>
        )}
        {branch && main_status && main_status !== item.status && (
          <>
            <span className={styles.metaLabel}>Pending</span>
            <span className={styles.metaValueBrass}>
              {main_status} &rarr; {item.status}
            </span>
          </>
        )}
        {branch && (
          <>
            <span className={styles.metaLabel}>Branch</span>
            {branch_url ? (
              <a href={branch_url} target="_blank" rel="noopener noreferrer" className={styles.metaMono}>
                {branch}
              </a>
            ) : (
              <span className={styles.metaMono}>{branch}</span>
            )}
          </>
        )}
        {pr_url && (
          <>
            <span className={styles.metaLabel}>PR</span>
            <a href={pr_url} target="_blank" rel="noopener noreferrer" className={styles.prLink}>
              {pr_url}
            </a>
          </>
        )}
      </div>

      {completion && (
        <Section title="Completion">
          <div className={styles.sectionContent}>
            <p className={styles.sectionText}>
              Completed by: <span className={styles.highlightBrass}>{completion.completed_by}</span>
            </p>
            {completion.evidence && <p className={styles.sectionText}>Evidence: {completion.evidence}</p>}
            {completion.validated_by && (
              <p className={styles.sectionTextLast}>
                Validated by: <span className={styles.highlightGreen}>{completion.validated_by}</span>
              </p>
            )}
          </div>
        </Section>
      )}

      {stamp && (
        <Section title="Stamp">
          <div className={styles.sectionContent}>
            <p className={styles.sectionText}>
              Author: <span className={styles.highlightBrass}>{stamp.author}</span>
            </p>
            <p className={styles.sectionText}>Subject: {stamp.subject}</p>
            <p className={styles.sectionText}>
              Quality: {stamp.quality} / Reliability: {stamp.reliability}
            </p>
            {stamp.message && <p className={styles.sectionTextLast}>{stamp.message}</p>}
          </div>
        </Section>
      )}

      {delta && (
        <Section title="Branch Delta">
          <pre className={styles.diffPre}>{delta}</pre>
          {branch && diffContent === null && (
            <button type="button" className={styles.diffBtn} onClick={handleLoadDiff} disabled={diffLoading}>
              {diffLoading ? "Loading diff..." : "View diff"}
            </button>
          )}
          {diffContent && <pre className={styles.diffResult}>{diffContent}</pre>}
        </Section>
      )}

      {showDoneForm && (
        <Section title="Submit for Review">
          <div className={styles.sectionContent}>
            <label className={styles.doneLabel}>Evidence (URL or description)</label>
            <input
              className={styles.evidenceInput}
              type="text"
              value={evidenceInput}
              onChange={(e) => setEvidenceInput(e.target.value)}
              placeholder="https://github.com/..."
              onKeyDown={(e) => {
                if (e.key === "Enter" && !doneSubmitting) handleDone();
              }}
            />
            <div className={styles.formActions}>
              <button
                type="button"
                className={styles.submitBtn}
                onClick={handleDone}
                disabled={!evidenceInput.trim() || doneSubmitting}
              >
                {doneSubmitting ? "Submitting..." : "Submit"}
              </button>
              <button
                type="button"
                className={styles.formCancelBtn}
                onClick={() => {
                  setShowDoneForm(false);
                  setEvidenceInput("");
                }}
              >
                Cancel
              </button>
            </div>
          </div>
        </Section>
      )}

      {(actions.length > 0 || branchActions.length > 0) && (
        <div className={styles.actions}>
          {actions.map((action) => (
            <ActionButton key={action} action={action} onAction={async () => onActionClick(action)} />
          ))}
          {branchActions.map((action) => (
            <ActionButton key={action} action={action.replace("_", " ")} onAction={async () => onActionClick(action)} />
          ))}
        </div>
      )}

      {showEditForm && (
        <WantedForm
          item={item}
          onClose={() => setShowEditForm(false)}
          onSaved={(detail) => {
            if (detail) setData(detail);
            else load();
          }}
        />
      )}

      {confirm && (
        <ConfirmDialog
          message={`Are you sure you want to ${confirm} this item?`}
          onCancel={() => setConfirm(null)}
          onConfirm={async () => {
            const action = confirm;
            setConfirm(null);
            await handleAction(action);
          }}
        />
      )}
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className={styles.section}>
      <h3 className={styles.sectionTitle}>{title}</h3>
      {children}
    </div>
  );
}
