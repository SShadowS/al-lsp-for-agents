package wrapper

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// telemDownloadFn is the callback type used to emit download.failure telemetry.
// stage is one of "lookup", "download", "extract". httpStatus is 0 when not from HTTP.
// urlHost is the hostname extracted from the URL, or empty string.
type telemDownloadFn func(stage, errMsg string, httpStatus int, urlHost string)

// extractVsix extracts the extension/ subdirectory from a .vsix file to targetDir.
// Only files under the extension/ prefix are extracted, with that prefix stripped.
func extractVsix(vsixPath, targetDir string) error {
	r, err := zip.OpenReader(vsixPath)
	if err != nil {
		return fmt.Errorf("failed to open vsix: %w", err)
	}
	defer r.Close()

	const prefix = "extension/"

	for _, f := range r.File {
		// Only extract files under extension/
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}

		// Strip the extension/ prefix
		relPath := strings.TrimPrefix(f.Name, prefix)
		if relPath == "" {
			continue
		}

		targetPath := filepath.Join(targetDir, filepath.FromSlash(relPath))

		// Zip slip guard: ensure extracted path stays within targetDir
		cleanTarget := filepath.Clean(targetPath) + string(os.PathSeparator)
		cleanBase := filepath.Clean(targetDir) + string(os.PathSeparator)
		if !strings.HasPrefix(cleanTarget, cleanBase) {
			return fmt.Errorf("illegal path in vsix: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0755)
			continue
		}

		// Create parent directories
		os.MkdirAll(filepath.Dir(targetPath), 0755)

		outFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create %s: %w", targetPath, err)
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return fmt.Errorf("failed to open %s in vsix: %w", f.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return fmt.Errorf("failed to extract %s: %w", f.Name, err)
		}
	}

	return nil
}

// downloadFile downloads a URL to a local file path.
// telemFn is called before returning any error so the caller can emit telemetry
// with the correct HTTP status code (0 for non-HTTP errors).
func downloadFile(rawURL, destPath string, telemFn telemDownloadFn) error {
	os.MkdirAll(filepath.Dir(destPath), 0755)

	urlHost := hostOf(rawURL)

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(rawURL)
	if err != nil {
		errMsg := fmt.Sprintf("download failed: %s", err)
		telemFn("download", errMsg, 0, urlHost)
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("download returned status %d", resp.StatusCode)
		telemFn("download", errMsg, resp.StatusCode, urlHost)
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", destPath, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		errMsg := fmt.Sprintf("failed to write download: %s", err)
		telemFn("download", errMsg, 0, urlHost)
		return fmt.Errorf("failed to write download: %w", err)
	}

	return nil
}

// hostOf extracts the hostname from a URL string.
// Returns empty string if parsing fails.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
