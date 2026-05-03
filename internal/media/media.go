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
	"sync"
)

type Format struct {
	ID          string        `json:"id"`
	Label       string        `json:"label"`
	MediaType   string        `json:"mediaType"`
	Inputs      []string      `json:"inputs"`
	VideoCodecs []CodecChoice `json:"videoCodecs,omitempty"`
	AudioCodecs []CodecChoice `json:"audioCodecs,omitempty"`
}

type Catalog struct {
	Formats       []Format       `json:"formats"`
	Presets       []string       `json:"presets"`
	PresetDetails []PresetDetail `json:"presetDetails"`
}

type PresetDetail struct {
	ID      string                  `json:"id"`
	Label   string                  `json:"label"`
	Summary string                  `json:"summary"`
	Effects map[string]PresetEffect `json:"effects"`
}

type PresetEffect struct {
	Summary string             `json:"summary"`
	Details []PresetDetailItem `json:"details"`
	Values  Options            `json:"values"`
}

type PresetDetailItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Options struct {
	MaxHeight             int    `json:"maxHeight,omitempty"`
	MaxWidth              int    `json:"maxWidth,omitempty"`
	VideoBitrate          int    `json:"videoBitrate,omitempty"`
	AudioBitrate          int    `json:"audioBitrate,omitempty"`
	Framerate             int    `json:"framerate,omitempty"`
	Quality               int    `json:"quality,omitempty"`
	Loop                  bool   `json:"loop,omitempty"`
	VideoCodec            string `json:"videoCodec,omitempty"`
	AudioCodec            string `json:"audioCodec,omitempty"`
	EffectiveVideoEncoder string `json:"effectiveVideoEncoder,omitempty"`
	EffectiveAudioEncoder string `json:"effectiveAudioEncoder,omitempty"`
}

type CodecChoice struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Encoder         string   `json:"encoder"`
	EncoderChoices  []string `json:"-"`
	Available       bool     `json:"available"`
	Recommended     bool     `json:"recommended,omitempty"`
	SupportsBitrate bool     `json:"supportsBitrate,omitempty"`
}

type ProbeInfo struct {
	MediaType    string `json:"mediaType"`
	DetectedMIME string `json:"detectedMime"`
	HasVideo     bool   `json:"hasVideo"`
	HasAudio     bool   `json:"hasAudio"`
	Duration     string `json:"duration,omitempty"`
}

var (
	encoderMu       sync.Mutex
	encoderCache    map[string]bool
	encoderOverride map[string]bool
)

var videoCodecMatrix = map[string][]CodecChoice{
	"mp4": {
		{ID: "h264", Label: "H.264", Encoder: "libx264", Recommended: true},
		{ID: "h265", Label: "H.265 / HEVC", Encoder: "libx265"},
		{ID: "av1", Label: "AV1", EncoderChoices: []string{"libsvtav1", "librav1e", "libaom-av1"}},
	},
	"webm": {
		{ID: "vp9", Label: "VP9", Encoder: "libvpx-vp9", Recommended: true},
		{ID: "vp8", Label: "VP8", Encoder: "libvpx"},
		{ID: "av1", Label: "AV1", EncoderChoices: []string{"libsvtav1", "librav1e", "libaom-av1"}},
	},
	"mov": {
		{ID: "h264", Label: "H.264", Encoder: "libx264", Recommended: true},
		{ID: "h265", Label: "H.265 / HEVC", Encoder: "libx265"},
	},
	"avi": {
		{ID: "mpeg4", Label: "MPEG-4", Encoder: "mpeg4", Recommended: true},
	},
	"mkv": {
		{ID: "h264", Label: "H.264", Encoder: "libx264", Recommended: true},
		{ID: "h265", Label: "H.265 / HEVC", Encoder: "libx265"},
		{ID: "vp9", Label: "VP9", Encoder: "libvpx-vp9"},
		{ID: "vp8", Label: "VP8", Encoder: "libvpx"},
		{ID: "av1", Label: "AV1", EncoderChoices: []string{"libsvtav1", "librav1e", "libaom-av1"}},
		{ID: "mpeg4", Label: "MPEG-4", Encoder: "mpeg4"},
	},
}

