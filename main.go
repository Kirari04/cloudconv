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
	"github.com/google/uuid"
)

const (
	uploadDir    = "uploads"
	convertedDir = "converted"
)

// ConversionJob holds the state and details of a conversion task.
type ConversionJob struct {
	ID                 string             `json:"id"`
	Status             string             `json:"status"` // queued, converting, finished, error
	OriginalFilename   string             `json:"originalFilename"`
	TargetFormat       string             `json:"targetFormat"`
	DownloadURL        string             `json:"downloadUrl,omitempty"`
	ErrorMessage       string             `json:"error,omitempty"`
	QueuePosition      int                `json:"queuePosition,omitempty"`
	ProgressPercentage int                `json:"progressPercentage,omitempty"`
	CreatedAt          time.Time          `json:"createdAt"`
	Options            *ConversionOptions `json:"-"` // Store options, but don't expose in API responses
}

// ConversionOptions holds the user-defined settings for the conversion.
type ConversionOptions struct {
	Format       string
	Resolution   int
	Bitrate      int
	AudioBitrate int
	Framerate    int
	GifLoop      string
}

var (
	jobStore      = make(map[string]*ConversionJob)
	jobStoreMutex = &sync.RWMutex{}
	jobQueue      = make([]string, 0)
	jobQueueMutex = &sync.Mutex{}
)

func main() {
	setupDirectories()

	// Start background workers
	go worker()
	go cleanupWorker()

	r := chi.NewRouter()
	r.Get("/", serveIndex)
	r.Post("/api/uploads/initiate", initiateUploadHandler)
	r.Post("/api/uploads/{uploadId}", fileUploadHandler)
	r.Get("/api/uploads/{uploadId}/status", statusHandler)
	r.Get("/download/{filename}", downloadHandler)

	log.Println("Server starting on :3000")
	if err := http.ListenAndServe(":3000", r); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

// setupDirectories ensures that the necessary directories exist and are empty on startup.
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

// serveIndex serves the main HTML page.
func serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "templates/index.html")
}

// initiateUploadHandler creates a new job entry.
func initiateUploadHandler(w http.ResponseWriter, r *http.Request) {
	uploadId := uuid.New().String()
	job := &ConversionJob{
		ID:        uploadId,
		Status:    "idle", // Status before a file is uploaded
		CreatedAt: time.Now(),
	}

	jobStoreMutex.Lock()
	jobStore[uploadId] = job
	jobStoreMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"uploadId": uploadId})
}

// validateAndParseOptions checks the user-submitted form values for correctness.
func validateAndParseOptions(r *http.Request) (*ConversionOptions, error) {
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB max memory
		return nil, fmt.Errorf("could not parse multipart form: %w", err)
	}

	formValues := r.MultipartForm.Value
	opts := &ConversionOptions{}
	submittedOptions := make(map[string]string)

	for key, values := range formValues {
		if len(values) > 0 {
			submittedOptions[key] = values[0]
		}
	}

	// --- Check for unknown parameters ---
	knownParams := map[string]bool{
		"format":       true,
		"resolution":   true,
		"bitrate":      true,
		"audioBitrate": true,
		"framerate":    true,
		"gifLoop":      true,
	}
	for key := range submittedOptions {
		if !knownParams[key] {
			return nil, fmt.Errorf("unknown option specified: %s", key)
		}
	}

	// --- Format validation (required) ---
	opts.Format = submittedOptions["format"]
	if opts.Format == "" {
		return nil, fmt.Errorf("target format must be specified")
	}
	validFormats := []string{"mp4", "webm", "mov", "avi", "mkv", "gif"}
	isValidFormat := false
	for _, f := range validFormats {
		if f == opts.Format {
			isValidFormat = true
			break
		}
	}
	if !isValidFormat {
		return nil, fmt.Errorf("invalid target format specified")
	}

	// --- GIF-specific parameter validation ---
	if opts.Format == "gif" {
		if _, ok := submittedOptions["bitrate"]; ok {
			return nil, fmt.Errorf("bitrate is not a valid option for format gif")
		}
		if _, ok := submittedOptions["audioBitrate"]; ok {
			return nil, fmt.Errorf("audioBitrate is not a valid option for format gif")
		}
		if val, ok := submittedOptions["gifLoop"]; ok && val != "" {
			if val != "true" && val != "false" {
				return nil, fmt.Errorf("gifLoop value must be 'true' or 'false'")
			}
			opts.GifLoop = val
		}
	} else {
		if _, ok := submittedOptions["gifLoop"]; ok {
			return nil, fmt.Errorf("gifLoop is only a valid option for format gif")
		}
	}

	// --- Generic parameter validation ---
	var err error
	if val := submittedOptions["resolution"]; val != "" {
		opts.Resolution, err = strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("resolution must be a number")
		}
		validResolutions := []int{1080, 720, 480, 360, 240}
		isValidRes := false
		for _, res := range validResolutions {
			if res == opts.Resolution {
				isValidRes = true
				break
			}
		}
		if !isValidRes {
			return nil, fmt.Errorf("invalid resolution value")
		}
	}

	if val := submittedOptions["bitrate"]; val != "" {
		opts.Bitrate, err = strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("bitrate must be a number")
		}
		if opts.Bitrate < 100 || opts.Bitrate > 10000 {
			return nil, fmt.Errorf("bitrate must be a number between 100 and 10000")
		}
	}

	if val := submittedOptions["audioBitrate"]; val != "" {
		opts.AudioBitrate, err = strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("audio bitrate must be a number")
		}
		if opts.AudioBitrate < 32 || opts.AudioBitrate > 320 {
			return nil, fmt.Errorf("audio bitrate must be a number between 32 and 320")
		}
	}

	if val := submittedOptions["framerate"]; val != "" {
		opts.Framerate, err = strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("framerate must be a number")
		}
		if opts.Framerate < 1 || opts.Framerate > 60 {
			return nil, fmt.Errorf("framerate must be a number between 1 and 60")
		}
	}

	return opts, nil
}

