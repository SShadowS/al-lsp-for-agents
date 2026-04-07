package wrapper

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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

// downloadFile downloads a URL to a local file path
func downloadFile(url, destPath string) error {
	os.MkdirAll(filepath.Dir(destPath), 0755)

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", destPath, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write download: %w", err)
	}

	return nil
}
