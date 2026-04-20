// Static layout shell rendered to HTML at build time by
// scripts/render-shell.ts via @vue/server-renderer. Islands hydrate
// onto the data-island nodes at runtime; nothing reactive lives here,
// so the shell is a render-function component (no SFC compilation
// step needed when rendering from a plain Node script).

import { defineComponent, h } from 'vue'

export const Shell = defineComponent({
  name: 'Shell',
  render() {
    return h('div', { class: 'shell' }, [
      h('aside',   { class: 'shell__sidebar', 'data-island': 'sidebar' }),
      h('header',  { class: 'shell__toolbar', 'data-island': 'toolbar' }),
      h('main',    { class: 'shell__grid',    'data-island': 'photo-grid' }),
      h('footer',  { class: 'shell__status',  'data-island': 'status-bar' }),
      // TaggingQueue and Duplicates mount full-viewport overlays when
      // opened from the Toolbar. The static shell just reserves their
      // mount nodes; nothing is visible until the user triggers them.
      h('div',     { 'data-island': 'tagging-queue' }),
      h('div',     { 'data-island': 'duplicates' }),
    ])
  },
})

export const shellStyles = `
  .shell {
    display: grid;
    grid-template-columns: 220px 1fr;
    grid-template-rows: 40px 1fr 28px;
    grid-template-areas:
      "sidebar toolbar"
      "sidebar grid"
      "status  status";
    height: 100vh;
    background: var(--surface-bg);
    color: var(--text-primary);
  }
  .shell__sidebar {
    grid-area: sidebar;
    background: var(--surface-bg);
    border-right: 1px solid var(--border-subtle);
    overflow: hidden;
    min-height: 0;
  }
  .shell__toolbar {
    grid-area: toolbar;
    background: var(--surface-bg);
    border-bottom: 1px solid var(--border-subtle);
    padding: 0 var(--space-5);
    display: flex;
    align-items: center;
  }
  .shell__grid {
    grid-area: grid;
    padding: var(--space-5);
    overflow: hidden;
    min-height: 0;
    min-width: 0;
  }
  .shell__status {
    grid-area: status;
    background: var(--surface-bg);
    border-top: 1px solid var(--border-subtle);
    padding: 0 var(--space-5);
    color: var(--text-secondary);
    font-size: var(--fs-small);
    display: flex;
    align-items: center;
  }
`
