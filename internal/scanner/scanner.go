// Package scanner walks a directory tree and writes library.Photo
// entries straight into the in-memory Library as each worker finishes
// processing a file. This matches docs/system-design.md → "Background
// scan" data flow: producer walks → workers hash + EXIF + thumb →
// write metadata to in-memory map (Lock) → broadcast progress.
//
// Writes are additive: a scan never wipes existing photos. Scanning a
// new root deposits more entries; re-scanning the same root overwrites
// by absolute path (the map key) and leaves unrelated photos alone.
// That gives us multi-root for free and lets the UI start browsing
// the first results while later workers are still hashing.
//
// Cancelling ctx aborts the walk and unblocks every worker promptly —
// no goroutine-per-file, no leaks if the caller bails out early.
package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/library"
	"github.com/WeaponizedLego/kestrel/internal/metadata"
	"github.com/WeaponizedLego/kestrel/internal/metadata/autotag"
	"github.com/WeaponizedLego/kestrel/internal/vision/protocol"
)

// pathQueueSize bounds how many discovered paths can sit in the channel
// before the walker blocks. Big enough to keep workers fed on fast
// disks, small enough that cancellation drains quickly.
const pathQueueSize = 128

// progressEvery controls how often Scan emits a "scan:progress" event.
// Publishing on every file would flood the WS hub for libraries with
// tens of thousands of images; batching to every 25 keeps updates
// frequent enough for a smooth progress bar.
const progressEvery = 25

// Publisher is the subset of server.Hub the scanner needs to report
// progress. Defined here (rather than imported from server) so the
// dependency direction stays scanner → nothing.
type Publisher interface {
	Publish(kind string, payload any)
}

// ThumbStore is the slice of *thumbnail.Pack the scanner uses to
// persist a generated thumbnail. Declared as an interface so tests
// can stub it and so scanner doesn't need to import server.
type ThumbStore interface {
	Put(hash [32]byte, data []byte) error
}

// Thumbnailer renders a single image file into JPEG bytes plus a
// 64-bit perceptual hash. Declared locally (instead of depending on
// thumbnail.GenerateWithHash) so this package stays a leaf. The hash
// feeds cluster.Manager; a zero value means "not computed" and is
// treated as absent by consumers.
type Thumbnailer func(path string) ([]byte, uint64, error)

// Detector is the minimum surface the scanner needs from
// internal/vision. Declared here (not imported) so the dependency
// direction stays scanner → protocol only. A nil Detector, or one
// whose Available returns false, disables detection for the scan —
// every other pipeline step runs unchanged.
type Detector interface {
	Available() bool
	Detect(ctx context.Context, path string) (*protocol.DetectResponse, error)
}

// Library is the slice of *library.Library Scan writes into. Declared
// as an interface so tests can stub it with a recording fake.
//
// GetPhoto and Len exist for the skip-unchanged fast path and the
// library:updated payload respectively — both are read-only, but
// keeping them here means a test stub can omit neither.
type Library interface {
	AddPhoto(p *library.Photo)
	GetPhoto(path string) (*library.Photo, error)
	Len() int
}

// Options parameterises Scan. Publisher, ThumbStore and Thumbnailer
// are optional enhancements; a zero-value Options is a valid "just
// catalogue the files, don't broadcast, don't generate thumbs" scan.
type Options struct {
	Publisher   Publisher
	ThumbStore  ThumbStore
	Thumbnailer Thumbnailer

	// Detector is the optional vision sidecar client. When nil or
	// when Available reports false, detection is skipped entirely
	// and the rest of the pipeline runs unchanged — graceful
	// degradation is a core property of the sidecar architecture.
	Detector Detector

	// Autotag is passed to internal/metadata/autotag.Derive on every
	// fresh photo. The zero value is safe: it produces a minimal set
	// (kind, year, camera, …) without folder tags or geocoding.
	Autotag autotag.Options

	// Workers caps the number of concurrent file workers. Zero means
	// runtime.NumCPU() — the historic "full throttle" behaviour. A
	// low-intensity background rescan sets this to a small fraction
	// (e.g. NumCPU/4) so the user can keep a game or video call
	// running without feeling Kestrel in the background.
	Workers int

	// ThrottleSleep is paused between every file a worker processes.
	// Zero means no pause — historic full-speed behaviour. A few
	// milliseconds is enough to cap disk I/O pressure without making
	// rescans feel glacial. The sleep is ctx-aware so a cancel is
	// still prompt.
	ThrottleSleep time.Duration
}

