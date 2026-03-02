# 📖 Go Readability Standards — Kestrel

> This document defines the readability and style standards for all Go code in Kestrel.
> It is referenced by `.github/copilot-instructions.md` and should be consulted when writing or reviewing Go code.
>
> **Guiding principle:** Code is read far more often than it is written.
> Optimize for the reader — a developer who has never seen this code before.

---

## Function Length

- **Target:** ≤ 40 lines per function body.
- **Hard limit:** If a function exceeds 60 lines, it must be refactored.
- **How:** Extract logical steps into well-named helper functions. Each helper should do one thing and its name should describe that thing.

```go
// ❌ BAD — one giant function doing everything
func ProcessPhoto(path string) (*Photo, error) {
    // ... 80 lines of hashing, EXIF extraction, thumbnail gen, validation ...
}

// ✅ GOOD — each step is a readable, testable unit
func ProcessPhoto(path string) (*Photo, error) {
    hash, err := computeFileHash(path)
    if err != nil {
        return nil, fmt.Errorf("hashing %s: %w", path, err)
    }

    meta, err := extractMetadata(path)
    if err != nil {
        return nil, fmt.Errorf("extracting metadata for %s: %w", path, err)
    }

    thumb, err := generateThumbnail(path)
    if err != nil {
        return nil, fmt.Errorf("generating thumbnail for %s: %w", path, err)
    }

    return &Photo{
        Path:      path,
        Hash:      hash,
        Metadata:  meta,
        Thumbnail: thumb,
    }, nil
}
```

---

## Naming Philosophy

### General Rules

- **Descriptive names over short names.** `photoCount` not `cnt`. `thumbnailPath` not `tp`.
- **Single-letter variables** are acceptable only in:
  - Loop indices (`i`, `j`, `k`)
  - Very short closures where the type makes the meaning obvious
- **Boolean variables/functions** should read as a question: `isLoaded`, `hasMetadata`, `ShouldRegenerate`.
- **Acronyms** stay consistently cased: `ID`, `HTTP`, `URL` (not `Id`, `Http`, `Url`).

### Naming by Scope

| Scope | Style | Example |
|---|---|---|
| Exported type / func | `PascalCase` | `type PhotoLibrary struct`, `func LoadLibrary()` |
| Unexported type / func | `camelCase` | `type photoIndex struct`, `func computeHash()` |
| Local variable | `camelCase`, descriptive | `thumbnailBytes`, `scanResults` |
| Constant | `PascalCase` if exported, `camelCase` if not | `MaxThumbnailSize`, `defaultBufferSize` |
| Interface | Name by behavior, often `-er` suffix | `Scanner`, `Persister`, `PhotoProvider` |

### Receiver Names

- Use a **short, consistent abbreviation** of the type name (1–2 letters).
- Same receiver name across all methods of a type.

```go
// ✅ Consistent receiver name
func (l *Library) AddPhoto(p *Photo) { ... }
func (l *Library) GetPhoto(path string) (*Photo, bool) { ... }
func (l *Library) RemovePhoto(path string) { ... }
```

---

## Comment Rules

### Doc Comments (Exported Symbols)

Every exported function, type, method, and constant must have a doc comment.
The comment starts with the symbol name and describes what it does, not how.

```go
// Library is the in-memory store for all photos in the active collection.
// It is safe for concurrent use.
type Library struct { ... }

// AddPhoto stores a photo in the library, keyed by its absolute file path.
// It overwrites any existing entry for the same path.
func (l *Library) AddPhoto(p *Photo) { ... }
```

### Inline Comments

- Write **"why" comments**, not "what" comments. The code shows *what*; the comment explains *why*.
- If you need a "what" comment, the code should probably be refactored for clarity.

```go
// ❌ BAD — restates the code
// Increment the counter
count++

// ✅ GOOD — explains a non-obvious decision
// Cap at 8 workers to avoid exhausting file descriptors on macOS.
workerCount := min(runtime.NumCPU(), 8)
```

### Section Comments

Use a blank line + comment to mark logical sections within longer functions:

```go
func (s *ScannerService) FullScan(root string) error {
    // Discover all image files
    paths, err := discoverImages(root)
    if err != nil {
        return fmt.Errorf("discovering images in %s: %w", root, err)
    }

    // Process files in parallel
    results, err := processInParallel(paths)
    if err != nil {
        return fmt.Errorf("processing images: %w", err)
    }

    // Update the in-memory library
    s.lib.BulkAdd(results)
    return nil
}
```

---

## Package Design

- **One package = one responsibility.** `internal/scanner` scans. `internal/thumbnail` generates thumbnails.
- **No `utils` or `helpers` packages.** If a function doesn't belong to an existing package, create a new focused package or reconsider the design.
- **Package names are lowercase, single-word** when possible: `library`, `scanner`, `metadata`.
- **`internal/` for private packages.** Everything under `internal/` is module-private and cannot be imported externally.

