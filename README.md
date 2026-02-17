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
Downloaded binaries are accepted only after SHA-256 verification.

Usage: run the GUI, paste a video URL, and choose download.

## Release Notes (Current Build)

- File: `YoutubeVideoDownloader.exe`
- SHA-256: `0cfd757b042930cb18ecc800ae4ba45b8397be9d3f5d10675001b01dac709f11`

### User verification (Linux/macOS/WSL)

```bash
gpg --import public-key.asc
gpg --verify YoutubeVideoDownloader.exe.asc YoutubeVideoDownloader.exe
gpg --verify YoutubeVideoDownloader.exe.sha256.asc YoutubeVideoDownloader.exe.sha256
sha256sum -c YoutubeVideoDownloader.exe.sha256
```

Expected output includes:

- `Good signature from "HSVISHAL <hsvishal26@gmail.com>"`
- `YoutubeVideoDownloader.exe: OK`

### User verification (Windows)

PowerShell checksum check:

```powershell
Get-FileHash .\YoutubeVideoDownloader.exe -Algorithm SHA256
```

Expected SHA-256:

`0cfd757b042930cb18ecc800ae4ba45b8397be9d3f5d10675001b01dac709f11`

Optional signature verification on Windows (requires Gpg4win):

```powershell
gpg --import .\public-key.asc
gpg --verify .\YoutubeVideoDownloader.exe.asc .\YoutubeVideoDownloader.exe
gpg --verify .\YoutubeVideoDownloader.exe.sha256.asc .\YoutubeVideoDownloader.exe.sha256
```

### First-run ffmpeg download

- `ffmpeg.exe` is downloaded on first run if missing from the app cache.
- Default source: `https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip`
- Override source with env var: `YTGUI_FFMPEG_URL`
- Optional explicit ffmpeg checksum override: `YTGUI_FFMPEG_SHA256`
- Optional explicit ffmpeg checksum URL override: `YTGUI_FFMPEG_SHA256_URL`
- For the default source, checksum is resolved from:
  `https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip.sha256`
- Supported formats for `YTGUI_FFMPEG_URL`: direct `.exe` or `.zip` containing `ffmpeg.exe` (preferably under `bin/`).

### yt-dlp checksum override

- `yt-dlp.exe` checksum is read from the official `SHA2-256SUMS` release asset.
- Optional explicit checksum override: `YTGUI_YTDLP_SHA256`

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
