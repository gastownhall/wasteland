import { describe, expect, it } from "vitest";
import { makeSummary } from "../test-utils";
import { findSelectedIndex, resolveSelectedIdAfterRefresh } from "./browseSelection";

describe("browseSelection", () => {
  it("finds the selected item by id", () => {
    const items = [makeSummary({ id: "1" }), makeSummary({ id: "2" }), makeSummary({ id: "3" })];

    expect(findSelectedIndex(items, "2")).toBe(1);
  });

  it("preserves the selected id when the item still exists after refresh", () => {
    const nextItems = [makeSummary({ id: "3" }), makeSummary({ id: "1" }), makeSummary({ id: "2" })];

    expect(resolveSelectedIdAfterRefresh("2", nextItems)).toBe("2");
  });

  it("clears the selected id when the item disappears after refresh", () => {
    const nextItems = [makeSummary({ id: "1" }), makeSummary({ id: "3" })];

    expect(resolveSelectedIdAfterRefresh("2", nextItems)).toBeNull();
  });
});
