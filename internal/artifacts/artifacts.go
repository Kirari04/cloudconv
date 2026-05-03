package artifacts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirari04/cloudconv/internal/media"
	"github.com/kirari04/cloudconv/internal/store"
)

type Result struct {
	Deleted []string `json:"deleted"`
	Errors  []string `json:"errors,omitempty"`
}

func (r Result) ErrorString() *string {
	if len(r.Errors) == 0 {
		return nil
	}
	joined := strings.Join(r.Errors, "; ")
	return &joined
}

func (r *Result) Merge(next Result) {
	r.Deleted = append(r.Deleted, next.Deleted...)
	r.Errors = append(r.Errors, next.Errors...)
}

func DeleteJobArtifacts(job *store.Job, convertedDir string) Result {
	var result Result
	seen := map[string]bool{}
	if job.OutputPath != nil {
		result.removeFile(convertedDir, *job.OutputPath, seen)
	}
	if job.ID != "" && job.TargetFormat != "" {
		predicted := filepath.Join(convertedDir, job.ID+"."+media.ExtensionFor(job.TargetFormat))
		result.removeFile(convertedDir, predicted, seen)
	}
	return result
}

func DeleteUploadArtifacts(upload *store.Upload, uploadDir string) Result {
	var result Result
	seen := map[string]bool{}
	if upload.SourcePath != nil {
		result.removeFile(uploadDir, *upload.SourcePath, seen)
		if legacyDir := legacyUploadDir(uploadDir, *upload.SourcePath); legacyDir != "" {
			result.removeDir(uploadDir, legacyDir, seen)
		}
	}
	if upload.ID != "" {
		result.removeDir(uploadDir, filepath.Join(uploadDir, upload.ID), seen)
	}
	return result
}

func (r *Result) removeFile(base, path string, seen map[string]bool) {
	clean, ok := cleanScoped(base, path)
	if !ok {
		r.Errors = append(r.Errors, fmt.Sprintf("refused to delete path outside storage: %s", path))
		return
	}
	if seen[clean] {
		return
	}
	seen[clean] = true
	if err := os.Remove(clean); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", clean, err))
		return
	}
	r.Deleted = append(r.Deleted, clean)
}

func (r *Result) removeDir(base, path string, seen map[string]bool) {
	clean, ok := cleanScoped(base, path)
	if !ok {
		r.Errors = append(r.Errors, fmt.Sprintf("refused to delete path outside storage: %s", path))
		return
	}
	if filepath.Clean(clean) == filepath.Clean(base) {
		r.Errors = append(r.Errors, fmt.Sprintf("refused to delete storage root: %s", clean))
		return
	}
	if seen[clean] {
		return
	}
	seen[clean] = true
	if _, err := os.Stat(clean); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", clean, err))
		return
	}
	if err := os.RemoveAll(clean); err != nil {
		r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", clean, err))
		return
	}
	r.Deleted = append(r.Deleted, clean)
}

func cleanScoped(base, path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return absPath, false
	}
	return absPath, true
}

func legacyUploadDir(uploadDir, sourcePath string) string {
	absUpload, err := filepath.Abs(uploadDir)
	if err != nil {
		return ""
	}
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return ""
	}
	legacyRoot := filepath.Join(absUpload, "legacy")
	rel, err := filepath.Rel(legacyRoot, absSource)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ""
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return filepath.Join(legacyRoot, parts[0])
}
