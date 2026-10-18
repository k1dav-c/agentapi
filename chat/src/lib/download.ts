// Saves content as a file called name, the browser's way: through a link
// to a blob.
export function downloadFile(name: string, content: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], {type}));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  document.body.appendChild(link);
  link.click();
  link.remove();
  // Revoked once the click was handled.
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}
