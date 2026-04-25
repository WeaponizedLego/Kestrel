#!/usr/bin/env bash
# Prepare the three ONNX model files kestrel-vision needs in
# cmd/kestrel-vision/model/models/.
#
# The script has three modes, tried in order:
#
#   1. Files already present → verify SHA-256 (when known), report, exit.
#   2. URLs provided via env vars → curl each, verify, write.
#   3. Nothing to do → print instructions for getting each file.
#
# The env-var mode exists because "the one true URL" for these models
# drifts over time (HuggingFace mirrors come and go, InsightFace moves
# between hosts) and is better owned by whoever is running the build
# than hardcoded here. CI sets the three env vars in workflow config;
# devs on a laptop set them in a local .envrc or just drop the files
# in by hand.

set -euo pipefail

cd "$(dirname "$0")/.."
dest="cmd/kestrel-vision/model/models"
mkdir -p "$dest"

# --- Catalogue --------------------------------------------------
#
# Each row: local filename  |  expected SHA-256  |  env-var holding
# the URL  |  human-readable hint about where to get it.
#
# Expected SHA-256 values stay empty until someone locks in a
# specific upstream. Once locked, paste the same values into
# cmd/kestrel-vision/model/embed_cgo.go → modelSHA so every session
# load re-verifies the embedded bytes.
models=(
  "scrfd_2.5g.onnx||KESTREL_SCRFD_URL|InsightFace buffalo_m pack (det_2.5g.onnx). Search HuggingFace for 'buffalo_m' and pick a high-traffic mirror (look for org repos, not personal accounts). File is ~3 MB."
  "arcface_r100.onnx||KESTREL_ARCFACE_URL|InsightFace antelopev2 pack (glintr100.onnx). Search HuggingFace for 'antelopev2'. File is ~260 MB — larger than the other two combined."
  "yolov8n.onnx||KESTREL_YOLO_URL|Export from Ultralytics yourself for maximum trust:  pip install ultralytics  &&  yolo export model=yolov8n.pt format=onnx imgsz=640 opset=12. Produces yolov8n.onnx (~12 MB) in the current directory."
)

fail=0

for entry in "${models[@]}"; do
  IFS='|' read -r name expected urlenv hint <<<"$entry"
  target="$dest/$name"

  # Mode 1 — file already present, just verify.
  if [[ -f "$target" ]]; then
    actual=$(sha256sum "$target" | awk '{print $1}')
    if [[ -z "$expected" ]]; then
      echo "✓ $name present (sha256 $actual — expected hash not pinned yet)"
    elif [[ "$actual" == "$expected" ]]; then
      echo "✓ $name present and verified"
    else
      echo "✗ $name sha256 mismatch: got $actual, want $expected" >&2
      fail=1
    fi
    continue
  fi

  # Mode 2 — URL env var set, try downloading.
  url=""
  if [[ -n "$urlenv" ]]; then
    url="${!urlenv-}"
  fi
  if [[ -n "$url" ]]; then
    echo "↓ fetching $name from $url"
    if ! curl -fL --retry 3 --retry-delay 2 -o "$target" "$url"; then
      echo "✗ download failed for $name" >&2
      fail=1
      continue
    fi
    actual=$(sha256sum "$target" | awk '{print $1}')
    echo "  sha256: $actual"
    if [[ -n "$expected" && "$actual" != "$expected" ]]; then
      echo "  ✗ sha256 mismatch: want $expected" >&2
      rm "$target"
      fail=1
      continue
    fi
    echo "✓ $name fetched"
    continue
  fi

  # Mode 3 — nothing to go on. Print a hint.
  echo "✗ $name missing and no URL provided" >&2
  echo "    hint: $hint" >&2
  if [[ -n "$urlenv" ]]; then
    echo "    set \$$urlenv to a direct download URL and re-run, or drop the file in $dest/ manually." >&2
  else
    echo "    drop the file in $dest/ manually with the exact filename '$name'." >&2
  fi
  fail=1
done

if (( fail )); then
  echo ""
  echo "One or more models missing or mismatched. See messages above." >&2
  exit 1
fi
echo ""
echo "All models present and verified under $dest"
