package thumbnail

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"sync"
)

// thumbs.pack on-disk layout (see docs/system-design.md):
//
//   [4 bytes magic "KTMB"]
//   [4 bytes version uint32, big-endian]
//   [4 bytes entry count uint32, big-endian]
//   [count × index entries] each:
//       [32 bytes: SHA-256 hash of the source path]
//       [ 8 bytes: offset uint64, big-endian — bytes into the file]
//       [ 4 bytes: size uint32, big-endian — JPEG payload length]
//   [concatenated JPEG payloads in index order]
//
// The index is deliberately contiguous at the head so a cold open can
// `read(44*N)` bytes and build the in-memory map without touching any
// pixel data.
//
// At runtime we take a write-ahead-log approach for new thumbnails:
// Put() appends to thumbs.pack.log rather than rewriting the pack.
// Save() compacts the log back into a fresh pack atomically (.tmp +
// rename). A crash between Put and Save is recoverable on reopen via
// replayLog — any partial trailing record is ignored.

const (
	packMagic       = "KTMB"
	packVersion     = uint32(1)
	packHashSize    = 32
	packHeaderBytes = 12 // 4 magic + 4 version + 4 count
	packEntryBytes  = packHashSize + 8 + 4
)

// ErrBadMagic is returned by Open when the pack file exists but does
// not start with "KTMB". Almost certainly the wrong file.
var ErrBadMagic = errors.New("not a kestrel thumbnail pack")

// ErrUnsupportedVersion is returned when the pack was written by a
// future build that this reader doesn't understand.
var ErrUnsupportedVersion = errors.New("unsupported thumbnail pack version")

// source tags where a given index entry's bytes live. New thumbs
// written during the session live in the log; entries loaded from the
// pack at Open time live in the pack itself until the next Save.
type source uint8

const (
	sourcePack source = iota
	sourceLog
)

type packEntry struct {
	Offset int64
	Size   uint32
	Source source
}

// Pack is the on-disk thumbnail store. Callers hold exactly one Pack
// per library and use Get/Put; reads are concurrent (RLock), writes
// serialize (Lock).
type Pack struct {
	mu       sync.RWMutex
	path     string
	logPath  string
	packFile *os.File // read handle for committed data; nil on a fresh library
	logFile  *os.File // read+write handle for pending appends
	logSize  int64
	index    map[[packHashSize]byte]packEntry
}

// Open reads the pack index into memory and opens both the pack (for
// random reads) and the log (for fresh appends). A missing pack file
// is not an error — first-run binaries create one on the first Save.
// Existing log records are replayed so an interrupted previous run
// doesn't lose its pending thumbnails.
func Open(path string) (*Pack, error) {
	p := &Pack{
		path:    path,
		logPath: path + ".log",
		index:   make(map[[packHashSize]byte]packEntry),
	}

	if err := p.loadPack(); err != nil {
		return nil, fmt.Errorf("loading pack %s: %w", path, err)
	}
	if err := p.openLog(); err != nil {
		return nil, fmt.Errorf("opening log %s: %w", p.logPath, err)
	}
	return p, nil
}

// Get returns the JPEG bytes for hash if present. A missing entry
// returns (nil, false, nil) — the caller decides whether that means
// "not generated yet" or "unknown photo".
func (p *Pack) Get(hash [packHashSize]byte) ([]byte, bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	e, ok := p.index[hash]
	if !ok {
		return nil, false, nil
	}
	data, err := p.readEntryLocked(e)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// Put records a new thumbnail. The bytes are appended to the log
// immediately so they survive a crash; the in-memory index is updated
// in the same critical section. Overwrites an existing entry with the
// same hash.
func (p *Pack) Put(hash [packHashSize]byte, data []byte) error {
	if len(data) > int(^uint32(0)) {
		return fmt.Errorf("thumbnail too large: %d bytes", len(data))
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	var head [packHashSize + 4]byte
	copy(head[:packHashSize], hash[:])
	binary.BigEndian.PutUint32(head[packHashSize:], uint32(len(data)))

	dataOffset := p.logSize + int64(len(head))
	if _, err := p.logFile.Write(head[:]); err != nil {
		return fmt.Errorf("writing log header: %w", err)
	}
	if _, err := p.logFile.Write(data); err != nil {
		return fmt.Errorf("writing log data: %w", err)
	}
	p.logSize = dataOffset + int64(len(data))
	p.index[hash] = packEntry{
		Offset: dataOffset,
		Size:   uint32(len(data)),
		Source: sourceLog,
	}
	return nil
}

// Len reports how many thumbnails the pack currently knows about,
// committed and pending combined.
func (p *Pack) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.index)
}

