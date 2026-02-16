package downloader

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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

func downloadToTemp(url, prefix string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
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

	tmp, err := os.CreateTemp("", prefix)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	return tmp.Name(), nil
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

func downloadBinaryByName(name, path string) error {
	switch strings.ToLower(name) {
	case "yt-dlp.exe":
		tmp, err := downloadToTemp(latestBinaryURL, "ytgui-ytdlp-*")
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
		tmp, err := downloadToTemp(srcURL, "ytgui-ffmpeg-*")
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
			return extractFFmpegFromZip(tmp, path)
		}
		return fmt.Errorf("unsupported ffmpeg download format from %s (expected .exe or .zip)", srcURL)
	default:
		return fmt.Errorf("no download source configured for %s", name)
	}
}

func EnsureBinary(name string, data []byte) (string, error) {
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
			if err := downloadBinaryByName(name, path); err != nil {
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
