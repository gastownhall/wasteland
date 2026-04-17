import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { BrowseFilter } from "../api/types";
import { FilterBar } from "./FilterBar";

describe("FilterBar", () => {
  const baseFilter: BrowseFilter = {};
  const onChange = vi.fn();

  afterEach(() => vi.clearAllMocks());

  it("status select calls onChange with updated filter", () => {
    render(<FilterBar filter={baseFilter} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Filter by status"), { target: { value: "open" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ status: "open" }));
  });

  it("type select calls onChange with updated filter", () => {
    render(<FilterBar filter={baseFilter} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Filter by type"), { target: { value: "bug" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ type: "bug" }));
  });

  it("sort select calls onChange with updated filter", () => {
    render(<FilterBar filter={baseFilter} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Sort order"), { target: { value: "newest" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ sort: "newest" }));
  });

  it("search input calls onChange with updated filter", () => {
    render(<FilterBar filter={baseFilter} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("Search items"), { target: { value: "test" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ search: "test" }));
  });

  it("values reflect current filter prop", () => {
    const filter: BrowseFilter = { status: "claimed", type: "feature", sort: "alpha", search: "hello" };
    render(<FilterBar filter={filter} onChange={onChange} />);
    expect(screen.getByLabelText("Filter by status")).toHaveValue("claimed");
    expect(screen.getByLabelText("Filter by type")).toHaveValue("feature");
    expect(screen.getByLabelText("Sort order")).toHaveValue("alpha");
    expect(screen.getByLabelText("Search items")).toHaveValue("hello");
  });

  it("view mode select calls onChange with updated filter", () => {
    render(<FilterBar filter={baseFilter} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText("View mode"), { target: { value: "all" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ view: "all" }));
  });

  it("view mode defaults to mine", () => {
    render(<FilterBar filter={baseFilter} onChange={onChange} />);
    expect(screen.getByLabelText("View mode")).toHaveValue("mine");
  });

  it("view mode reflects current filter prop", () => {
    const filter: BrowseFilter = { view: "upstream" };
    render(<FilterBar filter={filter} onChange={onChange} />);
    expect(screen.getByLabelText("View mode")).toHaveValue("upstream");
  });

  it("includes validated in the status filter options", () => {
    render(<FilterBar filter={baseFilter} onChange={onChange} />);
    const statusSelect = screen.getByLabelText("Filter by status");
    expect(Array.from(statusSelect.querySelectorAll("option")).map((option) => option.value)).toContain("validated");
  });
});
