// Renders frontend/index.html at build time. Vite then picks that file
// up as its HTML entry, processes the per-island <script type="module">
// tags, hashes the bundles, and writes the final dist/index.html.
//
// The shell is a static render-function component (src/shell/Shell.ts);
// renderToString from @vue/server-renderer turns it into HTML once, so
// the browser never re-creates the shell DOM at runtime.
//
// The <!--KESTREL_TOKEN_META--> placeholder is preserved literally so
// the Go server can substitute the real per-run token at request time
// (see internal/server/assets.go).

import { writeFile } from 'node:fs/promises'
import { fileURLToPath, URL } from 'node:url'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

import { Shell, shellStyles } from '../src/shell/Shell'

// PhotoViewer is no longer a top-level island: PhotoGrid loads it on
// demand via a dynamic import (see Phase 8 in TASKS.md). Keeping it
// out of the shell skips the extra HTTP request on first paint.
const islandEntries = [
  '/src/islands/Sidebar.entry.ts',
  '/src/islands/Toolbar.entry.ts',
  '/src/islands/PhotoGrid.entry.ts',
  '/src/islands/StatusBar.entry.ts',
]

async function main() {
  const shellMarkup = await renderToString(createSSRApp(Shell))

  const scripts = islandEntries
    .map((src) => `    <script type="module" src="${src}"></script>`)
    .join('\n')

  const html = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="color-scheme" content="dark" />
    <title>Kestrel</title>
    <!--KESTREL_TOKEN_META-->
    <link rel="stylesheet" href="/src/shell/tokens.css" />
    <style>${shellStyles}</style>
  </head>
  <body>
    ${shellMarkup}
${scripts}
  </body>
</html>
`

  const outPath = fileURLToPath(new URL('../index.html', import.meta.url))
  await writeFile(outPath, html, 'utf8')
  console.log(`wrote ${outPath}`)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
