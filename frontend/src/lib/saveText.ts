// Hands text built in the browser to the user as a file: a Blob behind an
// object URL, followed by a link the page clicks and drops. A thin shim over
// the DOM, kept apart so what builds the text is tested without it.

/** Saves `text` as a file named `fileName`, of the given media type. */
export function saveText(text: string, fileName: string, type: string): void {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const link = document.createElement("a");
  link.href = url;
  link.download = fileName;
  link.rel = "noopener";
  document.body.appendChild(link);
  try {
    link.click();
  } finally {
    link.remove();
    // On the next task: a browser may still be reading the URL the click
    // handed it.
    setTimeout(() => URL.revokeObjectURL(url));
  }
}
