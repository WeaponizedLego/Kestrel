# Supported file types

This is the authoritative list of file extensions Kestrel's scanner will pick up. Everything else is silently skipped during a walk. Extensions are matched case-insensitively.

The set is defined in lockstep across four places — keep them in sync when adding a new format:

- `internal/scanner/scanner.go` — `supportedExts` (the gate)
- `internal/metadata/autotag/autotag.go` — `videoExts` / `audioExts` (drives `kind:*`)
- `internal/metadata/audio.go` — `audioExts` (drives `IsAudioPath`)
- `internal/thumbnail/audio.go` — `audioExts` (drives audio thumbnail dispatch)

## Images

| Extension | Decoder | Notes |
|---|---|---|
| `.jpg` / `.jpeg` | stdlib `image/jpeg` | Full EXIF support (capture time, camera, GPS, …) |
| `.png` | stdlib `image/png` | No EXIF; year-from-mtime fallback |
| `.gif` | stdlib `image/gif` | First frame used for the thumbnail |
| `.webp` | `golang.org/x/image/webp` | Pure-Go decoder, CGO-free |

Pixel dimensions, EXIF, perceptual-hash clustering, and full-quality previews all work for every image type without external tools.

## Videos

| Extension | Container | Served as | Notes |
|---|---|---|---|
| `.mp4` | MP4 | `video/mp4` | |
| `.m4v` | MP4 | `video/mp4` | |
| `.mov` | QuickTime | `video/quicktime` | |
| `.webm` | WebM | `video/webm` | |
| `.mkv` | Matroska | `video/x-matroska` | Browser support varies; `<video>` may refuse some codec combinations |
| `.avi` | AVI | `video/x-msvideo` | Browser support varies; legacy codecs often fail to play |

**Optional dependency: `ffmpeg` / `ffprobe`.** Without it, video files still get indexed and tagged with `kind:video`, but the thumbnail is a static placeholder and dimensions/capture time stay zero. With `ffprobe` available, the scanner extracts width/height and `creation_time`; with `ffmpeg` available, the thumbnailer pulls a real frame.

## Audio

| Extension | Container/Codec | Served as | Notes |
|---|---|---|---|
| `.mp3` | MPEG audio | `audio/mpeg` | Universal browser support |
| `.m4a` | MP4/AAC | `audio/mp4` | All major browsers |
| `.aac` | Raw AAC | `audio/mp4` | Most browsers |
| `.flac` | FLAC | `audio/flac` | Modern browsers including recent Safari |
| `.wav` | PCM WAV | `audio/wav` | Universal browser support |
| `.ogg` | Ogg Vorbis | `audio/ogg` | Chrome/Firefox/Edge; Safari does not play |
| `.opus` | Ogg Opus | `audio/ogg; codecs=opus` | Chrome/Firefox/Edge; Safari support limited |

Audio files use a generated **filename card** as their thumbnail (no waveform decode required) and live in a sibling library map distinct from photos. Browseability, search, and tagging always work.

**Optional dependency: `ffprobe`.** Without it, audio still indexes with `kind:audio`, `codec:<ext>`, and `year:YYYY` from mtime. With it, the autotag pipeline also emits:

- `duration:short` (< 5 min) / `duration:medium` (5–30 min) / `duration:long` (> 30 min)
- `bitrate:low` (< 128 kbps) / `bitrate:mid` (128–256) / `bitrate:high` (> 256)
- `channels:mono` / `channels:stereo` / `channels:surround`

**Optional dependency: `fpcalc` (Chromaprint).** Without it, audio entries carry `PHash = 0` and never appear in Duplicate/Similar clusters. With it, audio gets a 64-bit acoustic fingerprint and clusters in its own bucket (audio fingerprints never merge with image dHash values, even if the 64-bit values happen to be Hamming-close). Chromaprint ships separately from FFmpeg — install `chromaprint` (Arch/macOS), `libchromaprint-tools` (Debian/Ubuntu), or grab a build from acoustid.org.

## Not supported (yet)

These are intentionally out-of-scope today; adding one is a four-place edit (see top of file) plus a MIME-type entry in `internal/api/library.go` if browser playback is desired.

- **Images:** `.bmp`, `.tiff`/`.tif`, `.heic`/`.heif`, `.avif`, raw camera formats (`.cr2`, `.nef`, `.arw`, `.dng`, …)
- **Video:** `.flv`, `.wmv`, `.mpeg`/`.mpg`, `.m2ts`, `.3gp`
- **Audio:** `.wma`, `.aiff`/`.aif`, `.ape`, `.mka`, tracker formats (`.mod`, `.xm`, `.s3m`)

Raw camera formats in particular are a deliberate omission — they require a different decode pipeline (libraw or per-vendor SDKs) and would need EXIF-aware preview rendering before they're useful in the grid.
