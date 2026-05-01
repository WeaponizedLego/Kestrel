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
	"github.com/WeaponizedLego/kestrel/internal/metadata/fingerprint"
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

	// AddAudio mirrors AddPhoto for audio files. Audio entries live
	// in a sibling map (see internal/library) and never enter the
	// photo cluster bucket.
	AddAudio(a *library.Audio)
	// GetAudio is the audio analogue of GetPhoto, used by the
	// skip-unchanged fast path.
	GetAudio(path string) (*library.Audio, error)
}

// Options parameterises Scan. Publisher, ThumbStore and Thumbnailer
// are optional enhancements; a zero-value Options is a valid "just
// catalogue the files, don't broadcast, don't generate thumbs" scan.
type Options struct {
	Publisher   Publisher
	ThumbStore  ThumbStore
	Thumbnailer Thumbnailer

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

	// OnDirsFound, if non-nil, receives batches of absolute directory
	// paths as the parallel walker discovers them, including root
	// itself. Used by the API layer to register every directory under
	// a freshly-added root as its own sub-root in watchroots.Store.
	// Callbacks fire from a background goroutine; the implementation
	// must be safe to call from arbitrary goroutines.
	OnDirsFound func(dirs []string)

	// SingleThreadWalk forces the legacy single-threaded directory
	// walk regardless of the KESTREL_SINGLE_THREAD_WALK env var.
	// Useful for tests that need parity with the old behaviour.
	SingleThreadWalk bool
}

// WorkerStatus is one entry in the "scan:workers" event payload. Kind
// is one of "walking", "hashing", or "idle"; the heartbeat goroutine
// in Scan publishes a snapshot of every active worker periodically so
// the UI can show what each core is doing without per-event WS
// chatter.
type WorkerStatus struct {
	ID      int    `json:"id"`
	Current string `json:"current"`
	Kind    string `json:"kind"`
}

const (
	workerKindIdle    = "idle"
	workerKindHashing = "hashing"
	workerKindWalking = "walking"
)

// workerHeartbeat is how often the scanner snapshots and publishes
// per-worker status. Fast enough that the UI feels alive, slow enough
// that millions of files don't translate into millions of WS frames.
const workerHeartbeat = 200 * time.Millisecond

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
	// Audio — must stay in lockstep with autotag.audioExts /
	// metadata.audioExts / thumbnail.audioExts.
	".mp3":  {},
	".m4a":  {},
	".aac":  {},
	".flac": {},
	".wav":  {},
	".ogg":  {},
	".opus": {},
}

// audioExts is the scanner's local copy used to branch processing
// between photo/video and audio paths. Mirrors the audio entries
// above.
var audioExts = map[string]struct{}{
	".mp3":  {},
	".m4a":  {},
	".aac":  {},
	".flac": {},
	".wav":  {},
	".ogg":  {},
	".opus": {},
}

// isAudioPath reports whether path is a recognised audio file.
func isAudioPath(path string) bool {
	_, ok := audioExts[strings.ToLower(filepath.Ext(path))]
	return ok
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
	// Compose the walker's directory-discovery callback so a single
	// batch fans out both to the caller's hook (e.g. registering
	// sub-roots) and to the WS hub as a "scan:dirs-found" event the UI
	// renders into its discovery view.
	var totalDirs atomic.Int64
	dirsCb := opts.OnDirsFound
	if opts.Publisher != nil {
		original := dirsCb
		dirsCb = func(batch []string) {
			if original != nil {
				original(batch)
			}
			n := totalDirs.Add(int64(len(batch)))
			opts.Publisher.Publish("scan:dirs-found", map[string]any{
				"root":  root,
				"paths": batch,
				"total": n,
			})
		}
	}

	go func() {
		wopts := walkOptions{
			Workers:      workerCount(opts),
			OnDirsFound:  dirsCb,
			SingleThread: opts.SingleThreadWalk,
		}
		count, err := walkPathsBFS(ctx, root, paths, wopts)
		discovered.Store(int64(count))
		walkErrCh <- err
		close(paths)
	}()

	processorCount := workerCount(opts)
	slots := make([]*atomic.Pointer[string], processorCount)
	for i := range slots {
		slots[i] = &atomic.Pointer[string]{}
	}
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		runWorkerHeartbeat(heartbeatCtx, opts.Publisher, root, slots)
	}()

	var added atomic.Int64
	var workers sync.WaitGroup
	for i := range processorCount {
		workers.Add(1)
		go func(workerID int) {
			defer workers.Done()
			processPaths(ctx, paths, lib, opts, root, &added, &discovered, slots[workerID])
		}(i)
	}
	workers.Wait()
	stopHeartbeat()
	<-heartbeatDone
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
//
// slot is the worker's status slot; the worker writes a pointer to
// the path it's currently hashing so the heartbeat goroutine can
// publish a coherent snapshot. nil clears the slot.
func processPaths(
	ctx context.Context,
	paths <-chan string,
	lib Library,
	opts Options,
	root string,
	added *atomic.Int64,
	discovered *atomic.Int64,
	slot *atomic.Pointer[string],
) {
	defer slot.Store(nil)
	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-paths:
			if !ok {
				return
			}
			current := path
			slot.Store(&current)

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

			if isAudioPath(path) {
				audio, err := buildAudio(path)
				if err != nil {
					slog.Warn("scanner skipping audio", "path", path, "err", err)
					continue
				}
				if err := storeAudioThumbnail(audio, opts); err != nil {
					slog.Warn("audio thumbnail generation failed", "path", path, "err", err)
				}
				lib.AddAudio(audio)
			} else {
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
				lib.AddPhoto(photo)
			}

			n := added.Add(1)
			publishProgress(opts, n, root, discovered)
			if !throttle(ctx, opts.ThrottleSleep) {
				return
			}
		}
	}
}