---

## Error Handling

### The Wrap Rule

Always wrap errors with context describing *what you were trying to do*:

```go
if err != nil {
    return fmt.Errorf("loading thumbnail for %s: %w", path, err)
}
```

### Error Wrapping Conventions

- Use `%w` (not `%v`) so callers can use `errors.Is()` and `errors.As()`.
- Wrap message format: `"<doing what> for <which thing>: %w"`.
- Do **not** start the message with a capital letter or end with punctuation (Go convention).

### Sentinel Errors

Define package-level sentinel errors for conditions callers need to check:

```go
var ErrPhotoNotFound = errors.New("photo not found")

func (l *Library) GetPhoto(path string) (*Photo, error) {
    l.mu.RLock()
    defer l.mu.RUnlock()
    p, ok := l.photos[path]
    if !ok {
        return nil, fmt.Errorf("looking up %s: %w", path, ErrPhotoNotFound)
    }
    return p, nil
}
```

---

## Struct Design

- **Group related fields** with blank lines between logical groups.
- **Use constructor functions** (`NewX`) for structs that need initialization.
- **Zero values should be useful** — design structs so the zero value is valid when possible.

```go
type Photo struct {
    // Identity
    Path string
    Hash string

    // Metadata
    Width    int
    Height   int
    TakenAt  time.Time
    CameraMake string

    // Runtime cache (not persisted)
    Thumbnail []byte
}

func NewPhoto(path, hash string) *Photo {
    return &Photo{
        Path: path,
        Hash: hash,
    }
}
```

---

## Interface Discipline

- **Define interfaces at the consumer**, not the producer.
- **Keep interfaces small** — 1–2 methods is ideal. Compose larger behaviors from small interfaces.
- **Name by behavior** — `Reader`, `Scanner`, `Persister`, not `ILibrary` or `LibraryInterface`.

```go
// ✅ Defined where it's consumed, not where it's implemented
// In package scanner:
type MetadataExtractor interface {
    Extract(path string) (*Metadata, error)
}

// The implementation in package metadata doesn't need to know about this interface.
```

---

## Testing Standards

### Table-Driven Tests

Use table-driven tests for any function with multiple input/output scenarios:

```go
func TestComputeHash(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        wantHash string
        wantErr  bool
    }{
        {name: "valid jpeg", input: "testdata/photo.jpg", wantHash: "abc123", wantErr: false},
        {name: "missing file", input: "testdata/nope.jpg", wantHash: "", wantErr: true},
        {name: "empty file", input: "testdata/empty.jpg", wantHash: "", wantErr: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := computeHash(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("computeHash(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
                return
            }
            if got != tt.wantHash {
                t.Errorf("computeHash(%q) = %q, want %q", tt.input, got, tt.wantHash)
            }
        })
    }
}
```

### Test File Conventions

- Test files live next to the code they test: `library.go` → `library_test.go`.
- Test helpers that are shared across a package go in `helpers_test.go`.
- Use `testdata/` directories for fixture files.
- Test function names: `TestFunctionName` or `TestType_MethodName`.

### What to Test

- **Always test:** Public API functions, error paths, edge cases, concurrent access.
- **Skip testing:** Simple getters/setters with no logic, generated code.

---

## Code Organization Within a File

Order code within a `.go` file as follows:

```
1. Package declaration
2. Imports
3. Constants and package-level variables
4. Type definitions (structs, interfaces)
5. Constructor functions (NewX)
6. Exported methods
7. Unexported methods
8. Standalone unexported functions
```

This ensures a reader can understand the public API by reading from the top, and dig into implementation details as they scroll down.

---

## Concurrency Readability

- **Name goroutine functions clearly.** If a goroutine runs an anonymous function longer than 5 lines, extract it into a named function.
- **Name channels by what they carry:** `filePaths`, `scanResults`, `thumbnailJobs` — not `ch`, `c`, `input`.
- **Always document goroutine ownership:** who starts it, who stops it, who closes the channel.
- **Propagate `context.Context`** through long-running operations so they can be cancelled cleanly.

```go
// ✅ Clear ownership and naming
func (s *Scanner) Start(ctx context.Context, root string) error {
    filePaths := make(chan string, 100) // producer: walkFiles, consumers: workers

    // Start workers (stopped when filePaths is closed)
    var wg sync.WaitGroup
    for range runtime.NumCPU() {
        wg.Add(1)
        go s.processFiles(ctx, filePaths, &wg)
    }

    // Walk the directory tree and feed paths to workers
    err := s.walkFiles(ctx, root, filePaths)
    close(filePaths) // signal workers to drain and exit
    wg.Wait()
    return err
}
```
