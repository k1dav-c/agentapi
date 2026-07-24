import React from "react";
import { useDropzone } from "react-dropzone";

export interface DragDropProps {
  onFilesAdded: (files: File[]) => void;
  disabled?: boolean;
  children: React.ReactNode;
  className?: string;
}

export function DragDrop({ onFilesAdded, disabled = false, children, className = "" }: DragDropProps) {
  const { getRootProps, getInputProps, isDragActive } = useDropzone({
    noClick: true,
    disabled,
    onDropAccepted: (files: File[]) => {
      onFilesAdded(files);
    },
    multiple: true,
  });

  return (
    <div
      {...getRootProps()}
      className={`relative ${className} ${
        isDragActive && !disabled ? 'rounded-2xl ring-2 ring-primary ring-offset-2 text-center transition-colors' : ''
      }`}
    >
      <input {...getInputProps()} />
      {isDragActive && !disabled && (
        <div className="absolute inset-0 z-20 flex items-center justify-center rounded-2xl bg-background/90 backdrop-blur-sm">
          <p className="rounded-full border bg-card px-4 py-2 text-sm font-medium text-foreground shadow-sm">Drop files to attach</p>
        </div>
      )}
      {children}
    </div>
  );
}
