import React from "react";
import {splitByQuery} from "@/lib/search-highlight";

// content with the matches of a search's query marked; renderText draws
// the rest (plain text unless set).
export function HighlightedText({
  content,
  query,
  renderText = (text) => text,
}: {
  content: string;
  query: string;
  renderText?: (text: string) => React.ReactNode;
}) {
  if (!query.trim()) return renderText(content);
  return splitByQuery(content, query).map(({text, match}, index) =>
    match ? (
      <mark key={`${index}-${text}`} className="rounded-sm bg-search-highlight px-0.5 text-search-highlight-text">
        {text}
      </mark>
    ) : (
      <React.Fragment key={`${index}-${text}`}>{renderText(text)}</React.Fragment>
    ),
  );
}
