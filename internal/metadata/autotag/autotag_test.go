package autotag

import (
	"reflect"
	"testing"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/metadata"
)

type stubGeo struct {
	place, country string
	ok             bool
}

func (s stubGeo) Lookup(lat, lon float64) (string, string, bool) {
	return s.place, s.country, s.ok
}

func TestDerive(t *testing.T) {
	taken := time.Date(2024, 6, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		path string
		meta metadata.Metadata
		opts Options
		want []string
	}{
		{
			name: "minimal photo — just kind tag",
			path: "/pictures/IMG_1234.jpg",
			want: []string{"kind:photo"},
		},
		{
			name: "video file classified by extension",
			path: "/pictures/clip.mp4",
			want: []string{"kind:video"},
		},
		{
			name: "full EXIF (no GPS, no folder)",
			path: "/pictures/IMG_1234.jpg",
			meta: metadata.Metadata{
				Width: 4000, Height: 6000,
				TakenAt:     taken,
				CameraMake:  "Canon",
				CameraModel: "EOS R5",
				LensModel:   "RF 24-70mm F2.8",
				ISO:         3200,
				Orientation: 6,
				FlashFired:  true,
			},
			want: []string{
				"camera:canon",
				"camera:canon-eos-r5",
				"flash:on",
				"iso:high",
				"kind:photo",
				"lens:rf-24-70mm-f2.8",
				"month:2024-06",
				"orientation:portrait",
				"year:2024",
			},
		},
		{
			name: "folder tag opt-in emits parent dir",
			path: "/pictures/italy-2024/IMG.jpg",
			opts: Options{FolderTags: true},
			want: []string{"folder:italy-2024", "kind:photo"},
		},
		{
			name: "GPS + geo lookup yields place/country",
			path: "/pictures/IMG.jpg",
			meta: metadata.Metadata{GPSLat: 41.9, GPSLon: 12.5, GPSValid: true},
			opts: Options{Geo: stubGeo{place: "Rome", country: "IT", ok: true}},
			want: []string{"country:it", "kind:photo", "place:rome"},
		},
		{
			name: "geo lookup miss emits no place tag",
			path: "/pictures/IMG.jpg",
			meta: metadata.Metadata{GPSLat: 41.9, GPSLon: 12.5, GPSValid: true},
			opts: Options{Geo: stubGeo{ok: false}},
			want: []string{"kind:photo"},
		},
		{
			name: "landscape orientation inferred from dims when EXIF absent",
			path: "/pictures/IMG.jpg",
			meta: metadata.Metadata{Width: 6000, Height: 4000},
			want: []string{"kind:photo", "orientation:landscape"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Derive(tc.path, tc.meta, tc.opts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Derive tags mismatch\n got:  %v\n want: %v", got, tc.want)
			}
		})
	}
}

func TestFormatTag_CollapsesSeparators(t *testing.T) {
	got := formatTag("camera", "Canon  EOS_R5 / Mk IV")
	want := "camera:canon-eos-r5-mk-iv"
	if got != want {
		t.Fatalf("formatTag got %q, want %q", got, want)
	}
}