// Progress is the payload of a "scan:progress" event. Total is -1
// while the walk is still discovering files, flipping to the final
// discovered count the instant the walker finishes (well before
// processing completes) so the UI can render a real progress bar
// through the bulk of the scan instead of only at the very end.
type Progress struct {
	Processed int    `json:"processed"`
	Total     int    `json:"total"`
	Root      string `json:"root"`
}

// supportedExts is the set of file extensions the scanner treats as
// media (still images and videos). Extensions are compared
// case-insensitively. Videos that depend on ffmpeg for thumbnail and
// metadata are still indexed when ffmpeg is missing — they fall back
// to a placeholder thumbnail and zero dimensions, so library state
// stays consistent across machines with and without ffmpeg.
var supportedExts = map[string]struct{}{
	// Images
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".gif":  {},
	".webp": {},
	// Videos — must stay in lockstep with autotag.videoExts.
	".mp4":  {},
	".mov":  {},
	".m4v":  {},
	".avi":  {},
	".mkv":  {},
	".webm": {},
}

// Scan walks root in parallel and writes each discovered photo
// directly into lib via AddPhoto. Returns the number of photos added
// during this scan; the library's total may be larger if previous
// scans populated it. The walk respects ctx: cancelling stops further
// work and returns ctx.Err wrapped with the root path.
//
// Options.Publisher (when non-nil) receives "scan:progress" events
// every progressEvery photos from whichever worker hits the boundary,
// plus a terminal event with Total set to the final added count.
// Options.ThumbStore + Options.Thumbnailer (when both non-nil) cause
// each worker to render and store a 256×256 JPEG thumbnail for its
// photo before committing it to the library.
func Scan(ctx context.Context, root string, lib Library, opts Options) (int, error) {
	paths := make(chan string, pathQueueSize)

	// walkErrCh isolates the walker's error so the main goroutine
	// reads it via a channel receive rather than a shared variable —
	// the race detector doesn't always propagate happens-before
	// through the close(paths) → worker-done → WaitGroup chain, even
	// though the logical ordering is safe.
	walkErrCh := make(chan error, 1)
	// discovered holds the total file count the walker enqueued. It
	// starts at -1 ("still walking") and is set exactly once, from the
	// walker goroutine, the moment filepath.WalkDir returns. Workers
	// draining `paths` read it on every progressEvery boundary so their
	// emissions carry a real total as soon as enumeration is done.
	var discovered atomic.Int64
	discovered.Store(-1)
	go func() {
		count, err := walkPaths(ctx, root, paths)
		discovered.Store(int64(count))
		walkErrCh <- err
		close(paths)
	}()

	var added atomic.Int64
	var workers sync.WaitGroup
	for range workerCount(opts) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			processPaths(ctx, paths, lib, opts, root, &added, &discovered)
		}()
	}
	workers.Wait()
	walkErr := <-walkErrCh

	total := int(added.Load())
	if opts.Publisher != nil {
		opts.Publisher.Publish("scan:progress", Progress{
			Processed: total,
			Total:     total,
			Root:      root,
		})
	}

	if walkErr != nil {
		return total, fmt.Errorf("scanning %s: %w", root, walkErr)
	}
	if err := ctx.Err(); err != nil {
		return total, fmt.Errorf("scanning %s: %w", root, err)
	}
	return total, nil
}

// walkPaths is the producer half of the pool: it walks the tree and
// pushes every supported image path onto paths. It returns the number
// of paths successfully enqueued so the caller can publish a known
// total once enumeration is done. Stops early if ctx is cancelled or
// filepath.WalkDir reports an error on the root itself.
func walkPaths(ctx context.Context, root string, paths chan<- string) (int, error) {
	var count int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", path, err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() || !isSupportedMedia(path) {
			return nil
		}
		select {
		case paths <- path:
			count++
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	return count, err
}

