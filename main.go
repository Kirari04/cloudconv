package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

const (
	uploadDir    = "uploads"
	convertedDir = "converted"
)

// ConversionJob holds the state of a single conversion task.
type ConversionJob struct {
	ID                 string        `json:"id"`
	Status             string        `json:"status"` // idle, queued, converting, finished, error
	ProgressPercentage int           `json:"progressPercentage"`
	DownloadURL        string        `json:"downloadUrl,omitempty"`
	ErrorMessage       string        `json:"error,omitempty"`
	QueuePosition      int           `json:"queuePosition,omitempty"`
	OriginalFilename   string        `json:"-"`
	TargetFormat       string        `json:"-"`
	Options            FFmpegOptions `json:"-"`
	CreatedAt          time.Time     `json:"-"`
}

// FFmpegOptions holds all the user-configurable conversion settings.
type FFmpegOptions struct {
	Resolution   string
	Bitrate      string
	Framerate    string
	AudioBitrate string
	GifLoop      bool
}

var (
	jobStore      = make(map[string]*ConversionJob)
	jobQueue      []string
	jobStoreMutex = &sync.RWMutex{}
	jobQueueMutex = &sync.Mutex{}
	activeJob     = &sync.Mutex{}
)

// Main function to set up the server.
func main() {
	setupDirectories()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// API routes
	r.Post("/api/uploads/initiate", initiateUploadHandler)
	r.Post("/api/uploads/{uploadId}", fileUploadHandler)
	r.Get("/api/uploads/{uploadId}/status", statusHandler)

	// Serve static files (converted videos)
	fs := http.FileServer(http.Dir(convertedDir))
	r.Handle("/converted/*", http.StripPrefix("/converted/", fs))

	// Serve the main HTML page
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "templates/index.html")
	})

	// Start background workers
	go worker()
	go cleanupWorker()

	log.Println("Server starting on :3000")
	if err := http.ListenAndServe(":3000", r); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// setupDirectories ensures the necessary folders exist and are empty on startup.
func setupDirectories() {
	log.Println("Clearing temporary directories...")
	os.RemoveAll(uploadDir)
	os.RemoveAll(convertedDir)
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Fatalf("Failed to create upload directory: %v", err)
	}
	if err := os.MkdirAll(convertedDir, 0755); err != nil {
		log.Fatalf("Failed to create converted directory: %v", err)
	}
}

// initiateUploadHandler creates a new job with a unique ID.
func initiateUploadHandler(w http.ResponseWriter, r *http.Request) {
	jobID := uuid.New().String()
	job := &ConversionJob{
		ID:        jobID,
		Status:    "idle",
		CreatedAt: time.Now(),
	}

	jobStoreMutex.Lock()
	jobStore[jobID] = job
	jobStoreMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"uploadId": jobID})
}

// validateAndParseOptions checks all user inputs for validity and populates the FFmpegOptions struct.
func validateAndParseOptions(r *http.Request) (FFmpegOptions, error) {
	opts := FFmpegOptions{}

	// Ensure no unknown options are passed
	knownOptions := map[string]bool{
		"format":       true,
		"resolution":   true,
		"bitrate":      true,
		"audioBitrate": true,
		"framerate":    true,
		"gifLoop":      true,
		"videoFile":    true, // multipart form field name
	}
	for key := range r.Form {
		if !knownOptions[key] {
			return opts, fmt.Errorf("unknown option specified: %s", key)
		}
	}

	targetFormat := r.FormValue("format")
	if targetFormat == "" {
		return opts, fmt.Errorf("target format must be specified")
	}
	validFormats := []string{"mp4", "webm", "mov", "avi", "mkv", "gif"}
	if !contains(validFormats, targetFormat) {
		return opts, fmt.Errorf("invalid target format specified")
	}

	// Resolution validation
	opts.Resolution = r.FormValue("resolution")
	if opts.Resolution != "" {
		validResolutions := []string{"1080", "720", "480", "360", "240"}
		if _, err := strconv.Atoi(opts.Resolution); err != nil {
			return opts, fmt.Errorf("resolution must be a number")
		}
		if !contains(validResolutions, opts.Resolution) {
			return opts, fmt.Errorf("invalid resolution value")
		}
	}

	// Bitrate validation (generic and audio)
	bitrateStr := r.FormValue("bitrate")
	if bitrateStr != "" {
		val, err := strconv.Atoi(bitrateStr)
		if err != nil {
			return opts, fmt.Errorf("bitrate must be a number")
		}
		if val < 100 || val > 10000 {
			return opts, fmt.Errorf("bitrate must be a number between 100 and 10000")
		}
		opts.Bitrate = bitrateStr
	}

	audioBitrateStr := r.FormValue("audioBitrate")
	if audioBitrateStr != "" {
		val, err := strconv.Atoi(audioBitrateStr)
		if err != nil {
			return opts, fmt.Errorf("audio bitrate must be a number")
		}
		if val < 32 || val > 320 {
			return opts, fmt.Errorf("audio bitrate must be a number between 32 and 320")
		}
		opts.AudioBitrate = audioBitrateStr
	}

	// Framerate validation
	framerateStr := r.FormValue("framerate")
	if framerateStr != "" {
		val, err := strconv.Atoi(framerateStr)
		if err != nil {
			return opts, fmt.Errorf("framerate must be a number")
		}
		if val < 1 || val > 60 {
			return opts, fmt.Errorf("framerate must be a number between 1 and 60")
		}
		opts.Framerate = framerateStr
	}

	// GIF-specific validation
	if targetFormat == "gif" {
		opts.GifLoop = r.FormValue("gifLoop") == "true"
	}

	return opts, nil
}

