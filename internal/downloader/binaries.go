package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DownloadStats struct {
	Tool            string
	URL             string
	Phase           string
	DownloadedBytes int64
	TotalBytes      int64
}

type DownloadProgressFunc func(DownloadStats)

func emitDownloadProgress(progress DownloadProgressFunc, stats DownloadStats) {
	if progress == nil {
		return
	}
	progress(stats)
}

type countingWriter struct {
	total int64
	onAdd func(int64)
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.total += int64(n)
	if w.onAdd != nil {
		w.onAdd(w.total)
	}
	return n, nil
}

func appDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve cache dir: %w", err)
	}
	dir = filepath.Join(dir, "ytgui")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("could not create app dir: %w", err)
	}
	return dir, nil
}

const (
	defaultFFmpegArchiveURL = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
	envFFmpegURL            = "YTGUI_FFMPEG_URL"
	downloadTimeout         = 30 * time.Minute
	maxDownloadAttempts     = 3
)

func ffmpegSourceURL() string {
	if v := strings.TrimSpace(os.Getenv(envFFmpegURL)); v != "" {
		return v
	}
	return defaultFFmpegArchiveURL
}

func looksLikeWindowsExe(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var sig [2]byte
	n, err := f.Read(sig[:])
	if err != nil && err != io.EOF {
		return false, err
	}
	return n == 2 && bytes.Equal(sig[:], []byte("MZ")), nil
}

func looksLikeZip(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var sig [4]byte
	n, err := f.Read(sig[:])
	if err != nil && err != io.EOF {
		return false, err
	}
	return n == 4 && bytes.Equal(sig[:], []byte("PK\x03\x04")), nil
}

func shouldRetryDownload(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	return false
}

func downloadToTempOnce(ctx context.Context, client *http.Client, tool, url, prefix string, progress DownloadProgressFunc) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %s", resp.Status)
	}

	emitDownloadProgress(progress, DownloadStats{
		Tool:            tool,
		URL:             url,
		Phase:           "start",
		DownloadedBytes: 0,
		TotalBytes:      resp.ContentLength,
	})

	tmp, err := os.CreateTemp("", prefix)
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if success {
			return
		}
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	counter := &countingWriter{
		onAdd: func(downloaded int64) {
			emitDownloadProgress(progress, DownloadStats{
				Tool:            tool,
				URL:             url,
				Phase:           "downloading",
				DownloadedBytes: downloaded,
				TotalBytes:      resp.ContentLength,
			})
		},
	}
	if _, err := io.Copy(tmp, io.TeeReader(resp.Body, counter)); err != nil {
		if errors.Is(err, context.Canceled) {
			emitDownloadProgress(progress, DownloadStats{
				Tool:            tool,
				URL:             url,
				Phase:           "canceled",
				DownloadedBytes: counter.total,
				TotalBytes:      resp.ContentLength,
			})
		}
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	success = true
	emitDownloadProgress(progress, DownloadStats{
		Tool:            tool,
		URL:             url,
		Phase:           "done",
		DownloadedBytes: counter.total,
		TotalBytes:      resp.ContentLength,
	})

	return tmpPath, nil
}

func downloadToTemp(ctx context.Context, tool, url, prefix string, progress DownloadProgressFunc) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	client := &http.Client{Timeout: downloadTimeout}
	var lastErr error
	for attempt := 1; attempt <= maxDownloadAttempts; attempt++ {
		tmp, err := downloadToTempOnce(ctx, client, tool, url, prefix, progress)
		if err == nil {
			return tmp, nil
		}
		lastErr = err
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", ctx.Err()
		}
		if attempt == maxDownloadAttempts || !shouldRetryDownload(err) {
			break
		}
		emitDownloadProgress(progress, DownloadStats{
			Tool:  tool,
			URL:   url,
			Phase: "retry",
		})
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(attempt*2) * time.Second):
		}
	}
	return "", lastErr
}

