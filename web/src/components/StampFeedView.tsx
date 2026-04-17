import { useState } from "react";
import type { StampFeedEntry, StampFeedResponse } from "../api/types";
import styles from "./StampFeedView.module.css";

const MESSAGE_TRUNCATE_CHARS = 200;

export function StampFeedView({ data }: { data: StampFeedResponse }) {
  const githubHref = safeHref(data.github_url);

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h2 className={styles.name}>{data.handle}</h2>
        {githubHref && (
          <a
            className={styles.githubLink}
            href={githubHref}
            target="_blank"
            rel="noopener noreferrer"
            aria-label="View profile on GitHub (opens in new tab)"
          >
            View on GitHub ↗
          </a>
        )}
      </div>
      <p className={styles.banner}>No character sheet yet — showing federation activity</p>

      {data.stamps_error ? (
        <div className={styles.errorBanner} role="alert">
          <span className={styles.errorIcon} aria-hidden="true">
            ⚠
          </span>
          <span>Couldn't load recent stamps — try again later.</span>
        </div>
      ) : (
        <section className={styles.stampList}>
          {data.stamps.map((stamp) => (
            <StampCard key={stamp.id} stamp={stamp} />
          ))}
        </section>
      )}
    </div>
  );
}

function StampCard({ stamp }: { stamp: StampFeedEntry }) {
  const date = formatDate(stamp.created_at);
  const hasValence = stamp.quality > 0 || stamp.reliability > 0;
  const evidenceHref = safeHref(stamp.evidence_url);

  return (
    <article className={styles.card}>
      <header className={styles.cardHeader}>
        <div className={styles.tagList}>
          {stamp.skill_tags.map((tag, idx) => (
            <span key={`${tag}-${idx}`} className={styles.tag}>
              {tag}
            </span>
          ))}
        </div>
        {hasValence && (
          <span className={styles.valence} title="Quality / Reliability (0-5)">
            <span className={styles.srOnly}>
              Quality {stamp.quality} of 5, Reliability {stamp.reliability} of 5
            </span>
            <span aria-hidden="true">
              Q{stamp.quality} R{stamp.reliability}
            </span>
          </span>
        )}
      </header>

      {evidenceHref && (
        <div className={styles.evidenceLine}>
          <span aria-hidden="true">→ </span>
          <a
            className={styles.evidenceLink}
            href={evidenceHref}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={`View evidence ${stamp.evidence_label || evidenceHref} (opens in new tab)`}
          >
            {stamp.evidence_label || evidenceHref}
          </a>
        </div>
      )}

      <div className={styles.meta}>
        <span>validated by {stamp.validator}</span>
        {date && <span>· {date}</span>}
      </div>

      {stamp.message && <TruncatedMessage text={stamp.message} label="message" />}
      {stamp.evidence_text && !evidenceHref && <TruncatedMessage text={stamp.evidence_text} label="evidence" />}
    </article>
  );
}

function TruncatedMessage({ text, label }: { text: string; label: string }) {
  const [expanded, setExpanded] = useState(false);
  // Count and slice by Unicode code points rather than UTF-16 code units so
  // emoji and other non-BMP characters straddling the boundary can't be
  // split into a broken glyph. Note: this is not grapheme-cluster safe —
  // ZWJ sequences (👨‍👩‍👧) or flag sequences (🇺🇸) can still split at
  // their join point. Acceptable for prose validator messages; revisit
  // with Intl.Segmenter if emoji-heavy content becomes common.
  const codepoints = Array.from(text);
  const needsTruncation = codepoints.length > MESSAGE_TRUNCATE_CHARS;
  if (!needsTruncation) {
    return <p className={styles.message}>{text}</p>;
  }
  const displayed = expanded ? text : `${codepoints.slice(0, MESSAGE_TRUNCATE_CHARS).join("").trimEnd()}…`;
  return (
    <p className={styles.message}>
      {displayed}{" "}
      <button
        type="button"
        className={styles.showMore}
        aria-expanded={expanded}
        aria-label={expanded ? `show less of ${label}` : `show more of ${label}`}
        onClick={() => setExpanded((v) => !v)}
      >
        {expanded ? "show less" : "show more"}
      </button>
    </p>
  );
}

// safeHref returns the URL only when its scheme is http(s). Prevents
// javascript: or data: URLs from backend evidence fields becoming
// clickable XSS gadgets. Exported for direct testing.
export function safeHref(raw: string | undefined): string | null {
  if (!raw) return null;
  try {
    const parsed = new URL(raw);
    if (parsed.protocol === "http:" || parsed.protocol === "https:") {
      return raw;
    }
  } catch {
    // Not a valid URL; fall through.
  }
  return null;
}

// formatDate produces a locale-specific date string ("Apr 13, 2026")
// in the viewer's timezone, or the raw input on parse failure.
// Exported for direct testing.
export function formatDate(raw: string): string {
  if (!raw) return "";
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return raw;
  return date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}