var audioCodecMatrix = map[string][]CodecChoice{
	"mp4": {
		{ID: "aac", Label: "AAC", Encoder: "aac", Recommended: true, SupportsBitrate: true},
		{ID: "mp3", Label: "MP3", Encoder: "libmp3lame", SupportsBitrate: true},
	},
	"webm": {
		{ID: "opus", Label: "Opus", Encoder: "libopus", Recommended: true, SupportsBitrate: true},
		{ID: "vorbis", Label: "Vorbis", Encoder: "libvorbis", SupportsBitrate: true},
	},
	"mov": {
		{ID: "aac", Label: "AAC", Encoder: "aac", Recommended: true, SupportsBitrate: true},
		{ID: "pcm_s16le", Label: "PCM 16-bit", Encoder: "pcm_s16le"},
	},
	"avi": {
		{ID: "mp3", Label: "MP3", Encoder: "libmp3lame", Recommended: true, SupportsBitrate: true},
		{ID: "pcm_s16le", Label: "PCM 16-bit", Encoder: "pcm_s16le"},
	},
	"mkv": {
		{ID: "aac", Label: "AAC", Encoder: "aac", Recommended: true, SupportsBitrate: true},
		{ID: "mp3", Label: "MP3", Encoder: "libmp3lame", SupportsBitrate: true},
		{ID: "opus", Label: "Opus", Encoder: "libopus", SupportsBitrate: true},
		{ID: "vorbis", Label: "Vorbis", Encoder: "libvorbis", SupportsBitrate: true},
		{ID: "flac", Label: "FLAC", Encoder: "flac"},
		{ID: "pcm_s16le", Label: "PCM 16-bit", Encoder: "pcm_s16le"},
	},
}

var defaultVideoCodecs = map[string]string{
	"mp4":  "h264",
	"webm": "vp9",
	"mov":  "h264",
	"avi":  "mpeg4",
	"mkv":  "h264",
}

var defaultAudioCodecs = map[string]string{
	"mp4":  "aac",
	"webm": "opus",
	"mov":  "aac",
	"avi":  "mp3",
	"mkv":  "aac",
}

var presetIDs = []string{"small", "balanced", "high"}

var presetLabels = map[string]string{
	"small":    "Small",
	"balanced": "Balanced",
	"high":     "High",
}

var presetSummaries = map[string]string{
	"small":    "Smaller output with lower visual detail.",
	"balanced": "Good default quality with moderate output size.",
	"high":     "Higher quality output with larger files.",
}

var videoPresetMaxHeights = map[string]int{
	"small":    480,
	"balanced": 720,
	"high":     1080,
}

var gifPresetWidths = map[string]int{
	"small":    320,
	"balanced": 480,
	"high":     720,
}

var gifPresetFramerates = map[string]int{
	"small":    15,
	"balanced": 15,
	"high":     20,
}

var imagePresetQualities = map[string]int{
	"small":    72,
	"balanced": 86,
	"high":     95,
}

