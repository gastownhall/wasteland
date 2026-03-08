import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { toast } from "sonner";
import { profile as fetchProfile } from "../api/client";
import type { ProfileProject, ProfileResponse, ProfileSkillEntry } from "../api/types";
import styles from "./ProfileView.module.css";
import { SkeletonRows } from "./Skeleton";

export function ProfileView() {
  const { handle } = useParams<{ handle: string }>();
  const [data, setData] = useState<ProfileResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!handle) return;
    let cancelled = false;
    setLoading(true);
    setError("");
    (async () => {
      try {
        const result = await fetchProfile(handle);
        if (!cancelled) setData(result);
      } catch (e) {
        if (cancelled) return;
        const msg = e instanceof Error ? e.message : "Failed to load profile";
        setError(msg);
        toast.error(msg);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [handle]);

  if (loading)
    return (
      <div className={styles.page}>
        <SkeletonRows count={8} />
      </div>
    );
  if (error) return <p className={styles.errorText}>{error}</p>;
  if (!data) return <p className={styles.dimText}>No profile data.</p>;

  const name = data.display_name || data.handle;

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h2 className={styles.name}>{name}</h2>
        <span className={styles.handle}>@{data.handle}</span>
        {data.bio && <p className={styles.bio}>{data.bio}</p>}
        <div className={styles.meta}>
          {data.location && <span>{data.location}</span>}
          {data.followers != null && data.followers > 0 && <span>{data.followers.toLocaleString()} followers</span>}
          {data.account_age != null && data.account_age > 0 && <span>{data.account_age.toFixed(1)} years</span>}
        </div>
        <div className={styles.provenance}>
          <span className={styles.sourceBadge} data-source={data.source}>
            {data.source === "github" ? "GitHub profile" : data.source}
          </span>
          <span className={styles.confidence} title="How confident the system is in this profile data">
            {(data.confidence * 100).toFixed(0)}% confidence
          </span>
          {data.assessment_count > 0 && (
            <span
              className={styles.assessmentBadge}
              title="Skill assessments from GitHub profile analysis — not Wasteland reputation stamps"
            >
              {data.assessment_count} {data.assessment_count === 1 ? "assessment" : "assessments"}
            </span>
          )}
          {data.assessment_count === 0 && <span className={styles.unverifiedNote}>No assessments yet</span>}
        </div>
      </div>

      <section className={styles.section}>
        <h3 className={styles.sectionTitle}>Value Dimensions</h3>
        <div className={styles.bars}>
          <DimensionBar label="Quality" value={data.quality} />
          <DimensionBar label="Reliability" value={data.reliability} />
          <DimensionBar label="Creativity" value={data.creativity} />
        </div>
      </section>

      {data.languages && data.languages.length > 0 && (
        <section className={styles.section}>
          <h3 className={styles.sectionTitle}>Languages ({data.languages.length})</h3>
          <SkillTable entries={data.languages} />
        </section>
      )}

      {data.domains && data.domains.length > 0 && (
        <section className={styles.section}>
          <h3 className={styles.sectionTitle}>Domains ({data.domains.length})</h3>
          <SkillTable entries={data.domains} />
        </section>
      )}

      {data.capabilities && data.capabilities.length > 0 && (
        <section className={styles.section}>
          <h3 className={styles.sectionTitle}>Capabilities ({data.capabilities.length})</h3>
          <SkillTable entries={data.capabilities} />
        </section>
      )}

      {data.notable_projects && data.notable_projects.length > 0 && (
        <section className={styles.section}>
          <h3 className={styles.sectionTitle}>Notable Projects ({data.notable_projects.length})</h3>
          <ProjectTable projects={data.notable_projects} />
        </section>
      )}

      <footer className={styles.footer}>
        {data.total_stars != null && <span>Total stars: {data.total_stars.toLocaleString()}</span>}
        {data.total_repos != null && <span>Repos: {data.total_repos}</span>}
      </footer>
    </div>
  );
}

function DimensionBar({ label, value }: { label: string; value: number }) {
  // Values are on 0-5 stamp scale
  const clamped = Math.max(0, Math.min(5, value));
  const pct = Math.round((clamped / 5) * 100);
  return (
    <div className={styles.barRow}>
      <span className={styles.barLabel}>{label}</span>
      <div className={styles.barTrack}>
        <div className={styles.barFill} style={{ width: `${pct}%` }} />
      </div>
      <span className={styles.barValue}>{clamped.toFixed(1)}</span>
    </div>
  );
}

function SkillTable({ entries }: { entries: ProfileSkillEntry[] }) {
  return (
    <table className={styles.table}>
      <thead>
        <tr>
          <th>Name</th>
          <th>Q</th>
          <th>R</th>
          <th>C</th>
          <th>Evidence</th>
        </tr>
      </thead>
      <tbody>
        {entries.map((e) => (
          <tr key={e.name}>
            <td className={styles.cellName}>{e.name}</td>
            <td className={styles.cellScore}>{e.quality}</td>
            <td className={styles.cellScore}>{e.reliability}</td>
            <td className={styles.cellScore}>{e.creativity}</td>
            <td className={styles.cellEvidence}>{e.message}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function ProjectTable({ projects }: { projects: ProfileProject[] }) {
  return (
    <table className={styles.table}>
      <thead>
        <tr>
          <th>Project</th>
          <th>Stars</th>
          <th>Tier</th>
          <th>Role</th>
          <th>Languages</th>
        </tr>
      </thead>
      <tbody>
        {projects.map((p) => (
          <tr key={p.name}>
            <td className={styles.cellName}>{p.name}</td>
            <td className={styles.cellScore}>{p.stars.toLocaleString()}</td>
            <td className={styles.cellScore}>{p.impact_tier || "-"}</td>
            <td>{p.role || "-"}</td>
            <td>{p.languages?.join(", ") || "-"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
