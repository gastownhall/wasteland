import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import type { BrowseFilter } from "../api/types";

export function useFilterParams(): [BrowseFilter, (filter: BrowseFilter) => void] {
  const [searchParams, setSearchParams] = useSearchParams();

  const filter = useMemo<BrowseFilter>(() => {
    const f: BrowseFilter = {};
    const status = searchParams.get("status");
    const type = searchParams.get("type");
    const sort = searchParams.get("sort");
    const search = searchParams.get("search");
    const project = searchParams.get("project");
    const priority = searchParams.get("priority");
    const view = searchParams.get("view");
    if (status) f.status = status;
    if (type) f.type = type;
    if (sort) f.sort = sort;
    if (search) f.search = search;
    if (project) f.project = project;
    if (priority) f.priority = Number(priority);
    if (view) f.view = view;
    return f;
  }, [searchParams]);

  const setFilter = useCallback(
    (f: BrowseFilter) => {
      const params = new URLSearchParams();
      if (f.status) params.set("status", f.status);
      if (f.type) params.set("type", f.type);
      if (f.sort && f.sort !== "priority") params.set("sort", f.sort);
      if (f.search) params.set("search", f.search);
      if (f.project) params.set("project", f.project);
      if (f.priority !== undefined && f.priority >= 0) params.set("priority", String(f.priority));
      if (f.view && f.view !== "mine") params.set("view", f.view);
      setSearchParams(params, { replace: true });
    },
    [setSearchParams],
  );

  return [filter, setFilter];
}
