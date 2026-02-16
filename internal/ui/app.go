package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"ytgui/internal/downloader"
)

type Assets struct {
	YTDLP  []byte
	FFmpeg []byte
}

var percentRegex = regexp.MustCompile(`\[(download|ffmpeg)\]\s+(\d+(\.\d+)?)%`)
var progressLineRegex = regexp.MustCompile(`\[download\]\s+(.+)`)
var etaRegex = regexp.MustCompile(`ETA\s+([0-9:]+)`)

const maxLogLineLen = 220
const prefDownloadDir = "download_dir"

func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "Videos", "YoutubeDownloads")
}

func folderButtonText(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "Choose Folder"
	}
	return trimmed
}

func runOnMain(f func()) {
	f()
}

func appendLog(logBox *widget.Entry, msg string, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	runOnMain(func() {
		logBox.SetText(logBox.Text + msg + "\n")
	})
}

func appendNerdLog(nerdLogBox *widget.Entry, msg string, mu *sync.Mutex) {
	if nerdLogBox == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	runOnMain(func() {
		nerdLogBox.SetText(nerdLogBox.Text + msg + "\n")
	})
}

func quoteArg(arg string) string {
	if arg == "" {
		return "\"\""
	}
	if strings.ContainsAny(arg, " \t\n\"") {
		return strconv.Quote(arg)
	}
	return arg
}

func formatCommandLine(exe string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteArg(exe))
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func parseProgress(line string) float64 {
	m := percentRegex.FindStringSubmatch(line)
	if len(m) >= 3 {
		p, err := strconv.ParseFloat(m[2], 64)
		if err == nil {
			return p / 100.0
		}
	}
	return -1
}

func compactStatus(line string) string {
	m := percentRegex.FindStringSubmatch(line)
	if len(m) < 3 {
		return ""
	}
	pct := m[2]
	if em := etaRegex.FindStringSubmatch(line); len(em) > 1 {
		return fmt.Sprintf("Downloading %s%% (ETA %s)", pct, em[1])
	}
	return fmt.Sprintf("Downloading %s%%", pct)
}

type downloadProgressTracker struct {
	mu            sync.Mutex
	totalStages   int
	stageIndex    int
	hasStage      bool
	stageProgress float64
	seenDest      map[string]struct{}
}

func newDownloadProgressTracker(quality string, subOpt *downloader.SubOption, playlist bool) *downloadProgressTracker {
	if playlist {
		return nil
	}
	stages := 1
	if quality != "Audio Only" {
		stages = 2
	}
	if subOpt != nil {
		stages++
	}
	if stages < 1 {
		stages = 1
	}
	return &downloadProgressTracker{
		totalStages: stages,
		seenDest:    make(map[string]struct{}),
	}
}

func (t *downloadProgressTracker) update(rawLine string) (float64, string, bool) {
	if t == nil {
		return 0, "", false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	line := strings.TrimSpace(rawLine)
	const destPrefix = "[download] Destination:"
	if strings.HasPrefix(line, destPrefix) {
		dest := strings.TrimSpace(strings.TrimPrefix(line, destPrefix))
		if _, ok := t.seenDest[dest]; !ok {
			t.seenDest[dest] = struct{}{}
			if !t.hasStage {
				t.hasStage = true
				t.stageIndex = 0
				t.stageProgress = 0
			} else if t.stageIndex < t.totalStages-1 {
				t.stageIndex++
				t.stageProgress = 0
			}
		}
		v := float64(t.stageIndex) / float64(t.totalStages)
		return v, fmt.Sprintf("Downloading (%d/%d)...", t.stageIndex+1, t.totalStages), true
	}

	if p := parseProgress(rawLine); p >= 0 && t.hasStage {
		if p < t.stageProgress {
			p = t.stageProgress
		}
		t.stageProgress = p
		v := (float64(t.stageIndex) + p) / float64(t.totalStages)
		return v, compactStatus(rawLine), true
	}

	if strings.Contains(line, "[Merger]") {
		v := (float64(t.totalStages) - 0.1) / float64(t.totalStages)
		return v, "Merging formats...", true
	}
	if strings.Contains(line, "[EmbedSubtitle]") {
		v := (float64(t.totalStages) - 0.05) / float64(t.totalStages)
		return v, "Embedding subtitles...", true
	}

	return 0, "", false
}

func shouldShowInUserLog(rawLine string) bool {
	line := strings.TrimSpace(strings.ReplaceAll(rawLine, "\r", ""))
	if line == "" {
		return false
	}
	if strings.Contains(line, "[download]") && strings.Contains(line, "% of") {
		return false
	}

	if strings.HasPrefix(line, "WARNING:") || strings.HasPrefix(line, "ERROR:") {
		return true
	}
	if strings.HasPrefix(line, "[youtube]") {
		return strings.Contains(line, "Extracting URL")
	}
	if strings.HasPrefix(line, "[info]") {
		return strings.Contains(line, "Downloading subtitles:") ||
			strings.Contains(line, "Downloading 1 format(s):") ||
			strings.Contains(line, "Downloading 2 format(s):") ||
			strings.Contains(line, "Writing video subtitles to:")
	}
	if strings.Contains(line, "[SubtitlesConvertor]") ||
		strings.Contains(line, "[Merger]") ||
		strings.Contains(line, "[EmbedSubtitle]") ||
		strings.HasPrefix(line, "Deleting original file") {
		return true
	}
	if strings.HasPrefix(line, "[download] Destination:") {
		return true
	}
	return false
}

func scanAndLog(r io.Reader, logBox *widget.Entry, nerdLogBox *widget.Entry, status *widget.Label, progress *widget.ProgressBar, mu *sync.Mutex, onProgress func(string) (float64, string, bool)) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		rawLine := sc.Text()
		appendNerdLog(nerdLogBox, rawLine, mu)
		if onProgress != nil {
			if p, s, ok := onProgress(rawLine); ok {
				runOnMain(func() {
					progress.SetValue(p)
					if strings.TrimSpace(s) != "" {
						status.SetText(s)
					}
				})
			}
		}
		if !shouldShowInUserLog(rawLine) {
			continue
		}
		line := rawLine
		if len(line) > maxLogLineLen {
			line = line[:maxLogLineLen] + " ..."
		}
		appendLog(logBox, line, mu)

		if m := progressLineRegex.FindStringSubmatch(rawLine); len(m) > 1 {
			runOnMain(func() {
				if s := compactStatus(rawLine); s != "" {
					status.SetText(s)
				}
			})
		}

	}
	if err := sc.Err(); err != nil {
		appendLog(logBox, fmt.Sprintf("log stream error: %v", err), mu)
	}
}

