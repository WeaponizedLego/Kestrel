import { createApp } from 'vue'
import Duplicates from './Duplicates.vue'

const el = document.querySelector('[data-island="duplicates"]')
if (el) createApp(Duplicates).mount(el)
