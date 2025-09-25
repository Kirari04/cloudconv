package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
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

// --- 1. Constants and Global Variables ---
const (
	uploadDir    = "uploads"
	convertedDir = "converted"
	port         = "3000"
)

// ConversionJob represents the state of a single video conversion task.
type ConversionJob struct {
	ID                   string            `json:"id"`
	Status               string            `json:"status"` // idle, queued, converting, finished, error
	QueuePosition        int               `json:"queuePosition,omitempty"`
	ProgressPercentage   int               `json:"progressPercentage"`
	ConversionOptions    map[string]string `json:"-"` // Internal use for worker
	TotalDurationSeconds float64           `json:"-"`
	InputPath            string            `json:"-"`
	OutputPath           string            `json:"-"`
	DownloadURL          string            `json:"downloadUrl,omitempty"`
	ErrorMessage         string            `json:"error,omitempty"`
	CreationTime         time.Time         `json:"-"` // Time the conversion finished
}

// Structs for parsing ffprobe's JSON output
type ffprobeFormat struct {
	Duration string `json:"duration"`
}
type ffprobeOutput struct {
	Format ffprobeFormat `json:"format"`
}

// In-memory store and queue for conversion jobs.
var (
	templates     *template.Template
	jobStore      = make(map[string]*ConversionJob)
	jobStoreMux   sync.RWMutex
	jobQueue      []string // A slice of job IDs
	jobQueueMutex sync.Mutex
)

