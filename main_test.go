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
	r.Get("/api/uploads/{uploadId}/status", statusHandler)

	// Start the background workers for the test
	go worker()
	go cleanupWorker()

	return httptest.NewServer(r)
}

// createUploadRequest is a helper to build the multipart/form-data request for file uploads.
func createUploadRequest(url, filePath string, options map[string]string, includeFile bool) (*http.Request, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if includeFile {
		file, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		part, err := writer.CreateFormFile("videoFile", filepath.Base(filePath))
		if err != nil {
			return nil, err
		}
		io.Copy(part, file)
	}

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

	statusURL := serverURL + "/api/uploads/" + jobID + "/status"

	for {
		select {
		case <-timeout:
			t.Fatalf("Polling for job %s timed out", jobID)
		case <-ticker.C:
			resp, err := http.Get(statusURL)
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

	t.Run("Successful Conversions", func(t *testing.T) {
		testCases := []struct {
			name           string
			options        map[string]string
			expectedSuffix string
		}{
			{
				name:           "MP4 Conversion - Default",
				options:        map[string]string{"format": "mp4"},
				expectedSuffix: ".mp4",
			},
			{
				name:           "MP4 Conversion - With Options",
				options:        map[string]string{"format": "mp4", "resolution": "360", "bitrate": "1000", "framerate": "24", "audioBitrate": "128"},
				expectedSuffix: ".mp4",
			},
			{
				name:           "WebM Conversion",
				options:        map[string]string{"format": "webm", "resolution": "480", "bitrate": "800"},
				expectedSuffix: ".webm",
			},
			{
				name:           "MOV Conversion",
				options:        map[string]string{"format": "mov", "resolution": "360"},
				expectedSuffix: ".mov",
			},
			{
				name:           "AVI Conversion",
				options:        map[string]string{"format": "avi", "resolution": "360"},
				expectedSuffix: ".avi",
			},
			{
				name:           "MKV Conversion",
				options:        map[string]string{"format": "mkv", "resolution": "360"},
				expectedSuffix: ".mkv",
			},
			{
				name:           "GIF Conversion - Looped",
				options:        map[string]string{"format": "gif", "resolution": "240", "gifLoop": "true", "framerate": "10"},
				expectedSuffix: ".gif",
			},
			{
				name:           "GIF Conversion - Not Looped",
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
				resp.Body.Close()

				// 2. Upload file
				uploadURL := server.URL + "/api/uploads/" + jobID
				req, err := createUploadRequest(uploadURL, testFilePath, tc.options, true)
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
				uploadResp.Body.Close()

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
					os.Remove(convertedFilePath)
				}
			})
		}
	})

	t.Run("Invalid User Inputs", func(t *testing.T) {
		invalidTestCases := []struct {
			name        string
			options     map[string]string
			expectedMsg string
			includeFile bool
		}{
			// --- Format Validation (5 cases) ---
			{name: "Format - Not in list", options: map[string]string{"format": "exe"}, expectedMsg: "invalid target format specified", includeFile: true},
			{name: "Format - Empty String", options: map[string]string{"format": ""}, expectedMsg: "target format must be specified", includeFile: true},
			{name: "Format - Not Specified", options: map[string]string{"resolution": "1080"}, expectedMsg: "target format must be specified", includeFile: true},
			{name: "Format - Uppercase", options: map[string]string{"format": "MP4"}, expectedMsg: "invalid target format specified", includeFile: true},
			{name: "Format - With special chars", options: map[string]string{"format": "mp4;"}, expectedMsg: "invalid target format specified", includeFile: true},

			// --- Resolution Validation (7 cases) ---
			{name: "Resolution - Not in list", options: map[string]string{"format": "mp4", "resolution": "9999"}, expectedMsg: "invalid resolution value", includeFile: true},
			{name: "Resolution - Non-numeric", options: map[string]string{"format": "mp4", "resolution": "FullHD"}, expectedMsg: "resolution must be a number", includeFile: true},
			{name: "Resolution - Negative", options: map[string]string{"format": "mp4", "resolution": "-720"}, expectedMsg: "invalid resolution value", includeFile: true},
			{name: "Resolution - Floating Point", options: map[string]string{"format": "mp4", "resolution": "720.5"}, expectedMsg: "resolution must be a number", includeFile: true},
			{name: "Resolution - Zero", options: map[string]string{"format": "mp4", "resolution": "0"}, expectedMsg: "invalid resolution value", includeFile: true},
			{name: "Resolution - With units", options: map[string]string{"format": "mp4", "resolution": "720p"}, expectedMsg: "resolution must be a number", includeFile: true},
			{name: "Resolution - With spaces", options: map[string]string{"format": "mp4", "resolution": " 720 "}, expectedMsg: "resolution must be a number", includeFile: true},

			// --- Video Bitrate Validation (8 cases) ---
			{name: "Bitrate - Non-numeric", options: map[string]string{"format": "mp4", "bitrate": "abc"}, expectedMsg: "bitrate must be a number", includeFile: true},
			{name: "Bitrate - Too Low", options: map[string]string{"format": "mp4", "bitrate": "50"}, expectedMsg: "bitrate must be a number between 100 and 10000", includeFile: true},
			{name: "Bitrate - Too High", options: map[string]string{"format": "mp4", "bitrate": "20000"}, expectedMsg: "bitrate must be a number between 100 and 10000", includeFile: true},
			{name: "Bitrate - Negative", options: map[string]string{"format": "mp4", "bitrate": "-1000"}, expectedMsg: "bitrate must be a number between 100 and 10000", includeFile: true},
			{name: "Bitrate - Floating Point", options: map[string]string{"format": "mp4", "bitrate": "1000.5"}, expectedMsg: "bitrate must be a number", includeFile: true},
			{name: "Bitrate - Zero", options: map[string]string{"format": "mp4", "bitrate": "0"}, expectedMsg: "bitrate must be a number between 100 and 10000", includeFile: true},
			{name: "Bitrate - With units", options: map[string]string{"format": "mp4", "bitrate": "1000k"}, expectedMsg: "bitrate must be a number", includeFile: true},
			{name: "Bitrate - With spaces", options: map[string]string{"format": "mp4", "bitrate": " 1000 "}, expectedMsg: "bitrate must be a number", includeFile: true},

			// --- Audio Bitrate Validation (7 cases) ---
			{name: "Audio Bitrate - Too High", options: map[string]string{"format": "mp4", "audioBitrate": "1000"}, expectedMsg: "audio bitrate must be a number between 32 and 320", includeFile: true},
			{name: "Audio Bitrate - Too Low", options: map[string]string{"format": "mp4", "audioBitrate": "16"}, expectedMsg: "audio bitrate must be a number between 32 and 320", includeFile: true},
			{name: "Audio Bitrate - Non-numeric", options: map[string]string{"format": "mp4", "audioBitrate": "high_quality"}, expectedMsg: "audio bitrate must be a number", includeFile: true},
			{name: "Audio Bitrate - Negative", options: map[string]string{"format": "mp4", "audioBitrate": "-128"}, expectedMsg: "audio bitrate must be a number between 32 and 320", includeFile: true},
			{name: "Audio Bitrate - Floating Point", options: map[string]string{"format": "mp4", "audioBitrate": "128.5"}, expectedMsg: "audio bitrate must be a number", includeFile: true},
			{name: "Audio Bitrate - Zero", options: map[string]string{"format": "mp4", "audioBitrate": "0"}, expectedMsg: "audio bitrate must be a number between 32 and 320", includeFile: true},
			{name: "Audio Bitrate - With units", options: map[string]string{"format": "mp4", "audioBitrate": "128k"}, expectedMsg: "audio bitrate must be a number", includeFile: true},

			// --- Framerate Validation (6 cases) ---
			{name: "Framerate - Negative", options: map[string]string{"format": "mp4", "framerate": "-10"}, expectedMsg: "framerate must be a number between 1 and 60", includeFile: true},
			{name: "Framerate - Too High", options: map[string]string{"format": "mp4", "framerate": "120"}, expectedMsg: "framerate must be a number between 1 and 60", includeFile: true},
			{name: "Framerate - Non-numeric", options: map[string]string{"format": "mp4", "framerate": "sixty"}, expectedMsg: "framerate must be a number", includeFile: true},
			{name: "Framerate - Zero", options: map[string]string{"format": "mp4", "framerate": "0"}, expectedMsg: "framerate must be a number between 1 and 60", includeFile: true},
			{name: "Framerate - Floating Point", options: map[string]string{"format": "mp4", "framerate": "29.97"}, expectedMsg: "framerate must be a number", includeFile: true},
			{name: "Framerate - With units", options: map[string]string{"format": "mp4", "framerate": "30fps"}, expectedMsg: "framerate must be a number", includeFile: true},

			// --- GIF-Specific Validation (6 cases) ---
			{name: "GIF - Invalid Resolution", options: map[string]string{"format": "gif", "resolution": "9999"}, expectedMsg: "invalid resolution value", includeFile: true},
			{name: "GIF - Bitrate provided", options: map[string]string{"format": "gif", "bitrate": "1000"}, expectedMsg: "bitrate is not a valid option for format gif", includeFile: true},
			{name: "GIF - Audio Bitrate provided", options: map[string]string{"format": "gif", "audioBitrate": "128"}, expectedMsg: "audioBitrate is not a valid option for format gif", includeFile: true},
			{name: "GIF - Invalid loop value (yes)", options: map[string]string{"format": "gif", "resolution": "360", "gifLoop": "yes"}, expectedMsg: "gifLoop value must be 'true' or 'false'", includeFile: true},
			{name: "GIF - Invalid loop value (1)", options: map[string]string{"format": "gif", "resolution": "360", "gifLoop": "1"}, expectedMsg: "gifLoop value must be 'true' or 'false'", includeFile: true},
			{name: "GIF - Non-numeric resolution", options: map[string]string{"format": "gif", "resolution": "small"}, expectedMsg: "resolution must be a number", includeFile: true},

			// --- Cross-Parameter, Unknown & File Validation (7 cases) ---
			{name: "Non-GIF - gifLoop provided", options: map[string]string{"format": "mp4", "gifLoop": "true"}, expectedMsg: "gifLoop is only a valid option for format gif", includeFile: true},
			{name: "Unknown Option - quality", options: map[string]string{"format": "mp4", "quality": "best"}, expectedMsg: "unknown option specified: quality", includeFile: true},
			{name: "Unknown Option - crf", options: map[string]string{"format": "mp4", "crf": "23"}, expectedMsg: "unknown option specified: crf", includeFile: true},
			{name: "Unknown Option - empty value", options: map[string]string{"format": "mp4", "some_random_param": ""}, expectedMsg: "unknown option specified: some_random_param", includeFile: true},
			{name: "Multiple Invalid - Format and Bitrate", options: map[string]string{"format": "exe", "bitrate": "abc"}, expectedMsg: "invalid target format specified", includeFile: true},
			{name: "Multiple Invalid - Resolution and Framerate", options: map[string]string{"format": "mp4", "resolution": "abc", "framerate": "abc"}, expectedMsg: "resolution must be a number", includeFile: true},
			{name: "File Upload - No file part in form", options: map[string]string{"format": "mp4"}, expectedMsg: "Could not retrieve file.", includeFile: false},
			{name: "Empty Options Map", options: map[string]string{}, expectedMsg: "target format must be specified", includeFile: true},
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
				req, err := createUploadRequest(uploadURL, testFilePath, tc.options, tc.includeFile)
				if err != nil {
					t.Fatalf("Failed to create upload request: %v", err)
				}

				uploadResp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("Failed to perform upload request: %v", err)
				}
				defer uploadResp.Body.Close()

				if uploadResp.StatusCode != http.StatusBadRequest {
					bodyBytes, _ := io.ReadAll(uploadResp.Body)
					t.Errorf("Expected status 400 Bad Request, got %d. Body: %s", uploadResp.StatusCode, string(bodyBytes))
				}

				bodyBytes, _ := io.ReadAll(uploadResp.Body)
				bodyString := strings.TrimSpace(string(bodyBytes))
				if !strings.Contains(bodyString, tc.expectedMsg) {
					t.Errorf("Expected response body to contain '%s', but got '%s'", tc.expectedMsg, bodyString)
				}

				// 3. Verify job status is 'error' (unless the upload itself failed pre-job-update)
				jobStoreMutex.RLock()
				job, exists := jobStore[jobID]
				jobStoreMutex.RUnlock()

				if !exists {
					t.Fatal("Job was not found in the store after failed upload")
				}
				// The job status is only set to error if validation runs.
				// If the file is missing, the handler exits before validation.
				if tc.includeFile {
					if job.Status != "error" {
						t.Errorf("Expected job status to be 'error', but got '%s'", job.Status)
					}
					if !strings.Contains(job.ErrorMessage, tc.expectedMsg) {
						t.Errorf("Expected job error message to contain '%s', but got '%s'", tc.expectedMsg, job.ErrorMessage)
					}
				}
			})
		}
	})
}