func formatFromChoice(choice, outputProfile string) []string {
	if choice == "Audio Only" {
		return []string{"-x", "--audio-format", "mp3"}
	}

	if outputProfile == "Compatibility (H.264/AAC)" {
		switch choice {
		case "1080p":
			return []string{"-f", "bestvideo[vcodec^=avc1][height<=1080]+bestaudio[acodec^=mp4a]/best[vcodec^=avc1][acodec^=mp4a][height<=1080]/bestvideo[height<=1080]+bestaudio/best[height<=1080]"}
		case "720p":
			return []string{"-f", "bestvideo[vcodec^=avc1][height<=720]+bestaudio[acodec^=mp4a]/best[vcodec^=avc1][acodec^=mp4a][height<=720]/bestvideo[height<=720]+bestaudio/best[height<=720]"}
		case "480p":
			return []string{"-f", "bestvideo[vcodec^=avc1][height<=480]+bestaudio[acodec^=mp4a]/best[vcodec^=avc1][acodec^=mp4a][height<=480]/bestvideo[height<=480]+bestaudio/best[height<=480]"}
		default:
			return []string{"-f", "bestvideo[vcodec^=avc1]+bestaudio[acodec^=mp4a]/best[vcodec^=avc1][acodec^=mp4a]/bestvideo+bestaudio/best"}
		}
	}

	switch choice {
	case "1080p":
		return []string{"-f", "bestvideo[vcodec^=av01][height<=1080]+bestaudio[acodec^=opus]/bestvideo[vcodec^=vp9][height<=1080]+bestaudio[acodec^=opus]/bestvideo[height<=1080]+bestaudio/best[height<=1080]"}
	case "720p":
		return []string{"-f", "bestvideo[vcodec^=av01][height<=720]+bestaudio[acodec^=opus]/bestvideo[vcodec^=vp9][height<=720]+bestaudio[acodec^=opus]/bestvideo[height<=720]+bestaudio/best[height<=720]"}
	case "480p":
		return []string{"-f", "bestvideo[vcodec^=av01][height<=480]+bestaudio[acodec^=opus]/bestvideo[vcodec^=vp9][height<=480]+bestaudio[acodec^=opus]/bestvideo[height<=480]+bestaudio/best[height<=480]"}
	default:
		return []string{"-f", "bestvideo[vcodec^=av01]+bestaudio[acodec^=opus]/bestvideo[vcodec^=vp9]+bestaudio[acodec^=opus]/bestvideo+bestaudio/best"}
	}
}

func subtitleLangBase(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" {
		return ""
	}
	if i := strings.Index(c, "-"); i > 0 {
		return c[:i]
	}
	if i := strings.Index(c, "_"); i > 0 {
		return c[:i]
	}
	return c
}