// processPaths is the consumer half: each worker pulls paths, builds
// the Photo, optionally renders a thumbnail, and writes the result
// straight into lib. Per-file failures are logged and skipped — the
// scanner is best-effort, matching docs/system-design.md "skip
// unreadable files".
//
// added is the shared counter used for progress events. A worker
// emits scan:progress whenever its increment crosses a progressEvery
// boundary, so updates arrive at roughly the same cadence as in a
// single-producer model regardless of which worker finished first.
func processPaths(
	ctx context.Context,
	paths <-chan string,
	lib Library,
	opts Options,
	root string,
	added *atomic.Int64,
	discovered *atomic.Int64,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-paths:
			if !ok {
				return
			}

			// Skip-unchanged fast path. If the library already has an
			// entry for this path whose size + mtime match what's on
			// disk, we skip hashing entirely — a rescan after a
			// cancelled scan (or periodic rescan) then resumes
			// cheaply instead of re-hashing every file.
			if unchanged(path, lib) {
				n := added.Add(1)
				publishProgress(opts, n, root, discovered)
				if !throttle(ctx, opts.ThrottleSleep) {
					return
				}
				continue
			}

			photo, err := buildPhoto(path, opts.Autotag)
			if err != nil {
				slog.Warn("scanner skipping file", "path", path, "err", err)
				continue
			}
			if err := storeThumbnail(photo, opts); err != nil {
				// Thumb generation is best-effort — a broken thumbnail
				// must not lose the photo from the library. pHash stays
				// zero, cluster.Manager will skip the photo until the
				// next successful rebuild.
				slog.Warn("thumbnail generation failed", "path", path, "err", err)
			}
			// Detection is best-effort too: a sidecar failure never
			// loses the photo or blocks the scan.
			applyDetection(ctx, photo, opts)
			lib.AddPhoto(photo)

			n := added.Add(1)
			publishProgress(opts, n, root, discovered)
			if !throttle(ctx, opts.ThrottleSleep) {
				return
			}
		}
	}
}

// workerCount resolves opts.Workers into a usable pool size, clamped
// to [1, runtime.NumCPU()]. Zero means "default" and maps to NumCPU;
// negatives and out-of-range values are rounded into the valid band
// so a misconfigured scheduler can't deadlock the scanner or blow
// past the hardware.
func workerCount(opts Options) int {
	max := runtime.NumCPU()
	if opts.Workers <= 0 {
		return max
	}
	if opts.Workers > max {
		return max
	}
	return opts.Workers
}

