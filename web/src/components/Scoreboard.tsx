import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { scoreboard } from "../api/client";
import type { ScoreboardEntry, ScoreboardResponse } from "../api/types";
import { EmptyState } from "./EmptyState";
import styles from "./Scoreboard.module.css";
import { SkeletonRows } from "./Skeleton";

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "just now";
  if (mins === 1) return "1 minute ago";
  if (mins < 60) return `${mins} minutes ago`;
  const hours = Math.floor(mins / 60);
  if (hours === 1) return "1 hour ago";
  return `${hours} hours ago`;
}

function TrustBadge({ tier }: { tier: string }) {
  return (
    <span className={styles.trustBadge} data-tier={tier}>
      {tier}
    </span>
  );
}

function SkillTags({ skills }: { skills?: string[] }) {
  if (!skills || skills.length === 0) return null;
  return (
    <span className={styles.skillTags}>
      {skills.map((s) => (
        <span key={s} className={styles.skillTag}>
          {s}
        </span>
      ))}
    </span>
  );
}

function PodiumCard({ entry, rank }: { entry: ScoreboardEntry; rank: number }) {
  const name = entry.display_name || entry.rig_handle;
  return (
    <div className={styles.podiumCard} data-rank={rank} data-testid={`podium-${rank}`}>
      <span className={styles.podiumRank}>#{rank}</span>
      <Link to={`/profile/${entry.rig_handle}`} className={`${styles.podiumName} ${styles.rigLink}`}>
        {name}
      </Link>
      <TrustBadge tier={entry.trust_tier} />
      <span className={styles.podiumScore}>{entry.weighted_score}</span>
      <div className={styles.podiumStats}>
        <span>{entry.stamp_count} stamps</span>
        <span>{entry.unique_towns} towns</span>
        <span>{entry.completions} done</span>
      </div>
      <SkillTags skills={entry.top_skills} />
    </div>
  );
}

export function Scoreboard() {
  const [data, setData] = useState<ScoreboardResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    (async () => {
      try {
        setData(await scoreboard());
      } catch (e) {
        const msg = e instanceof Error ? e.message : "Failed to load";
        setError(msg);
        toast.error(msg);
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  if (loading)
    return (
      <div className={styles.page}>
        <h2 className={styles.heading}>Scoreboard</h2>
        <SkeletonRows count={10} />
      </div>
    );
  if (error) return <p className={styles.errorText}>{error}</p>;
  if (!data || data.entries.length === 0)
    return (
      <div className={styles.page}>
        <h2 className={styles.heading}>Scoreboard</h2>
        <EmptyState title="No scoreboard data yet" description="Scores will appear here once rigs earn stamps." />
      </div>
    );

  const top3 = data.entries.slice(0, 3);
  const all = data.entries;

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <h2 className={styles.heading}>Scoreboard</h2>
        <span className={styles.updatedAt}>Updated {timeAgo(data.updated_at)}</span>
      </div>

      <div className={styles.podium}>
        {top3.map((entry, i) => (
          <PodiumCard key={entry.rig_handle} entry={entry} rank={i + 1} />
        ))}
      </div>

      <table className={styles.table}>
        <thead className={styles.thead}>
          <tr>
            <th className={styles.th}>Rank</th>
            <th className={styles.th}>Rig</th>
            <th className={styles.th}>Trust Tier</th>
            <th className={styles.th}>Score</th>
            <th className={styles.th}>Stamps</th>
            <th className={styles.th}>Towns</th>
            <th className={styles.th}>Completions</th>
            <th className={styles.th}>Quality</th>
            <th className={styles.th}>Reliability</th>
            <th className={styles.th}>Skills</th>
          </tr>
        </thead>
        <tbody>
          {all.map((entry, i) => (
            <tr key={entry.rig_handle} className={styles.row}>
              <td className={`${styles.td} ${styles.rank}`}>{i + 1}</td>
              <td className={styles.td}>
                <Link to={`/profile/${entry.rig_handle}`} className={styles.rigLink}>
                  {entry.display_name || entry.rig_handle}
                </Link>
              </td>
              <td className={styles.td}>
                <TrustBadge tier={entry.trust_tier} />
              </td>
              <td className={`${styles.td} ${styles.scoreCell}`}>{entry.weighted_score}</td>
              <td className={styles.td}>{entry.stamp_count}</td>
              <td className={styles.td}>{entry.unique_towns}</td>
              <td className={styles.td}>{entry.completions}</td>
              <td className={styles.td}>{entry.avg_quality.toFixed(1)}</td>
              <td className={styles.td}>{entry.avg_reliability.toFixed(1)}</td>
              <td className={styles.td}>
                <SkillTags skills={entry.top_skills} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <div className={styles.cardList}>
        {all.map((entry, i) => (
          <div key={entry.rig_handle} className={styles.card}>
            <div className={styles.cardTop}>
              <span className={styles.rank}>{i + 1}</span>
              <Link to={`/profile/${entry.rig_handle}`} className={`${styles.rigLink} ${styles.cardTitle}`}>
                {entry.display_name || entry.rig_handle}
              </Link>
            </div>
            <div className={styles.cardMeta}>
              <TrustBadge tier={entry.trust_tier} />
              <span className={styles.scoreCell}>{entry.weighted_score} pts</span>
              <SkillTags skills={entry.top_skills} />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