func subtitleAvailabilitySummary(opts []downloader.SubOption) []string {
	var creatorOriginal bool
	var creatorEnglish bool
	var autoOriginal bool
	var autoEnglish bool
	langs := make(map[string]struct{})

	for _, o := range opts {
		base := subtitleLangBase(o.Code)
		if base == "" {
			base = strings.ToLower(strings.TrimSpace(o.Code))
		}
		if base != "" {
			langs[base] = struct{}{}
		}

		if o.IsAuto {
			if o.IsOriginal {
				autoOriginal = true
			}
			if base == "en" {
				autoEnglish = true
			}
			continue
		}

		if o.IsOriginal {
			creatorOriginal = true
		}
		if base == "en" {
			creatorEnglish = true
		}
	}

	langList := make([]string, 0, len(langs))
	for lang := range langs {
		langList = append(langList, lang)
	}
	sort.Strings(langList)
	if len(langList) == 0 {
		langList = []string{"none"}
	}

	yesNo := func(v bool) string {
		if v {
			return "YES"
		}
		return "NO"
	}

	return []string{
		"Subtitle availability:",
		"  Creator Uploaded (Original): " + yesNo(creatorOriginal),
		"  Creator Uploaded (English): " + yesNo(creatorEnglish),
		"  Auto Generated (Original): " + yesNo(autoOriginal),
		"  Auto Generated (English): " + yesNo(autoEnglish),
		"  Languages: " + strings.Join(langList, ", "),
	}
}

type subtitleCategoryChoice struct {
	label string
	opt   downloader.SubOption
}

func subtitleCategoryChoices(opts []downloader.SubOption) []subtitleCategoryChoice {
	type category struct {
		label string
		match func(downloader.SubOption) bool
	}

	categories := []category{
		{
			label: "Creator Uploaded (Original)",
			match: func(o downloader.SubOption) bool {
				return !o.IsAuto && o.IsOriginal
			},
		},
		{
			label: "Creator Uploaded (English)",
			match: func(o downloader.SubOption) bool {
				return !o.IsAuto && subtitleLangBase(o.Code) == "en"
			},
		},
		{
			label: "Auto Generated (Original)",
			match: func(o downloader.SubOption) bool {
				return o.IsAuto && o.IsOriginal
			},
		},
		{
			label: "Auto Generated (English)",
			match: func(o downloader.SubOption) bool {
				return o.IsAuto && subtitleLangBase(o.Code) == "en"
			},
		},
	}

	var out []subtitleCategoryChoice
	for _, c := range categories {
		var candidates []downloader.SubOption
		for _, o := range opts {
			if c.match(o) {
				candidates = append(candidates, o)
			}
		}
		best := pickBestSubtitleOption(candidates)
		if best == nil {
			continue
		}
		out = append(out, subtitleCategoryChoice{
			label: c.label,
			opt:   *best,
		})
	}
	return out
}

func subtitleCategoryOptions(opts []downloader.SubOption) []downloader.SubOption {
	choices := subtitleCategoryChoices(opts)
	out := make([]downloader.SubOption, 0, len(choices))
	for _, c := range choices {
		out = append(out, c.opt)
	}
	return out
}

func onlyEnglishSubtitles(opts []downloader.SubOption) bool {
	if len(opts) == 0 {
		return false
	}
	for _, o := range opts {
		if subtitleLangBase(o.Code) != "en" {
			return false
		}
	}
	return true
}

func hasMultipleSubtitleLanguages(opts []downloader.SubOption) bool {
	langs := make(map[string]struct{})
	for _, o := range opts {
		base := subtitleLangBase(o.Code)
		if base == "" {
			base = strings.ToLower(strings.TrimSpace(o.Code))
		}
		if base == "" {
			continue
		}
		langs[base] = struct{}{}
	}
	return len(langs) > 1
}

func subtitlePriorityScore(o downloader.SubOption) int {
	switch {
	case !o.IsAuto && o.IsOriginal:
		return 0
	case !o.IsAuto:
		return 1
	case o.IsOriginal:
		return 2
	case subtitleLangBase(o.Code) == "en":
		return 3
	default:
		return 4
	}
}

func pickBestSubtitleOption(opts []downloader.SubOption) *downloader.SubOption {
	if len(opts) == 0 {
		return nil
	}
	candidates := append([]downloader.SubOption(nil), opts...)
	sort.Slice(candidates, func(i, j int) bool {
		si := subtitlePriorityScore(candidates[i])
		sj := subtitlePriorityScore(candidates[j])
		if si != sj {
			return si < sj
		}
		ci := strings.ToLower(candidates[i].Code)
		cj := strings.ToLower(candidates[j].Code)
		if ci != cj {
			return ci < cj
		}
		return candidates[i].Label < candidates[j].Label
	})
	chosen := candidates[0]
	return &chosen
}

func planSubtitleSelection(opts []downloader.SubOption) (*downloader.SubOption, []downloader.SubOption) {
	var manualOriginal []downloader.SubOption
	var manualAny []downloader.SubOption
	var autoOriginal []downloader.SubOption
	var autoAny []downloader.SubOption

	for _, o := range opts {
		if o.IsAuto {
			autoAny = append(autoAny, o)
			if o.IsOriginal {
				autoOriginal = append(autoOriginal, o)
			}
			continue
		}
		manualAny = append(manualAny, o)
		if o.IsOriginal {
			manualOriginal = append(manualOriginal, o)
		}
	}

	var pool []downloader.SubOption
	switch {
	case len(manualOriginal) > 0:
		pool = manualOriginal
	case len(manualAny) > 0:
		pool = manualAny
	case len(autoOriginal) > 0:
		pool = autoOriginal
	case len(autoAny) > 0:
		pool = autoAny
	default:
		return nil, nil
	}

	if len(pool) == 1 || !hasMultipleSubtitleLanguages(pool) || onlyEnglishSubtitles(pool) {
		return pickBestSubtitleOption(pool), nil
	}
	return nil, pool
}

