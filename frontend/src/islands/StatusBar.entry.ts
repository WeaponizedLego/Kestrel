import { createApp } from 'vue'
import StatusBar from './StatusBar.vue'

const el = document.querySelector('[data-island="status-bar"]')
if (el) createApp(StatusBar).mount(el)
