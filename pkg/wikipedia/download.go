package wikipedia

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// AvailableDumps lists available Wikipedia dump sources
var AvailableDumps = map[string]string{
	// Kiwix ZIM files - Wikipedia Top 100 (~313MB)
	"top100": "https://download.kiwix.org/zim/wikipedia/wikipedia_en_100_2025-10.zim",
	// Kiwix ZIM files - Wikipedia Top 100 Mini (~2MB, good for testing)
	"top100-mini": "https://download.kiwix.org/zim/wikipedia/wikipedia_en_100_mini_2025-10.zim",
	// Wikipedia English - 48GB
	"en": "https://download.kiwix.org/zim/wikipedia/wikipedia_en_all_nopic_2025-12.zim",
	// Wikipedia English Maxi - 111GB
	"en_maxi": "https://download.kiwix.org/zim/wikipedia/wikipedia_en_all_maxi_2025-08.zim",
}

// DownloadProgress represents download progress
type DownloadProgress struct {
	TotalBytes      int64
	DownloadedBytes int64
	Percentage      float64
}

// ProgressCallback is called during download with progress updates
type ProgressCallback func(progress DownloadProgress)

// DownloadDump downloads a Wikipedia ZIM dump
func DownloadDump(language, destDir string, callback ProgressCallback) (string, error) {
	url, ok := AvailableDumps[language]
	if !ok {
		return "", fmt.Errorf("unknown language/dump: %s. Available: %v", language, getAvailableLanguages())
	}

	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Extract filename from URL
	parts := strings.Split(url, "/")
	filename := parts[len(parts)-1]
	destPath := filepath.Join(destDir, filename)

	// Check if file already exists
	if _, err := os.Stat(destPath); err == nil {
		fmt.Printf("File %s already exists, skipping download\n", destPath)
		return destPath, nil
	}

	// Create HTTP request
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to start download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP error: %s", resp.Status)
	}

	// Create destination file
	tempPath := destPath + ".tmp"
	out, err := os.Create(tempPath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Get total size
	totalSize := resp.ContentLength

	// Download with progress
	var downloaded int64
	buffer := make([]byte, 32*1024) // 32KB buffer

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			_, writeErr := out.Write(buffer[:n])
			if writeErr != nil {
				os.Remove(tempPath)
				return "", fmt.Errorf("failed to write: %w", writeErr)
			}
			downloaded += int64(n)

			if callback != nil {
				progress := DownloadProgress{
					TotalBytes:      totalSize,
					DownloadedBytes: downloaded,
				}
				if totalSize > 0 {
					progress.Percentage = float64(downloaded) / float64(totalSize) * 100
				}
				callback(progress)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			os.Remove(tempPath)
			return "", fmt.Errorf("download error: %w", err)
		}
	}

	// Rename temp file to final destination
	if err := os.Rename(tempPath, destPath); err != nil {
		os.Remove(tempPath)
		return "", fmt.Errorf("failed to rename file: %w", err)
	}

	return destPath, nil
}

// ListAvailableDumps returns a list of available dumps
func ListAvailableDumps() map[string]string {
	return AvailableDumps
}

func getAvailableLanguages() []string {
	var langs []string
	for k := range AvailableDumps {
		langs = append(langs, k)
	}
	return langs
}

// GetDumpPath returns the expected path for a dump file
func GetDumpPath(language, dataDir string) string {
	url, ok := AvailableDumps[language]
	if !ok {
		return ""
	}
	parts := strings.Split(url, "/")
	filename := parts[len(parts)-1]
	return filepath.Join(dataDir, filename)
}

// FindZIMFiles finds all ZIM files in a directory
func FindZIMFiles(dir string) ([]string, error) {
	var files []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".zim") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	return files, nil
}
