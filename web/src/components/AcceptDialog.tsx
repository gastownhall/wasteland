import { useEffect, useState } from "react";
import { useFocusTrap } from "../hooks/useFocusTrap";
import styles from "./AcceptDialog.module.css";

export interface AcceptStampInput {
  quality: number;
  reliability?: number;
  severity?: string;
  skill_tags?: string[];
  message?: string;
}

interface AcceptDialogProps {
  label: string;
  submitting: boolean;
  onCancel: () => void;
  onSubmit: (stamp: AcceptStampInput) => Promise<void>;
}

export function AcceptDialog({ label, submitting, onCancel, onSubmit }: AcceptDialogProps) {
  const trapRef = useFocusTrap(true);
  const titleId = "accept-submission-title";
  const [quality, setQuality] = useState("5");
  const [reliability, setReliability] = useState("");
  const [severity, setSeverity] = useState("leaf");
  const [skillTags, setSkillTags] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !submitting) onCancel();
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [onCancel, submitting]);

  const submit = async () => {
    const parsedTags = skillTags
      .split(",")
      .map((tag) => tag.trim())
      .filter(Boolean);
    await onSubmit({
      quality: Number(quality),
      reliability: reliability ? Number(reliability) : undefined,
      severity,
      skill_tags: parsedTags.length > 0 ? parsedTags : undefined,
      message: message.trim() || undefined,
    });
  };

  return (
    <div className={styles.overlay} onClick={() => !submitting && onCancel()}>
      <div
        ref={trapRef}
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        <h3 id={titleId} className={styles.title}>
          Accept Submission
        </h3>
        <p className={styles.message}>Add a stamp for {label} before marking it complete.</p>

        <label className={styles.field}>
          <span className={styles.label}>Quality</span>
          <select
            className={styles.select}
            value={quality}
            onChange={(e) => setQuality(e.target.value)}
            disabled={submitting}
          >
            {[1, 2, 3, 4, 5].map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </label>

        <label className={styles.field}>
          <span className={styles.label}>Reliability</span>
          <select
            className={styles.select}
            value={reliability}
            onChange={(e) => setReliability(e.target.value)}
            disabled={submitting}
          >
            <option value="">Match quality</option>
            {[1, 2, 3, 4, 5].map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </label>

        <label className={styles.field}>
          <span className={styles.label}>Severity</span>
          <select
            className={styles.select}
            value={severity}
            onChange={(e) => setSeverity(e.target.value)}
            disabled={submitting}
          >
            <option value="leaf">Leaf</option>
            <option value="branch">Branch</option>
            <option value="root">Root</option>
          </select>
        </label>

        <label className={styles.field}>
          <span className={styles.label}>Tags</span>
          <input
            className={styles.input}
            type="text"
            value={skillTags}
            onChange={(e) => setSkillTags(e.target.value)}
            placeholder="go, sql, testing"
            disabled={submitting}
          />
        </label>

        <label className={styles.field}>
          <span className={styles.label}>Message</span>
          <textarea
            className={styles.textarea}
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            placeholder="Optional feedback for the completion"
            disabled={submitting}
            rows={3}
          />
        </label>

        <div className={styles.actions}>
          <button type="button" className={styles.cancelBtn} onClick={onCancel} disabled={submitting}>
            Cancel
          </button>
          <button type="button" className={styles.confirmBtn} onClick={submit} disabled={submitting}>
            {submitting ? "Accepting..." : "Accept"}
          </button>
        </div>
      </div>
    </div>
  );
}
