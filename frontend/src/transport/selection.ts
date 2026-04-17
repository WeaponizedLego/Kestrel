// Cross-island selection state. Vite bundles each island as its own
// entry, but modules imported by multiple entries still resolve to a
// single runtime instance — so the `selectedFolder` ref is shared
// across every mounted Vue app on the page. Sidebar writes it,
// PhotoGrid reads it, Vue's reactivity does the rest.
//
// Add state here only when two or more islands need to see the same
// value. Anything that lives inside one island stays inside it.

import { ref } from 'vue'

// selectedFolder drives the photo grid's ?folder= filter. null means
// "no filter — show all photos". Any absolute path asks the backend
// to include that folder and its descendants.
export const selectedFolder = ref<string | null>(null)
