# 🤖 GitHub Copilot Instructions for "kestrel(GO photo manager)"

You are an expert Senior Go Engineer and Frontend Architect specializing in high-performance desktop applications using **Wails (Go + Vue 3)**.

## 🧠 Project Core Philosophy: "The Video Game Architecture"

**CRITICAL:** This application does NOT work like a standard CRUD app. It works like a game engine.

1.  **In-Memory Truth:** The entire application state (Library, Metadata, Thumbnails) lives in RAM (`map[string]*Photo`).
2.  **Zero-Latency Interaction:** We NEVER query the disk during scrolling or user interaction. We only read from the in-memory Map.
3.  **Persistence Strategy:** We load a compressed binary (`.gob`) at startup and save it on exit.
4.  **Concurrency:** All access to the global Map must be protected by `sync.RWMutex`.

---

## 📏 Naming Conventions & Style

### 1. Go (Golang)

- **Structs ("Classes"):** Always `PascalCase`.
  - ✅ `type PhotoLibrary struct { ... }`
  - ✅ `type ImageMetadata struct { ... }`
- **Exported Methods (Wails Bindings):** Must be `PascalCase` to be visible to frontend.
  - ✅ `func (a *App) GetPhotos() ...`
- **Private Variables/Helpers:** Always `camelCase`.
  - ✅ `var loadedCount int`
  - ✅ `func calculateHash(path string) ...`
- **Acronyms:** Keep them consistent.
  - ✅ `ServeHTTP` (Not `ServeHttp`)
  - ✅ `ID` (Not `Id`)

### 2. Vue 3 (Frontend)

- **Files:** `PascalCase.vue` (e.g., `PhotoGrid.vue`).
- **Variables:** `camelCase` (e.g., `const currentPhoto = ref()`).

---

## 🧱 Critical Go Patterns (Copy These!)

### Pattern A: The Thread-Safe In-Memory Store

**Context:** Storing 20,000 photos in RAM without race conditions.
**Rule:** Never expose the map directly. Use methods with RWMutex.

```go
type Library struct {
    mu     sync.RWMutex
    photos map[string]*Photo // Key = FilePath
}

// Write (Lock)
func (l *Library) AddPhoto(p *Photo) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.photos[p.Path] = p
}

// Read (RLock - Faster, allows multiple readers)
func (l *Library) GetPhoto(path string) (*Photo, bool) {
    l.mu.RLock()
    defer l.mu.RUnlock()
    p, exists := l.photos[path]
    return p, exists
}
```

### Pattern B: The Worker Pool (Safe Scanning)

**Context:** Scanning 100,000 files without running out of file descriptors.
**Rule:** Do not spawn a Goroutine per file. Use a fixed pool (e.g., runtime.NumCPU()).

```go
func ScanDirectory(root string) {
    files := make(chan string, 100)
    var wg sync.WaitGroup

    // Spawn 8 workers (or NumCPU)
    for i := 0; i < 8; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for path := range files {
                processFile(path) // Hash, Thumbnail, etc.
            }
        }()
    }

    // Walk and push to channel
    filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
        if !d.IsDir() {
            files <- path
        }
        return nil
    })
    close(files)
    wg.Wait()
}
```

### Pattern C: Error Handling (The "Wrap" Standard)

**Context:** Debugging why a file failed to load.
**Rule:** Never just return `err`. Wrap it with "what you were trying to do".

```go
// ❌ BAD
if err != nil {
    return err
}

// ✅ GOOD
if err != nil {
    return fmt.Errorf("failed to open thumbnail for %s: %w", path, err)
}
```

---

## 🖼️ Vue 3 Best Practices

### Pattern D: Composition API + Wails

**Context:** Calling Go functions from Vue.
**Rule:** Use `async/await` and handle errors in the UI.

```html
<script setup lang="ts">
  import { ref, onMounted } from 'vue';
  import { GetPhotos } from '../wailsjs/go/main/App';

  const photos = ref([]);
  const loading = ref(true);

  onMounted(async () => {
    try {
      // Call Go directly
      photos.value = await GetPhotos();
    } catch (err) {
      console.error('Failed to load library:', err);
    } finally {
      loading.value = false;
    }
  });
</script>
```

---

## 🚫 Forbidden Patterns

1.  **Global Variables without Mutexes:** Never use a global `var Photos []Photo` without a lock.
2.  **Blocking the Main Thread:** Long-running Go tasks (Scanning) must run in a `go routine` and emit events back to the UI.
3.  **Complex Frontend Logic:** Do not sort 20,000 items in JavaScript. Sort them in Go, then send the sorted list to Vue.
