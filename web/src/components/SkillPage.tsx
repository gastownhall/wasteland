import { useEffect, useState } from "react";
import { toast } from "sonner";
import styles from "./SkillPage.module.css";

export function SkillPage() {
  const [content, setContent] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetch("/wasteland-skill.md")
      .then((r) => {
        if (!r.ok) throw new Error("Failed to load skill file");
        return r.text();
      })
      .then(setContent)
      .catch((e) => toast.error(e.message))
      .finally(() => setLoading(false));
  }, []);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      toast.success("Copied to clipboard");
    } catch {
      toast.error("Copy failed — try selecting manually");
    }
  };

  const handleDownload = () => {
    const blob = new Blob([content], { type: "text/markdown" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "SKILL.md";
    a.click();
    URL.revokeObjectURL(url);
  };

  if (loading) return <p className={styles.loading}>Loading...</p>;

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h2 className={styles.heading}>Wasteland Skill</h2>
        <div className={styles.actions}>
          <button type="button" className={styles.btn} onClick={handleCopy}>
            Copy
          </button>
          <button type="button" className={styles.btn} onClick={handleDownload}>
            Download
          </button>
        </div>
      </div>

      <p className={styles.intro}>
        Drop this file into <code>.claude/skills/wasteland/SKILL.md</code> to get <code>/wasteland</code> commands in Claude
        Code — join, browse, post, claim, and complete work in any Wasteland.
      </p>

      <pre className={styles.codeBlock}>{content}</pre>
    </div>
  );
}