// Save compacts all pending log records back into the pack file
// atomically: a new .tmp is written from scratch, renamed over the
// original, and the log is truncated. Safe to call on a pack with no
// pending writes — the result is a byte-for-byte identical file plus
// a freshly-emptied log.
func (p *Pack) Save() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.saveLocked()
}

// Close flushes pending writes and releases file handles. After Close
// the Pack is unusable; callers should discard the value.
func (p *Pack) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.saveLocked(); err != nil {
		return err
	}
	if p.packFile != nil {
		p.packFile.Close()
		p.packFile = nil
	}
	if p.logFile != nil {
		p.logFile.Close()
		p.logFile = nil
	}
	_ = os.Remove(p.logPath)
	return nil
}

// loadPack reads the pack header and index into memory. A missing
// file is treated as a fresh library — no error, empty index.
func (p *Pack) loadPack() error {
	f, err := os.Open(p.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("opening: %w", err)
	}

	var hdr [packHeaderBytes]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		f.Close()
		return fmt.Errorf("reading header: %w", err)
	}
	if string(hdr[0:4]) != packMagic {
		f.Close()
		return ErrBadMagic
	}
	version := binary.BigEndian.Uint32(hdr[4:8])
	if version != packVersion {
		f.Close()
		return fmt.Errorf("got version %d: %w", version, ErrUnsupportedVersion)
	}
	count := binary.BigEndian.Uint32(hdr[8:12])

	entries := make([]byte, int(count)*packEntryBytes)
	if _, err := io.ReadFull(f, entries); err != nil {
		f.Close()
		return fmt.Errorf("reading %d index entries: %w", count, err)
	}
	for i := uint32(0); i < count; i++ {
		base := int(i) * packEntryBytes
		var hash [packHashSize]byte
		copy(hash[:], entries[base:base+packHashSize])
		offset := int64(binary.BigEndian.Uint64(entries[base+packHashSize : base+packHashSize+8]))
		size := binary.BigEndian.Uint32(entries[base+packHashSize+8 : base+packHashSize+12])
		p.index[hash] = packEntry{Offset: offset, Size: size, Source: sourcePack}
	}

	p.packFile = f
	return nil
}

// openLog opens the write-ahead log, replaying any surviving records
// from a previous session first. Partial trailing records (torn from
// a crash mid-Put) are silently dropped.
func (p *Pack) openLog() error {
	if err := p.replayLog(); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	f, err := os.OpenFile(p.logPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("opening: %w", err)
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return fmt.Errorf("seeking: %w", err)
	}
	p.logFile = f
	p.logSize = size
	return nil
}