// --- 2. Main Function ---
func main() {
	setupDirectories()
	parseTemplates()

	// Start the worker goroutines
	go worker()
	go cleanupWorker()

	r := chi.NewRouter()
	r.Use(middleware.Logger)

	r.Get("/", homeHandler)
	r.Route("/api/uploads", func(r chi.Router) {
		r.Post("/initiate", initiateUploadHandler)
		r.Post("/{uploadId}", fileUploadHandler)
		r.Get("/{uploadId}/status", statusHandler)
	})

	fs := http.FileServer(http.Dir(convertedDir))
	r.Handle("/files/*", http.StripPrefix("/files/", fs))

	log.Printf("Starting server on http://localhost:%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// --- 3. Job Workers ---
func worker() {
	log.Println("Conversion worker started.")
	for {
		var jobID string

		jobQueueMutex.Lock()
		if len(jobQueue) > 0 {
			// Dequeue the job ID
			jobID = jobQueue[0]
			jobQueue = jobQueue[1:]
		}
		jobQueueMutex.Unlock()

		if jobID != "" {
			job, exists := getJob(jobID)
			if !exists {
				log.Printf("Worker: Job %s not found in store, skipping.", jobID)
				continue
			}
			// Process the job
			convertVideo(job)
		} else {
			// Wait if the queue is empty
			time.Sleep(1 * time.Second)
		}
	}
}

func cleanupWorker() {
	log.Println("Cleanup worker started.")
	// Ticker will run the check every 5 minutes.
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		jobStoreMux.Lock()
		for id, job := range jobStore {
			// Check for finished jobs older than 1 hour
			if job.Status == "finished" && time.Since(job.CreationTime) > 1*time.Hour {
				log.Printf("Cleanup worker: Deleting old converted file %s for job %s.", job.OutputPath, id)
				// Best-effort deletion, log error if it fails but don't block.
				if err := os.Remove(job.OutputPath); err != nil && !os.IsNotExist(err) {
					log.Printf("Cleanup worker: ERROR failed to delete file %s: %v", job.OutputPath, err)
				}
				// Also remove the job from the store to prevent memory leak
				delete(jobStore, id)
			}
		}
		jobStoreMux.Unlock()
	}
}

// --- 4. Route Handlers ---
func homeHandler(w http.ResponseWriter, r *http.Request) {
	if err := templates.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func initiateUploadHandler(w http.ResponseWriter, r *http.Request) {
	jobID := uuid.New().String()
	job := &ConversionJob{ID: jobID, Status: "idle"}
	jobStoreMux.Lock()
	jobStore[jobID] = job
	jobStoreMux.Unlock()
	respondWithJSON(w, http.StatusCreated, map[string]string{"uploadId": jobID})
}

func fileUploadHandler(w http.ResponseWriter, r *http.Request) {
	uploadID := chi.URLParam(r, "uploadId")
	job, exists := getJob(uploadID)
	if !exists {
		http.Error(w, "Upload session not found", http.StatusNotFound)
		return
	}

	if err := r.ParseMultipartForm(500 << 20); err != nil { // 500 MB limit
		handleUploadError(w, job.ID, "File is too large (max 500MB).", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("videoFile")
	if err != nil {
		handleUploadError(w, job.ID, "Could not retrieve file.", http.StatusBadRequest)
		return
	}
	defer file.Close()

	options := map[string]string{
		"format":       r.FormValue("format"),
		"resolution":   r.FormValue("resolution"),
		"bitrate":      r.FormValue("bitrate"),
		"framerate":    r.FormValue("framerate"),
		"audioBitrate": r.FormValue("audioBitrate"),
		"gifLoop":      r.FormValue("gifLoop"),
	}
	if err := validateOptions(options); err != nil {
		handleUploadError(w, job.ID, err.Error(), http.StatusBadRequest)
		return
	}

	fileExtension := filepath.Ext(handler.Filename)
	safeFilename := uploadID + fileExtension
	inputPath := filepath.Join(uploadDir, safeFilename)
	dst, err := os.Create(inputPath)
	if err != nil {
		handleUploadError(w, job.ID, "Failed to save uploaded file.", http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	io.Copy(dst, file)

	// Add job to the queue
	jobStoreMux.Lock()
	job.InputPath = inputPath
	job.ConversionOptions = options
	job.Status = "queued"
	jobStoreMux.Unlock()

	jobQueueMutex.Lock()
	jobQueue = append(jobQueue, job.ID)
	jobQueueMutex.Unlock()

	respondWithJSON(w, http.StatusOK, map[string]string{"message": "Upload complete, conversion is queued."})
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	uploadID := chi.URLParam(r, "uploadId")
	job, exists := getJob(uploadID)
	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// If job is queued, calculate its position
	if job.Status == "queued" {
		jobQueueMutex.Lock()
		position := 0
		for i, id := range jobQueue {
			if id == uploadID {
				position = i + 1
				break
			}
		}
		job.QueuePosition = position
		jobQueueMutex.Unlock()
	}

	respondWithJSON(w, http.StatusOK, job)
}

// --- 5. Helper and Business Logic Functions ---

func convertVideo(job *ConversionJob) {
	// Defer deletion of the original uploaded file.
	// This will execute after the function returns, regardless of success or failure.
	defer os.Remove(job.InputPath)

	updateJobStatus(job.ID, "converting", "", 0)

	duration, err := getVideoDuration(job.InputPath)
	if err != nil {
		log.Printf("[Job %s] ERROR: Failed to get video duration: %v", job.ID, err)
		updateJobStatus(job.ID, "error", "Could not read video properties.", 0)
		return
	}
	updateJobDuration(job.ID, duration)

	socketPath := filepath.Join(os.TempDir(), job.ID+".sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Printf("[Job %s] ERROR: Failed to create socket: %v", job.ID, err)
		updateJobStatus(job.ID, "error", "Failed to initialize progress tracking.", 0)
		return
	}
	defer os.Remove(socketPath)
	go handleProgressConnection(listener, job)

	targetFormat := job.ConversionOptions["format"]
	outputFilename := strings.TrimSuffix(filepath.Base(job.InputPath), filepath.Ext(job.InputPath)) + "." + targetFormat
	outputPath := filepath.Join(convertedDir, outputFilename)

	cmd := buildFFmpegCommand(job.InputPath, outputPath, targetFormat, job.ConversionOptions, socketPath)
	log.Printf("[Job %s] Executing FFmpeg Command: %s", job.ID, cmd.String())

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[Job %s] ERROR: FFmpeg command failed: %v\n--- FFmpeg Output ---\n%s\n---------------------", job.ID, err, string(output))
		updateJobStatus(job.ID, "error", "Video conversion failed. Check server logs for details.", 0)
		return
	}

	jobStoreMux.Lock()
	defer jobStoreMux.Unlock()
	if j, ok := jobStore[job.ID]; ok {
		j.Status = "finished"
		j.OutputPath = outputPath
		j.DownloadURL = "/files/" + outputFilename
		j.ProgressPercentage = 100
		j.CreationTime = time.Now() // Record the time of successful conversion
	}
	log.Printf("[Job %s] Successfully converted file.", job.ID)
}

func buildFFmpegCommand(inputPath, outputPath, format string, options map[string]string, socketPath string) *exec.Cmd {
	args := []string{"-i", inputPath, "-progress", "unix://" + socketPath}

	if format == "gif" {
		args = append(args, "-an") // No audio for GIFs
		res := options["resolution"]
		if res == "" {
			res = "360"
		}
		fps := options["framerate"]
		if fps == "" {
			fps = "15"
		}
		if options["gifLoop"] != "false" {
			args = append(args, "-loop", "0")
		}
		scaleFilter := fmt.Sprintf("scale=%s:-1:flags=lanczos", res)
		complexFilter := fmt.Sprintf("fps=%s,%s,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse", fps, scaleFilter)
		args = append(args, "-vf", complexFilter)
	} else {
		// --- Video Codec and Options ---
		switch format {
		case "mp4", "mov", "mkv":
			args = append(args, "-c:v", "libx264", "-profile:v", "baseline", "-level", "3.0", "-pix_fmt", "yuv420p")
		case "webm":
			args = append(args, "-c:v", "libvpx") // VP8 is broadly compatible
		case "avi":
			args = append(args, "-c:v", "mpeg4")
		}

		videoFilters := []string{}
		if res := options["resolution"]; res != "" {
			videoFilters = append(videoFilters, fmt.Sprintf("scale=-2:%s", res))
		}
		if len(videoFilters) > 0 {
			args = append(args, "-vf", strings.Join(videoFilters, ","))
		}
		if br := options["bitrate"]; br != "" {
			args = append(args, "-b:v", br+"k")
		}
		if fr := options["framerate"]; fr != "" {
			args = append(args, "-r", fr)
		}

		// --- Audio Codec and Options ---
		switch format {
		case "webm":
			args = append(args, "-c:a", "libopus")
		default:
			args = append(args, "-c:a", "aac")
		}
		args = append(args, "-af", "aformat=channel_layouts='7.1|5.1|stereo'")

		if abr := options["audioBitrate"]; abr != "" {
			args = append(args, "-b:a", abr+"k")
		}
	}

	args = append(args, "-y", outputPath) // Overwrite output file if it exists
	return exec.Command("ffmpeg", args...)
}

func validateOptions(options map[string]string) error {
	allowedFormats := map[string]bool{"mp4": true, "webm": true, "mov": true, "avi": true, "mkv": true, "gif": true}
	if !allowedFormats[options["format"]] {
		return errors.New("invalid target format specified")
	}
	if res := options["resolution"]; res != "" {
		allowedResolutions := map[string]bool{"1080": true, "720": true, "480": true, "360": true, "240": true}
		if !allowedResolutions[res] {
			return errors.New("invalid resolution value")
		}
	}
	if br := options["bitrate"]; br != "" {
		brInt, err := strconv.Atoi(br)
		if err != nil || brInt < 100 || brInt > 10000 {
			return errors.New("bitrate must be a number between 100 and 10000")
		}
	}
	if fr := options["framerate"]; fr != "" {
		frInt, err := strconv.Atoi(fr)
		if err != nil || frInt < 1 || frInt > 60 {
			return errors.New("framerate must be a number between 1 and 60")
		}
	}
	if abr := options["audioBitrate"]; abr != "" {
		abrInt, err := strconv.Atoi(abr)
		if err != nil || abrInt < 32 || abrInt > 320 {
			return errors.New("audio bitrate must be a number between 32 and 320")
		}
	}
	return nil
}

func getVideoDuration(inputPath string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", inputPath)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	var ffprobeData ffprobeOutput
	if err := json.Unmarshal(output, &ffprobeData); err != nil {
		return 0, err
	}

	duration, err := strconv.ParseFloat(ffprobeData.Format.Duration, 64)
	if err != nil {
		return 0, err
	}
	return duration, nil
}

func handleProgressConnection(listener net.Listener, job *ConversionJob) {
	conn, err := listener.Accept()
	if err != nil {
		log.Printf("[Job %s] Progress listener failed: %v", job.ID, err)
		return
	}
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "=")
		if len(parts) == 2 && parts[0] == "out_time_us" {
			if progressTimeUs, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
				currentTimeSeconds := progressTimeUs / 1_000_000
				percentage := 0
				if job.TotalDurationSeconds > 0 {
					percentage = int((currentTimeSeconds / job.TotalDurationSeconds) * 100)
				}
				if percentage > 100 {
					percentage = 100
				}
				updateJobStatus(job.ID, "converting", "", percentage)
			}
		}
	}
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if payload != nil {
		json.NewEncoder(w).Encode(payload)
	}
}

func getJob(jobID string) (*ConversionJob, bool) {
	jobStoreMux.RLock()
	defer jobStoreMux.RUnlock()
	job, exists := jobStore[jobID]
	return job, exists
}

func updateJobStatus(jobID, status, errorMessage string, progress int) {
	jobStoreMux.Lock()
	defer jobStoreMux.Unlock()
	if job, exists := jobStore[jobID]; exists {
		job.Status = status
		job.ErrorMessage = errorMessage
		job.ProgressPercentage = progress
	}
}

func updateJobDuration(jobID string, duration float64) {
	jobStoreMux.Lock()
	defer jobStoreMux.Unlock()
	if job, exists := jobStore[jobID]; exists {
		job.TotalDurationSeconds = duration
	}
}

func handleUploadError(w http.ResponseWriter, jobID, message string, statusCode int) {
	updateJobStatus(jobID, "error", message, 0)
	http.Error(w, message, statusCode)
}

func setupDirectories() {
	log.Println("Clearing temporary directories...")
	os.RemoveAll(uploadDir)
	os.RemoveAll(convertedDir)

	os.MkdirAll(uploadDir, os.ModePerm)
	os.MkdirAll(convertedDir, os.ModePerm)
}

func parseTemplates() {
	var err error
	templates, err = template.ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("Failed to parse templates: %v", err)
	}
}
