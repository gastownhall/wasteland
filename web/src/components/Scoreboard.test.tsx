import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { makeScoreboardEntry, makeScoreboardResponse, mockFetch, renderWithRouter } from "../test-utils";
import { Scoreboard } from "./Scoreboard";

let cleanupFetch: () => void;
afterEach(() => cleanupFetch?.());

describe("Scoreboard", () => {
  it("renders scoreboard entries", async () => {
    cleanupFetch = mockFetch(() =>
      makeScoreboardResponse({
        entries: [
          makeScoreboardEntry({ rig_handle: "alice", display_name: "Alice Chen" }),
          makeScoreboardEntry({ rig_handle: "bob", display_name: "Bob Lee" }),
        ],
      }),
    );
    renderWithRouter(<Scoreboard />);
    await waitFor(() => expect(screen.getAllByText("Alice Chen").length).toBeGreaterThan(0));
    expect(screen.getAllByText("Bob Lee").length).toBeGreaterThan(0);
  });

  it("shows empty state when no entries", async () => {
    cleanupFetch = mockFetch(() => makeScoreboardResponse());
    renderWithRouter(<Scoreboard />);
    await waitFor(() => expect(screen.getByText("No scoreboard data yet")).toBeInTheDocument());
  });

  it("shows skeleton while loading", () => {
    cleanupFetch = mockFetch(() => new Promise(() => {}));
    renderWithRouter(<Scoreboard />);
    expect(screen.getByText("Scoreboard")).toBeInTheDocument();
  });

  it("shows error on fetch failure", async () => {
    cleanupFetch = mockFetch(() => new Response(JSON.stringify({ error: "scoreboard error" }), { status: 500 }));
    renderWithRouter(<Scoreboard />);
    await waitFor(() => expect(screen.getByText("scoreboard error")).toBeInTheDocument());
  });

  it("top 3 podium cards render with correct ranks", async () => {
    cleanupFetch = mockFetch(() =>
      makeScoreboardResponse({
        entries: [
          makeScoreboardEntry({ rig_handle: "first", display_name: "First Place", weighted_score: 100 }),
          makeScoreboardEntry({ rig_handle: "second", display_name: "Second Place", weighted_score: 80 }),
          makeScoreboardEntry({ rig_handle: "third", display_name: "Third Place", weighted_score: 60 }),
        ],
      }),
    );
    renderWithRouter(<Scoreboard />);
    await waitFor(() => expect(screen.getByTestId("podium-1")).toBeInTheDocument());
    expect(screen.getByTestId("podium-2")).toBeInTheDocument();
    expect(screen.getByTestId("podium-3")).toBeInTheDocument();
    expect(screen.getByText("#1")).toBeInTheDocument();
    expect(screen.getByText("#2")).toBeInTheDocument();
    expect(screen.getByText("#3")).toBeInTheDocument();
  });

  it("rig handles link to profile pages", async () => {
    cleanupFetch = mockFetch(() =>
      makeScoreboardResponse({
        entries: [makeScoreboardEntry({ rig_handle: "alice", display_name: "Alice Chen" })],
      }),
    );
    renderWithRouter(<Scoreboard />);
    await waitFor(() => expect(screen.getAllByText("Alice Chen").length).toBeGreaterThan(0));
    const links = screen.getAllByRole("link", { name: "Alice Chen" });
    expect(links.length).toBeGreaterThan(0);
    expect(links[0]).toHaveAttribute("href", "/profile/alice");
  });

  it("trust tier badges render with correct tier text", async () => {
    cleanupFetch = mockFetch(() =>
      makeScoreboardResponse({
        entries: [
          makeScoreboardEntry({ rig_handle: "a", trust_tier: "warrior" }),
          makeScoreboardEntry({ rig_handle: "b", trust_tier: "settler" }),
        ],
      }),
    );
    renderWithRouter(<Scoreboard />);
    await waitFor(() => expect(screen.getAllByText("warrior").length).toBeGreaterThan(0));
    expect(screen.getAllByText("settler").length).toBeGreaterThan(0);
  });
});
