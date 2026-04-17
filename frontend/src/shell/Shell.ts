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
    ])
  },
})

export const shellStyles = `
  .shell {
    display: grid;
    grid-template-columns: 240px 1fr;
    grid-template-rows: auto 1fr auto;
    grid-template-areas:
      "sidebar toolbar"
      "sidebar grid"
      "sidebar status";
    height: 100vh;
    background: var(--bg);
  }
  .shell__sidebar { grid-area: sidebar; background: var(--surface-inset); box-shadow: var(--elev-inset); }
  .shell__toolbar { grid-area: toolbar; background: var(--surface-raised); box-shadow: var(--elev-raised); padding: var(--space-4) var(--space-5); }
  .shell__grid    { grid-area: grid; padding: var(--space-5); overflow: hidden; min-height: 0; }
  .shell__status  { grid-area: status; background: var(--surface-inset); box-shadow: var(--elev-inset); padding: var(--space-3) var(--space-5); color: var(--text-secondary); font-size: var(--fs-body); }
`
