import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { createItem, updateItem } from "../api/client";
import type { DetailResponse, MutationResponse, WantedItem } from "../api/types";
import { useFocusTrap } from "../hooks/useFocusTrap";
import styles from "./WantedForm.module.css";

const types = ["feature", "bug", "design", "rfc", "docs"];
const priorities = [0, 1, 2, 3, 4];
const efforts = ["trivial", "small", "medium", "large", "epic"];

interface WantedFormProps {
  item?: WantedItem;
  mode?: "default" | "inference";
  onClose: () => void;
  onSaved: (detail?: DetailResponse) => void;
}

function inferTitle(prompt: string): string {
  const maxLen = 60;
  let s = prompt;
  if (s.length > maxLen) {
    s = `${s.slice(0, maxLen)}...`;
  }
  return `infer: ${s}`;
}

export function WantedForm({ item, mode = "default", onClose, onSaved }: WantedFormProps) {
  const isEdit = !!item;
  const isInfer = mode === "inference";

  const [title, setTitle] = useState(item?.title ?? "");
  const [description, setDescription] = useState(item?.description ?? "");
  const [project, setProject] = useState(item?.project ?? "");
  const [type, setType] = useState(item?.type ?? "feature");
  const [priority, setPriority] = useState(item?.priority ?? 2);
  const [effortLevel, setEffortLevel] = useState(item?.effort_level ?? "medium");
  const [tags, setTags] = useState(item?.tags?.join(", ") ?? "");
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);

  // Inference-mode state
  const [prompt, setPrompt] = useState("");
  const [model, setModel] = useState("");
  const [seed, setSeed] = useState(42);
  const [maxTokens, setMaxTokens] = useState(0);
  const [showAdvanced, setShowAdvanced] = useState(false);

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
        e.preventDefault();
        handleSubmit();
      }
    };
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  });

  const handleSubmit = async () => {
    if (isInfer) {
      if (!prompt.trim() || !model.trim() || savingRef.current) return;
    } else {
      if (!title.trim() || savingRef.current) return;
    }
    savingRef.current = true;
    setSaving(true);

    try {
      let resp: MutationResponse;
      if (isInfer) {
        resp = await createItem({
          title: inferTitle(prompt.trim()),
          description: JSON.stringify({
            prompt: prompt.trim(),
            model: model.trim(),
            seed,
            max_tokens: maxTokens,
          }),
          type: "inference",
          priority: 2,
          effort_level: "small",
        });
        toast.success("Inference job posted");
      } else if (isEdit && item) {
        const parsedTags = tags
          .split(",")
          .map((t) => t.trim())
          .filter(Boolean);
        resp = await updateItem(item.id, {
          title: title.trim(),
          description: description.trim(),
          project: project.trim(),
          type,
          priority,
          effort_level: effortLevel,
          tags: parsedTags,
          tags_set: true,
        });
        toast.success("Item updated");
      } else {
        const parsedTags = tags
          .split(",")
          .map((t) => t.trim())
          .filter(Boolean);
        resp = await createItem({
          title: title.trim(),
          description: description.trim() || undefined,
          project: project.trim() || undefined,
          type,
          priority,
          effort_level: effortLevel,
          tags: parsedTags.length > 0 ? parsedTags : undefined,
        });
        toast.success("Item posted");
      }

      if (resp.detail?.pr_url) {
        toast.success(`PR submitted: ${resp.detail.pr_url}`);
      }

      onSaved(resp.detail ?? undefined);
      onClose();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to save");
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  };

  const trapRef = useFocusTrap(true);

  const canSubmit = isInfer ? !!(prompt.trim() && model.trim()) : !!title.trim();

  return (
    <div className={isInfer ? styles.overlayInfer : styles.overlay} onClick={onClose}>
      <div
        ref={trapRef}
        className={isInfer ? styles.dialogInfer : styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-label={isInfer ? "Post inference job" : isEdit ? "Edit item" : "Post new item"}
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className={isInfer ? styles.titleInfer : styles.title}>
          {isInfer ? "Post Inference Job" : isEdit ? "Edit Item" : "Post New Item"}
        </h2>

        {isInfer ? (
          <>
            <div className={styles.field}>
              <label className={styles.label}>Prompt</label>
              <textarea
                className={styles.textareaMono}
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                placeholder="What should the model generate?"
                autoFocus
              />
            </div>

            <div className={styles.field}>
              <label className={styles.label}>Model</label>
              <input
                className={styles.inputMono}
                type="text"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder="llama3.2:1b"
              />
            </div>

            <button type="button" className={styles.advancedToggle} onClick={() => setShowAdvanced(!showAdvanced)}>
              {showAdvanced ? "− Advanced" : "+ Advanced"}
            </button>

            {showAdvanced && (
              <div className={styles.row}>
                <div className={styles.field}>
                  <label className={styles.label}>Seed</label>
                  <input
                    className={styles.inputMono}
                    type="number"
                    value={seed}
                    onChange={(e) => setSeed(Number(e.target.value))}
                  />
                </div>
                <div className={styles.field}>
                  <label className={styles.label}>Max Tokens</label>
                  <input
                    className={styles.inputMono}
                    type="number"
                    value={maxTokens}
                    onChange={(e) => setMaxTokens(Number(e.target.value))}
                  />
                </div>
              </div>
            )}
          </>
        ) : (
          <>
            <div className={styles.field}>
              <label className={styles.label}>Title</label>
              <input
                className={styles.input}
                type="text"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="What needs to be done?"
                autoFocus
              />
            </div>

            <div className={styles.field}>
              <label className={styles.label}>Description</label>
              <textarea
                className={styles.textarea}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Details, context, acceptance criteria..."
              />
            </div>

            <div className={styles.row}>
              <div className={styles.field}>
                <label className={styles.label}>Project</label>
                <input
                  className={styles.input}
                  type="text"
                  value={project}
                  onChange={(e) => setProject(e.target.value)}
                  placeholder="project name"
                />
              </div>
              <div className={styles.field}>
                <label className={styles.label}>Type</label>
                <select className={styles.select} value={type} onChange={(e) => setType(e.target.value)}>
                  {types.map((t) => (
                    <option key={t} value={t}>
                      {t}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className={styles.row}>
              <div className={styles.field}>
                <label className={styles.label}>Priority</label>
                <select
                  className={styles.select}
                  value={priority}
                  onChange={(e) => setPriority(Number(e.target.value))}
                >
                  {priorities.map((p) => (
                    <option key={p} value={p}>
                      P{p}
                    </option>
                  ))}
                </select>
              </div>
              <div className={styles.field}>
                <label className={styles.label}>Effort</label>
                <select className={styles.select} value={effortLevel} onChange={(e) => setEffortLevel(e.target.value)}>
                  {efforts.map((e) => (
                    <option key={e} value={e}>
                      {e}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className={styles.field}>
              <label className={styles.label}>Tags</label>
              <input
                className={styles.input}
                type="text"
                value={tags}
                onChange={(e) => setTags(e.target.value)}
                placeholder="tag1, tag2, ..."
              />
            </div>
          </>
        )}

        <div className={styles.actions}>
          <button type="button" className={styles.cancelBtn} onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className={isInfer ? styles.submitBtnInfer : styles.submitBtn}
            onClick={handleSubmit}
            disabled={!canSubmit || saving}
          >
            {saving ? "Saving..." : isInfer ? "Post Job" : isEdit ? "Update" : "Post"}
          </button>
        </div>

        <p className={styles.hint}>Cmd+Enter to submit &middot; Esc to close</p>
      </div>
    </div>
  );
}
