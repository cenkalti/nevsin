package nevsin

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

// FetchSubtitlesCmd: Reads videos/, saves transcripts/videoID.txt
var FetchSubtitlesCmd = &cobra.Command{
	Use:   "fetch-subtitles",
	Short: "Extract transcripts from videos",
	Run: func(cmd *cobra.Command, args []string) {
		files, err := os.ReadDir("videos")
		if err != nil {
			log.Printf("Failed to read videos directory: %v", err)
			return
		}
		var wg sync.WaitGroup
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			wg.Add(1)
			go func(filename string) {
				defer wg.Done()
				data, err := os.ReadFile(filepath.Join("videos", filename))
				if err != nil {
					log.Printf("Failed to read %s: %v", filename, err)
					return
				}
				var video YouTubeVideo
				if err := json.Unmarshal(data, &video); err != nil {
					log.Printf("Failed to parse %s: %v", filename, err)
					return
				}
				outPath := filepath.Join("transcripts", video.ID+".txt")
				cmdArgs := []string{
					"--write-auto-subs",
					"--sub-lang", "tr",
					"--sub-format", "srt",
					"--skip-download",
					"--output", "%(id)s.%(ext)s",
					video.URL,
				}
				tmpDir := "transcripts_tmp"
				if err := os.MkdirAll(tmpDir, 0755); err != nil {
					log.Printf("Failed to create temp directory: %v", err)
					return
				}
				cmd := exec.Command("yt-dlp", cmdArgs...)
				cmd.Dir = tmpDir
				if err := cmd.Run(); err != nil {
					log.Printf("yt-dlp failed for %s: %v (skipping)", video.ID, err)
					return
				}
				// Find the .srt file
				files2, _ := os.ReadDir(tmpDir)
				for _, f := range files2 {
					if strings.HasPrefix(f.Name(), video.ID) && strings.HasSuffix(f.Name(), ".srt") {
						srtPath := filepath.Join(tmpDir, f.Name())
						srtData, _ := os.ReadFile(srtPath)
						// Save as .txt (could convert to plain text here)
						if err := os.WriteFile(outPath, srtData, 0644); err != nil {
							log.Printf("Failed to write transcript file: %v", err)
						}
						if err := os.Remove(srtPath); err != nil {
							log.Printf("Failed to remove temp file: %v", err)
						}
					}
				}
			}(file.Name())
		}
		wg.Wait()
		log.Println("Subtitle extraction complete.")
	},
}