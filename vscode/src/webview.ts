// Minimal HTML wrapper that embeds the webui inside an iframe.

export function webviewHTML(url: string, token: string): string {
  // Strip a trailing slash from url before re-adding "/" so we never
  // produce "//?token=…" — vscode.Uri.toString() returns a trailing
  // slash, but info.url from md webui doesn't, so the concat would
  // otherwise produce a double slash that triggers a 307 redirect.
  const base = url.replace(/\/+$/, "");
  const full = base + "/?token=" + encodeURIComponent(token);
  const frameOrigin = new URL(base).origin;
  const csp =
    "default-src 'none'; " +
    "style-src 'unsafe-inline'; " +
    `frame-src ${frameOrigin};`;
  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <meta http-equiv="Content-Security-Policy" content="${csp}">
  <title>Meads</title>
  <style>
    html, body, iframe { margin: 0; padding: 0; height: 100%; width: 100%; border: 0; }
    body { background: var(--vscode-editor-background); }
    iframe { display: block; }
  </style>
</head>
<body>
  <iframe src="${escapeHtml(full)}" allow="clipboard-read; clipboard-write"></iframe>
</body>
</html>`;
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
