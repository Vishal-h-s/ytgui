#!/bin/bash

# 1. Create a directory for the Windows tools
mkdir -p ./win_tools
cd ./win_tools

# 2. Download yt-dlp.exe (Official Windows Release)
echo "Downloading yt-dlp.exe..."
wget https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe

# 3. Download FFmpeg Essentials Zip (Windows Build)
echo "Downloading FFmpeg Essentials Zip..."
wget https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip

# 4. Extract only ffmpeg.exe from the zip
echo "Extracting ffmpeg.exe..."
# This finds the ffmpeg.exe inside the nested folders of the zip and pulls it to the current dir
unzip -j ffmpeg-release-essentials.zip '**/bin/ffmpeg.exe'

# 5. Cleanup
rm ffmpeg-release-essentials.zip

echo "---------------------------------------"
echo "Success! Your Windows files are ready:"
ls -lh