// fileUploadHandler handles the file upload and queues the conversion.
func fileUploadHandler(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "uploadId")
	jobStoreMutex.Lock()
	job, exists := jobStore[jobID]
	if !exists {
		jobStoreMutex.Unlock()
		http.Error(w, "Invalid upload session.", http.StatusNotFound)
		return
	}
	jobStoreMutex.Unlock()

	// Parse multipart form
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB max file size
		http.Error(w, "Could not parse multipart form.", http.StatusBadRequest)
		return
	}

	// Validate all options before proceeding
	opts, err := validateAndParseOptions(r)
	if err != nil {
		job.Status = "error"
		job.ErrorMessage = err.Error()
		jobStoreMutex.Lock()
		jobStore[jobID] = job
		jobStoreMutex.Unlock()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("videoFile")
	if err != nil {
		http.Error(w, "Could not retrieve file.", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Use jobID to create a secure filename, preserving the original extension
	ext := filepath.Ext(header.Filename)
	safeFilename := jobID + ext
	filePath := filepath.Join(uploadDir, safeFilename)

	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Could not save file.", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Could not write file to disk.", http.StatusInternalServerError)
		return
	}

	// Update job with parsed info and add to queue
	job.OriginalFilename = safeFilename
	job.TargetFormat = r.FormValue("format")
	job.Options = opts
	job.Status = "queued"

	jobQueueMutex.Lock()
	jobQueue = append(jobQueue, jobID)
	job.QueuePosition = len(jobQueue)
	jobQueueMutex.Unlock()

	jobStoreMutex.Lock()
	jobStore[jobID] = job
	jobStoreMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Upload complete, conversion is queued."})
}

// statusHandler returns the current status of a job.
func statusHandler(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "uploadId")

	jobStoreMutex.RLock()
	job, exists := jobStore[jobID]
	jobStoreMutex.RUnlock()

	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// Update queue position if the job is still queued
	if job.Status == "queued" {
		jobQueueMutex.Lock()
		for i, id := range jobQueue {
			if id == jobID {
				job.QueuePosition = i + 1
				break
			}
		}
		jobQueueMutex.Unlock()
		jobStoreMutex.Lock()
		jobStore[jobID] = job
		jobStoreMutex.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

// worker processes jobs from the queue one by one.
func worker() {
	log.Println("Conversion worker started.")
	for {
		var jobID string

		jobQueueMutex.Lock()
		if len(jobQueue) > 0 {
			jobID = jobQueue[0]
			jobQueue = jobQueue[1:]
		}
		jobQueueMutex.Unlock()

		if jobID != "" {
			activeJob.Lock()
			processJob(jobID)
			activeJob.Unlock()
		} else {
			time.Sleep(1 * time.Second)
		}
	}
}

// processJob handles the conversion logic for a single job.
func processJob(jobID string) {
	jobStoreMutex.Lock()
	job := jobStore[jobID]
	job.Status = "converting"
	job.QueuePosition = 0
	jobStore[jobID] = job
	jobStoreMutex.Unlock()

	err := convertVideo(job)

	jobStoreMutex.Lock()
	// Re-fetch job to avoid race conditions
	job = jobStore[jobID]
	if err != nil {
		log.Printf("FFmpeg failed for job %s: %v", job.ID, err)
		job.Status = "error"
		job.ErrorMessage = err.Error()
	} else {
		log.Printf("[Job %s] Successfully converted file.", job.ID)
		job.Status = "finished"
		job.DownloadURL = fmt.Sprintf("/converted/%s.%s", job.ID, job.TargetFormat)
		job.ProgressPercentage = 100
	}
	jobStore[jobID] = job
	jobStoreMutex.Unlock()

	// Clean up original uploaded file immediately after conversion attempt
	uploadedFilePath := filepath.Join(uploadDir, job.OriginalFilename)
	if err := os.Remove(uploadedFilePath); err != nil {
		log.Printf("Warning: Failed to delete uploaded file %s: %v", uploadedFilePath, err)
	}
}

// convertVideo uses FFmpeg to perform the video conversion.
func convertVideo(job *ConversionJob) error {
	inputPath := filepath.Join(uploadDir, job.OriginalFilename)
	outputPath := filepath.Join(convertedDir, fmt.Sprintf("%s.%s", job.ID, job.TargetFormat))

	// Get video duration for progress calculation
	duration, err := getVideoDuration(inputPath)
	if err != nil {
		return fmt.Errorf("failed to get video duration: %w", err)
	}

	// Setup Unix socket for progress reporting
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("%s.sock", job.ID))
	_ = os.Remove(socketPath) // Clean up any old socket file
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to create progress socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	go listenForProgress(listener, job, duration)

	args := buildFFmpegCommand(inputPath, outputPath, job, socketPath)

	log.Printf("[Job %s] Executing FFmpeg Command: %s %s", job.ID, "ffmpeg", strings.Join(args, " "))

	cmd := exec.Command("ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exit status %v\nOutput: %s", err, string(output))
	}
	return nil
}

