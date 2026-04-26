package autotag

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/WeaponizedLego/kestrel/internal/metadata"
)

func TestDeriveAudio_Buckets(t *testing.T) {
	mtime := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		path string
		am   metadata.AudioMeta
		want []string
	}{
		{
			name: "full metadata",
			path: "/m/track.mp3",
			am:   metadata.AudioMeta{Codec: "mp3", DurationSec: 180, BitrateKbps: 192, Channels: 2},
			want: []string{"bitrate:mid", "channels:stereo", "codec:mp3", "duration:short", "kind:audio", "year:2024"},
		},
		{
			name: "long flac mono",
			path: "/m/lecture.flac",
			am:   metadata.AudioMeta{Codec: "flac", DurationSec: 60 * 60, BitrateKbps: 800, Channels: 1},
			want: []string{"bitrate:high", "channels:mono", "codec:flac", "duration:long", "kind:audio", "year:2024"},
		},
		{
			name: "missing ffprobe falls back to extension",
			path: "/m/voice.opus",
			am:   metadata.AudioMeta{},
			want: []string{"codec:opus", "kind:audio", "year:2024"},
		},
		{
			name: "low bitrate medium duration",
			path: "/m/podcast.mp3",
			am:   metadata.AudioMeta{Codec: "mp3", DurationSec: 10 * 60, BitrateKbps: 64, Channels: 2},
			want: []string{"bitrate:low", "channels:stereo", "codec:mp3", "duration:medium", "kind:audio", "year:2024"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveAudio(tc.path, mtime, tc.am)
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestKindTagRecognisesAudio(t *testing.T) {
	if got := kindTag("/m/song.mp3"); got != "kind:audio" {
		t.Fatalf("kindTag(.mp3) = %q, want kind:audio", got)
	}
	if got := kindTag("/v/clip.mp4"); got != "kind:video" {
		t.Fatalf("kindTag(.mp4) = %q, want kind:video", got)
	}
	if got := kindTag("/p/pic.jpg"); got != "kind:photo" {
		t.Fatalf("kindTag(.jpg) = %q, want kind:photo", got)
	}
}