func replaceFileAtomic(dst string, src string) error {
	tmp := dst + ".new"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(src, tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func extractFFmpegFromZip(zipPath, dst string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	var selected *zip.File
	for i := range zr.File {
		f := zr.File[i]
		name := strings.ToLower(filepath.ToSlash(f.Name))
		if !strings.HasSuffix(name, "/ffmpeg.exe") {
			continue
		}
		if selected == nil || strings.Contains(name, "/bin/ffmpeg.exe") {
			selected = f
		}
	}
	if selected == nil {
		return fmt.Errorf("ffmpeg.exe not found in archive")
	}

	r, err := selected.Open()
	if err != nil {
		return err
	}
	defer r.Close()

	tmp, err := os.CreateTemp("", "ytgui-ffmpeg-*.exe")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}

	ok, err := looksLikeWindowsExe(tmp.Name())
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if !ok {
		os.Remove(tmp.Name())
		return fmt.Errorf("archive ffmpeg payload is not a Windows executable")
	}

	return replaceFileAtomic(dst, tmp.Name())
}

func downloadBinaryByName(ctx context.Context, name, path string, progress DownloadProgressFunc) error {
	switch strings.ToLower(name) {
	case "yt-dlp.exe":
		tmp, err := downloadToTemp(ctx, name, latestBinaryURL, "ytgui-ytdlp-*", progress)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		ok, err := looksLikeWindowsExe(tmp)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("downloaded yt-dlp is not a Windows executable")
		}
		return replaceFileAtomic(path, tmp)
	case "ffmpeg.exe":
		srcURL := ffmpegSourceURL()
		tmp, err := downloadToTemp(ctx, name, srcURL, "ytgui-ffmpeg-*", progress)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)

		if isExe, err := looksLikeWindowsExe(tmp); err != nil {
			return err
		} else if isExe {
			return replaceFileAtomic(path, tmp)
		}
		if isZip, err := looksLikeZip(tmp); err != nil {
			return err
		} else if isZip {
			emitDownloadProgress(progress, DownloadStats{
				Tool:  name,
				URL:   srcURL,
				Phase: "extract_start",
			})
			defer emitDownloadProgress(progress, DownloadStats{
				Tool:  name,
				URL:   srcURL,
				Phase: "extract_done",
			})
			return extractFFmpegFromZip(tmp, path)
		}
		return fmt.Errorf("unsupported ffmpeg download format from %s (expected .exe or .zip)", srcURL)
	default:
		return fmt.Errorf("no download source configured for %s", name)
	}
}

func EnsureBinary(name string, data []byte) (string, error) {
	return EnsureBinaryWithProgress(name, data, nil)
}

func EnsureBinaryWithProgress(name string, data []byte, progress DownloadProgressFunc) (string, error) {
	return EnsureBinaryWithProgressCtx(context.Background(), name, data, progress)
}

func EnsureBinaryWithProgressCtx(ctx context.Context, name string, data []byte, progress DownloadProgressFunc) (string, error) {
	dir, err := appDir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if len(data) > 0 {
			if err := os.WriteFile(path, data, 0o755); err != nil {
				return "", fmt.Errorf("could not write %s: %w", name, err)
			}
		} else {
			if err := downloadBinaryByName(ctx, name, path, progress); err != nil {
				return "", fmt.Errorf("could not download %s: %w", name, err)
			}
		}
	} else if err != nil {
		return "", fmt.Errorf("could not access %s: %w", name, err)
	}

	return path, nil
}

func BinaryPath(name string) (string, error) {
	dir, err := appDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func BinaryExists(name string) (bool, string, error) {
	path, err := BinaryPath(name)
	if err != nil {
		return false, "", err
	}
	if _, err := os.Stat(path); err == nil {
		return true, path, nil
	} else if os.IsNotExist(err) {
		return false, path, nil
	} else {
		return false, "", err
	}
}

func CleanupDownloadTemps() int {
	patterns := []string{
		filepath.Join(os.TempDir(), "ytgui-ffmpeg-*"),
		filepath.Join(os.TempDir(), "ytgui-ytdlp-*"),
	}
	deleted := 0
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, p := range matches {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			info, err := os.Stat(p)
			if err != nil || info.IsDir() {
				continue
			}
			if err := os.Remove(p); err == nil || os.IsNotExist(err) {
				deleted++
			}
		}
	}
	return deleted
}
