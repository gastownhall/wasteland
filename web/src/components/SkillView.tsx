import { useCallback, useEffect, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { toast } from "sonner";
import { SkeletonRows } from "./Skeleton";
import styles from "./SkillView.module.css";

const RAW_URL =
  "https://raw.githubusercontent.com/gastownhall/marketplace/main/plugins/wasteland/skills/wasteland/SKILL.md";
const REPO_URL = "https://github.com/gastownhall/marketplace/blob/main/plugins/wasteland/skills/wasteland/SKILL.md";

export function SkillView() {
  const [markdown, setMarkdown] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();

    fetch(RAW_URL, { signal: controller.signal })
      .then((res) => {
        if (!res.ok) throw new Error(`Failed to fetch (${res.status})`);
        return res.text();
      })
      .then((text) => text.replace(/^---\n[\s\S]*?\n---\n*/, ""))
      .then(setMarkdown)
      .catch((err) => {
        if (err.name !== "AbortError") setError(err.message);
      });

    return () => controller.abort();
  }, []);

  const copyToClipboard = useCallback(() => {
    if (!markdown) return;
    navigator.clipboard.writeText(markdown).then(
      () => toast.success("Copied to clipboard"),
      () => toast.error("Failed to copy"),
    );
  }, [markdown]);

  const downloadFile = useCallback(() => {
    if (!markdown) return;
    const blob = new Blob([markdown], { type: "text/markdown" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "wasteland-skill.md";
    a.click();
    URL.revokeObjectURL(url);
  }, [markdown]);

  if (error) {
    return (
      <div className={styles.container}>
        <p className={styles.error}>Failed to load skill documentation: {error}</p>
        <a href={REPO_URL} target="_blank" rel="noopener noreferrer" className={styles.fallbackLink}>
          View on GitHub
        </a>
      </div>
    );
  }

  if (markdown === null) {
    return (
      <div className={styles.container}>
        <SkeletonRows count={8} />
      </div>
    );
  }

  return (
    <div className={styles.container}>
      <div className={styles.actions}>
        <button type="button" className={styles.actionBtn} onClick={copyToClipboard}>
          copy
        </button>
        <button type="button" className={styles.actionBtn} onClick={downloadFile}>
          download
        </button>
        <a href={REPO_URL} target="_blank" rel="noopener noreferrer" className={styles.actionBtn}>
          github
        </a>
      </div>
      <div className={styles.content}>
        <Markdown remarkPlugins={[remarkGfm]}>{markdown}</Markdown>
      </div>
    </div>
  );
}