// runWorkerHeartbeat periodically snapshots the per-worker slots and
// publishes a "scan:workers" event with each worker's current path.
// Returns when ctx is cancelled (the scan is finishing). One emitted
// snapshot per heartbeat regardless of throughput keeps the WS rate
// constant.
func runWorkerHeartbeat(ctx context.Context, pub Publisher, root string, slots []*atomic.Pointer[string]) {
	if pub == nil || len(slots) == 0 {
		return
	}
	ticker := time.NewTicker(workerHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Final emit so the UI sees workers idle out cleanly.
			pub.Publish("scan:workers", snapshotWorkers(slots, root))
			return
		case <-ticker.C:
			pub.Publish("scan:workers", snapshotWorkers(slots, root))
		}
	}
}

// snapshotWorkers reads each worker slot and returns a JSON-friendly
// status slice. A nil slot is reported as "idle" so the UI can render
// a row per core regardless of activity.
func snapshotWorkers(slots []*atomic.Pointer[string], root string) []WorkerStatus {
	out := make([]WorkerStatus, len(slots))
	for i, slot := range slots {
		s := WorkerStatus{ID: i, Kind: workerKindIdle}
		if p := slot.Load(); p != nil {
			s.Current = *p
			s.Kind = workerKindHashing
		}
		out[i] = s
	}
	_ = root // reserved for future per-root scoping if scans interleave.
	return out
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
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if isAudioPath(path) {
		existing, err := lib.GetAudio(path)
		if err != nil {
			return false
		}
		return existing.SizeBytes == info.Size() && existing.ModTime.Equal(info.ModTime())
	}
	existing, err := lib.GetPhoto(path)
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
	slog.Debug("scan progress", "root", root, "processed", n, "total", total)
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

// buildAudio stats path, hashes the file contents, runs ffprobe for
// codec/duration/bitrate/channels, computes the Chromaprint
// fingerprint, and derives the autotag set. Errors from stat / hash
// abort the build (the worker logs and skips); ffprobe and fpcalc
// are best-effort, so a missing tool yields zero metadata and a
// zero PHash respectively.
func buildAudio(path string) (*library.Audio, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat for %s: %w", path, err)
	}
	hash, err := hashFile(path)
	if err != nil {
		return nil, fmt.Errorf("hashing %s: %w", path, err)
	}
	am := metadata.ExtractAudio(path)
	phash, err := fingerprint.AudioPHash(path)
	if err != nil {
		// Already best-effort inside the package; log and continue.
		slog.Warn("audio fingerprint failed", "path", path, "err", err)
		phash = 0
	}
	return &library.Audio{
		Path:        path,
		Hash:        hash,
		Name:        filepath.Base(path),
		SizeBytes:   info.Size(),
		ModTime:     info.ModTime(),
		Codec:       am.Codec,
		DurationSec: am.DurationSec,
		BitrateKbps: am.BitrateKbps,
		Channels:    am.Channels,
		PHash:       phash,
		AutoTags:    autotag.DeriveAudio(path, info.ModTime(), am),
	}, nil
}

// storeAudioThumbnail renders a filename-card thumbnail for the
// audio file and stores it in the pack keyed by the audio's hash.
// Mirrors storeThumbnail's contract: a zero Thumbnailer or zero
// ThumbStore is a no-op, and a render failure is logged by the
// caller rather than failing the audio's library entry.
func storeAudioThumbnail(audio *library.Audio, opts Options) error {
	if opts.Thumbnailer == nil || opts.ThumbStore == nil {
		return nil
	}
	data, _, err := opts.Thumbnailer(audio.Path)
	if err != nil {
		return fmt.Errorf("rendering: %w", err)
	}
	hashBytes, err := hex.DecodeString(audio.Hash)
	if err != nil || len(hashBytes) != 32 {
		return fmt.Errorf("decoding hash %q: %w", audio.Hash, err)
	}
	var hash [32]byte
	copy(hash[:], hashBytes)
	if err := opts.ThumbStore.Put(hash, data); err != nil {
		return fmt.Errorf("storing: %w", err)
	}
	return nil
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