func askSubtitleChoice(w fyne.Window, opts []downloader.SubOption) *downloader.SubOption {
	if len(opts) == 0 {
		return nil
	}
	choices := subtitleCategoryChoices(opts)
	if len(choices) == 0 {
		return nil
	}

	choiceChan := make(chan *downloader.SubOption, 1)
	runOnMain(func() {
		var choiceStrings []string
		byLabel := map[string]downloader.SubOption{}
		for _, c := range choices {
			choiceStrings = append(choiceStrings, c.label)
			byLabel[c.label] = c.opt
		}

		combo := widget.NewSelect(choiceStrings, nil)
		combo.SetSelected(choiceStrings[0])

		d := dialog.NewCustomConfirm(
			"Select Subtitles",
			"Download",
			"Cancel",
			container.NewVBox(
				widget.NewLabel("Choose a subtitle track:"),
				combo,
			),
			func(confirmed bool) {
				if !confirmed {
					choiceChan <- nil
					return
				}
				selection := combo.Selected
				if o, ok := byLabel[selection]; ok {
					opt := o
					choiceChan <- &opt
					return
				}
				choiceChan <- nil
			},
			w,
		)
		d.Resize(fyne.NewSize(380, 220))
		d.Show()
	})

	return <-choiceChan
}

func askDownloadWithoutSubs(w fyne.Window) bool {
	choiceCh := make(chan bool, 1)
	runOnMain(func() {
		d := dialog.NewCustomConfirm(
			"No Subtitles Available",
			"Download without subtitles",
			"Abort",
			container.NewVBox(
				widget.NewLabel("No preferred subtitle type is available."),
				widget.NewLabel("Continue download without subtitles?"),
			),
			func(confirmed bool) {
				choiceCh <- confirmed
			},
			w,
		)
		d.Resize(fyne.NewSize(430, 190))
		d.Show()
	})
	return <-choiceCh
}

func checkMissingTools() ([]string, error) {
	required := []string{"yt-dlp.exe", "ffmpeg.exe"}
	var missing []string
	for _, tool := range required {
		exists, _, err := downloader.BinaryExists(tool)
		if err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, tool)
		}
	}
	return missing, nil
}

func askDownloadRequiredTools(w fyne.Window, missing []string) bool {
	choiceCh := make(chan bool, 1)
	runOnMain(func() {
		msg := "The app needs to download required tools:\n" + strings.Join(missing, "\n")
		d := dialog.NewCustomConfirm(
			"Setup Required",
			"Download",
			"Abort",
			container.NewVBox(
				widget.NewLabel(msg),
				widget.NewLabel("Download now? This should happen only once."),
			),
			func(confirmed bool) {
				choiceCh <- confirmed
			},
			w,
		)
		d.Resize(fyne.NewSize(460, 220))
		d.Show()
	})
	return <-choiceCh
}

func askDuplicateAction(w fyne.Window, file string) string {
	choiceCh := make(chan string, 1)
	runOnMain(func() {
		var d dialog.Dialog
		choiceSet := false
		sendChoice := func(choice string) {
			if choiceSet {
				return
			}
			choiceSet = true
			choiceCh <- choice
			d.Hide()
		}

		buttons := container.NewGridWithColumns(3,
			widget.NewButton("Rename", func() {
				sendChoice("rename")
			}),
			widget.NewButton("Replace", func() {
				sendChoice("replace")
			}),
			widget.NewButton("Cancel", func() {
				sendChoice("rename")
			}),
		)

		d = dialog.NewCustom(
			"File Exists",
			"",
			container.NewVBox(
				widget.NewLabel("File already exists:"),
				widget.NewLabel(file),
				widget.NewLabel("Choose what to do:"),
				buttons,
			),
			w,
		)
		d.SetOnClosed(func() {
			if choiceSet {
				return
			}
			choiceSet = true
			choiceCh <- "rename"
		})
		d.Resize(fyne.NewSize(420, 220))
		d.Show()
	})

	return <-choiceCh
}

func cleanupSubtitleSidecars(videoPath string) int {
	if strings.TrimSpace(videoPath) == "" || strings.Contains(videoPath, "%(") {
		return 0
	}

	dir := filepath.Dir(videoPath)
	videoName := filepath.Base(videoPath)
	ext := filepath.Ext(videoName)
	base := strings.TrimSuffix(videoName, ext)
	if strings.TrimSpace(base) == "" || base == videoName {
		return 0
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	subtitleExts := map[string]struct{}{
		".vtt":  {},
		".srt":  {},
		".ass":  {},
		".ssa":  {},
		".ttml": {},
		".lrc":  {},
	}

	deleted := 0
	baseLower := strings.ToLower(base)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if _, ok := subtitleExts[ext]; !ok {
			continue
		}

		stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		if stem != baseLower && !strings.HasPrefix(stem, baseLower+".") {
			continue
		}

		fullPath := filepath.Join(dir, name)
		if rmErr := os.Remove(fullPath); rmErr == nil || os.IsNotExist(rmErr) {
			deleted++
		}
	}

	return deleted
}

