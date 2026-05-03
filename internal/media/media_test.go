package media

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultCatalogPresetDetails(t *testing.T) {
	catalog := DefaultCatalog()
	if !reflect.DeepEqual(catalog.Presets, []string{"small", "balanced", "high"}) {
		t.Fatalf("expected stable preset IDs, got %#v", catalog.Presets)
	}
	if len(catalog.PresetDetails) != len(catalog.Presets) {
		t.Fatalf("expected preset details for every preset, got %#v", catalog.PresetDetails)
	}
	for _, preset := range catalog.Presets {
		detail := presetDetailByID(catalog.PresetDetails, preset)
		if detail.ID == "" {
			t.Fatalf("missing preset detail for %s", preset)
		}
		for _, key := range []string{"video", "gif", "image", "audio"} {
			effect, ok := detail.Effects[key]
			if !ok {
				t.Fatalf("missing %s effect for %s", key, preset)
			}
			if effect.Summary == "" || len(effect.Details) == 0 {
				t.Fatalf("expected useful %s effect for %s, got %#v", key, preset, effect)
			}
		}
	}
}

func TestPresetDetailsMatchApplyPreset(t *testing.T) {
	for _, preset := range []string{"small", "balanced", "high"} {
		detail := presetDetailByID(DefaultPresetDetails(), preset)
		if detail.ID == "" {
			t.Fatalf("missing detail for %s", preset)
		}

		videoApplied := ApplyPreset("mp4", preset, Options{})
		if detail.Effects["video"].Values.MaxHeight != videoApplied.MaxHeight {
			t.Fatalf("%s video detail max height does not match ApplyPreset: %#v vs %#v", preset, detail.Effects["video"].Values, videoApplied)
		}

		gifApplied := ApplyPreset("gif", preset, Options{})
		if detail.Effects["gif"].Values.MaxWidth != gifApplied.MaxWidth || detail.Effects["gif"].Values.Framerate != gifApplied.Framerate {
			t.Fatalf("%s GIF detail values do not match ApplyPreset: %#v vs %#v", preset, detail.Effects["gif"].Values, gifApplied)
		}

		imageApplied := ApplyPreset("jpg", preset, Options{})
		if detail.Effects["image"].Values.Quality != imageApplied.Quality {
			t.Fatalf("%s image detail quality does not match ApplyPreset: %#v vs %#v", preset, detail.Effects["image"].Values, imageApplied)
		}

		audioApplied := ApplyPreset("mp3", preset, Options{})
		if detail.Effects["audio"].Values != (Options{}) || audioApplied != (Options{}) {
			t.Fatalf("%s audio preset should not change options, detail=%#v applied=%#v", preset, detail.Effects["audio"].Values, audioApplied)
		}
	}
}

func presetDetailByID(details []PresetDetail, id string) PresetDetail {
	for _, detail := range details {
		if detail.ID == id {
			return detail
		}
	}
	return PresetDetail{}
}

func TestCodecValidationMatrix(t *testing.T) {
	restore := SetAvailableEncodersForTest(map[string]bool{
		"libsvtav1":  true,
		"librav1e":   true,
		"libx264":    true,
		"libx265":    true,
		"libaom-av1": true,
		"libvpx-vp9": true,
		"libvpx":     true,
		"mpeg4":      true,
		"aac":        true,
		"libmp3lame": true,
		"libopus":    true,
		"libvorbis":  true,
		"flac":       true,
		"pcm_s16le":  true,
	})
	defer restore()

	tests := []struct {
		name    string
		format  string
		opts    Options
		wantErr string
	}{
		{name: "mp4 h264 aac", format: "mp4", opts: Options{VideoCodec: "h264", AudioCodec: "aac"}},
		{name: "mp4 rejects vp9", format: "mp4", opts: Options{VideoCodec: "vp9"}, wantErr: "video codec vp9 is not supported for mp4"},
		{name: "webm vp9 opus", format: "webm", opts: Options{VideoCodec: "vp9", AudioCodec: "opus"}},
		{name: "webm rejects h264", format: "webm", opts: Options{VideoCodec: "h264"}, wantErr: "video codec h264 is not supported for webm"},
		{name: "avi mpeg4 mp3", format: "avi", opts: Options{VideoCodec: "mpeg4", AudioCodec: "mp3"}},
		{name: "avi rejects h265", format: "avi", opts: Options{VideoCodec: "h265"}, wantErr: "video codec h265 is not supported for avi"},
		{name: "mkv av1 flac", format: "mkv", opts: Options{VideoCodec: "av1", AudioCodec: "flac"}},
		{name: "mp4 av1", format: "mp4", opts: Options{VideoCodec: "av1"}},
		{name: "webm av1", format: "webm", opts: Options{VideoCodec: "av1"}},
		{name: "mp4 rejects encoder name", format: "mp4", opts: Options{VideoCodec: "libsvtav1"}, wantErr: "video codec libsvtav1 is not supported for mp4"},
		{name: "mov rejects av1", format: "mov", opts: Options{VideoCodec: "av1"}, wantErr: "video codec av1 is not supported for mov"},
		{name: "avi rejects av1", format: "avi", opts: Options{VideoCodec: "av1"}, wantErr: "video codec av1 is not supported for avi"},
		{name: "gif rejects codec", format: "gif", opts: Options{VideoCodec: "h264"}, wantErr: "codec options are not valid for GIF output"},
		{name: "gif rejects av1", format: "gif", opts: Options{VideoCodec: "av1"}, wantErr: "codec options are not valid for GIF output"},
		{name: "gif rejects video bitrate", format: "gif", opts: Options{VideoBitrate: 2500}, wantErr: "videoBitrate is not valid for GIF output"},
		{name: "jpg rejects codec", format: "jpg", opts: Options{VideoCodec: "h264"}, wantErr: "codec options are not valid for image outputs"},
		{name: "pcm rejects bitrate", format: "mov", opts: Options{AudioCodec: "pcm_s16le", AudioBitrate: 128}, wantErr: "audioBitrate is not valid for pcm_s16le"},
		{name: "unknown codec rejected", format: "mp4", opts: Options{VideoCodec: "not-real"}, wantErr: "video codec not-real is not supported for mp4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate("video", tt.format, "balanced", tt.opts)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("expected valid options, got %v", err)
			}
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
			}
		})
	}
}

