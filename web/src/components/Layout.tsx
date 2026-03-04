import { useCallback, useMemo, useState, useSyncExternalStore } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useWasteland } from "../context/WastelandContext";
import { CommandsContext, useCommandRegistry } from "../hooks/useCommands";
import { useGlobalShortcuts } from "../hooks/useGlobalShortcuts";
import { CommandPalette } from "./CommandPalette";
import styles from "./Layout.module.css";
import { ShortcutHelp } from "./ShortcutHelp";

export function Layout() {
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const navigate = useNavigate();
  const { wastelands, active, switchTo } = useWasteland();

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
          <NavLink to="/me" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
            me
          </NavLink>
          <NavLink to="/profile" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
            profiles
          </NavLink>
          <NavLink to="/scoreboard" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
            scoreboard
          </NavLink>
          <NavLink to="/settings" className={({ isActive }) => (isActive ? styles.navLinkActive : styles.navLink)}>
            settings
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
