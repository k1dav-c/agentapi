// Copies text to the clipboard. The Clipboard API only exists on secure
// pages (https, localhost), and an embedding page can block it, so a chat
// opened over plain http copies through a selection instead.
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Blocked: try the selection below.
    }
  }
  if (!copyThroughSelection(text)) throw new Error("Copying isn't available in this browser");
}

function copyThroughSelection(text: string): boolean {
  if (typeof document === "undefined") return false;
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  try {
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
  }
}
