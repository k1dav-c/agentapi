// What a tool call card shows, worked out from the tool call alone, so the
// card only lays it out.

import {formatToolInput, getExitCode, getToolCommand, getToolImagePath, getToolSummary} from "./tool-format";
import {isPatchScriptTool, toolFileTargets, type FileTarget} from "./file-refs";
import type {ToolCall} from "./task-timeline";

export interface ToolCallView {
  failed: boolean;
  // Still running, or not answered yet.
  pending: boolean;
  // The input as text, for the Input section; empty without one.
  input: string;
  // The files it reads or changes, and its change to them as a unified diff.
  targets: FileTarget[];
  change: string;
  // The tool's name as shown: a Codex code-mode script that applies a
  // patch reads as apply_patch.
  label: string;
  // The command line of a shell tool.
  command?: string;
  // What the header shows after the name, for a tool that is neither a
  // command nor about files.
  summary: string;
  exitCode?: number;
  // How long it ran, or 0 when unknown.
  durationMs: number;
  // The first lines of a command's output, shown while the card is closed.
  resultPreview: string;
  // The images in the result (an image file it read), named after the file.
  images: string[];
  imageName: string;
}

export function describeToolCall(toolCall: ToolCall): ToolCallView {
  const targets = toolFileTargets(toolCall.name, toolCall.input);
  const patchScript = targets.length > 0 && isPatchScriptTool(toolCall.name);
  const command = patchScript ? undefined : getToolCommand(toolCall.name, toolCall.input);
  return {
    failed: toolCall.status === "failed" || Boolean(toolCall.isError),
    pending: toolCall.status === "running" || (toolCall.status === undefined && toolCall.result === undefined),
    input: formatToolInput(toolCall.input),
    targets,
    change: targets.flatMap((target) => (target.change ? [target.change] : [])).join("\n"),
    label: patchScript ? "apply_patch" : toolCall.name,
    command,
    summary: getToolSummary(toolCall.name, toolCall.input),
    exitCode: getExitCode(toolCall.result),
    durationMs:
      toolCall.resultTimestamp && toolCall.timestamp ? Date.parse(toolCall.resultTimestamp) - Date.parse(toolCall.timestamp) : 0,
    resultPreview: command && toolCall.result ? toolCall.result.replace(/\s+$/, "").split("\n").slice(0, 4).join("\n") : "",
    images: toolCall.images ?? [],
    imageName: getToolImagePath(toolCall.input)?.split("/").pop() || "image",
  };
}
