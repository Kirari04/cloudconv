package jobs

import (
	"context"
	"testing"

	"github.com/kirari04/cloudconv/internal/media"
	"github.com/kirari04/cloudconv/internal/store"
)

func TestVideoCodecCommandArgs(t *testing.T) {
	restore := media.SetAvailableEncodersForTest(map[string]bool{
		"libx264":    true,
		"libx265":    true,
		"libvpx-vp9": true,
		"libvpx":     true,
		"mpeg4":      true,
		"aac":        true,
		"libmp3lame": true,
		"libopus":    true,
		"libsvtav1":  true,
		"librav1e":   true,
		"libaom-av1": true,
		"pcm_s16le":  true,
		"flac":       true,
		"libvorbis":  true,
	})
	defer restore()

	tests := []struct {
		name        string
		format      string
		opts        media.Options
		want        []string
		doesNotWant []string
	}{
		{name: "mp4 auto", format: "mp4", want: []string{"-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart"}},
		{name: "mp4 h265", format: "mp4", opts: media.Options{VideoCodec: "h265"}, want: []string{"-c:v", "libx265", "-tag:v", "hvc1"}},
		{name: "webm vp8", format: "webm", opts: media.Options{VideoCodec: "vp8"}, want: []string{"-c:v", "libvpx"}},
		{name: "webm vp9", format: "webm", opts: media.Options{VideoCodec: "vp9"}, want: []string{"-c:v", "libvpx-vp9"}},
		{name: "avi auto", format: "avi", want: []string{"-c:v", "mpeg4", "-c:a", "libmp3lame"}},
		{name: "mkv opus", format: "mkv", opts: media.Options{AudioCodec: "opus"}, want: []string{"-c:a", "libopus"}},
		{name: "gif skips codec args", format: "gif", want: []string{"-loop"}, doesNotWant: []string{"-c:v", "-c:a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &store.Job{ID: "job-id", TargetFormat: tt.format, Preset: "balanced"}
			upload := &store.Upload{OriginalFilename: "input.mp4"}
			cmd := buildFFmpegCommand(context.Background(), job, upload, tt.opts, "in.mp4", "out."+tt.format, "progress.sock")
			for _, value := range tt.want {
				if !containsArg(cmd.Args, value) {
					t.Fatalf("expected args to contain %q: %#v", value, cmd.Args)
				}
			}
			for _, value := range tt.doesNotWant {
				if containsArg(cmd.Args, value) {
					t.Fatalf("expected args not to contain %q: %#v", value, cmd.Args)
				}
			}
		})
	}
}

func TestAV1CommandArgs(t *testing.T) {
	tests := []struct {
		name        string
		format      string
		preset      string
		opts        media.Options
		encoders    map[string]bool
		want        []string
		doesNotWant []string
	}{
		{
			name:   "mp4 svt balanced",
			format: "mp4",
			preset: "balanced",
			opts: media.Options{
				VideoCodec:            "av1",
				AudioCodec:            "aac",
				EffectiveVideoEncoder: "libsvtav1",
				EffectiveAudioEncoder: "aac",
			},
			want: []string{"-c:v", "libsvtav1", "-preset", "8", "-crf", "34", "-pix_fmt", "yuv420p", "-movflags", "+faststart"},
		},
		{
			name:   "webm rav1e small",
			format: "webm",
			preset: "small",
			opts: media.Options{
				VideoCodec:            "av1",
				AudioCodec:            "opus",
				EffectiveVideoEncoder: "librav1e",
				EffectiveAudioEncoder: "libopus",
			},
			want: []string{"-c:v", "librav1e", "-speed", "10", "-qp", "140"},
		},
		{
			name:   "mkv aom high",
			format: "mkv",
			preset: "high",
			opts: media.Options{
				VideoCodec:            "av1",
				AudioCodec:            "aac",
				EffectiveVideoEncoder: "libaom-av1",
				EffectiveAudioEncoder: "aac",
			},
			want: []string{"-c:v", "libaom-av1", "-cpu-used", "4", "-row-mt", "1", "-threads", "0", "-crf", "32"},
		},
		{
			name:   "bitrate omits quality flags",
			format: "mp4",
			preset: "balanced",
			opts: media.Options{
				VideoCodec:            "av1",
				AudioCodec:            "aac",
				VideoBitrate:          2500,
				EffectiveVideoEncoder: "libsvtav1",
				EffectiveAudioEncoder: "aac",
			},
			want:        []string{"-c:v", "libsvtav1", "-preset", "8", "-b:v", "2500k"},
			doesNotWant: []string{"-crf", "-qp"},
		},
		{
			name:   "old queued job falls back to svt",
			format: "mp4",
			preset: "balanced",
			opts: media.Options{
				VideoCodec: "av1",
				AudioCodec: "aac",
			},
			encoders: map[string]bool{
				"libsvtav1": true,
				"aac":       true,
			},
			want: []string{"-c:v", "libsvtav1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var restore func()
			if tt.encoders != nil {
				restore = media.SetAvailableEncodersForTest(tt.encoders)
			} else {
				restore = media.SetAvailableEncodersForTest(map[string]bool{
					"libsvtav1":  true,
					"librav1e":   true,
					"libaom-av1": true,
					"aac":        true,
					"libopus":    true,
				})
			}
			defer restore()

			job := &store.Job{ID: "job-id", TargetFormat: tt.format, Preset: tt.preset}
			upload := &store.Upload{OriginalFilename: "input.mp4"}
			cmd := buildFFmpegCommand(context.Background(), job, upload, tt.opts, "in.mp4", "out."+tt.format, "progress.sock")
			for _, value := range tt.want {
				if !containsArg(cmd.Args, value) {
					t.Fatalf("expected args to contain %q: %#v", value, cmd.Args)
				}
			}
			for _, value := range tt.doesNotWant {
				if containsArg(cmd.Args, value) {
					t.Fatalf("expected args not to contain %q: %#v", value, cmd.Args)
				}
			}
		})
	}
}

func containsArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}
