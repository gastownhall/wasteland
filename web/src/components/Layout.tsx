import { useCallback, useMemo, useState, useSyncExternalStore } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { getImpersonation, setImpersonation } from "../api/client";
import { useWasteland } from "../context/WastelandContext";
import { CommandsContext, useCommandRegistry } from "../hooks/useCommands";
import { useGlobalShortcuts } from "../hooks/useGlobalShortcuts";
import { CommandPalette } from "./CommandPalette";
import styles from "./Layout.module.css";
import { ShortcutHelp } from "./ShortcutHelp";

export function Layout() {
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [impersonating, setImpersonating] = useState<string>(getImpersonation() ?? "");
  const [impersonateInput, setImpersonateInput] = useState(getImpersonation() ?? "");
  const navigate = useNavigate();
  const { wastelands, active, authenticated, connected, environment, switchTo } = useWasteland();

  const { register, getCommands, subscribe } = useCommandRegistry();
  const commands = useSyncExternalStore(subscribe, getCommands);

  const navCommands = useMemo(
    () => [
      { id: "nav-board", label: "Go to Board", group: "Navigation", shortcut: "g b", action: () => navigate("/") },
      {
        id: "nav-dashboard",
        label: "Go to Dashboard",
        group: "Navigation",
        shortcut: "g d",
        action: () => navigate("/me"),
      },
      {
        id: "nav-scoreboard",
        label: "Go to Scoreboard",
        group: "Navigation",
        shortcut: "g l",
        action: () => navigate("/scoreboard"),
      },
      {
        id: "nav-settings",
        label: "Go to Settings",
        group: "Navigation",
        shortcut: "g s",
        action: () => navigate("/settings"),
      },
    ],
    [navigate],
  );

  // Register navigation commands once
  useState(() => register(navCommands));

  const togglePalette = useCallback(() => setPaletteOpen((o) => !o), []);
  const toggleHelp = useCallback(() => setHelpOpen((o) => !o), []);

  useGlobalShortcuts({
    onTogglePalette: togglePalette,
    onToggleHelp: toggleHelp,
  });

  const contextValue = useMemo(() => ({ commands, register }), [commands, register]);

  return (
    <CommandsContext.Provider value={contextValue}>
      <div className={styles.layout}>
        {environment === "staging" && (
          <div className={styles.stagingBanner}>
            <span>staging</span>
            <span className={styles.impersonateBar}>
              {impersonating ? (
                <>
                  <span className={styles.impersonateLabel}>viewing as {impersonating}</span>
                  <button
                    type="button"
                    className={styles.impersonateBtn}
                    onClick={() => {
                      setImpersonation(null);
                      setImpersonating("");
                      setImpersonateInput("");
                      window.location.reload();
                    }}
                  >
                    stop
                  </button>
                </>
              ) : (
                <>
                  <input
                    className={styles.impersonateInput}
                    type="text"
                    placeholder="impersonate rig handle..."
                    value={impersonateInput}
                    onChange={(e) => setImpersonateInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && impersonateInput.trim()) {
                        setImpersonation(impersonateInput.trim());
                        setImpersonating(impersonateInput.trim());
                        window.location.reload();
                      }
                    }}
                  />
                  <button
                    type="button"
                    className={styles.impersonateBtn}
                    disabled={!impersonateInput.trim()}
                    onClick={() => {
                      if (impersonateInput.trim()) {
                        setImpersonation(impersonateInput.trim());
                        setImpersonating(impersonateInput.trim());
                        window.location.reload();
                      }
                    }}
                  >
                    go
                  </button>
                </>
              )}
            </span>
          </div>
        )}
        <a href="#main-content" className="skip-link">
          Skip to content
        </a>
        <nav className={styles.nav} aria-label="Main navigation">
          <span className={styles.logo}>wasteland</span>
          {wastelands.length > 1 ? (
            <select
              className={styles.switcher}
              value={active ?? ""}
              onChange={(e) => switchTo(e.target.value)}
              aria-label="Active wasteland"
            >
              {wastelands.map((w) => (
                <option key={w.upstream} value={w.upstream}>
                  {w.upstream}
                </option>
              ))}
            </select>
          ) : wastelands.length === 1 ? (
            <span className={styles.upstreamLabel}>{wastelands[0].upstream}</span>
          ) : null}
          <NavLink to="/" end className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
            board
          </NavLink>
          {connected && (
            <NavLink to="/me" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
              me
            </NavLink>
          )}
          <NavLink to="/profile" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
            profiles
          </NavLink>
          <NavLink to="/scoreboard" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
            scoreboard
          </NavLink>
          {connected ? (
            <NavLink to="/settings" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
              settings
            </NavLink>
          ) : (
            <NavLink to="/connect" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
              {authenticated ? "connect" : "sign in"}
            </NavLink>
          )}
          <NavLink to="/skill" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
            skill
          </NavLink>
        </nav>
        <main id="main-content" className={styles.main}>
          <Outlet />
        </main>
      </div>

      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
      <ShortcutHelp open={helpOpen} onClose={() => setHelpOpen(false)} />
    </CommandsContext.Provider>
  );
}