// replayLog walks the log file once and merges surviving records into
// the in-memory index, shadowing any pack entries with the same hash.
func (p *Pack) replayLog() error {
	f, err := os.Open(p.logPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	var pos int64
	var head [packHashSize + 4]byte
	for {
		n, err := io.ReadFull(f, head[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Torn record at EOF — ignore and stop.
			return nil
		}
		var hash [packHashSize]byte
		copy(hash[:], head[:packHashSize])
		size := binary.BigEndian.Uint32(head[packHashSize:])
		dataOffset := pos + int64(n)

		// Sanity-check we've got the whole payload; short files get
		// the same "torn record" treatment.
		if _, err := f.Seek(int64(size), io.SeekCurrent); err != nil {
			return nil
		}
		statInfo, err := f.Stat()
		if err != nil {
			return fmt.Errorf("stat log: %w", err)
		}
		if dataOffset+int64(size) > statInfo.Size() {
			return nil
		}

		p.index[hash] = packEntry{
			Offset: dataOffset,
			Size:   size,
			Source: sourceLog,
		}
		pos = dataOffset + int64(size)
	}
}

// readEntryLocked copies the bytes for e out of whichever file holds
// them. Caller must already hold p.mu (R or W).
func (p *Pack) readEntryLocked(e packEntry) ([]byte, error) {
	var f *os.File
	switch e.Source {
	case sourcePack:
		f = p.packFile
	case sourceLog:
		f = p.logFile
	}
	if f == nil {
		return nil, fmt.Errorf("entry source %d has no open file", e.Source)
	}
	buf := make([]byte, e.Size)
	if _, err := f.ReadAt(buf, e.Offset); err != nil {
		return nil, fmt.Errorf("reading %d bytes at %d: %w", e.Size, e.Offset, err)
	}
	return buf, nil
}

// saveLocked is the body of Save; assumes the write lock is held.
func (p *Pack) saveLocked() error {
	tmp := p.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating %s: %w", tmp, err)
	}
	// Best-effort cleanup if anything below fails pre-rename.
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}

	count := uint32(len(p.index))
	hashes := sortedHashes(p.index)
	dataStart := int64(packHeaderBytes) + int64(count)*int64(packEntryBytes)

	if err := writeHeader(f, count); err != nil {
		cleanup()
		return fmt.Errorf("writing header to %s: %w", tmp, err)
	}
	newOffsets, err := writeIndex(f, hashes, p.index, dataStart)
	if err != nil {
		cleanup()
		return fmt.Errorf("writing index to %s: %w", tmp, err)
	}
	if err := p.copyPayloads(f, hashes); err != nil {
		cleanup()
		return fmt.Errorf("copying payloads to %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("flushing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing %s: %w", tmp, err)
	}

	// Release old handles before the rename; Windows won't replace a
	// file that has an open handle, and it costs nothing on Unix.
	if p.packFile != nil {
		p.packFile.Close()
		p.packFile = nil
	}
	if p.logFile != nil {
		p.logFile.Close()
		p.logFile = nil
	}

	if err := os.Rename(tmp, p.path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmp, p.path, err)
	}
	_ = os.Remove(p.logPath)

	// Reopen pack for reads, truncate the log for fresh appends, and
	// rewrite the in-memory index to point into the new pack.
	newPack, err := os.Open(p.path)
	if err != nil {
		return fmt.Errorf("reopening %s: %w", p.path, err)
	}
	newLog, err := os.OpenFile(p.logPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		newPack.Close()
		return fmt.Errorf("reopening log %s: %w", p.logPath, err)
	}
	p.packFile = newPack
	p.logFile = newLog
	p.logSize = 0
	for h, off := range newOffsets {
		e := p.index[h]
		e.Offset = off
		e.Source = sourcePack
		p.index[h] = e
	}
	return nil
}

// copyPayloads streams every entry's bytes into w in index order.
// Reading from the current file handles (pack or log) before the
// rename means we don't need a separate staging buffer.
func (p *Pack) copyPayloads(w io.Writer, hashes [][packHashSize]byte) error {
	for _, h := range hashes {
		data, err := p.readEntryLocked(p.index[h])
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// writeHeader emits the four-byte magic, the version, and the entry
// count — the 12 bytes that precede the index.
func writeHeader(w io.Writer, count uint32) error {
	var hdr [packHeaderBytes]byte
	copy(hdr[0:4], packMagic)
	binary.BigEndian.PutUint32(hdr[4:8], packVersion)
	binary.BigEndian.PutUint32(hdr[8:12], count)
	_, err := w.Write(hdr[:])
	return err
}

// writeIndex writes every entry in hashes with offsets starting at
// dataStart and growing by each entry's size. Returns the new
// (hash → offset) mapping so saveLocked can update the in-memory
// index once the file has been renamed into place.
func writeIndex(
	w io.Writer,
	hashes [][packHashSize]byte,
	index map[[packHashSize]byte]packEntry,
	dataStart int64,
) (map[[packHashSize]byte]int64, error) {
	offsets := make(map[[packHashSize]byte]int64, len(hashes))
	running := dataStart
	var buf [packEntryBytes]byte
	for _, h := range hashes {
		e := index[h]
		copy(buf[:packHashSize], h[:])
		binary.BigEndian.PutUint64(buf[packHashSize:packHashSize+8], uint64(running))
		binary.BigEndian.PutUint32(buf[packHashSize+8:packHashSize+12], e.Size)
		if _, err := w.Write(buf[:]); err != nil {
			return nil, err
		}
		offsets[h] = running
		running += int64(e.Size)
	}
	return offsets, nil
}

// sortedHashes returns the keys of index sorted by raw byte order.
// Stable ordering keeps the on-disk layout reproducible — useful for
// diffing pack files and for deterministic tests.
func sortedHashes(index map[[packHashSize]byte]packEntry) [][packHashSize]byte {
	hashes := make([][packHashSize]byte, 0, len(index))
	for h := range index {
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool {
		return bytes.Compare(hashes[i][:], hashes[j][:]) < 0
	})
	return hashes
}
