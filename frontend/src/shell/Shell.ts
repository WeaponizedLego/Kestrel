// Static layout shell rendered to HTML at build time by
// scripts/render-shell.ts. Islands hydrate onto the data-island nodes
// at runtime; no reactivity lives here.

import { defineComponent, h } from 'vue'

export const Shell = defineComponent({
  name: 'Shell',
  render() {
    return h(
      'div',
      {
        class:
          'grid h-screen min-h-0 grid-cols-[220px_1fr] grid-rows-[3rem_1fr_1.75rem] bg-base-100 text-base-content',
        style: {
          gridTemplateAreas: '"sidebar toolbar" "sidebar grid" "status status"',
        },
      },
      [
        h('aside', {
          class: 'min-h-0 overflow-hidden border-r border-base-300 bg-base-200',
          style: { gridArea: 'sidebar' },
          'data-island': 'sidebar',
        }),
        h('header', {
          class: 'flex items-center border-b border-base-300 bg-base-100 px-3',
          style: { gridArea: 'toolbar' },
          'data-island': 'toolbar',
        }),
        h('main', {
          class: 'min-h-0 min-w-0 overflow-hidden bg-base-100 p-3',
          style: { gridArea: 'grid' },
          'data-island': 'photo-grid',
        }),
        h('footer', {
          class:
            'flex items-center border-t border-base-300 bg-base-200 px-3 text-xs text-base-content/70',
          style: { gridArea: 'status' },
          'data-island': 'status-bar',
        }),
        h('div', { 'data-island': 'tagging-queue' }),
        h('div', { 'data-island': 'similarity-review' }),
        h('div', { 'data-island': 'tag-manager' }),
        h('div', { 'data-island': 'file-ops' }),
      ],
    )
  },
})
