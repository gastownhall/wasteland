import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { StampFeedResponse } from "../api/types";
import { formatDate, StampFeedView, safeHref } from "./StampFeedView";

describe("safeHref", () => {
  it("keeps http URLs", () => {
    expect(safeHref("http://example.com/path")).toBe("http://example.com/path");
  });

  it("keeps https URLs", () => {
    expect(safeHref("https://github.com/foo/bar")).toBe("https://github.com/foo/bar");
  });

  it("rejects javascript: URLs", () => {
    expect(safeHref("javascript:alert(1)")).toBeNull();
  });

  it("rejects data: URLs", () => {
    expect(safeHref("data:text/html,<script>alert(1)</script>")).toBeNull();
  });

  it("rejects file: URLs", () => {
    expect(safeHref("file:///etc/passwd")).toBeNull();
  });

  it("rejects unparseable input", () => {
    expect(safeHref("not a url")).toBeNull();
  });

  it("rejects empty and undefined", () => {
    expect(safeHref("")).toBeNull();
    expect(safeHref(undefined)).toBeNull();
  });

  it("rejects protocol-relative URLs", () => {
    // `new URL("//example.com/x")` throws without a base — classified as unsafe.
    expect(safeHref("//example.com/x")).toBeNull();
  });
});

describe("formatDate", () => {
  it("returns empty string for empty input", () => {
    expect(formatDate("")).toBe("");
  });

  it("returns the raw string for unparseable input", () => {
    expect(formatDate("not a date")).toBe("not a date");
  });

  it("formats a valid ISO timestamp without throwing", () => {
    const out = formatDate("2026-04-13T09:33:05Z");
    // Locale-sensitive — don't assert exact format, just that it's non-empty
    // and doesn't match the raw input (meaning it was parsed and formatted).
    expect(out).not.toBe("");
    expect(out).not.toBe("2026-04-13T09:33:05Z");
  });
});

describe("StampFeedView", () => {
  function baseResponse(overrides: Partial<StampFeedResponse> = {}): StampFeedResponse {
    return {
      kind: "stamp_feed",
      handle: "rileywhite",
      github_url: "https://github.com/rileywhite",
      stamps_error: null,
      stamps: [],
      ...overrides,
    };
  }

  it("renders the stamps_error banner with role=alert and no cards", () => {
    render(<StampFeedView data={baseResponse({ stamps_error: "stamps_unavailable" })} />);
    const banner = screen.getByRole("alert");
    expect(banner).toHaveTextContent(/Couldn't load recent stamps/);
    expect(screen.queryByRole("article")).toBeNull();
  });

  it("truncates long messages and reveals them on show-more", () => {
    const longMessage = `Long evidence narrative. ${"x".repeat(250)} end of narrative.`;
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["go"],
              quality: 4,
              reliability: 5,
              validator: "val",
              message: longMessage,
              evidence_url: "https://github.com/foo/bar/pull/1",
              evidence_label: "foo/bar#1",
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );

    // Initially the message is truncated and there's a show-more button.
    const showMore = screen.getByRole("button", { name: /show more/ });
    expect(showMore).toBeInTheDocument();
    // The tail of the message is not present in the DOM text initially.
    expect(screen.queryByText(/end of narrative/)).toBeNull();

    // Expand.
    fireEvent.click(showMore);
    expect(screen.getByText(/end of narrative/)).toBeInTheDocument();
    // Button now says "show less".
    expect(screen.getByRole("button", { name: /show less/ })).toBeInTheDocument();
  });

  it("does not render the show-more button for short messages", () => {
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["go"],
              quality: 4,
              reliability: 5,
              validator: "val",
              message: "short",
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );
    expect(screen.getByText("short")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /show more/ })).toBeNull();
  });

  it("renders evidence_text when no safe evidence_url is present", () => {
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["docs"],
              quality: 0,
              reliability: 0,
              validator: "val",
              evidence_text: "Wrote a blog post",
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );
    expect(screen.getByText("Wrote a blog post")).toBeInTheDocument();
  });

  it("omits the GitHub link when github_url has an unsafe scheme", () => {
    render(<StampFeedView data={baseResponse({ github_url: "javascript:alert(1)" })} />);
    expect(screen.queryByRole("link", { name: /View on GitHub/ })).toBeNull();
    // Header name still renders.
    expect(screen.getByText("rileywhite")).toBeInTheDocument();
  });

  it("does not truncate at exactly 200 characters", () => {
    const exactly200 = "a".repeat(200);
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["go"],
              quality: 0,
              reliability: 0,
              validator: "val",
              message: exactly200,
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );
    expect(screen.queryByRole("button", { name: /show more/ })).toBeNull();
    expect(screen.getByText(exactly200)).toBeInTheDocument();
  });

  it("truncates at 201 characters", () => {
    const over = "a".repeat(201);
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["go"],
              quality: 0,
              reliability: 0,
              validator: "val",
              message: over,
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );
    expect(screen.getByRole("button", { name: /show more/ })).toBeInTheDocument();
  });

  it("does not split surrogate pairs at the truncation boundary", () => {
    // 199 ASCII + one non-BMP codepoint (🎉 = code point U+1F389). If the
    // truncation counted UTF-16 code units, it would cut inside the
    // surrogate pair and emit a replacement character. With code-point
    // counting the whole emoji survives under the threshold.
    const message = "x".repeat(199) + "🎉";
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["go"],
              quality: 0,
              reliability: 0,
              validator: "val",
              message,
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );
    // 200 code points → under threshold → no truncation.
    expect(screen.queryByRole("button", { name: /show more/ })).toBeNull();
    // The emoji should render intact (no U+FFFD replacement).
    expect(screen.queryByText("\uFFFD")).toBeNull();
  });

  it("toggles from expanded back to collapsed on show-less", () => {
    const long = `Start ${"z".repeat(300)} end`;
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["go"],
              quality: 0,
              reliability: 0,
              validator: "val",
              message: long,
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /show more/ }));
    expect(screen.getByText(/ end$/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /show less/ }));
    expect(screen.queryByText(/ end$/)).toBeNull();
    expect(screen.getByRole("button", { name: /show more/ })).toBeInTheDocument();
  });

  it("renders both message and evidence_text when both are present", () => {
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["go"],
              quality: 0,
              reliability: 0,
              validator: "val",
              message: "validator reasoning",
              evidence_text: "free-form evidence",
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );
    expect(screen.getByText("validator reasoning")).toBeInTheDocument();
    expect(screen.getByText("free-form evidence")).toBeInTheDocument();
  });

  it("exposes aria-expanded state on the show-more button", () => {
    const long = "w".repeat(250);
    render(
      <StampFeedView
        data={baseResponse({
          stamps: [
            {
              id: "s1",
              skill_tags: ["go"],
              quality: 0,
              reliability: 0,
              validator: "val",
              message: long,
              created_at: "2026-04-13T00:00:00Z",
            },
          ],
        })}
      />,
    );
    const button = screen.getByRole("button", { name: /show more/ });
    expect(button).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(button);
    expect(screen.getByRole("button", { name: /show less/ })).toHaveAttribute("aria-expanded", "true");
  });
});
