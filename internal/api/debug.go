package api

import (
	"bufio"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strconv"
)

// debugTailDefault is the number of lines returned when the caller
// doesn't pass ?lines=. Sized so the modal renders quickly without
// scrolling forever, while still covering several minutes of normal
// activity.
const debugTailDefault = 200

// debugTailMax caps an over-eager caller. Reading the whole log into
// memory is fine for diagnostic snapshots, but at multi-MB scale we'd
// rather force the user to grab the file directly.
const debugTailMax = 2000

// DebugHandler serves /api/debug: a small read-only window into the
// running process for support and self-help. Today it exposes the log
// file path plus the tail of the active log so a user can paste a
// recent slice into a bug report without leaving the app.
type DebugHandler struct {
	logPath string
}

// NewDebugHandler wires the handler to the resolved log path. Pass
// the same value used to open the rotating log writer; an empty
// string disables the tail (we still serve the metadata so the UI
// can show "log file disabled").
func NewDebugHandler(logPath string) *DebugHandler {
	return &DebugHandler{logPath: logPath}
}

// Register attaches the /debug route. The server strips the "/api"
// prefix before calling in.
func (h *DebugHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/debug", h.handle)
}

type debugResponse struct {
	LogPath       string   `json:"log_path"`
	LogPathBackup string   `json:"log_path_backup"`
	FileSize      int64    `json:"file_size"`
	Lines         []string `json:"lines"`
	LinesReturned int      `json:"lines_returned"`
	Truncated     bool     `json:"truncated"`
}

// handle returns the log path and a tail of the active log file. The
// number of lines is clamped to [1, debugTailMax]; missing files are
// reported as an empty tail rather than a 404 so the UI can still
// display the path the user should look at on disk.
func (h *DebugHandler) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is allowed")
		return
	}

	n := debugTailDefault
	if raw := r.URL.Query().Get("lines"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "lines must be a positive integer")
			return
		}
		if parsed > debugTailMax {
			parsed = debugTailMax
		}
		n = parsed
	}

	resp := debugResponse{
		LogPath:       h.logPath,
		LogPathBackup: h.logPath + ".1",
		Lines:         []string{},
	}

	if h.logPath == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	info, statErr := os.Stat(h.logPath)
	if statErr == nil {
		resp.FileSize = info.Size()
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "stat log file: "+statErr.Error())
		return
	}

	lines, truncated, readErr := tailLines(h.logPath, n)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "reading log file: "+readErr.Error())
		return
	}
	resp.Lines = lines
	resp.LinesReturned = len(lines)
	resp.Truncated = truncated

	writeJSON(w, http.StatusOK, resp)
}

// tailLines reads up to n trailing lines from path using a streaming
// ring buffer so we never hold the entire file in memory. truncated
// reports whether older lines were dropped (file had more than n
// lines). A missing file returns (nil, false, fs.ErrNotExist).
func tailLines(path string, n int) ([]string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// slog text lines are usually ~200B but a stack-traced error can be
	// long; bump the buffer so we don't truncate the rare wide line.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	ring := make([]string, 0, n)
	var seen int
	for scanner.Scan() {
		seen++
		if len(ring) < n {
			ring = append(ring, scanner.Text())
			continue
		}
		// Shift left by one and append. n is small (<=2000) so this
		// is cheap and avoids the index bookkeeping a true ring needs.
		copy(ring, ring[1:])
		ring[len(ring)-1] = scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return ring, seen > n, nil
}
