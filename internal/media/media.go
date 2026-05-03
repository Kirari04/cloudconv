package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Format struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	MediaType string   `json:"mediaType"`
	Inputs    []string `json:"inputs"`
}

type Catalog struct {
	Formats []Format `json:"formats"`
	Presets []string `json:"presets"`
}

type Options struct {
	MaxHeight    int  `json:"maxHeight,omitempty"`
	MaxWidth     int  `json:"maxWidth,omitempty"`
	VideoBitrate int  `json:"videoBitrate,omitempty"`
	AudioBitrate int  `json:"audioBitrate,omitempty"`
	Framerate    int  `json:"framerate,omitempty"`
	Quality      int  `json:"quality,omitempty"`
	Loop         bool `json:"loop,omitempty"`
}

type ProbeInfo struct {
	MediaType    string `json:"mediaType"`
	DetectedMIME string `json:"detectedMime"`
	HasVideo     bool   `json:"hasVideo"`
	HasAudio     bool   `json:"hasAudio"`
	Duration     string `json:"duration,omitempty"`
}

func DefaultCatalog() Catalog {
	return Catalog{
		Presets: []string{"small", "balanced", "high"},
		Formats: []Format{
			{ID: "mp4", Label: "MP4", MediaType: "video", Inputs: []string{"video"}},
			{ID: "webm", Label: "WebM", MediaType: "video", Inputs: []string{"video"}},
			{ID: "mov", Label: "MOV", MediaType: "video", Inputs: []string{"video"}},
			{ID: "avi", Label: "AVI", MediaType: "video", Inputs: []string{"video"}},
			{ID: "mkv", Label: "MKV", MediaType: "video", Inputs: []string{"video"}},
			{ID: "gif", Label: "GIF", MediaType: "video", Inputs: []string{"video", "image"}},
			{ID: "mp3", Label: "MP3", MediaType: "audio", Inputs: []string{"video", "audio"}},
			{ID: "wav", Label: "WAV", MediaType: "audio", Inputs: []string{"video", "audio"}},
			{ID: "ogg", Label: "OGG", MediaType: "audio", Inputs: []string{"video", "audio"}},
			{ID: "flac", Label: "FLAC", MediaType: "audio", Inputs: []string{"video", "audio"}},
			{ID: "jpg", Label: "JPG", MediaType: "image", Inputs: []string{"image", "video"}},
			{ID: "jpeg", Label: "JPEG", MediaType: "image", Inputs: []string{"image", "video"}},
			{ID: "png", Label: "PNG", MediaType: "image", Inputs: []string{"image", "video"}},
			{ID: "webp", Label: "WebP", MediaType: "image", Inputs: []string{"image", "video"}},
			{ID: "bmp", Label: "BMP", MediaType: "image", Inputs: []string{"image"}},
			{ID: "tiff", Label: "TIFF", MediaType: "image", Inputs: []string{"image"}},
		},
	}
}

func Probe(ctx context.Context, path string) (ProbeInfo, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type:format=format_name,duration", "-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		return ProbeInfo{}, err
	}
	var payload struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return ProbeInfo{}, err
	}
	info := ProbeInfo{
		DetectedMIME: payload.Format.FormatName,
		Duration:     payload.Format.Duration,
	}
	for _, stream := range payload.Streams {
		switch stream.CodecType {
		case "video":
			info.HasVideo = true
		case "audio":
			info.HasAudio = true
		}
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch {
	case isImageExt(ext) && ext != "gif":
		info.MediaType = "image"
	case info.HasVideo:
		info.MediaType = "video"
	case info.HasAudio:
		info.MediaType = "audio"
	default:
		return info, errors.New("could not detect supported media stream")
	}
	return info, nil
}

