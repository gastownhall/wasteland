import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { authStatus, connectSession, joinWasteland, notifyConnect } from "../api/client";
import { connectDoltHub, initNango } from "../api/nango";
import { useWasteland } from "../context/WastelandContext";
import styles from "./ConnectPage.module.css";

export function ConnectPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams] = useSearchParams();
  const { refresh } = useWasteland();

  const rawReturnTo = searchParams.get("return_to");
  const returnTo = rawReturnTo && /^\/[^/]/.test(rawReturnTo) ? rawReturnTo : null;
  const reason = searchParams.get("reason");
  const [view, setView] = useState<"connect" | "join">("connect");
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);

  // Credentials.
  const [username, setUsername] = useState("");
  const [apiToken, setApiToken] = useState("");

  // Advanced overrides.
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [rigHandleOverride, setRigHandleOverride] = useState("");
  const [forkOrgOverride, setForkOrgOverride] = useState("");
  const [forkDB, setForkDB] = useState("wl-commons");
  const [upstream, setUpstream] = useState("hop/wl-commons");

  // Join form state (for /join route).
  const [joinForkOrg, setJoinForkOrg] = useState("");
  const [joinForkDB, setJoinForkDB] = useState("wl-commons");
  const [joinUpstream, setJoinUpstream] = useState("hop/wl-commons");

  // Check if already authenticated on mount.
  useEffect(() => {
    (async () => {
      try {
        const status = await authStatus();
        if (status.authenticated && status.connected) {
          if (location.pathname === "/join") {
            setView("join");
          } else {
            navigate(returnTo ?? "/", { replace: true });
            return;
          }
        }
      } catch {
        // Server may not be in hosted mode -- status 404 is expected.
      } finally {
        setLoading(false);
      }
    })();
  }, [navigate, location.pathname, returnTo]);

  const handleConnect = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!username.trim()) {
      toast.error("DoltHub username is required");
      return;
    }
    if (!apiToken.trim()) {
      toast.error("DoltHub API token is required");
      return;
    }

    const effectiveRigHandle = rigHandleOverride.trim() || username.trim();
    const effectiveForkOrg = forkOrgOverride.trim() || username.trim();

    setSubmitting(true);
    try {
      const session = await connectSession(effectiveRigHandle);
      const nango = initNango(session.token);
      const authResult = await connectDoltHub(nango, session.integration_id, apiToken.trim());

      const connectResp = await notifyConnect({
        connection_id: authResult.connectionId,
        rig_handle: effectiveRigHandle,
        fork_org: effectiveForkOrg,
        fork_db: forkDB.trim() || "wl-commons",
        upstream: upstream.trim() || "hop/wl-commons",
        display_name: username.trim(),
      });

      await refresh();
      if (connectResp.setup_warning) {
        toast.warning(connectResp.setup_warning);
      }
      toast.success("Connected to DoltHub");
      navigate(returnTo ?? "/", { replace: true });
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : "Connection failed");
    } finally {
      setSubmitting(false);
    }
  };

  const handleJoin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!joinForkOrg.trim() || !joinForkDB.trim() || !joinUpstream.trim()) {
      toast.error("Fork org, fork DB, and upstream are required");
      return;
    }

    setSubmitting(true);
    try {
      const joinResp = await joinWasteland({
        fork_org: joinForkOrg.trim(),
        fork_db: joinForkDB.trim(),
        upstream: joinUpstream.trim(),
      });

      await refresh();
      if (joinResp.setup_warning) {
        toast.warning(joinResp.setup_warning);
      }
      toast.success("Joined wasteland");
      navigate(returnTo ?? "/", { replace: true });
    } catch (err) {
      toast.error(err instanceof Error && err.message ? err.message : "Join failed");
    } finally {
      setSubmitting(false);
    }
  };

  if (loading) return <p className={styles.loadingText}>Loading...</p>;

  return (
    <div className={styles.page}>
      {reason === "expired" && (
        <p className={styles.errorHint}>
          Your DoltHub API token has expired or is invalid. Please reconnect. Make sure you create an API{" "}
          <strong>token</strong> (not a credential) at{" "}
          <a
            href="https://www.dolthub.com/settings/tokens"
            target="_blank"
            rel="noopener noreferrer"
            className={styles.link}
          >
            dolthub.com/settings/tokens
          </a>
          .
        </p>
      )}
      {returnTo && !reason && <p className={styles.hint}>Sign in to continue.</p>}
      <h2 className={styles.heading}>{view === "join" ? "Join a Wasteland" : "Connect to Wasteland"}</h2>

      {view === "join" && (
        <form onSubmit={handleJoin}>
          <div className={styles.section}>
            <h3 className={styles.sectionTitle}>Wasteland Details</h3>

            <label className={styles.fieldLabel}>
              Upstream
              <input
                className={styles.input}
                type="text"
                value={joinUpstream}
                onChange={(e) => setJoinUpstream(e.target.value)}
                placeholder="org/wl-commons"
              />
              <span className={styles.fieldHint}>
                The upstream wasteland to join. Only change for third-party wastelands.
              </span>
            </label>

            <label className={styles.fieldLabel}>
              Fork Org
              <input
                className={styles.input}
                type="text"
                value={joinForkOrg}
                onChange={(e) => setJoinForkOrg(e.target.value)}
                placeholder="your-dolthub-org"
              />
              <span className={styles.fieldHint}>
                DoltHub org where your fork lives. Usually your DoltHub username.
              </span>
            </label>

            <label className={styles.fieldLabel}>
              Fork DB
              <input
                className={styles.input}
                type="text"
                value={joinForkDB}
                onChange={(e) => setJoinForkDB(e.target.value)}
                placeholder="wl-commons"
              />
              <span className={styles.fieldHint}>
                Name of the forked database. Only change for third-party wastelands.
              </span>
            </label>
          </div>

          <div className={styles.actions}>
            <button type="button" className={styles.secondaryBtn} onClick={() => navigate(returnTo ?? "/")}>
              Cancel
            </button>
            <button type="submit" className={styles.primaryBtn} disabled={submitting}>
              {submitting ? "Joining..." : "Join"}
            </button>
          </div>
        </form>
      )}

      {view === "connect" && (
        <form onSubmit={handleConnect}>
          {/* Prerequisites */}
          <div className={styles.section}>
            <h3 className={styles.sectionTitle}>Prerequisites</h3>
            <p className={styles.prose}>
              Wasteland uses{" "}
              <a href="https://www.dolthub.com" target="_blank" rel="noopener noreferrer" className={styles.link}>
                DoltHub
              </a>
              {"  "}
              &mdash; a versioned database host &mdash; as its federation layer. You&rsquo;ll need a free account and an
              API token.
            </p>
            <ol className={styles.stepList}>
              <li>
                Create a DoltHub account at{" "}
                <a
                  href="https://www.dolthub.com/signin"
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.link}
                >
                  dolthub.com/signin
                </a>
              </li>
              <li>
                Generate an API token at{" "}
                <a
                  href="https://www.dolthub.com/settings/tokens"
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.link}
                >
                  dolthub.com/settings/tokens
                </a>{" "}
                with <strong>database</strong>, <strong>pull request</strong>, <strong>SQL</strong>, and{" "}
                <strong>branch</strong> permissions
              </li>
            </ol>
          </div>

          {/* Credentials */}
          <div className={styles.section}>
            <h3 className={styles.sectionTitle}>Credentials</h3>

            <label className={styles.fieldLabel}>
              DoltHub Username
              <input
                className={styles.input}
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="alice-dev"
              />
              <span className={styles.fieldHint}>
                Your DoltHub username or organization name. This is used as your rig handle and fork organization by
                default.
              </span>
            </label>

            <label className={styles.fieldLabel}>
              API Token
              <input
                className={styles.input}
                type="password"
                value={apiToken}
                onChange={(e) => setApiToken(e.target.value)}
                placeholder="your-dolthub-api-token"
              />
              <span className={styles.fieldHint}>
                Found at{" "}
                <a
                  href="https://www.dolthub.com/settings/tokens"
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.link}
                >
                  dolthub.com/settings/tokens
                </a>
                . Requires database, pull request, SQL, and branch permissions. Your token is stored securely and never
                written to disk.
              </span>
            </label>
          </div>

          {/* Advanced */}
          <button type="button" className={styles.advancedToggle} onClick={() => setShowAdvanced(!showAdvanced)}>
            {showAdvanced ? "\u2212 Advanced" : "+ Advanced"}
          </button>

          {showAdvanced && (
            <div className={styles.advancedBody}>
              <div className={styles.section}>
                <label className={styles.fieldLabel}>
                  Rig Handle
                  <input
                    className={styles.input}
                    type="text"
                    value={rigHandleOverride}
                    onChange={(e) => setRigHandleOverride(e.target.value)}
                    placeholder={username || "your-handle"}
                  />
                  <span className={styles.fieldHint}>
                    Your identity in the wasteland registry. Defaults to your DoltHub username.
                  </span>
                </label>

                <label className={styles.fieldLabel}>
                  Fork Org
                  <input
                    className={styles.input}
                    type="text"
                    value={forkOrgOverride}
                    onChange={(e) => setForkOrgOverride(e.target.value)}
                    placeholder={username || "your-dolthub-org"}
                  />
                  <span className={styles.fieldHint}>
                    DoltHub org where your fork lives. Defaults to your DoltHub username.
                  </span>
                </label>

                <label className={styles.fieldLabel}>
                  Fork DB
                  <input
                    className={styles.input}
                    type="text"
                    value={forkDB}
                    onChange={(e) => setForkDB(e.target.value)}
                    placeholder="wl-commons"
                  />
                  <span className={styles.fieldHint}>
                    Name of the forked database. Only change for third-party wastelands.
                  </span>
                </label>

                <label className={styles.fieldLabel}>
                  Upstream
                  <input
                    className={styles.input}
                    type="text"
                    value={upstream}
                    onChange={(e) => setUpstream(e.target.value)}
                    placeholder="hop/wl-commons"
                  />
                  <span className={styles.fieldHint}>
                    The upstream wasteland to join. Only change for third-party wastelands.
                  </span>
                </label>
              </div>
            </div>
          )}

          <div className={styles.actions}>
            <button type="button" className={styles.secondaryBtn} onClick={() => navigate(returnTo ?? "/")}>
              Cancel
            </button>
            <button type="submit" className={styles.primaryBtn} disabled={submitting}>
              {submitting ? "Connecting..." : "Connect"}
            </button>
          </div>
        </form>
      )}
    </div>
  );
}