func TestLogicalAV1RuntimeResolution(t *testing.T) {
	tests := []struct {
		name     string
		encoders map[string]bool
		want     string
	}{
		{
			name: "prefers svt",
			encoders: map[string]bool{
				"libsvtav1":  true,
				"librav1e":   true,
				"libaom-av1": true,
			},
			want: "libsvtav1",
		},
		{
			name: "falls back to rav1e",
			encoders: map[string]bool{
				"librav1e":   true,
				"libaom-av1": true,
			},
			want: "librav1e",
		},
		{
			name: "falls back to aom",
			encoders: map[string]bool{
				"libaom-av1": true,
			},
			want: "libaom-av1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := SetAvailableEncodersForTest(tt.encoders)
			defer restore()

			choice := ResolveVideoCodec("mp4", Options{VideoCodec: "av1"})
			got, ok := ResolveRuntimeCodecEncoder(context.Background(), choice)
			if !ok || got != tt.want {
				t.Fatalf("expected AV1 to resolve to %s, got %q ok=%v", tt.want, got, ok)
			}

			catalog := RuntimeCatalog(context.Background())
			var av1 CodecChoice
			for _, format := range catalog.Formats {
				if format.ID != "mp4" {
					continue
				}
				for _, codec := range format.VideoCodecs {
					if codec.ID == "av1" {
						av1 = codec
					}
				}
			}
			if av1.ID != "av1" || av1.Encoder != tt.want {
				t.Fatalf("expected catalog AV1 encoder %s, got %#v", tt.want, av1)
			}
		})
	}
}

func TestLogicalAV1HiddenWhenUnavailable(t *testing.T) {
	restore := SetAvailableEncodersForTest(map[string]bool{
		"libx264": true,
		"aac":     true,
	})
	defer restore()

	err := Validate("video", "mp4", "balanced", Options{VideoCodec: "av1"})
	if err == nil || !strings.Contains(err.Error(), "video codec av1 is not available on this server") {
		t.Fatalf("expected unavailable av1 error, got %v", err)
	}

	catalog := RuntimeCatalog(context.Background())
	for _, format := range catalog.Formats {
		if format.ID != "mp4" {
			continue
		}
		for _, codec := range format.VideoCodecs {
			if codec.ID == "av1" {
				t.Fatalf("expected AV1 to be hidden when no AV1 encoder is available: %#v", format.VideoCodecs)
			}
		}
	}
}

func TestUnavailableCodecValidationAndRuntimeCatalog(t *testing.T) {
	restore := SetAvailableEncodersForTest(map[string]bool{
		"libx264": true,
		"aac":     true,
	})
	defer restore()

	err := Validate("video", "mp4", "balanced", Options{VideoCodec: "h265"})
	if err == nil || !strings.Contains(err.Error(), "video codec h265 is not available on this server") {
		t.Fatalf("expected unavailable h265 error, got %v", err)
	}

	catalog := RuntimeCatalog(context.Background())
	var mp4 Format
	var gif Format
	for _, format := range catalog.Formats {
		if format.ID == "mp4" {
			mp4 = format
		}
		if format.ID == "gif" {
			gif = format
		}
	}
	if len(mp4.VideoCodecs) != 1 || mp4.VideoCodecs[0].ID != "h264" {
		t.Fatalf("expected only h264 in mp4 video codecs, got %#v", mp4.VideoCodecs)
	}
	if len(mp4.AudioCodecs) != 1 || mp4.AudioCodecs[0].ID != "aac" {
		t.Fatalf("expected only aac in mp4 audio codecs, got %#v", mp4.AudioCodecs)
	}
	if len(gif.VideoCodecs) != 0 || len(gif.AudioCodecs) != 0 {
		t.Fatalf("expected no GIF codec choices, got %#v %#v", gif.VideoCodecs, gif.AudioCodecs)
	}
}
