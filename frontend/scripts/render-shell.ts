// Renders frontend/index.html at build time. Vite then picks that file
// up as its HTML entry, processes the per-island <script type="module">
// tags, hashes the bundles, and writes the final dist/index.html.
//
// The <!--KESTREL_TOKEN_META--> placeholder is preserved literally so
// the Go server can substitute the real per-run token at request time
// (see internal/server/assets.go).

import { writeFile } from 'node:fs/promises'
import { fileURLToPath, URL } from 'node:url'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

import { Shell } from '../src/shell/Shell'

const islandEntries = [
  '/src/islands/Sidebar.entry.ts',
  '/src/islands/Toolbar.entry.ts',
  '/src/islands/PhotoGrid.entry.ts',
  '/src/islands/StatusBar.entry.ts',
  '/src/islands/TaggingQueue.entry.ts',
  '/src/islands/SimilarityReview.entry.ts',
  '/src/islands/TagManager.entry.ts',
  '/src/islands/FileOps.entry.ts',
  '/src/islands/ScanDetail.entry.ts',
]

// Runs in <head> before any island bootstraps so daisyUI's data-theme
// is applied before first paint — prevents a flash of the default
// theme when the user has picked another one. The theme is read from
// the <meta name="kestrel-theme"> tag the Go server injects per
// request (see internal/server/assets.go), not from localStorage:
// the prod binary binds a random loopback port each launch so
// localStorage (which is keyed per-origin) doesn't survive restarts.
const themeBootstrap = `(function(){try{var m=document.querySelector('meta[name="kestrel-theme"]');var t=m&&m.content;if(t)document.documentElement.dataset.theme=t;}catch(e){}})();`

async function main() {
  const shellMarkup = await renderToString(createSSRApp(Shell))

  const scripts = islandEntries
    .map((src) => `    <script type="module" src="${src}"></script>`)
    .join('\n')

  const html = `<!doctype html>
<html lang="en" data-theme="dark">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Kestrel</title>
    <!--KESTREL_TOKEN_META-->
    <!--KESTREL_THEME_META-->
    <script>${themeBootstrap}</script>
    <link rel="stylesheet" href="/src/shell/app.css" />
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
