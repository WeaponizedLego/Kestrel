import { createApp } from 'vue'
import ScanDetail from './ScanDetail.vue'

const el = document.querySelector('[data-island="scan-detail"]')
if (el) createApp(ScanDetail).mount(el)
