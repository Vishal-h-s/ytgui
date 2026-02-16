# ytgui

Portable GUI wrapper for yt-dlp (Windows build).

This repository bundles a small Fyne GUI that embeds `yt-dlp` and `ffmpeg` to provide a portable downloader.

## Build (Linux host -> Windows target)

This project is a Go GUI using Fyne. To build a Windows executable from Linux:

```bash
# install dependencies
go mod tidy

# build for windows (example for amd64)
GOOS=windows GOARCH=amd64 go build -o ytgui.exe
# see build-linux.sh for more details
```

`go mod tidy` will generate `go.sum` — commit `go.mod` and `go.sum` to the repository.

## Run

On Windows, this app checks for `yt-dlp.exe` and `ffmpeg.exe` in its cache folder on launch.
If missing, it prompts once and downloads them automatically.

Usage: run the GUI, paste a video URL, and choose download.

### First-run ffmpeg download

- `ffmpeg.exe` is downloaded on first run if missing from the app cache.
- Default source: `https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip`
- Override source with env var: `YTGUI_FFMPEG_URL`
- Supported formats for `YTGUI_FFMPEG_URL`: direct `.exe` or `.zip` containing `ffmpeg.exe` (preferably under `bin/`).

## Git / Large files

Do NOT commit large binaries directly. This repo's `.gitignore` excludes the `builds/` directory and common exe files.

If you accidentally committed large files and GitHub rejeсts a push (files >100MB):

- Option A (recommended): reinitialize history locally and push a cleaned repository (destroys old history on remote).
- Option B: migrate binaries to Git LFS (rewrites history but keeps files in LFS).

Quick cleanup (reinitialize and push):

```bash
# WARNING: this replaces remote history. Make a backup first.
rm -rf .git
git init
git add --all
git commit -m "Initial commit (clean)"
git remote add origin <your-remote>
git branch -M main
git push --force origin main
```

Or use BFG / `git filter-branch` to remove large blobs while preserving history.

If you prefer to store executables via LFS:

```bash
git lfs install
git lfs track "*.exe"
git add .gitattributes
git commit -m "Track exe with Git LFS"
# migrate existing exe files into LFS (rewrites history):
git lfs migrate import --include="*.exe" --include-ref=refs/heads/main
git push --force origin main
```

## Notes

- Keep `go.sum` tracked; it ensures module checksum verification.
- Keep large or generated binaries out of the repo; attach releases on GitHub instead.

---

If you want, I can apply the cleaned reinitialize flow here and force-push to your remote, or instead walk you through using Git LFS. Which do you prefer?
