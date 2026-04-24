// Package protocol defines the HTTP contract between the Kestrel core
// binary and the optional kestrel-vision sidecar. Both sides import
// this package so the request/response shapes cannot drift: the core
// client marshals these types, the sidecar server unmarshals into the
// same types.
//
// The contract is deliberately small — one health endpoint and one
// detection endpoint — so the sidecar stays a pure function over a
// single image. State (clusters, person names, tags) is all owned by
// core.
package protocol

// EmbeddingDim is the length of the face-identity embedding returned
// by the sidecar. ArcFace-family models produce 512-d float vectors;
// pinning it here lets the core client allocate precisely and tests
// validate shape without reaching into sidecar internals.
const EmbeddingDim = 512

// PathHealthz is the sidecar endpoint that reports readiness and
// loaded-model identity. Core calls it at startup and on a low-rate
// poll to drive the status badge in the UI.
const PathHealthz = "/healthz"

// PathDetect is the sidecar endpoint that consumes one image and
// returns the face and object detections for it.
const PathDetect = "/detect"

// Health is the /healthz response. Version pins the sidecar build so
// core can reject a skewed sidecar; Models lists the identifiers of
// the loaded ONNX models (e.g. "arcface-r100", "yolov8n") so the UI
// can surface what actually ran.
type Health struct {
	Version string   `json:"version"`
	Models  []string `json:"models"`
}

// BBox is an axis-aligned bounding box in pixel coordinates of the
// posted image. X and Y are the top-left corner; W and H are width
// and height. All four are non-negative.
type BBox struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Face is one face detection plus its identity embedding. The
// embedding is an L2-normalised float vector so consumers can use
// raw dot-product as cosine similarity.
type Face struct {
	BBox       BBox      `json:"bbox"`
	Confidence float32   `json:"confidence"`
	Embedding  []float32 `json:"embedding"`
}

// Object is one object-class detection from the general-purpose
// detector. Label is the canonical class name (lowercase, dash-safe)
// so it flows through autotag.formatTag without surprises.
type Object struct {
	Label      string  `json:"label"`
	Confidence float32 `json:"confidence"`
	BBox       BBox    `json:"bbox"`
}

// DetectResponse is what the sidecar returns for POST /detect. Both
// slices may be empty — a photo with no faces and no recognised
// objects is a normal, successful response.
type DetectResponse struct {
	Faces   []Face   `json:"faces"`
	Objects []Object `json:"objects"`
}
