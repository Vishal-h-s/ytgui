package downloader

import (
	"bufio"
	"bytes"
	"context"
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

func getLatestVersion(client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, latestReleaseAPIURL, nil)
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

func downloadLatest(client *http.Client, path string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, latestBinaryURL, nil)
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

	if _, err := io.Copy(out, reader); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}

	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}

	return nil
}

func TryUpdateYTDLP(path string, logf func(string)) {
	client := &http.Client{Timeout: 15 * time.Second}

	local, err := getLocalVersion(path)
	if err != nil {
		logf(fmt.Sprintf("Could not read local yt-dlp version: %v", err))
		return
	}

	latest, err := getLatestVersion(client)
	if err != nil {
		logf(fmt.Sprintf("Could not check latest yt-dlp version: %v", err))
		return
	}

	if !needsUpdate(local, latest) {
		logf(fmt.Sprintf("yt-dlp is up to date (%s).", local))
		return
	}

	logf(fmt.Sprintf("Updating yt-dlp from %s to %s...", local, latest))
	if err := downloadLatest(client, path); err != nil {
		logf(fmt.Sprintf("yt-dlp update failed: %v", err))
		return
	}
	logf("yt-dlp update complete.")
}