// fileUploadHandler saves the uploaded file and adds the job to the queue.
func fileUploadHandler(w http.ResponseWriter, r *http.Request) {
	uploadId := chi.URLParam(r, "uploadId")
	jobStoreMutex.RLock()
	job, exists := jobStore[uploadId]
	jobStoreMutex.RUnlock()
	if !exists {
		http.Error(w, "Invalid upload session.", http.StatusNotFound)
		return
	}

	opts, err := validateAndParseOptions(r)
	if err != nil {
		jobStoreMutex.Lock()
		job.Status = "error"
		job.ErrorMessage = err.Error()
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

	ext := filepath.Ext(header.Filename)
	safeFilename := uploadId + ext
	filePath := filepath.Join(uploadDir, safeFilename)

	dst, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Could not save file.", http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	jobStoreMutex.Lock()
	job.OriginalFilename = header.Filename
	job.TargetFormat = opts.Format
	job.Options = opts // Store the validated options in the job
	jobStore[uploadId] = job
	jobStoreMutex.Unlock()

	// Enqueue the job
	jobQueueMutex.Lock()
	jobQueue = append(jobQueue, uploadId)
	jobStoreMutex.Lock()
	job.Status = "queued"
	job.QueuePosition = len(jobQueue)
	jobStoreMutex.Unlock()
	jobQueueMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Upload complete, conversion is queued."})
}

// statusHandler returns the current status of a job.
func statusHandler(w http.ResponseWriter, r *http.Request) {
	uploadId := chi.URLParam(r, "uploadId")

	jobStoreMutex.RLock()
	job, exists := jobStore[uploadId]
	jobStoreMutex.RUnlock()

	if !exists {
		http.Error(w, "Job not found.", http.StatusNotFound)
		return
	}

	// If the job is queued, update its position dynamically
	if job.Status == "queued" {
		jobQueueMutex.Lock()
		for i, id := range jobQueue {
			if id == uploadId {
				job.QueuePosition = i + 1
				break
			}
		}
		jobQueueMutex.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

// downloadHandler serves the converted file.
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")
	filePath := filepath.Join(convertedDir, filename)
	http.ServeFile(w, r, filePath)
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
			convertVideo(jobID)
		}

		time.Sleep(1 * time.Second)
	}
}

// buildFFmpegCommand constructs the FFmpeg command with appropriate arguments.
func buildFFmpegCommand(job *ConversionJob, opts *ConversionOptions, inputPath, outputPath, socketPath string) *exec.Cmd {
	args := []string{"-i", inputPath, "-progress", "unix://" + socketPath}

	// --- Video Arguments ---
	if opts.Format == "gif" {
		args = append(args, "-an") // No audio for GIFs
		loop := "0"                // Loop indefinitely by default
		if opts.GifLoop == "false" {
			loop = "-1" // Do not loop
		}
		args = append(args, "-loop", loop)

		fps := "15"
		if opts.Framerate != 0 {
			fps = strconv.Itoa(opts.Framerate)
		}
		scale := "360"
		if opts.Resolution != 0 {
			scale = strconv.Itoa(opts.Resolution)
		}
		// Complex filter for high-quality GIF
		filter := fmt.Sprintf("fps=%s,scale=%s:-2:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse", fps, scale)
		args = append(args, "-vf", filter)

	} else {
		// --- Codec Selection ---
		switch opts.Format {
		case "mp4", "mov", "mkv":
			args = append(args, "-c:v", "libx264", "-profile:v", "baseline", "-level", "3.0", "-pix_fmt", "yuv420p")
		case "webm":
			args = append(args, "-c:v", "libvpx") // vp8
		case "avi":
			args = append(args, "-c:v", "mpeg4")
		}

		// --- Filters (Resolution, Framerate, Transparency handling) ---
		var vfArgs []string
		if opts.Resolution != 0 {
			vfArgs = append(vfArgs, fmt.Sprintf("scale=-2:%d", opts.Resolution))
		}
		// If input is a GIF, convert pixel format to be compatible with video encoders
		if strings.HasSuffix(strings.ToLower(job.OriginalFilename), ".gif") {
			vfArgs = append(vfArgs, "format=yuv420p")
		}
		if len(vfArgs) > 0 {
			args = append(args, "-vf", strings.Join(vfArgs, ","))
		}

		if opts.Framerate != 0 {
			args = append(args, "-r", strconv.Itoa(opts.Framerate))
		}
		if opts.Bitrate != 0 {
			args = append(args, "-b:v", fmt.Sprintf("%dk", opts.Bitrate))
		}

		// --- Audio Arguments ---
		switch opts.Format {
		case "mp4", "mov", "mkv", "avi":
			args = append(args, "-c:a", "aac")
		case "webm":
			args = append(args, "-c:a", "libopus")
		}
		args = append(args, "-af", "aformat=channel_layouts='7.1|5.1|stereo'")
		if opts.AudioBitrate != 0 {
			args = append(args, "-b:a", fmt.Sprintf("%dk", opts.AudioBitrate))
		}
	}

	args = append(args, "-y", outputPath)
	return exec.Command("ffmpeg", args...)
}

