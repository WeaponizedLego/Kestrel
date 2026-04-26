// Package fingerprint computes a 64-bit perceptual fingerprint for
// audio files via Chromaprint's fpcalc CLI. The fold from
// Chromaprint's variable-length integer fingerprint to a fixed 64-bit
// value is deterministic so a re-run on the same file produces the
// same hash, and Hamming distance on the result correlates (coarsely)
// with "is this the same recording".
//
// The fold:
//   1. Take the raw fingerprint integers from `fpcalc -raw -length 30`.
//   2. Skip the first sampleSkip integers (typical leading silence /
//      intro effects do not generalise across re-encodes).
//   3. From the next sampleWindow integers, XOR-fold pairs into a
//      single uint64 alternating low/high 32-bit halves.
//
// fpcalc unavailable, parsing failures, and short fingerprints all
// yield (0, nil): zero is the absent sentinel that the cluster
// manager already understands, matching the photo PHash convention.
package fingerprint

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// fpcalcTimeout caps how long a single fpcalc call may run. 30
// seconds of audio is normally analysed in well under a second; the
// cap is a guard against pathological files.
const fpcalcTimeout = 15 * time.Second

// sampleSkip drops the first N integers of the raw fingerprint.
// Chromaprint's leading samples often include silence or intro fades
// that do not survive re-encoding; skipping them stabilises the fold
// across rips at different bitrates.
const sampleSkip = 8

// sampleWindow is the number of fingerprint integers fed into the
// XOR fold. 32 integers × 32 bits = 1024 bits, folded down to 64.
const sampleWindow = 32

// AudioPHash returns the 64-bit Chromaprint-derived fingerprint for
// path. Returns (0, nil) when fpcalc is missing, the file is too
// short to fingerprint, or the parser cannot read fpcalc's output —
// audio still indexes with PHash = 0 and the cluster manager skips
// it, matching the photo convention.
//
// A non-nil error is reserved for cases the caller should log (a
// child process that failed in an unexpected way). The scanner
// treats either zero hash or non-nil error as "no fingerprint" and
// keeps going.
func AudioPHash(path string) (uint64, error) {
	bin, err := exec.LookPath("fpcalc")
	if err != nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), fpcalcTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-raw", "-length", "30", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	hash, parseErr := parseFpcalcOutput(stdout)
	// Drain anything the child still has to say so Wait returns.
	_, _ = io.Copy(io.Discard, stdout)
	if waitErr := cmd.Wait(); waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
		// Child exited badly; treat fingerprint as absent.
		return 0, nil
	}
	if parseErr != nil {
		return 0, nil
	}
	return hash, nil
}

// parseFpcalcOutput reads fpcalc's "key=value" lines from r and folds
// the FINGERPRINT line into a 64-bit hash. Returns 0 with a non-nil
// error when no FINGERPRINT line was found; returns 0, nil when the
// line was found but the fold could not produce a meaningful hash
// (too few samples).
func parseFpcalcOutput(r io.Reader) (uint64, error) {
	scanner := bufio.NewScanner(r)
	// Fingerprint lines for 30s clips can run a few KB; bump the
	// scanner buffer well above the default 64KB to be safe.
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)

	var fingerprint string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "FINGERPRINT=") {
			fingerprint = strings.TrimPrefix(line, "FINGERPRINT=")
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if fingerprint == "" {
		return 0, errors.New("fpcalc emitted no FINGERPRINT line")
	}
	return foldFingerprint(fingerprint)
}

// foldFingerprint converts a comma-separated decimal Chromaprint
// fingerprint into a 64-bit hash. See package doc for the algorithm.
func foldFingerprint(raw string) (uint64, error) {
	parts := strings.Split(raw, ",")
	if len(parts) < sampleSkip+sampleWindow {
		// Too short — return absent (zero) without a hard error so the
		// scanner just skips clustering for this file.
		return 0, nil
	}
	var hash uint64
	for i := 0; i < sampleWindow; i++ {
		v, err := strconv.ParseInt(strings.TrimSpace(parts[sampleSkip+i]), 10, 64)
		if err != nil {
			return 0, err
		}
		// Alternate halves so 32-bit words contribute to both halves
		// of the resulting 64-bit hash.
		bits := uint64(uint32(v))
		if i%2 == 0 {
			hash ^= bits
		} else {
			hash ^= bits << 32
		}
	}
	return hash, nil
}
