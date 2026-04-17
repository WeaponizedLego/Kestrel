import { createApp } from 'vue'
import Toolbar from './Toolbar.vue'

const el = document.querySelector('[data-island="toolbar"]')
if (el) createApp(Toolbar).mount(el)
