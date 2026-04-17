import { createApp } from 'vue'
import PhotoGrid from './PhotoGrid.vue'

const el = document.querySelector('[data-island="photo-grid"]')
if (el) createApp(PhotoGrid).mount(el)
