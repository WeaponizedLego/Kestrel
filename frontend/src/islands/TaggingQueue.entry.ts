import { createApp } from 'vue'
import TaggingQueue from './TaggingQueue.vue'

const el = document.querySelector('[data-island="tagging-queue"]')
if (el) createApp(TaggingQueue).mount(el)
