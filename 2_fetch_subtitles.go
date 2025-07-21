package nevsin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

// FetchSubtitlesCmd: Reads videos/, saves subtitles/videoID.srt
var FetchSubtitlesCmd = &cobra.Command{
	Use:   "fetch-subtitles",
	Short: "Extract subtitles from videos",
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
				outPath := filepath.Join("subtitles", video.ID+".srt")
				cmdArgs := []string{
					"--write-auto-subs",
					"--sub-lang", "tr",
					"--sub-format", "srt",
					"--skip-download",
					"--output", "%(id)s.%(ext)s",
					"--user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
					"--sleep-interval", "1",
					"--max-sleep-interval", "3",
					"--extractor-retries", "3",
					"--no-check-certificate",
					video.URL,
				}
				tmpDir := "subtitles_tmp"
				if err := os.MkdirAll(tmpDir, 0755); err != nil {
					log.Printf("Failed to create temp directory: %v", err)
					return
				}
				cmd := exec.Command("yt-dlp", cmdArgs...)
				cmd.Dir = tmpDir
				output, err := cmd.CombinedOutput()
				if err != nil {
					log.Printf("yt-dlp failed for %s: %v", video.ID, err)
					log.Printf("yt-dlp error output: %s", string(output))
					return
				}
				// Find the .srt file
				files2, _ := os.ReadDir(tmpDir)
				for _, f := range files2 {
					if strings.HasPrefix(f.Name(), video.ID) && strings.HasSuffix(f.Name(), ".srt") {
						srtPath := filepath.Join(tmpDir, f.Name())
						// Post-process SRT file for LLM readability
						if err := processAndSaveSRT(srtPath, outPath); err != nil {
							log.Printf("Failed to process subtitle file: %v", err)
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

// processAndSaveSRT converts SRT format to simplified format for LLM processing
func processAndSaveSRT(inputPath, outputPath string) error {
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer func() {
		if closeErr := inputFile.Close(); closeErr != nil {
			log.Printf("Failed to close input file: %v", closeErr)
		}
	}()

	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() {
		if closeErr := outputFile.Close(); closeErr != nil {
			log.Printf("Failed to close output file: %v", closeErr)
		}
	}()

	scanner := bufio.NewScanner(inputFile)
	
	// Regex to parse SRT timestamp format: HH:MM:SS,mmm --> HH:MM:SS,mmm
	timestampRegex := regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2}),(\d{3})\s*-->\s*\d{2}:\d{2}:\d{2},\d{3}$`)
	
	var currentText strings.Builder
	var currentStartSeconds int
	
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		
		// Skip subtitle sequence numbers (lines that are just numbers)
		if matches := regexp.MustCompile(`^\d+$`).FindString(line); matches != "" {
			continue
		}
		
		// Check if it's a timestamp line
		if matches := timestampRegex.FindStringSubmatch(line); matches != nil {
			// Write previous subtitle if we have text
			if currentText.Len() > 0 {
				if _, writeErr := fmt.Fprintf(outputFile, "%d: %s\n", currentStartSeconds, strings.TrimSpace(currentText.String())); writeErr != nil {
					return fmt.Errorf("failed to write subtitle: %w", writeErr)
				}
				currentText.Reset()
			}
			
			// Parse start time to total seconds
			hours, _ := strconv.Atoi(matches[1])
			minutes, _ := strconv.Atoi(matches[2])
			seconds, _ := strconv.Atoi(matches[3])
			currentStartSeconds = hours*3600 + minutes*60 + seconds
			continue
		}
		
		// If it's not empty and not a timestamp, it's subtitle text
		if line != "" {
			if currentText.Len() > 0 {
				currentText.WriteString(" ")
			}
			currentText.WriteString(line)
		}
	}
	
	// Write the last subtitle if we have text
	if currentText.Len() > 0 {
		if _, writeErr := fmt.Fprintf(outputFile, "%d: %s\n", currentStartSeconds, strings.TrimSpace(currentText.String())); writeErr != nil {
			return fmt.Errorf("failed to write final subtitle: %w", writeErr)
		}
	}
	
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading input file: %w", err)
	}
	
	return nil
}