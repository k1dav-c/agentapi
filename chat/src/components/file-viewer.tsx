"use client";

import {useEffect, useMemo, useRef, useState, type ReactNode} from "react";
import dynamic from "next/dynamic";
import {FileViewerContext, type FileViewer, type FileViewerRequest} from "./file-viewer-context";

// The dialog loads after the page (it isn't needed for the first paint),
// like the Explorer, and is fetched in the background soon after so the
// first file opens without waiting for it.
const loadDialog = () => import("./file-viewer-dialog").then((module) => module.FileViewerDialog);
const FileViewerDialog = dynamic(loadDialog, {ssr: false});

// Lets anything in the chat open a file the agent works on: its contents
// and its changes (useFileViewer().open).
export function FileViewerProvider({children}: {children: ReactNode}) {
  const [request, setRequest] = useState<(FileViewerRequest & {id: number}) | null>(null);
  const [open, setOpen] = useState(false);
  const nextID = useRef(0);
  const viewer = useMemo<FileViewer>(
    () => ({
      open: (next) => {
        // Every open is a new request, so the dialog reads the file again:
        // files change while the agent works.
        nextID.current += 1;
        setRequest({...next, id: nextID.current});
        setOpen(true);
      },
    }),
    [],
  );

  useEffect(() => {
    const timer = window.setTimeout(() => void loadDialog().catch(() => undefined), 3000);
    return () => window.clearTimeout(timer);
  }, []);

  return (
    <FileViewerContext.Provider value={viewer}>
      {children}
      {request && <FileViewerDialog request={request} open={open} onOpenChange={setOpen} />}
    </FileViewerContext.Provider>
  );
}
