package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// TestMain sets up the required directories before running tests and cleans them up after.
func TestMain(m *testing.M) {
	// Setup: Create test directories
	setupDirectories()

	// Run the tests
	code := m.Run()

	// Teardown: Clean up test directories
	os.RemoveAll(uploadDir)
	os.RemoveAll(convertedDir)

	os.Exit(code)
}

// setupTestServer configures a new chi router and test server.
func setupTestServer() *httptest.Server {
	r := chi.NewRouter()
	r.Post("/api/uploads/initiate", initiateUploadHandler)
	r.Post("/api/uploads/{uploadId}", fileUploadHandler)
	r.Get("/{uploadId}/status", statusHandler)

	// Start the background workers for the test
	go worker()
	go cleanupWorker()

	return httptest.NewServer(r)
}

// createUploadRequest is a helper to build the multipart/form-data request for file uploads.
func createUploadRequest(url, filePath string, options map[string]string) (*http.Request, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("videoFile", filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	io.Copy(part, file)

	for key, val := range options {
		_ = writer.WriteField(key, val)
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, nil
}

// pollUntilFinished is a helper to check the job status endpoint until it's finished or errors out.
func pollUntilFinished(t *testing.T, serverURL, jobID string) *ConversionJob {
	var job *ConversionJob
	timeout := time.After(60 * time.Second) // 1-minute timeout for conversion
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatalf("Polling for job %s timed out", jobID)
		case <-ticker.C:
			resp, err := http.Get(serverURL + "/" + jobID + "/status")
			if err != nil {
				t.Fatalf("Failed to poll status for job %s: %v", jobID, err)
			}

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("Expected status 200 OK from status endpoint, got %d", resp.StatusCode)
			}

			if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
				t.Fatalf("Failed to decode status response: %v", err)
			}
			resp.Body.Close()

			if job.Status == "finished" || job.Status == "error" {
				return job
			}
		}
	}
}

