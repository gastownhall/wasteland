import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

const mocked = vi.hoisted(() => ({
  profile: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("../api/client", async () => {
  const actual = await vi.importActual<typeof import("../api/client")>("../api/client");
  return {
    ...actual,
    profile: mocked.profile,
  };
});

vi.mock("sonner", () => ({
  toast: {
    error: mocked.toastError,
  },
}));

import { ProfileView } from "./ProfileView";

function renderProfile(path = "/profile/alice") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/profile/:handle" element={<ProfileView />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  vi.clearAllMocks();
});

describe("ProfileView", () => {
  it("renders profile details and evidence tables", async () => {
    mocked.profile.mockResolvedValue({
      kind: "character_sheet",
      handle: "alice",
      display_name: "Alice Chen",
      bio: "Builds things",
      location: "Berlin",
      source: "github",
      confidence: 0.92,
      created_at: "2026-03-01T00:00:00Z",
      total_repos: 12,
      total_stars: 345,
      followers: 1200,
      account_age: 4.5,
      quality: 4.2,
      reliability: 4.1,
      creativity: 3.9,
      assessment_count: 2,
      languages: [{ name: "TypeScript", quality: 5, reliability: 4, creativity: 4, confidence: 0.8, message: "OSS" }],
      domains: [{ name: "Frontend", quality: 4, reliability: 4, creativity: 5, confidence: 0.8, message: "UI work" }],
      capabilities: [
        { name: "Testing", quality: 5, reliability: 5, creativity: 3, confidence: 0.9, message: "Deep tests" },
      ],
      notable_projects: [
        { name: "wasteland", stars: 345, impact_tier: "high", role: "maintainer", languages: ["TypeScript", "Go"] },
      ],
    });

    renderProfile();

    await waitFor(() => expect(screen.getByText("Alice Chen")).toBeInTheDocument());
    expect(screen.getByText("@alice")).toBeInTheDocument();
    expect(screen.getByText("Builds things")).toBeInTheDocument();
    expect(screen.getByText("1,200 followers")).toBeInTheDocument();
    expect(screen.getByText("92% confidence")).toBeInTheDocument();
    expect(screen.getByText("Languages (1)")).toBeInTheDocument();
    expect(screen.getByText("Domains (1)")).toBeInTheDocument();
    expect(screen.getByText("Capabilities (1)")).toBeInTheDocument();
    expect(screen.getByText("Notable Projects (1)")).toBeInTheDocument();
    expect(screen.getByText("wasteland")).toBeInTheDocument();
    expect(screen.getByText("TypeScript, Go")).toBeInTheDocument();
  });

  it("shows an error when profile loading fails", async () => {
    mocked.profile.mockRejectedValue(new Error("profile failed"));

    renderProfile();

    await waitFor(() => expect(screen.getByText("profile failed")).toBeInTheDocument());
    expect(mocked.toastError).toHaveBeenCalledWith("profile failed");
  });

  it("shows a fallback when no profile data is returned", async () => {
    mocked.profile.mockResolvedValue(null);

    renderProfile();

    await waitFor(() => expect(screen.getByText("No profile data.")).toBeInTheDocument());
  });

  it("renders the stamp feed variant with GitHub link and cards", async () => {
    mocked.profile.mockResolvedValue({
      kind: "stamp_feed",
      handle: "rileywhite",
      github_url: "https://github.com/rileywhite",
      stamps_error: null,
      stamps: [
        {
          id: "s1",
          skill_tags: ["go", "backend"],
          quality: 4,
          reliability: 5,
          validator: "julianknutsen",
          message: "Added retry middleware",
          evidence_url: "https://github.com/gastownhall/gascity/pull/548",
          evidence_label: "gastownhall/gascity#548",
          created_at: "2026-04-13T09:33:05Z",
        },
      ],
    });

    renderProfile("/profile/rileywhite");

    await waitFor(() => expect(screen.getByText("rileywhite")).toBeInTheDocument());
    expect(screen.getByText(/No character sheet yet/)).toBeInTheDocument();

    const githubLink = screen.getByText("View on GitHub ↗");
    expect(githubLink).toHaveAttribute("href", "https://github.com/rileywhite");
    expect(githubLink).toHaveAttribute("target", "_blank");
    expect(githubLink).toHaveAttribute("rel", "noopener noreferrer");

    expect(screen.getByText("gastownhall/gascity#548")).toBeInTheDocument();
    expect(screen.getByText("Q4 R5")).toBeInTheDocument();
    expect(screen.getByText(/validated by julianknutsen/)).toBeInTheDocument();
    expect(screen.getByText("Added retry middleware")).toBeInTheDocument();
  });

  it("shows an inline error message when stamps fail to load", async () => {
    mocked.profile.mockResolvedValue({
      kind: "stamp_feed",
      handle: "rileywhite",
      github_url: "https://github.com/rileywhite",
      stamps_error: "stamps_unavailable",
      stamps: [],
    });

    renderProfile("/profile/rileywhite");

    await waitFor(() => expect(screen.getByText(/Couldn't load recent stamps/)).toBeInTheDocument());
    expect(screen.getByText("rileywhite")).toBeInTheDocument();
    // Stamp cards must NOT render on the degraded path.
    expect(screen.queryByRole("article")).toBeNull();
  });

  it("rejects unsafe evidence_url schemes", async () => {
    mocked.profile.mockResolvedValue({
      kind: "stamp_feed",
      handle: "attacker",
      github_url: "https://github.com/attacker",
      stamps_error: null,
      stamps: [
        {
          id: "s-bad",
          skill_tags: ["go"],
          quality: 0,
          reliability: 0,
          validator: "reviewer",
          evidence_url: "javascript:alert(1)",
          evidence_label: "click me",
          created_at: "2026-04-13T00:00:00Z",
        },
      ],
    });

    renderProfile("/profile/attacker");

    await waitFor(() => expect(screen.getByText("attacker")).toBeInTheDocument());
    // The card still renders, but the javascript: link is filtered out.
    expect(screen.queryByText("click me")).toBeNull();
    expect(screen.queryByRole("link", { name: /click me/ })).toBeNull();
  });
});