// convertVideo runs the FFmpeg process for a given job.
func convertVideo(jobID string) {
	jobStoreMutex.Lock()
	job, exists := jobStore[jobID]
	if !exists || job.Options == nil { // Added nil check for safety
		jobStoreMutex.Unlock()
		log.Printf("Job %s not found or has no options for conversion.", jobID)
		if exists {
			updateJobError(jobID, "Internal error: job options were not saved.")
		}
		return
	}
	job.Status = "converting"
	jobStoreMutex.Unlock()

	inputPath := filepath.Join(uploadDir, filepath.Base(jobID+filepath.Ext(job.OriginalFilename)))
	outputPath := filepath.Join(convertedDir, jobID+"."+job.TargetFormat)

	// Use a temporary path for the Unix socket
	socketPath := filepath.Join(os.TempDir(), jobID+".sock")
	os.Remove(socketPath) // Clean up any old socket file

	totalDuration, err := getVideoDuration(inputPath)
	if err != nil {
		log.Printf("Failed to get duration for job %s: %v", jobID, err)
		// We can still proceed, progress will just not be a percentage.
	}

	// Start listening on the socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Printf("Failed to listen on socket for job %s: %v", jobID, err)
		updateJobError(jobID, "Failed to initialize progress monitoring.")
		return
	}
	defer listener.Close()

	go monitorProgress(listener, jobID, totalDuration)

	// Use the options that were stored in the job struct
	cmd := buildFFmpegCommand(job, job.Options, inputPath, outputPath, socketPath)

	log.Printf("[Job %s] Executing FFmpeg Command: %s", jobID, strings.Join(cmd.Args, " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		errMsg := fmt.Sprintf("exit status %v", err)
		log.Printf("FFmpeg failed for job %s: %s\nOutput: %s", jobID, errMsg, string(output))
		updateJobError(jobID, errMsg)
	} else {
		log.Printf("[Job %s] Successfully converted file.", jobID)
		jobStoreMutex.Lock()
		job.Status = "finished"
		job.DownloadURL = "/download/" + filepath.Base(outputPath)
		job.ProgressPercentage = 100
		jobStoreMutex.Unlock()
	}

	// Cleanup the uploaded file immediately after conversion
	if err := os.Remove(inputPath); err != nil {
		log.Printf("Warning: Failed to delete uploaded file %s: %v", inputPath, err)
	}
}

// getVideoDuration uses ffprobe to get the video duration in seconds.
func getVideoDuration(filePath string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", filePath)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
}

// monitorProgress reads from the socket and updates the job's progress.
func monitorProgress(listener net.Listener, jobID string, totalDuration float64) {
	conn, err := listener.Accept()
	if err != nil {
		log.Printf("Progress monitor accept error for job %s: %v", jobID, err)
		return
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "=")
		if len(parts) == 2 && parts[0] == "out_time_ms" {
			if currentTimeMs, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
				if totalDuration > 0 {
					progress := (float64(currentTimeMs) / 1000000.0) / totalDuration * 100
					if progress > 100 {
						progress = 100
					}
					jobStoreMutex.Lock()
					if job, exists := jobStore[jobID]; exists && job.Status == "converting" {
						job.ProgressPercentage = int(progress)
					}
					jobStoreMutex.Unlock()
				}
			}
		}
	}
}

// updateJobError sets a job's status to 'error' and records the message.
func updateJobError(jobID, message string) {
	jobStoreMutex.Lock()
	defer jobStoreMutex.Unlock()
	if job, exists := jobStore[jobID]; exists {
		job.Status = "error"
		job.ErrorMessage = message
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
			// Remove jobs and files older than 1 hour
			if (job.Status == "finished" || job.Status == "error") && time.Since(job.CreatedAt) > 1*time.Hour {
				if job.DownloadURL != "" {
					filename := filepath.Base(job.DownloadURL)
					filePath := filepath.Join(convertedDir, filename)
					if err := os.Remove(filePath); err == nil {
						log.Printf("Cleaned up old file: %s", filePath)
					}
				}
				delete(jobStore, id)
				log.Printf("Removed old job from store: %s", id)
			}
		}
		jobStoreMutex.Unlock()
	}
}