// TestConversionFunctionality runs integration tests for different conversion scenarios.
// It assumes that `ffmpeg` and `ffprobe` are installed on the system running the test.
func TestConversionFunctionality(t *testing.T) {
	server := setupTestServer()
	defer server.Close()

	const testFilePath = "testdata/test5.mkv"
	if _, err := os.Stat(testFilePath); os.IsNotExist(err) {
		t.Fatalf("Test video file not found: %s. Please create a 'testdata' directory with the 'test5.mkv' file.", testFilePath)
	}

	// Reset global state for a clean test run before starting subtests.
	jobStore = make(map[string]*ConversionJob)
	jobQueue = []string{}

	testCases := []struct {
		name           string
		options        map[string]string
		expectedSuffix string
	}{
		{
			name:           "Successful MP4 Conversion - Default",
			options:        map[string]string{"format": "mp4"},
			expectedSuffix: ".mp4",
		},
		{
			name:           "Successful MP4 Conversion - With Options",
			options:        map[string]string{"format": "mp4", "resolution": "360", "bitrate": "1000", "framerate": "24", "audioBitrate": "128"},
			expectedSuffix: ".mp4",
		},
		{
			name:           "Successful WebM Conversion",
			options:        map[string]string{"format": "webm", "resolution": "480", "bitrate": "800"},
			expectedSuffix: ".webm",
		},
		{
			name:           "Successful MOV Conversion",
			options:        map[string]string{"format": "mov", "resolution": "360"},
			expectedSuffix: ".mov",
		},
		{
			name:           "Successful AVI Conversion",
			options:        map[string]string{"format": "avi", "resolution": "360"},
			expectedSuffix: ".avi",
		},
		{
			name:           "Successful MKV Conversion",
			options:        map[string]string{"format": "mkv", "resolution": "360"},
			expectedSuffix: ".mkv",
		},
		{
			name:           "Successful GIF Conversion - Looped",
			options:        map[string]string{"format": "gif", "resolution": "240", "gifLoop": "true", "framerate": "10"},
			expectedSuffix: ".gif",
		},
		{
			name:           "Successful GIF Conversion - Not Looped",
			options:        map[string]string{"format": "gif", "resolution": "360", "gifLoop": "false"},
			expectedSuffix: ".gif",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Initiate upload
			resp, err := http.Post(server.URL+"/api/uploads/initiate", "application/json", nil)
			if err != nil {
				t.Fatalf("Failed to initiate upload: %v", err)
			}
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("Expected status 201 Created, got %d", resp.StatusCode)
			}

			var initResponse map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&initResponse); err != nil {
				t.Fatalf("Failed to decode init response: %v", err)
			}
			jobID := initResponse["uploadId"]
			if jobID == "" {
				t.Fatal("Did not receive a valid uploadId")
			}

			// 2. Upload file
			uploadURL := server.URL + "/api/uploads/" + jobID
			req, err := createUploadRequest(uploadURL, testFilePath, tc.options)
			if err != nil {
				t.Fatalf("Failed to create upload request: %v", err)
			}

			uploadResp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Failed to perform upload request: %v", err)
			}
			if uploadResp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(uploadResp.Body)
				t.Fatalf("Expected status 200 OK on upload, got %d. Body: %s", uploadResp.StatusCode, string(bodyBytes))
			}

			// 3. Poll for status until finished
			finalJob := pollUntilFinished(t, server.URL, jobID)

			if finalJob.Status != "finished" {
				t.Fatalf("Expected job status to be 'finished', but got '%s' with error: %s", finalJob.Status, finalJob.ErrorMessage)
			}
			if finalJob.ProgressPercentage != 100 {
				t.Errorf("Expected progress to be 100, got %d", finalJob.ProgressPercentage)
			}
			if !strings.HasSuffix(finalJob.DownloadURL, tc.expectedSuffix) {
				t.Errorf("Expected download URL to end with %s, got %s", tc.expectedSuffix, finalJob.DownloadURL)
			}

			// 4. Verify converted file exists
			convertedFileName := jobID + tc.expectedSuffix
			convertedFilePath := filepath.Join(convertedDir, convertedFileName)
			if _, err := os.Stat(convertedFilePath); os.IsNotExist(err) {
				t.Errorf("Converted file was not found at %s", convertedFilePath)
			} else {
				// Optional: clean up the successfully converted file to save space during long test runs
				os.Remove(convertedFilePath)
			}
		})
	}

	t.Run("Invalid User Inputs", func(t *testing.T) {
		invalidTestCases := []struct {
			name        string
			options     map[string]string
			expectedMsg string
		}{
			{
				name:        "Invalid Format - Empty",
				options:     map[string]string{"format": ""},
				expectedMsg: "format cannot be empty",
			},
			{
				name:        "Invalid Format - Case Sensitivity",
				options:     map[string]string{"format": "MP4"},
				expectedMsg: "invalid target format specified",
			},
			{
				name:        "Invalid Resolution - Non-numeric",
				options:     map[string]string{"format": "mp4", "resolution": "FullHD"},
				expectedMsg: "resolution must be a number",
			},
			{
				name:        "Invalid Resolution - With 'p' suffix",
				options:     map[string]string{"format": "mp4", "resolution": "1080p"},
				expectedMsg: "invalid resolution value",
			},
			{
				name:        "Invalid Resolution - Zero",
				options:     map[string]string{"format": "mp4", "resolution": "0"},
				expectedMsg: "invalid resolution value",
			},
			{
				name:        "Invalid Resolution - Empty",
				options:     map[string]string{"format": "mp4", "resolution": ""},
				expectedMsg: "resolution cannot be empty",
			},
			{
				name:        "Invalid Bitrate - Too High",
				options:     map[string]string{"format": "mp4", "bitrate": "20000"},
				expectedMsg: "bitrate must be a number between 100 and 10000",
			},
			{
				name:        "Invalid Bitrate - Zero",
				options:     map[string]string{"format": "mp4", "bitrate": "0"},
				expectedMsg: "bitrate must be a number between 100 and 10000",
			},
			{
				name:        "Invalid Bitrate - Negative",
				options:     map[string]string{"format": "mp4", "bitrate": "-1000"},
				expectedMsg: "bitrate must be a number between 100 and 10000",
			},
			{
				name:        "Invalid Bitrate - Floating Point",
				options:     map[string]string{"format": "mp4", "bitrate": "1500.5"},
				expectedMsg: "bitrate must be a whole number",
			},
			{
				name:        "Invalid Framerate - Too High",
				options:     map[string]string{"format": "mp4", "framerate": "120"},
				expectedMsg: "framerate must be a number between 1 and 60",
			},
			{
				name:        "Invalid Framerate - Zero",
				options:     map[string]string{"format": "mp4", "framerate": "0"},
				expectedMsg: "framerate must be a number between 1 and 60",
			},
			{
				name:        "Invalid Framerate - Non-numeric",
				options:     map[string]string{"format": "mp4", "framerate": "sixty"},
				expectedMsg: "framerate must be a number",
			},
			{
				name:        "Invalid Framerate - Floating Point",
				options:     map[string]string{"format": "mp4", "framerate": "29.97"},
				expectedMsg: "framerate must be a whole number",
			},
			{
				name:        "Invalid Audio Bitrate - Non-numeric",
				options:     map[string]string{"format": "mp4", "audioBitrate": "high_quality"},
				expectedMsg: "audio bitrate must be a number",
			},
			{
				name:        "Invalid Audio Bitrate - Too Low",
				options:     map[string]string{"format": "mp4", "audioBitrate": "16"},
				expectedMsg: "audio bitrate must be a number between 32 and 320",
			},
			{
				name:        "Invalid Audio Bitrate - Zero",
				options:     map[string]string{"format": "mp4", "audioBitrate": "0"},
				expectedMsg: "audio bitrate must be a number between 32 and 320",
			},
			{
				name:        "Invalid Audio Bitrate - Negative",
				options:     map[string]string{"format": "mp4", "audioBitrate": "-128"},
				expectedMsg: "audio bitrate must be a number between 32 and 320",
			},
			{
				name:        "Invalid Codec - Not Supported",
				options:     map[string]string{"format": "mp4", "codec": "vp9"},
				expectedMsg: "codec vp9 is not supported for format mp4",
			},
			{
				name:        "Invalid Codec - Fictional",
				options:     map[string]string{"format": "webm", "codec": "supercodec"},
				expectedMsg: "invalid video codec specified",
			},
			{
				name:        "Invalid Codec - Empty",
				options:     map[string]string{"format": "mp4", "codec": ""},
				expectedMsg: "codec cannot be empty",
			},
			{
				name:        "Invalid Audio Codec - Fictional",
				options:     map[string]string{"format": "mp4", "audioCodec": "superaudio"},
				expectedMsg: "invalid audio codec specified",
			},
			{
				name:        "Invalid Audio Codec - Not Supported",
				options:     map[string]string{"format": "mp4", "audioCodec": "vorbis"},
				expectedMsg: "audio codec vorbis is not supported for format mp4",
			},
			{
				name:        "Invalid CRF - Non-numeric",
				options:     map[string]string{"format": "mp4", "crf": "high"},
				expectedMsg: "crf must be a number",
			},
			{
				name:        "Invalid CRF - Too High",
				options:     map[string]string{"format": "mp4", "crf": "60"},
				expectedMsg: "crf must be a number between 0 and 51",
			},
			{
				name:        "Invalid CRF - Negative",
				options:     map[string]string{"format": "mp4", "crf": "-1"},
				expectedMsg: "crf must be a number between 0 and 51",
			},
			{
				name:        "Invalid CRF - Floating Point",
				options:     map[string]string{"format": "mp4", "crf": "23.5"},
				expectedMsg: "crf must be a whole number",
			},
			{
				name:        "Invalid Preset - Fictional",
				options:     map[string]string{"format": "mp4", "preset": "ultra-fast"},
				expectedMsg: "invalid preset specified",
			},
			{
				name:        "Invalid Preset - Empty",
				options:     map[string]string{"format": "mp4", "preset": ""},
				expectedMsg: "preset cannot be empty",
			},
			{
				name:        "Invalid Aspect Ratio - Wrong Separator",
				options:     map[string]string{"format": "mp4", "aspectRatio": "16-9"},
				expectedMsg: "invalid aspect ratio format, expected W:H",
			},
			{
				name:        "Invalid Aspect Ratio - Non-numeric",
				options:     map[string]string{"format": "mp4", "aspectRatio": "sixteen:nine"},
				expectedMsg: "invalid aspect ratio format, expected W:H",
			},
			{
				name:        "Invalid Aspect Ratio - Incomplete",
				options:     map[string]string{"format": "mp4", "aspectRatio": "16:"},
				expectedMsg: "invalid aspect ratio format, expected W:H",
			},
			{
				name:        "Invalid Aspect Ratio - Zero",
				options:     map[string]string{"format": "mp4", "aspectRatio": "4:0"},
				expectedMsg: "aspect ratio dimensions cannot be zero",
			},
			{
				name:        "Unknown Option",
				options:     map[string]string{"format": "mp4", "quality": "best"},
				expectedMsg: "unknown option specified: quality",
			},
			{
				name:        "No Format Specified",
				options:     map[string]string{"resolution": "1080"},
				expectedMsg: "target format must be specified",
			},
			{
				name:        "Empty Options Map",
				options:     map[string]string{},
				expectedMsg: "target format must be specified",
			},
			{
				name:        "Multiple Errors - Invalid Format and Bitrate",
				options:     map[string]string{"format": "exe", "bitrate": "abc"},
				expectedMsg: "invalid target format specified", // Assuming format is checked first
			},
			{
				name:        "Multiple Errors - Invalid Resolution and Framerate",
				options:     map[string]string{"format": "mp4", "resolution": "9999", "framerate": "-10"},
				expectedMsg: "invalid resolution value", // Assuming resolution is checked first
			},
			{
				name:        "Invalid MOV Codec",
				options:     map[string]string{"format": "mov", "codec": "vp9"},
				expectedMsg: "codec vp9 is not supported for format mov",
			},
			{
				name:        "Invalid WEBM Audio Codec",
				options:     map[string]string{"format": "webm", "audioCodec": "mp3"},
				expectedMsg: "audio codec mp3 is not supported for format webm",
			},
			{
				name:        "MOV with CRF",
				options:     map[string]string{"format": "mov", "crf": "20"},
				expectedMsg: "crf is not a valid option for format mov",
			},
			{
				name:        "CRF and Bitrate Conflict",
				options:     map[string]string{"format": "mp4", "crf": "22", "bitrate": "2000"},
				expectedMsg: "crf and bitrate cannot be specified simultaneously",
			},
			{
				name:        "Audio Bitrate on Mute",
				options:     map[string]string{"format": "mp4", "audioBitrate": "128", "mute": "true"},
				expectedMsg: "audioBitrate cannot be set when mute is true",
			},
			{
				name:        "Invalid Mute Value",
				options:     map[string]string{"format": "mp4", "mute": "yes"},
				expectedMsg: "mute value must be 'true' or 'false'",
			},
			{
				name:        "Invalid Keyframes Interval - Non-numeric",
				options:     map[string]string{"format": "mp4", "keyframes": "auto"},
				expectedMsg: "keyframes interval must be a number",
			},
			{
				name:        "Invalid Keyframes Interval - Negative",
				options:     map[string]string{"format": "mp4", "keyframes": "-60"},
				expectedMsg: "keyframes interval must be a positive number",
			},
			{
				name:        "Invalid Watermark Opacity - Non-numeric",
				options:     map[string]string{"format": "mp4", "watermarkOpacity": "low"},
				expectedMsg: "watermark opacity must be a number",
			},
			{
				name:        "Invalid Watermark Opacity - Out of Range",
				options:     map[string]string{"format": "mp4", "watermarkOpacity": "1.5"},
				expectedMsg: "watermark opacity must be between 0.0 and 1.0",
			},
			{
				name:        "Invalid Watermark Position",
				options:     map[string]string{"format": "mp4", "watermarkPosition": "middle-center"},
				expectedMsg: "invalid watermark position specified",
			},
			{
				name:        "Watermark Option Missing File",
				options:     map[string]string{"format": "mp4", "watermarkPosition": "top-right"},
				expectedMsg: "watermark file must be specified when using watermark options",
			},
		}

		for _, tc := range invalidTestCases {
			t.Run(tc.name, func(t *testing.T) {
				// 1. Initiate upload
				resp, err := http.Post(server.URL+"/api/uploads/initiate", "application/json", nil)
				if err != nil {
					t.Fatalf("Failed to initiate upload: %v", err)
				}
				var initResponse map[string]string
				json.NewDecoder(resp.Body).Decode(&initResponse)
				jobID := initResponse["uploadId"]
				resp.Body.Close()

				// 2. Upload file with invalid options
				uploadURL := server.URL + "/api/uploads/" + jobID
				req, err := createUploadRequest(uploadURL, testFilePath, tc.options)
				if err != nil {
					t.Fatalf("Failed to create upload request: %v", err)
				}

				uploadResp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("Failed to perform upload request: %v", err)
				}
				defer uploadResp.Body.Close()

				if uploadResp.StatusCode != http.StatusBadRequest {
					t.Errorf("Expected status 400 Bad Request, got %d", uploadResp.StatusCode)
				}

				bodyBytes, _ := io.ReadAll(uploadResp.Body)
				bodyString := strings.TrimSpace(string(bodyBytes))
				if !strings.Contains(bodyString, tc.expectedMsg) {
					t.Errorf("Expected response body to contain '%s', but got '%s'", tc.expectedMsg, bodyString)
				}

				// 3. Verify job status is 'error'
				job, exists := getJob(jobID)
				if !exists {
					t.Fatal("Job was not found in the store after failed upload")
				}
				if job.Status != "error" {
					t.Errorf("Expected job status to be 'error', but got '%s'", job.Status)
				}
				if !strings.Contains(job.ErrorMessage, tc.expectedMsg) {
					t.Errorf("Expected job error message to contain '%s', but got '%s'", tc.expectedMsg, job.ErrorMessage)
				}
			})
		}
	})
}
