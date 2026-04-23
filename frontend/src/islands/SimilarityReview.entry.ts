import { createApp } from 'vue'
import SimilarityReview from './SimilarityReview.vue'

const el = document.querySelector('[data-island="similarity-review"]')
if (el) createApp(SimilarityReview).mount(el)