func DefaultCatalog() Catalog {
	return Catalog{
		Presets:       cloneStrings(presetIDs),
		PresetDetails: DefaultPresetDetails(),
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

func DefaultPresetDetails() []PresetDetail {
	out := make([]PresetDetail, 0, len(presetIDs))
	for _, preset := range presetIDs {
		videoHeight := presetInt(videoPresetMaxHeights, preset)
		gifWidth := presetInt(gifPresetWidths, preset)
		gifFPS := presetInt(gifPresetFramerates, preset)
		imageQuality := presetInt(imagePresetQualities, preset)
		out = append(out, PresetDetail{
			ID:      preset,
			Label:   presetLabels[preset],
			Summary: presetSummaries[preset],
			Effects: map[string]PresetEffect{
				"video": {
					Summary: fmt.Sprintf("Limits video height to %dp unless overridden.", videoHeight),
					Details: []PresetDetailItem{
						{Label: "Max height", Value: fmt.Sprintf("%dp", videoHeight)},
						{Label: "Frame rate", Value: "Original unless changed"},
						{Label: "Bitrate", Value: "Auto unless changed"},
					},
					Values: Options{MaxHeight: videoHeight},
				},
				"gif": {
					Summary: fmt.Sprintf("Creates a %dpx wide GIF at %d FPS.", gifWidth, gifFPS),
					Details: []PresetDetailItem{
						{Label: "Width", Value: fmt.Sprintf("%dpx", gifWidth)},
						{Label: "Frame rate", Value: fmt.Sprintf("%d FPS", gifFPS)},
						{Label: "Loop", Value: "On by default"},
					},
					Values: Options{MaxWidth: gifWidth, Framerate: gifFPS, Loop: true},
				},
				"image": {
					Summary: fmt.Sprintf("Uses %d%% image quality unless overridden.", imageQuality),
					Details: []PresetDetailItem{
						{Label: "Quality", Value: fmt.Sprintf("%d%%", imageQuality)},
						{Label: "Max width", Value: "Original unless changed"},
					},
					Values: Options{Quality: imageQuality},
				},
				"audio": {
					Summary: "Does not change audio settings by itself.",
					Details: []PresetDetailItem{
						{Label: "Preset effect", Value: "No audio changes"},
						{Label: "Audio bitrate", Value: "Auto unless changed"},
					},
					Values: Options{},
				},
			},
		})
	}
	return out
}

func RuntimeCatalog(ctx context.Context) Catalog {
	catalog := DefaultCatalog()
	encoders := runtimeEncoderMap(ctx)
	for i := range catalog.Formats {
		format := catalog.Formats[i].ID
		if format == "gif" {
			continue
		}
		catalog.Formats[i].VideoCodecs = availableCodecChoices(videoCodecMatrix[format], encoders)
		catalog.Formats[i].AudioCodecs = availableCodecChoices(audioCodecMatrix[format], encoders)
	}
	return catalog
}

func AvailableEncoders(ctx context.Context) (map[string]bool, error) {
	encoderMu.Lock()
	if encoderOverride != nil {
		out := cloneEncoderMap(encoderOverride)
		encoderMu.Unlock()
		return out, nil
	}
	if encoderCache != nil {
		out := cloneEncoderMap(encoderCache)
		encoderMu.Unlock()
		return out, nil
	}
	encoderMu.Unlock()

	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-encoders")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	encoders := parseEncoders(string(out))
	encoderMu.Lock()
	encoderCache = cloneEncoderMap(encoders)
	encoderMu.Unlock()
	return encoders, nil
}

func SetAvailableEncodersForTest(encoders map[string]bool) func() {
	encoderMu.Lock()
	previousOverride := encoderOverride
	previousCache := encoderCache
	encoderOverride = cloneEncoderMap(encoders)
	encoderCache = nil
	encoderMu.Unlock()
	return func() {
		encoderMu.Lock()
		encoderOverride = previousOverride
		encoderCache = previousCache
		encoderMu.Unlock()
	}
}

func ResolveVideoCodec(format string, opts Options) CodecChoice {
	return resolveCodec(videoCodecMatrix[format], defaultVideoCodecs[format], opts.VideoCodec)
}

func ResolveAudioCodec(format string, opts Options) CodecChoice {
	return resolveCodec(audioCodecMatrix[format], defaultAudioCodecs[format], opts.AudioCodec)
}

func ResolveCodecEncoder(choice CodecChoice, available map[string]bool) (string, bool) {
	for _, encoder := range choice.EncoderChoices {
		if available[encoder] {
			return encoder, true
		}
	}
	if choice.Encoder != "" && available[choice.Encoder] {
		return choice.Encoder, true
	}
	return "", false
}

func ResolveRuntimeCodecEncoder(ctx context.Context, choice CodecChoice) (string, bool) {
	return ResolveCodecEncoder(choice, runtimeEncoderMap(ctx))
}

func ResolveEffectiveCodecs(ctx context.Context, targetFormat string, opts Options) (Options, error) {
	targetFormat = strings.ToLower(strings.TrimSpace(targetFormat))
	opts.VideoCodec = normalizeCodecID(opts.VideoCodec)
	opts.AudioCodec = normalizeCodecID(opts.AudioCodec)
	opts.EffectiveVideoEncoder = ""
	opts.EffectiveAudioEncoder = ""
	if outputKind(targetFormat) != "video" || targetFormat == "gif" {
		return opts, nil
	}
	encoders := runtimeEncoderMap(ctx)
	videoChoice := ResolveVideoCodec(targetFormat, opts)
	if videoChoice.ID != "" {
		encoder, ok := ResolveCodecEncoder(videoChoice, encoders)
		if !ok {
			return opts, fmt.Errorf("video codec %s is not available on this server", videoChoice.ID)
		}
		opts.EffectiveVideoEncoder = encoder
	}
	audioChoice := ResolveAudioCodec(targetFormat, opts)
	if audioChoice.ID != "" {
		encoder, ok := ResolveCodecEncoder(audioChoice, encoders)
		if !ok {
			return opts, fmt.Errorf("audio codec %s is not available on this server", audioChoice.ID)
		}
		opts.EffectiveAudioEncoder = encoder
	}
	return opts, nil
}

func VideoCodecsFor(format string) []CodecChoice {
	return cloneCodecChoices(videoCodecMatrix[format])
}

func AudioCodecsFor(format string) []CodecChoice {
	return cloneCodecChoices(audioCodecMatrix[format])
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
	opts.VideoCodec = normalizeCodecID(opts.VideoCodec)
	opts.AudioCodec = normalizeCodecID(opts.AudioCodec)
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
		if opts.VideoCodec != "" || opts.AudioCodec != "" {
			return errors.New("codec options are not valid for image outputs")
		}
		if opts.VideoBitrate != 0 || opts.AudioBitrate != 0 || opts.Framerate != 0 {
			return errors.New("video, audio bitrate, and framerate options are not valid for image outputs")
		}
	}
	if selected.MediaType == "audio" {
		if opts.VideoCodec != "" || opts.AudioCodec != "" {
			return errors.New("codec options are not valid for audio outputs")
		}
		if opts.MaxHeight != 0 || opts.MaxWidth != 0 || opts.VideoBitrate != 0 || opts.Framerate != 0 || opts.Quality != 0 {
			return errors.New("visual options are not valid for audio outputs")
		}
	}
	if targetFormat == "gif" {
		if opts.VideoCodec != "" || opts.AudioCodec != "" {
			return errors.New("codec options are not valid for GIF output")
		}
		if opts.VideoBitrate != 0 {
			return errors.New("videoBitrate is not valid for GIF output")
		}
		if opts.AudioBitrate != 0 {
			return errors.New("audioBitrate is not valid for GIF output")
		}
		return nil
	}
	if selected.MediaType == "video" {
		if !isVideoContainer(targetFormat) && (opts.VideoCodec != "" || opts.AudioCodec != "") {
			return fmt.Errorf("codec options are not valid for %s output", targetFormat)
		}
		encoders := runtimeEncoderMap(context.Background())
		if opts.VideoCodec != "" {
			choice, ok := codecByID(videoCodecMatrix[targetFormat], opts.VideoCodec)
			if !ok {
				return fmt.Errorf("video codec %s is not supported for %s", opts.VideoCodec, targetFormat)
			}
			if _, ok := ResolveCodecEncoder(choice, encoders); !ok {
				return fmt.Errorf("video codec %s is not available on this server", opts.VideoCodec)
			}
		}
		audioChoice := ResolveAudioCodec(targetFormat, opts)
		if opts.AudioCodec != "" {
			choice, ok := codecByID(audioCodecMatrix[targetFormat], opts.AudioCodec)
			if !ok {
				return fmt.Errorf("audio codec %s is not supported for %s", opts.AudioCodec, targetFormat)
			}
			if _, ok := ResolveCodecEncoder(choice, encoders); !ok {
				return fmt.Errorf("audio codec %s is not available on this server", opts.AudioCodec)
			}
			audioChoice = choice
		}
		if opts.AudioBitrate != 0 && audioChoice.ID != "" && !audioChoice.SupportsBitrate {
			return fmt.Errorf("audioBitrate is not valid for %s", audioChoice.ID)
		}
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
			opts.MaxWidth = presetInt(gifPresetWidths, preset)
		}
		if opts.Framerate == 0 {
			opts.Framerate = presetInt(gifPresetFramerates, preset)
		}
	default:
		if outputKind(targetFormat) == "video" && opts.MaxHeight == 0 {
			opts.MaxHeight = presetInt(videoPresetMaxHeights, preset)
		}
	}
	if outputKind(targetFormat) == "image" && opts.Quality == 0 {
		opts.Quality = presetInt(imagePresetQualities, preset)
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

func parseEncoders(raw string) map[string]bool {
	encoders := make(map[string]bool)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		flags := fields[0]
		if len(flags) < 6 || (flags[0] != 'V' && flags[0] != 'A') {
			continue
		}
		encoders[fields[1]] = true
	}
	return encoders
}

func availableCodecChoices(choices []CodecChoice, encoders map[string]bool) []CodecChoice {
	out := make([]CodecChoice, 0, len(choices))
	for _, choice := range choices {
		encoder, ok := ResolveCodecEncoder(choice, encoders)
		if !ok {
			continue
		}
		choice.Encoder = encoder
		choice.Available = true
		out = append(out, choice)
	}
	return out
}

func resolveCodec(choices []CodecChoice, fallbackID, selectedID string) CodecChoice {
	selectedID = normalizeCodecID(selectedID)
	if selectedID == "" {
		selectedID = fallbackID
	}
	choice, _ := codecByID(choices, selectedID)
	return choice
}

func codecByID(choices []CodecChoice, id string) (CodecChoice, bool) {
	id = normalizeCodecID(id)
	for _, choice := range choices {
		if choice.ID == id {
			return choice, true
		}
	}
	return CodecChoice{}, false
}

func normalizeCodecID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "auto" {
		return ""
	}
	return value
}

