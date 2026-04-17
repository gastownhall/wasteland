import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { StatusBadge } from "./StatusBadge";

describe("StatusBadge", () => {
  it("replaces underscores with spaces", () => {
    render(<StatusBadge status="in_review" />);
    expect(screen.getByText("in review")).toBeInTheDocument();
  });

  it("sets data-status attribute", () => {
    render(<StatusBadge status="open" />);
    expect(screen.getByText("open")).toHaveAttribute("data-status", "open");
  });

  it("renders simple status without modification", () => {
    render(<StatusBadge status="claimed" />);
    expect(screen.getByText("claimed")).toBeInTheDocument();
  });

  it("supports validated as a first-class status", () => {
    render(<StatusBadge status="validated" />);
    expect(screen.getByText("validated")).toHaveAttribute("data-status", "validated");
  });
});
