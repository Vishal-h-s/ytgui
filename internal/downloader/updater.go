package downloader

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type release struct {
	TagName string `json:"tag_name"`
}

const (
	latestReleaseAPIURL = "https://api.github.com/repos/yt-dlp/yt-dlp/releases/latest"
	latestBinaryURL     = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe"
)

func getLocalVersion(path string) (string, error) {
	cmd := exec.Command(path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func getLatestVersion(ctx context.Context, client *http.Client) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseAPIURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release API returned status %s", resp.Status)
	}

	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}

	if strings.TrimSpace(r.TagName) == "" {
		return "", fmt.Errorf("release API response missing tag_name")
	}

	return strings.TrimSpace(r.TagName), nil
}

func needsUpdate(local, latest string) bool {
	local = strings.TrimPrefix(strings.TrimSpace(local), "v")
	latest = strings.TrimPrefix(strings.TrimSpace(latest), "v")
	return local != "" && latest != "" && local != latest
}

func downloadLatest(ctx context.Context, client *http.Client, path string, progress DownloadProgressFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	expectedSHA, err := resolveYTDLPSHA256(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestBinaryURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binary download returned status %s", resp.Status)
	}

	emitDownloadProgress(progress, DownloadStats{
		Tool:            "yt-dlp.exe",
		URL:             latestBinaryURL,
		Phase:           "start",
		DownloadedBytes: 0,
		TotalBytes:      resp.ContentLength,
	})

	tmp := path + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(resp.Body)
	signature, err := reader.Peek(2)
	if err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("unable to inspect download: %w", err)
	}
	if !bytes.Equal(signature, []byte("MZ")) {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("downloaded file does not look like a Windows executable")
	}

	counter := &countingWriter{
		onAdd: func(downloaded int64) {
			emitDownloadProgress(progress, DownloadStats{
				Tool:            "yt-dlp.exe",
				URL:             latestBinaryURL,
				Phase:           "downloading",
				DownloadedBytes: downloaded,
				TotalBytes:      resp.ContentLength,
			})
		},
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hash), io.TeeReader(reader, counter)); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	actualSHA := hex.EncodeToString(hash.Sum(nil))
	if actualSHA != expectedSHA {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("yt-dlp.exe sha256 mismatch: expected %s, got %s", expectedSHA, actualSHA)
	}

	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	emitDownloadProgress(progress, DownloadStats{
		Tool:            "yt-dlp.exe",
		URL:             latestBinaryURL,
		Phase:           "done",
		DownloadedBytes: counter.total,
		TotalBytes:      resp.ContentLength,
	})

	return nil
}

func TryUpdateYTDLP(path string, logf func(string)) {
	_ = TryUpdateYTDLPWithProgress(path, logf, nil)
}

func TryUpdateYTDLPWithProgress(path string, logf func(string), progress DownloadProgressFunc) error {
	return TryUpdateYTDLPWithProgressCtx(context.Background(), path, logf, progress)
}

func TryUpdateYTDLPWithProgressCtx(ctx context.Context, path string, logf func(string), progress DownloadProgressFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	apiClient := &http.Client{Timeout: 15 * time.Second}
	downloadClient := &http.Client{Timeout: downloadTimeout}

	local, err := getLocalVersion(path)
	if err != nil {
		logf(fmt.Sprintf("Could not read local yt-dlp version: %v", err))
		return err
	}

	latest, err := getLatestVersion(ctx, apiClient)
	if err != nil {
		logf(fmt.Sprintf("Could not check latest yt-dlp version: %v", err))
		return err
	}

	if !needsUpdate(local, latest) {
		logf(fmt.Sprintf("yt-dlp is up to date (%s).", local))
		return nil
	}

	logf(fmt.Sprintf("Updating yt-dlp from %s to %s...", local, latest))
	if err := downloadLatest(ctx, downloadClient, path, progress); err != nil {
		logf(fmt.Sprintf("yt-dlp update failed: %v", err))
		return err
	}
	logf("yt-dlp update complete.")
	return nil
}
