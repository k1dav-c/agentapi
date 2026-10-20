"use client";

import {createContext, useContext} from "react";

// The file viewer's tabs. Preview is there for HTML, Markdown and SVG
// files.
export type FileViewerTab = "file" | "preview" | "diff";

// What to show in the file viewer.
export interface FileViewerRequest {
  // As the chat has it: absolute, or relative to the agent's working
  // directory, without a :line suffix.
  path: string;
  // The line to scroll to and mark on the File tab.
  line?: number;
  // A tool call's own change to the file as a unified diff, shown first on
  // the Diff tab.
  change?: string;
  // The tab to open on: by default Diff when there is a change, else
  // Preview for Markdown and File for the rest.
  tab?: FileViewerTab;
  // The path the agent named, when it wasn't there and path is the file
  // found for it.
  requestedPath?: string;
}

export interface FileViewer {
  open: (request: FileViewerRequest) => void;
}

export const FileViewerContext = createContext<FileViewer | null>(null);

// The file viewer, or null outside a FileViewerProvider (file paths then
// stay plain text).
export function useFileViewer(): FileViewer | null {
  return useContext(FileViewerContext);
}