func isVideoContainer(format string) bool {
	_, ok := videoCodecMatrix[format]
	return ok
}

func fallbackAutoEncoders() map[string]bool {
	out := make(map[string]bool)
	for format, id := range defaultVideoCodecs {
		if choice, ok := codecByID(videoCodecMatrix[format], id); ok {
			if choice.Encoder != "" {
				out[choice.Encoder] = true
			} else if len(choice.EncoderChoices) > 0 {
				out[choice.EncoderChoices[0]] = true
			}
		}
	}
	for format, id := range defaultAudioCodecs {
		if choice, ok := codecByID(audioCodecMatrix[format], id); ok {
			if choice.Encoder != "" {
				out[choice.Encoder] = true
			} else if len(choice.EncoderChoices) > 0 {
				out[choice.EncoderChoices[0]] = true
			}
		}
	}
	return out
}

func runtimeEncoderMap(ctx context.Context) map[string]bool {
	encoders, err := AvailableEncoders(ctx)
	if err != nil || len(encoders) == 0 {
		return fallbackAutoEncoders()
	}
	return encoders
}

func presetInt(values map[string]int, preset string) int {
	if value, ok := values[preset]; ok {
		return value
	}
	return values["balanced"]
}

func cloneEncoderMap(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneCodecChoices(in []CodecChoice) []CodecChoice {
	out := make([]CodecChoice, len(in))
	copy(out, in)
	return out
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}
