import { createApp } from 'vue'
import FileOps from './FileOps.vue'

const el = document.querySelector('[data-island="file-ops"]')
if (el) createApp(FileOps).mount(el)