func runYTDLP(url, downloadDir, quality, outputProfile, ytdlp, ffmpeg string, includeChannel, playlist bool, subOpt *downloader.SubOption, w fyne.Window, logBox *widget.Entry, nerdLogBox *widget.Entry, status *widget.Label, progress *widget.ProgressBar, mu *sync.Mutex) {
	if runtime.GOOS != "windows" {
		appendLog(logBox, "This build is intended for Windows only.", mu)
		runOnMain(func() { status.SetText("Windows build required") })
		return
	}

	output := "%(title)s.%(ext)s"
	if strings.TrimSpace(downloadDir) != "" {
		output = filepath.Join(downloadDir, "%(title)s.%(ext)s")
	}
	mergeFormat := "mp4"
	if outputProfile == "Smaller Files (AV1/VP9)" {
		mergeFormat = "mkv"
	}
	if !playlist {
		appendNerdLog(nerdLogBox, "> "+formatCommandLine(ytdlp, []string{"--print", "%(title)s", "--print", "%(uploader)s", "--encoding", "utf-8", "--no-warnings", "--skip-download", "--no-playlist", url}), mu)
		title, channel, infoErr := downloader.GetVideoInfo(ytdlp, url)
		if infoErr != nil {
			appendLog(logBox, fmt.Sprintf("Could not fetch metadata, using template output: %v", infoErr), mu)
		} else {
			targetDir := strings.TrimSpace(downloadDir)
			if targetDir == "" {
				targetDir, _ = os.Getwd()
			}

			targetExt := mergeFormat
			if quality == "Audio Only" {
				targetExt = "mp3"
			}

			fileName := downloader.BuildFileName(title, channel, targetExt, includeChannel)
			fullPath := filepath.Join(targetDir, fileName)
			if _, err := os.Stat(fullPath); err == nil {
				choice := askDuplicateAction(w, fullPath)
				switch choice {
				case "replace":
					if rmErr := os.Remove(fullPath); rmErr != nil && !os.IsNotExist(rmErr) {
						appendLog(logBox, fmt.Sprintf("Cannot replace existing file: %v", rmErr), mu)
						runOnMain(func() { status.SetText("Cannot replace existing file") })
						return
					}
				case "rename":
					fullPath = downloader.UniqueName(fullPath)
				default:
					fullPath = downloader.UniqueName(fullPath)
				}
			}
			output = fullPath
		}
	}

	args := []string{
		"--ffmpeg-location", filepath.Dir(ffmpeg),
		"-o", output,
	}
	args = append(args, formatFromChoice(quality, outputProfile)...)
	if playlist {
		args = append(args, "--yes-playlist")
	} else {
		args = append(args, "--no-playlist")
	}

	if subOpt != nil {
		appendLog(logBox, fmt.Sprintf("Selected Subtitles: %s", subOpt.Label), mu)
		args = append(args, "--embed-subs", "--sub-lang", subOpt.Code)
		if subOpt.IsAuto {
			args = append(args, "--write-auto-subs")
		} else {
			args = append(args, "--write-subs")
		}
		if mergeFormat == "mp4" {
			// MP4 is more reliable with converted text subtitle tracks.
			args = append(args, "--convert-subs", "srt")
		}
		// Mark first embedded subtitle track as default so players like VLC auto-pick it.
		args = append(args, "--postprocessor-args", "EmbedSubtitle+ffmpeg:-disposition:s:0 default")
	}

	args = append(args, "--merge-output-format", mergeFormat)
	appendLog(logBox, fmt.Sprintf("Output profile: %s (%s)", outputProfile, strings.ToUpper(mergeFormat)), mu)
	args = append(args, url)
	appendNerdLog(nerdLogBox, "> "+formatCommandLine(ytdlp, args), mu)
	cmd := exec.Command(ytdlp, args...)

	setCmdHideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		appendLog(logBox, fmt.Sprintf("Failed to capture stdout: %v", err), mu)
		runOnMain(func() { status.SetText("Error: stdout capture failed") })
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		appendLog(logBox, fmt.Sprintf("Failed to capture stderr: %v", err), mu)
		runOnMain(func() { status.SetText("Error: stderr capture failed") })
		return
	}

	if err := cmd.Start(); err != nil {
		appendLog(logBox, fmt.Sprintf("Failed to start yt-dlp: %v", err), mu)
		runOnMain(func() { status.SetText("Failed to start download") })
		return
	}

	tracker := newDownloadProgressTracker(quality, subOpt, playlist)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanAndLog(stdout, logBox, nerdLogBox, status, progress, mu, tracker.update)
	}()

	go func() {
		defer wg.Done()
		scanAndLog(stderr, logBox, nerdLogBox, status, progress, mu, tracker.update)
	}()

	err = cmd.Wait()
	wg.Wait()
	if err != nil {
		appendLog(logBox, fmt.Sprintf("yt-dlp exited with error: %v", err), mu)
		runOnMain(func() { status.SetText("Download failed") })
		return
	}
	if subOpt != nil && !playlist {
		if removed := cleanupSubtitleSidecars(output); removed > 0 {
			appendLog(logBox, fmt.Sprintf("Cleaned up %d subtitle sidecar file(s).", removed), mu)
		}
	}
	appendLog(logBox, "Download complete.", mu)
	runOnMain(func() {
		status.SetText("Download complete")
		progress.SetValue(1.0)
	})
}

