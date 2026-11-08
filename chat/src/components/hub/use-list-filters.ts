"use client";

import {useCallback, useMemo, useState} from "react";
import type {HubInstance} from "@/lib/hub";
import {agentsIn, countAgents, countFilters, matchesAgents, shownAgents, type HubFilter} from "@/lib/hub-filter";
import {listSections, type ListOrder} from "@/lib/hub-order";
import {useHubSetting} from "@/lib/hub-settings";

// useListFilters is what the hub's list shows of the instances (all): the
// state filter, the agents picked (which the browser keeps) and the
// search, the counts on their chips, and the list's sections in order and
// by machine as the list view says.
export function useListFilters(all: HubInstance[], {order, byMachine}: {order: ListOrder; byMachine: boolean}) {
  const [filter, setFilter] = useState<HubFilter>("all");
  const [picked, pickAgents] = useHubSetting("agents");
  const [query, setQuery] = useState("");
  const counts = useMemo(() => countFilters(all), [all]);
  // The agents the instances run get chips once there are two; the list is
  // filtered by those picked that it has.
  const presentAgents = useMemo(() => agentsIn(all), [all]);
  const agents = useMemo(() => shownAgents(picked, presentAgents), [picked, presentAgents]);
  // Each row of chips counts what the other row lets through.
  const statusCounts = useMemo(() => (agents.size === 0 ? counts : countFilters(all.filter((instance) => matchesAgents(instance, agents)))), [all, agents, counts]);
  const agentCounts = useMemo(() => countAgents(all, filter), [all, filter]);
  const sections = useMemo(() => listSections(all, {filter, agents, query, order, byMachine}), [all, filter, agents, query, order, byMachine]);
  // showAll takes the filters and the search away.
  const showAll = useCallback(() => {
    setFilter("all");
    pickAgents(new Set());
    setQuery("");
  }, [pickAgents]);
  return {
    filter,
    setFilter,
    // agents are the agents the list is filtered by; presentAgents those
    // the instances run.
    agents,
    presentAgents,
    pickAgents,
    query,
    setQuery,
    counts,
    statusCounts,
    agentCounts,
    sections,
    // filtered: a filter or a search narrows the list.
    filtered: filter !== "all" || agents.size > 0 || query.trim() !== "",
    showAll,
  };
}

export type ListFilters = ReturnType<typeof useListFilters>;
