package downloader

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func GetVideoInfo(ytdlp, url string) (title, channel string, err error) {
	cmd := exec.Command(ytdlp,
		"--print", "%(title)s",
		"--print", "%(uploader)s",
		"--encoding", "utf-8",
		"--no-warnings",
		"--skip-download",
		"--no-playlist",
		url,
	)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")

	setCmdHideWindow(cmd)

	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", "", fmt.Errorf("failed to parse title")
	}

	title = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		channel = strings.TrimSpace(lines[1])
	}
	return title, channel, nil
}

func sanitizeFileNamePart(s string) string {
	replacer := strings.NewReplacer(
		`<`, "_",
		`>`, "_",
		`:`, "_",
		`"`, "_",
		`/`, "_",
		`\\`, "_",
		`|`, "_",
		`?`, "_",
		`*`, "_",
	)
	clean := strings.TrimSpace(replacer.Replace(s))
	clean = strings.Trim(clean, ". ")
	if clean == "" {
		return "untitled"
	}
	return clean
}

func BuildFileName(title, channel, ext string, includeChannel bool) string {
	safeTitle := sanitizeFileNamePart(title)
	if includeChannel && strings.TrimSpace(channel) != "" {
		safeChannel := sanitizeFileNamePart(channel)
		return fmt.Sprintf("%s [%s].%s", safeTitle, safeChannel, ext)
	}
	return fmt.Sprintf("%s.%s", safeTitle, ext)
}

func UniqueName(path string) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)

	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}
