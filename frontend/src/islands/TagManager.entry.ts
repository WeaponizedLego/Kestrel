import { createApp } from 'vue'
import TagManager from './TagManager.vue'

const el = document.querySelector('[data-island="tag-manager"]')
if (el) createApp(TagManager).mount(el)