// buildFFmpegCommand constructs the FFmpeg command arguments.
func buildFFmpegCommand(inputPath, outputPath string, job *ConversionJob, socketPath string) []string {
	args := []string{"-i", inputPath, "-progress", "unix://" + socketPath}
	opts := job.Options

	var videoFilters []string

	// Handle GIF specific case
	if job.TargetFormat == "gif" {
		args = append(args, "-an") // No audio for GIFs
		loop := "0"                // Loop indefinitely
		if !opts.GifLoop {
			loop = "-1" // Do not loop
		}
		args = append(args, "-loop", loop)

		fps := "15" // Default fps for GIF
		if opts.Framerate != "" {
			fps = opts.Framerate
		}

		resolution := opts.Resolution
		if resolution == "" {
			resolution = "360" // Default width for GIF
		}

		// Complex filter for high-quality GIF creation
		filter := fmt.Sprintf("fps=%s,scale=%s:-1:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse", fps, resolution)
		args = append(args, "-vf", filter)
	} else {
		// Generic video/audio options
		// Video Codec
		switch job.TargetFormat {
		case "mp4", "mov", "mkv":
			args = append(args, "-c:v", "libx264", "-profile:v", "baseline", "-level", "3.0", "-pix_fmt", "yuv420p")
		case "webm":
			args = append(args, "-c:v", "libvpx")
		case "avi":
			args = append(args, "-c:v", "mpeg4")
		}

		// Resolution
		if opts.Resolution != "" {
			videoFilters = append(videoFilters, fmt.Sprintf("scale=-2:%s", opts.Resolution))
		}
		// Bitrate
		if opts.Bitrate != "" {
			args = append(args, "-b:v", opts.Bitrate+"k")
		}
		// Framerate
		if opts.Framerate != "" {
			args = append(args, "-r", opts.Framerate)
		}

		// Audio Codec and Options
		switch job.TargetFormat {
		case "webm":
			args = append(args, "-c:a", "libopus")
		default: // mp4, mov, avi, mkv
			args = append(args, "-c:a", "aac")
		}
		// Always specify channel layout for compatibility
		args = append(args, "-af", "aformat=channel_layouts='7.1|5.1|stereo'")

		if opts.AudioBitrate != "" {
			args = append(args, "-b:a", opts.AudioBitrate+"k")
		}
	}

	if len(videoFilters) > 0 {
		args = append(args, "-vf", strings.Join(videoFilters, ","))
	}

	args = append(args, "-y", outputPath)
	return args
}

// getVideoDuration uses ffprobe to get the total duration of a video file in seconds.
func getVideoDuration(filePath string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", filePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %s, %w", string(output), err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse duration: %w", err)
	}
	return duration, nil
}

// listenForProgress reads progress from the FFmpeg socket.
func listenForProgress(listener net.Listener, job *ConversionJob, duration float64) {
	conn, err := listener.Accept()
	if err != nil {
		log.Printf("Error accepting progress connection for job %s: %v", job.ID, err)
		return
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "=")
		if len(parts) == 2 && parts[0] == "out_time_ms" {
			if currentTimeUs, err := strconv.ParseFloat(parts[1], 64); err == nil {
				progress := (currentTimeUs / 1000000.0) / duration * 100
				if progress > 100 {
					progress = 100
				}
				jobStoreMutex.Lock()
				// Check if job still exists before updating
				if currentJob, ok := jobStore[job.ID]; ok {
					currentJob.ProgressPercentage = int(progress)
					jobStore[job.ID] = currentJob
				}
				jobStoreMutex.Unlock()
			}
		}
	}
}

// cleanupWorker periodically removes old converted files.
func cleanupWorker() {
	log.Println("Cleanup worker started.")
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		jobStoreMutex.Lock()
		for id, job := range jobStore {
			if job.Status == "finished" || job.Status == "error" {
				if time.Since(job.CreatedAt) > 1*time.Hour {
					log.Printf("Cleaning up old job: %s", id)
					if job.DownloadURL != "" {
						filePath := filepath.Join(convertedDir, filepath.Base(job.DownloadURL))
						if err := os.Remove(filePath); err != nil {
							log.Printf("Warning: Failed to delete old converted file %s: %v", filePath, err)
						}
					}
					delete(jobStore, id)
				}
			}
		}
		jobStoreMutex.Unlock()
	}
}

// contains checks if a string is in a slice of strings.
func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}