// throttle sleeps for d (when positive) or returns immediately.
// Returns false when ctx is cancelled during the sleep so the caller
// can bail out instead of processing another file. Factored out so
// the sleep-vs-cancel decision lives in one place.
func throttle(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// unchanged reports whether path matches an existing library entry
// at byte size + modification time. A match means the file hasn't
// moved and hasn't been edited since the prior scan, so re-hashing
// would produce the same result — skip it. Any error (including a
// missing entry or unreadable file) returns false: when in doubt,
// do the full build.
func unchanged(path string, lib Library) bool {
	existing, err := lib.GetPhoto(path)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return existing.SizeBytes == info.Size() && existing.ModTime.Equal(info.ModTime())
}

// publishProgress emits a scan:progress event when n hits the
// batching boundary. Extracted so both the skip-fast-path and the
// full-build path share one call site and one policy.
func publishProgress(opts Options, n int64, root string, discovered *atomic.Int64) {
	if opts.Publisher == nil || n%progressEvery != 0 {
		return
	}
	total := -1
	if discovered != nil {
		total = int(discovered.Load())
	}
	opts.Publisher.Publish("scan:progress", Progress{
		Processed: int(n),
		Total:     total,
		Root:      root,
	})
}

// storeThumbnail renders and stores a thumbnail when the scan was
// configured with both a Thumbnailer and a ThumbStore. With either
// one missing it is a no-op — Phase 6 keeps thumb generation optional
// so tests and future code paths can skip it.
//
// The perceptual hash returned by the thumbnailer is written straight
// onto the photo so a later cluster rebuild can use it. Decode runs
// once per file; doing the hash in the same pass is essentially free.
func storeThumbnail(photo *library.Photo, opts Options) error {
	if opts.Thumbnailer == nil || opts.ThumbStore == nil {
		return nil
	}
	data, phash, err := opts.Thumbnailer(photo.Path)
	if err != nil {
		return fmt.Errorf("rendering: %w", err)
	}
	photo.PHash = phash
	hashBytes, err := hex.DecodeString(photo.Hash)
	if err != nil || len(hashBytes) != 32 {
		return fmt.Errorf("decoding hash %q: %w", photo.Hash, err)
	}
	var hash [32]byte
	copy(hash[:], hashBytes)
	if err := opts.ThumbStore.Put(hash, data); err != nil {
		return fmt.Errorf("storing: %w", err)
	}
	return nil
}

// buildPhoto stats path, hashes the file contents, pulls the EXIF
// snapshot, and derives the photo's auto-tag set. Any of the first
// three steps failing produces an error wrapped with the path; the
// worker logs and skips so the scan as a whole completes. Auto-tag
// derivation is always best-effort — an empty tag slice is a valid
// output.
func buildPhoto(path string, autotagOpts autotag.Options) (*library.Photo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat for %s: %w", path, err)
	}
	hash, err := hashFile(path)
	if err != nil {
		return nil, fmt.Errorf("hashing %s: %w", path, err)
	}
	meta, err := metadata.Extract(path)
	if err != nil {
		return nil, fmt.Errorf("extracting metadata for %s: %w", path, err)
	}
	return &library.Photo{
		Path:       path,
		Hash:       hash,
		Name:       filepath.Base(path),
		SizeBytes:  info.Size(),
		ModTime:    info.ModTime(),
		Width:      meta.Width,
		Height:     meta.Height,
		TakenAt:    meta.TakenAt,
		CameraMake: meta.CameraMake,
		AutoTags:   autotag.Derive(path, meta, autotagOpts),
	}, nil
}

// applyDetection asks the vision sidecar about photo and merges the
// response into Faces (structured) and AutoTags (as object:<label>).
// Every failure mode — nil detector, unavailable, per-image error —
// is logged at debug and dropped. The scan completes regardless; a
// later re-scan with a healthy sidecar fills in what was missed.
//
// Object labels are routed through autotag's canonical formatter so
// "Golden Retriever" from a hypothetical finer-grained model collapses
// to "golden-retriever" just like folder/camera names do.
func applyDetection(ctx context.Context, photo *library.Photo, opts Options) {
	if opts.Detector == nil || !opts.Detector.Available() {
		return
	}
	result, err := opts.Detector.Detect(ctx, photo.Path)
	if err != nil {
		slog.Debug("vision detection skipped", "path", photo.Path, "err", err)
		return
	}
	if len(result.Faces) > 0 {
		faces := make([]library.FaceDetection, 0, len(result.Faces))
		for _, f := range result.Faces {
			faces = append(faces, library.FaceDetection{
				BBox:       [4]int{f.BBox.X, f.BBox.Y, f.BBox.W, f.BBox.H},
				Embedding:  f.Embedding,
				Confidence: f.Confidence,
			})
		}
		photo.Faces = faces
	}
	if len(result.Objects) > 0 {
		labels := make([]string, 0, len(result.Objects))
		for _, o := range result.Objects {
			if o.Label == "" {
				continue
			}
			labels = append(labels, "object:"+o.Label)
		}
		photo.AutoTags = autotag.MergeAndNormalize(photo.AutoTags, labels)
	}
}

// hashFile streams path through SHA-256 and returns the hex digest.
// Streaming keeps memory flat for multi-megabyte photos.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// isSupportedMedia reports whether path has a recognised media
// extension (image or video). Comparison is case-insensitive.
func isSupportedMedia(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := supportedExts[ext]
	return ok
}
