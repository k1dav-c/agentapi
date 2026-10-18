"use client";

import {useEffect, useRef, useState} from "react";
import type {TaskFilter} from "@/lib/task-timeline";

// useTaskSearch is the transcript's search: the query, the filter, whether
// the search bar is open (⌘F opens it, Escape in it closes it), and which
// matching task is the current one.
export function useTaskSearch() {
  const [taskQuery, setTaskQuery] = useState("");
  const [taskFilter, setTaskFilter] = useState<TaskFilter>("all");
  const [currentSearchResult, setCurrentSearchResult] = useState(0);
  const [searchOpen, setSearchOpen] = useState(false);
  const searchInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    setCurrentSearchResult(0);
  }, [taskFilter, taskQuery]);

  useEffect(() => {
    const handleGlobalSearchShortcut = (event: globalThis.KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "f") {
        event.preventDefault();
        setSearchOpen(true);
        window.requestAnimationFrame(() => {
          searchInputRef.current?.focus();
          searchInputRef.current?.select();
        });
      } else if (event.key === "Escape" && document.activeElement === searchInputRef.current) {
        setTaskQuery("");
        setSearchOpen(false);
        searchInputRef.current?.blur();
      }
    };
    window.addEventListener("keydown", handleGlobalSearchShortcut);
    return () => window.removeEventListener("keydown", handleGlobalSearchShortcut);
  }, []);

  return {
    taskQuery,
    setTaskQuery,
    taskFilter,
    setTaskFilter,
    filtersActive: taskQuery.trim() !== "" || taskFilter !== "all",
    currentSearchResult,
    setCurrentSearchResult,
    searchOpen,
    setSearchOpen,
    searchInputRef,
    close: () => {
      setTaskQuery("");
      setTaskFilter("all");
      setSearchOpen(false);
    },
  };
}