func RunApp(assets Assets) {
	a := app.NewWithID("com.wishall.ytgui")
	w := a.NewWindow("yt-dlp Portable GUI")
	w.Resize(fyne.NewSize(600, 400))
	confirmClose := func() {
		dialog.ShowConfirm(
			"Exit",
			"Close ytgui?",
			func(ok bool) {
				if ok {
					a.Quit()
				}
			},
			w,
		)
	}
	w.SetCloseIntercept(confirmClose)
	w.Canvas().AddShortcut(&desktop.CustomShortcut{
		KeyName:  fyne.KeyF4,
		Modifier: fyne.KeyModifierAlt,
	}, func(fyne.Shortcut) {
		confirmClose()
	})

	url := widget.NewEntry()
	url.SetPlaceHolder("Paste video URL")

	prefs := a.Preferences()
	defaultDir := defaultDownloadDir()
	savedDir := strings.TrimSpace(prefs.StringWithFallback(prefDownloadDir, ""))
	downloadDir := savedDir
	if downloadDir == "" {
		downloadDir = defaultDir
	}
	prefs.SetString(prefDownloadDir, downloadDir)
	qualitySelect := widget.NewSelect(
		[]string{"Best", "1080p", "720p", "480p", "Audio Only"},
		func(string) {},
	)
	qualitySelect.SetSelected("Best")
	profileSelect := widget.NewSelect(
		[]string{"Compatibility (H.264/AAC)", "Smaller Files (AV1/VP9)"},
		func(string) {},
	)
	profileSelect.SetSelected("Compatibility (H.264/AAC)")
	nameWithChannel := widget.NewCheck("Include channel name in filename", func(bool) {})
	playlistCheck := widget.NewCheck("Download Playlist", func(bool) {})
	subsCheck := widget.NewCheck("Download Subtitles (Ask which)", func(bool) {})
	subsCheck.SetChecked(false)
	nameWithChannel.SetChecked(true)
	status := widget.NewLabel("Idle")
	progress := widget.NewProgressBar()
	progress.SetValue(0)

	logBox := widget.NewMultiLineEntry()
	logBox.Wrapping = fyne.TextWrapWord
	nerdLogBox := widget.NewMultiLineEntry()
	nerdLogBox.Wrapping = fyne.TextWrapOff
	var logMu sync.Mutex

	var chooseFolder *widget.Button
	chooseFolder = widget.NewButton(folderButtonText(downloadDir), func() {
		dialog.ShowFolderOpen(func(lu fyne.ListableURI, err error) {
			if err != nil || lu == nil {
				return
			}
			downloadDir = lu.Path()
			prefs.SetString(prefDownloadDir, downloadDir)
			runOnMain(func() {
				chooseFolder.SetText(folderButtonText(downloadDir))
			})
			appendLog(logBox, "Download folder: "+downloadDir, &logMu)
		}, w)
	})
	openFolder := widget.NewButton("Open Folder", func() {
		target := strings.TrimSpace(downloadDir)
		if target == "" {
			appendLog(logBox, "No download folder selected.", &logMu)
			runOnMain(func() { status.SetText("No download folder selected") })
			return
		}
		info, err := os.Stat(target)
		if err != nil || !info.IsDir() {
			appendLog(logBox, "Download folder does not exist: "+target, &logMu)
			runOnMain(func() { status.SetText("Download folder missing") })
			return
		}

		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("explorer", target)
		case "darwin":
			cmd = exec.Command("open", target)
		default:
			cmd = exec.Command("xdg-open", target)
		}
		if err := cmd.Start(); err != nil {
			appendLog(logBox, fmt.Sprintf("Failed to open folder: %v", err), &logMu)
			runOnMain(func() { status.SetText("Failed to open folder") })
		}
	})

	var toolsReady atomic.Bool
	var preparedYTDLPPath string
	var preparedFFmpegPath string
	var btn *widget.Button
	btn = widget.NewButton("Download", func() {
		if !toolsReady.Load() {
			status.SetText("Preparing required tools...")
			return
		}
		btn.Disable()
		downloadURL := strings.TrimSpace(url.Text)
		selectedQuality := qualitySelect.Selected
		selectedProfile := profileSelect.Selected
		selectedFolder := strings.TrimSpace(downloadDir)
		selectedNameWithChannel := nameWithChannel.Checked
		selectedPlaylist := playlistCheck.Checked
		checkSubs := subsCheck.Checked

		if downloadURL == "" {
			status.SetText("Missing URL")
			btn.Enable()
			return
		}
		if selectedFolder != "" &&
			defaultDir != "" &&
			strings.EqualFold(filepath.Clean(selectedFolder), filepath.Clean(defaultDir)) {
			if err := os.MkdirAll(selectedFolder, 0o755); err != nil {
				status.SetText("Cannot create default download folder")
				appendLog(logBox, fmt.Sprintf("Failed to create default folder %s: %v", selectedFolder, err), &logMu)
				btn.Enable()
				return
			}
		}

		go func() {
			defer runOnMain(func() { btn.Enable() })
			ytdlpPath := preparedYTDLPPath
			ffmpegPath := preparedFFmpegPath
			if strings.TrimSpace(ytdlpPath) == "" || strings.TrimSpace(ffmpegPath) == "" {
				appendLog(logBox, "Tools are not ready yet. Please wait.", &logMu)
				runOnMain(func() { status.SetText("Preparing required tools...") })
				return
			}
			appendNerdLog(nerdLogBox, "Tool path: "+ytdlpPath, &logMu)
			appendNerdLog(nerdLogBox, "Tool path: "+ffmpegPath, &logMu)

			var selectedSub *downloader.SubOption
			if checkSubs && !selectedPlaylist {
				runOnMain(func() { status.SetText("Checking subtitles...") })
				appendLog(logBox, "Fetching subtitle list...", &logMu)

				appendNerdLog(nerdLogBox, "> "+formatCommandLine(ytdlpPath, []string{"--print", "%(subtitles)j", "--print", "%(automatic_captions)j", "--print", "%(language)s", "--encoding", "utf-8", "--no-warnings", "--skip-download", "--no-playlist", downloadURL}), &logMu)
				opts, err := downloader.GetAvailableSubtitles(ytdlpPath, downloadURL)
				if err != nil {
					appendLog(logBox, fmt.Sprintf("Could not list subtitles: %v. Proceeding without.", err), &logMu)
				} else {
					for _, line := range subtitleAvailabilitySummary(opts) {
						appendLog(logBox, line, &logMu)
					}

					categoryOpts := subtitleCategoryOptions(opts)
					if len(categoryOpts) == 0 {
						appendLog(logBox, "No preferred subtitle category available.", &logMu)
						if !askDownloadWithoutSubs(w) {
							appendLog(logBox, "Download aborted by user (no subtitles available).", &logMu)
							runOnMain(func() { status.SetText("Aborted") })
							return
						}
						appendLog(logBox, "Proceeding without subtitles.", &logMu)
						selectedSub = nil
					}

					autoSelected, promptOptions := planSubtitleSelection(categoryOpts)
					switch {
					case autoSelected != nil:
						selectedSub = autoSelected
						appendLog(logBox, "Auto-selected subtitles: "+selectedSub.Label, &logMu)
					case len(promptOptions) > 0:
						appendLog(logBox, "Multiple subtitle languages found. Please choose one.", &logMu)
						selectedSub = askSubtitleChoice(w, categoryOpts)
					default:
						selectedSub = nil
					}
				}
			}

			runOnMain(func() {
				status.SetText("Starting download...")
				progress.SetValue(0)
			})
			appendLog(logBox, "Starting download...", &logMu)

			runYTDLP(downloadURL, selectedFolder, selectedQuality, selectedProfile, ytdlpPath, ffmpegPath, selectedNameWithChannel, selectedPlaylist, selectedSub, w, logBox, nerdLogBox, status, progress, &logMu)
		}()
	})
	btn.Disable()
	go func() {
		runOnMain(func() {
			status.SetText("Checking required tools...")
			progress.SetValue(0.05)
		})
		appendLog(logBox, "Required tools check...", &logMu)
		for _, tool := range []string{"yt-dlp.exe", "ffmpeg.exe"} {
			if path, err := downloader.BinaryPath(tool); err == nil {
				appendNerdLog(nerdLogBox, "[setup] check exists "+path, &logMu)
			} else {
				appendNerdLog(nerdLogBox, fmt.Sprintf("[setup] resolve path for %s failed: %v", tool, err), &logMu)
			}
		}
		missing, err := checkMissingTools()
		if err != nil {
			appendLog(logBox, fmt.Sprintf("Failed to check required tools: %v", err), &logMu)
			runOnMain(func() { status.SetText("Tool check failed") })
			return
		}
		if len(missing) == 0 {
			appendNerdLog(nerdLogBox, "[setup] all required tools present", &logMu)
		} else {
			appendNerdLog(nerdLogBox, "[setup] missing tools: "+strings.Join(missing, ", "), &logMu)
		}
		appendLog(logBox, "Required tools check done.", &logMu)
		runOnMain(func() { progress.SetValue(0.15) })
		freshYTDLPDownloaded := false
		if len(missing) > 0 {
			appendLog(logBox, "Missing required tools: "+strings.Join(missing, ", "), &logMu)
			if !askDownloadRequiredTools(w, missing) {
				appendLog(logBox, "Setup aborted by user.", &logMu)
				runOnMain(func() { status.SetText("Missing required tools") })
				return
			}
			runOnMain(func() { status.SetText("Downloading required tools...") })
			totalMissing := len(missing)
			for i, tool := range missing {
				startP := 0.20 + (float64(i)/float64(totalMissing))*0.50
				doneP := 0.20 + (float64(i+1)/float64(totalMissing))*0.50
				runOnMain(func() { progress.SetValue(startP) })
				appendLog(logBox, "Downloading "+tool+"...", &logMu)
				appendNerdLog(nerdLogBox, "[setup] ensure "+tool, &logMu)
				var data []byte
				switch tool {
				case "yt-dlp.exe":
					data = assets.YTDLP
					freshYTDLPDownloaded = true
				case "ffmpeg.exe":
					data = assets.FFmpeg
				}
				if _, err := downloader.EnsureBinary(tool, data); err != nil {
					appendLog(logBox, fmt.Sprintf("Failed to prepare %s: %v", tool, err), &logMu)
					runOnMain(func() { status.SetText("Setup failed") })
					return
				}
				appendLog(logBox, tool+" is ready.", &logMu)
				appendNerdLog(nerdLogBox, "[setup] "+tool+" ready", &logMu)
				runOnMain(func() { progress.SetValue(doneP) })
			}
		}
		ytdlpPath, err := downloader.BinaryPath("yt-dlp.exe")
		if err != nil {
			appendLog(logBox, fmt.Sprintf("Failed to resolve yt-dlp path: %v", err), &logMu)
			runOnMain(func() { status.SetText("Setup failed") })
			return
		}
		ffmpegPath, err := downloader.BinaryPath("ffmpeg.exe")
		if err != nil {
			appendLog(logBox, fmt.Sprintf("Failed to resolve ffmpeg path: %v", err), &logMu)
			runOnMain(func() { status.SetText("Setup failed") })
			return
		}
		preparedYTDLPPath = ytdlpPath
		preparedFFmpegPath = ffmpegPath
		appendNerdLog(nerdLogBox, "Prepared tool path: "+preparedYTDLPPath, &logMu)
		appendNerdLog(nerdLogBox, "Prepared tool path: "+preparedFFmpegPath, &logMu)
		if freshYTDLPDownloaded {
			appendLog(logBox, "yt-dlp update check skipped (fresh install).", &logMu)
			appendLog(logBox, "yt-dlp update check done.", &logMu)
			runOnMain(func() { progress.SetValue(0.95) })
		} else {
			appendLog(logBox, "yt-dlp update check...", &logMu)
			runOnMain(func() {
				status.SetText("Checking yt-dlp updates...")
				progress.SetValue(0.75)
			})
			appendNerdLog(nerdLogBox, "> "+formatCommandLine(preparedYTDLPPath, []string{"--version"}), &logMu)
			appendNerdLog(nerdLogBox, "> GET https://api.github.com/repos/yt-dlp/yt-dlp/releases/latest", &logMu)
			downloader.TryUpdateYTDLP(preparedYTDLPPath, func(msg string) {
				appendLog(logBox, msg, &logMu)
				appendNerdLog(nerdLogBox, "[yt-dlp-update] "+msg, &logMu)
				lower := strings.ToLower(msg)
				switch {
				case strings.Contains(lower, "updating yt-dlp"):
					runOnMain(func() {
						status.SetText("Updating yt-dlp...")
						progress.SetValue(0.85)
					})
				case strings.Contains(lower, "update complete"):
					runOnMain(func() {
						status.SetText("yt-dlp update complete")
						progress.SetValue(0.95)
					})
				case strings.Contains(lower, "up to date"):
					runOnMain(func() {
						status.SetText("yt-dlp is up to date")
						progress.SetValue(0.95)
					})
				case strings.Contains(lower, "could not check latest yt-dlp version"):
					runOnMain(func() {
						status.SetText("Could not check yt-dlp updates")
						progress.SetValue(0.80)
					})
				}
			})
			appendLog(logBox, "yt-dlp update check done.", &logMu)
		}
		toolsReady.Store(true)
		runOnMain(func() {
			status.SetText("Idle")
			progress.SetValue(0)
			btn.Enable()
		})
	}()

	clear := widget.NewButton("Clear", func() {
		logBox.SetText("")
	})
	clearNerd := widget.NewButton("Clear Nerd", func() {
		nerdLogBox.SetText("")
	})

	logTabs := container.NewAppTabs(
		container.NewTabItem("Normal Logs", logBox),
		container.NewTabItem("Nerd Terminal", nerdLogBox),
	)

	controls := container.NewVBox(
		widget.NewLabel("Portable yt-dlp Downloader"),
		url,
		container.NewHBox(chooseFolder, openFolder),
		qualitySelect,
		profileSelect,
		nameWithChannel,
		subsCheck,
		playlistCheck,
		container.NewHBox(btn, clear, clearNerd),
		status,
		progress,
	)

	w.SetContent(container.NewBorder(
		controls,
		nil,
		nil,
		nil,
		logTabs,
	))

	w.ShowAndRun()
}
