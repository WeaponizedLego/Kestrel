import { createApp } from 'vue'
import Sidebar from './Sidebar.vue'

const el = document.querySelector('[data-island="sidebar"]')
if (el) createApp(Sidebar).mount(el)