func Validate(sourceType, targetFormat, preset string, opts Options) error {
	targetFormat = strings.ToLower(targetFormat)
	if preset == "" {
		preset = "balanced"
	}
	if preset != "small" && preset != "balanced" && preset != "high" {
		return fmt.Errorf("invalid preset: %s", preset)
	}
	var selected *Format
	catalog := DefaultCatalog()
	for _, f := range catalog.Formats {
		if f.ID == targetFormat {
			copy := f
			selected = &copy
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("invalid target format: %s", targetFormat)
	}
	allowed := false
	for _, input := range selected.Inputs {
		if input == sourceType {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("cannot convert %s to %s", sourceType, targetFormat)
	}
	if opts.MaxHeight != 0 && !isOneOf(opts.MaxHeight, 240, 360, 480, 720, 1080, 1440, 2160) {
		return errors.New("maxHeight must be one of 240, 360, 480, 720, 1080, 1440, 2160")
	}
	if opts.MaxWidth != 0 && (opts.MaxWidth < 16 || opts.MaxWidth > 10000) {
		return errors.New("maxWidth must be between 16 and 10000")
	}
	if opts.VideoBitrate != 0 && (opts.VideoBitrate < 100 || opts.VideoBitrate > 100000) {
		return errors.New("videoBitrate must be between 100 and 100000")
	}
	if opts.AudioBitrate != 0 && (opts.AudioBitrate < 32 || opts.AudioBitrate > 320) {
		return errors.New("audioBitrate must be between 32 and 320")
	}
	if opts.Framerate != 0 && (opts.Framerate < 1 || opts.Framerate > 120) {
		return errors.New("framerate must be between 1 and 120")
	}
	if opts.Quality != 0 && (opts.Quality < 1 || opts.Quality > 100) {
		return errors.New("quality must be between 1 and 100")
	}
	if selected.MediaType == "image" {
		if opts.VideoBitrate != 0 || opts.AudioBitrate != 0 || opts.Framerate != 0 {
			return errors.New("video, audio bitrate, and framerate options are not valid for image outputs")
		}
	}
	if selected.MediaType == "audio" {
		if opts.MaxHeight != 0 || opts.MaxWidth != 0 || opts.VideoBitrate != 0 || opts.Framerate != 0 || opts.Quality != 0 {
			return errors.New("visual options are not valid for audio outputs")
		}
	}
	if targetFormat == "gif" && opts.AudioBitrate != 0 {
		return errors.New("audioBitrate is not valid for GIF output")
	}
	return nil
}

func ApplyPreset(targetFormat, preset string, opts Options) Options {
	if preset == "" {
		preset = "balanced"
	}
	switch targetFormat {
	case "gif":
		if opts.MaxWidth == 0 {
			switch preset {
			case "small":
				opts.MaxWidth = 320
			case "high":
				opts.MaxWidth = 720
			default:
				opts.MaxWidth = 480
			}
		}
		if opts.Framerate == 0 {
			if preset == "high" {
				opts.Framerate = 20
			} else {
				opts.Framerate = 15
			}
		}
	default:
		if outputKind(targetFormat) == "video" && opts.MaxHeight == 0 {
			switch preset {
			case "small":
				opts.MaxHeight = 480
			case "high":
				opts.MaxHeight = 1080
			default:
				opts.MaxHeight = 720
			}
		}
	}
	if outputKind(targetFormat) == "image" && opts.Quality == 0 {
		if preset == "small" {
			opts.Quality = 72
		} else if preset == "high" {
			opts.Quality = 95
		} else {
			opts.Quality = 86
		}
	}
	return opts
}

func OutputKind(format string) string {
	return outputKind(format)
}

func outputKind(format string) string {
	switch format {
	case "mp4", "webm", "mov", "avi", "mkv", "gif":
		return "video"
	case "mp3", "wav", "ogg", "flac":
		return "audio"
	case "jpg", "jpeg", "png", "webp", "bmp", "tiff":
		return "image"
	default:
		return ""
	}
}

func EncodeOptions(opts Options) string {
	data, _ := json.Marshal(opts)
	return string(data)
}

func DecodeOptions(raw string) Options {
	var opts Options
	_ = json.Unmarshal([]byte(raw), &opts)
	return opts
}

func ExtensionFor(format string) string {
	if format == "jpeg" {
		return "jpg"
	}
	return format
}

func LegacyOptions(values map[string]string) (format, preset string, opts Options, err error) {
	format = strings.ToLower(values["format"])
	preset = values["preset"]
	if preset == "" {
		preset = "balanced"
	}
	if v := values["resolution"]; v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil {
			err = errors.New("resolution must be a number")
			return
		}
		if format == "gif" || outputKind(format) == "image" {
			opts.MaxWidth = n
		} else {
			opts.MaxHeight = n
		}
	}
	if v := values["bitrate"]; v != "" {
		opts.VideoBitrate, err = strconv.Atoi(v)
		if err != nil {
			err = errors.New("bitrate must be a number")
			return
		}
	}
	if v := values["audioBitrate"]; v != "" {
		opts.AudioBitrate, err = strconv.Atoi(v)
		if err != nil {
			err = errors.New("audio bitrate must be a number")
			return
		}
	}
	if v := values["framerate"]; v != "" {
		opts.Framerate, err = strconv.Atoi(v)
		if err != nil {
			err = errors.New("framerate must be a number")
			return
		}
	}
	if v := values["gifLoop"]; v != "" {
		if v != "true" && v != "false" {
			err = errors.New("gifLoop value must be 'true' or 'false'")
			return
		}
		opts.Loop = v == "true"
	} else {
		opts.Loop = true
	}
	return
}

func isImageExt(ext string) bool {
	switch ext {
	case "jpg", "jpeg", "png", "webp", "bmp", "tiff":
		return true
	default:
		return false
	}
}

func isOneOf(value int, values ...int) bool {
	for _, v := range values {
		if value == v {
			return true
		}
	}
	return false
}
