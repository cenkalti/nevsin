package nevsin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"
)

// NewsStory represents a single news story extracted from subtitle
type NewsStory struct {
	Title      string `json:"title" jsonschema:"description=Haberin başlığı"`
	Summary    string `json:"summary" jsonschema:"description=Haberin detaylı özeti"`
	StartIndex int    `json:"start_index" jsonschema:"description=Haberin başlangıç SRT index numarası"`
	EndIndex   int    `json:"end_index" jsonschema:"description=Haberin bitiş SRT index numarası"`
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time"`
	StoryURL   string `json:"story_url"`
	VideoID    string `json:"video_id"`
	ChannelID  string `json:"channel_id"`
	Reporter   string `json:"reporter"`
}

// NewsExtractionResponse represents the structured response from Azure OpenAI
type NewsExtractionResponse struct {
	Stories []NewsStory `json:"stories"`
}

// SRTEntry represents a single subtitle entry from SRT file
type SRTEntry struct {
	Index     int
	StartTime string
	EndTime   string
	Text      string
}

var ExtractStoriesCmd = &cobra.Command{
	Use:   "extract-stories [video-id]",
	Short: "Summarize subtitles",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// If video ID is provided, process single video
		if len(args) > 0 {
			videoID := args[0]
			if err := processVideoStories(videoID); err != nil {
				log.Printf("Failed to process video %s: %v", videoID, err)
				return
			}
			log.Printf("Story extraction complete for video: %s", videoID)
			return
		}

		// Otherwise, process all videos in batch
		files, err := os.ReadDir("subtitles")
		if err != nil {
			log.Printf("Failed to read subtitles directory: %v", err)
			return
		}

		var wg sync.WaitGroup
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".srt") {
				continue
			}

			videoID := strings.TrimSuffix(file.Name(), ".srt")

			wg.Add(1)
			go func(videoID string) {
				defer wg.Done()
				if err := processVideoStories(videoID); err != nil {
					log.Printf("Failed to process video %s: %v", videoID, err)
				}
			}(videoID)
		}
		wg.Wait()
		log.Println("Story extraction complete.")
	},
}

// processVideoStories processes a single video's subtitles to extract stories
func processVideoStories(videoID string) error {
	// Read subtitle file
	subtitlePath := filepath.Join("subtitles", videoID+".srt")
	data, err := os.ReadFile(subtitlePath)
	if err != nil {
		return fmt.Errorf("failed to read subtitle file: %w", err)
	}

	// Read video metadata to get channel ID
	video, err := readVideo(videoID)
	if err != nil {
		return fmt.Errorf("failed to read video metadata: %w", err)
	}

	// Call Azure OpenAI to extract stories from subtitle with retry logic
	newsResponse, err := extractStoriesWithRetry(string(data), videoID, video.ChannelID)
	if err != nil {
		return fmt.Errorf("failed to extract stories from subtitle: %w", err)
	}

	// Parse SRT file to get time information
	srtEntries := parseSRTFile(string(data))

	// Populate start and end times from SRT indices
	populateTimesFromSRT(newsResponse.Stories, srtEntries)

	// Marshal the response to JSON without HTML escaping
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(newsResponse); err != nil {
		return fmt.Errorf("failed to marshal news response: %w", err)
	}
	jsonData := buffer.Bytes()

	// Write to output file
	outPath := filepath.Join("stories", videoID+".json")
	if err := os.WriteFile(outPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write story file: %w", err)
	}

	return nil
}

// readVideo reads video metadata from videos/videoID.json
func readVideo(videoID string) (YouTubeVideo, error) {
	videoPath := filepath.Join("videos", videoID+".json")
	data, err := os.ReadFile(videoPath)
	if err != nil {
		return YouTubeVideo{}, fmt.Errorf("failed to read video metadata: %w", err)
	}

	var video YouTubeVideo
	if err := json.Unmarshal(data, &video); err != nil {
		return YouTubeVideo{}, fmt.Errorf("failed to parse video metadata: %w", err)
	}

	return video, nil
}

// getReporterName returns the reporter name for a given channel ID
func getReporterName(channelID string) string {
	for _, channel := range ChannelConfigs {
		if channel.ID == channelID {
			return channel.Name
		}
	}
	return "Unknown Reporter"
}

// parseRetryAfter parses the Retry-After header value and returns duration
func parseRetryAfter(retryAfter string) time.Duration {
	if retryAfter == "" {
		return 0
	}

	// Try to parse as seconds (numeric value)
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try to parse as HTTP date format
	if retryTime, err := time.Parse(time.RFC1123, retryAfter); err == nil {
		return time.Until(retryTime)
	}

	return 0
}

// makeOpenAIRequest makes a request to Azure OpenAI with retry logic for 429 errors
func makeOpenAIRequest(requestBody []byte, endpoint, apiKey, deployment string) ([]byte, error) {
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2024-08-01-preview", endpoint, deployment)
	client := &http.Client{Timeout: 120 * time.Second} // Increased timeout for longer waits

	// Retry configuration - increased for better resilience
	maxRetries := 5
	baseDelay := 5 * time.Second
	maxDelay := 120 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api-key", apiKey)

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to call Azure OpenAI: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		// Check for rate limit (429) errors
		if resp.StatusCode == 429 {
			if attempt == maxRetries {
				return nil, fmt.Errorf("azure OpenAI rate limit exceeded after %d retries (status %d): %s", maxRetries, resp.StatusCode, string(body))
			}

			// Check for Retry-After header
			retryAfter := resp.Header.Get("Retry-After")
			retryDelay := parseRetryAfter(retryAfter)

			// If no Retry-After header or invalid, use exponential backoff
			if retryDelay <= 0 {
				retryDelay = baseDelay * time.Duration(1<<attempt) // 5s, 10s, 20s, 40s, 80s
			}

			// Cap the delay to prevent extremely long waits
			if retryDelay > maxDelay {
				retryDelay = maxDelay
			}

			log.Printf("Rate limit hit (attempt %d/%d), retrying in %v (retry-after: %s)...", attempt+1, maxRetries+1, retryDelay, retryAfter)
			time.Sleep(retryDelay)
			continue
		}

		// Handle other non-success status codes
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("azure OpenAI error (status %d): %s", resp.StatusCode, string(body))
		}

		// Success - return the response body
		return body, nil
	}

	// This should never be reached due to the loop logic
	return nil, fmt.Errorf("unexpected error in retry loop")
}

func extractStories(subtitle, videoID, channelID string) (NewsExtractionResponse, error) {
	endpoint := Config.AzureOpenAIEndpoint
	apiKey := Config.AzureOpenAIAPIKey
	deployment := Config.AzureOpenAIDeployment

	// Generate JSON schema for structured output
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	schemaObj := reflector.Reflect(&NewsExtractionResponse{})

	// Ensure the schema has the correct type
	if schemaObj.Type == "" {
		schemaObj.Type = "object"
	}

	// Convert to map[string]any to ensure proper JSON serialization
	schemaBytes, err := json.Marshal(schemaObj)
	if err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to marshal schema: %w", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	// Prepare the request payload
	requestBody := map[string]any{
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "Sen Türkçe haber metinlerini analiz eden bir uzmansın. Verilen altyazıdan birden fazla haber hikayesini çıkarman gerekiyor. Her haber için başlık, özet ve SRT altyazı index numaralarını belirle. Index numaraları altyazı dosyasındaki satır numaralarını temsil eder. Sadece gerçek haber içeriğini çıkar, reklam veya genel konuşmaları dahil etme.",
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("Bu altyazıdan tüm haber hikayelerini çıkar ve her biri için başlık, detaylı özet ve SRT altyazı index numaralarını belirle. Index numaraları altyazı dosyasındaki satır numaralarını temsil eder:\n\n%s", subtitle),
			},
		},
		"max_tokens":  4000,
		"temperature": 0.1,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "news_extraction",
				"schema": schema,
			},
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make request to Azure OpenAI with retry logic
	responseBody, err := makeOpenAIRequest(jsonBody, endpoint, apiKey, deployment)
	if err != nil {
		return NewsExtractionResponse{}, err
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(responseBody, &result); err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return NewsExtractionResponse{}, fmt.Errorf("no content in response")
	}

	// Parse the structured JSON response
	var newsResponse NewsExtractionResponse
	if err := json.Unmarshal([]byte(result.Choices[0].Message.Content), &newsResponse); err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to parse structured response: %w", err)
	}

	// Add video ID, channel ID, and reporter name to each story
	reporterName := getReporterName(channelID)
	for i := range newsResponse.Stories {
		newsResponse.Stories[i].VideoID = videoID
		newsResponse.Stories[i].ChannelID = channelID
		newsResponse.Stories[i].Reporter = reporterName
	}

	return newsResponse, nil
}

// extractStoriesWithRetry wraps extractStories with retry logic for transient failures
func extractStoriesWithRetry(subtitle, videoID, channelID string) (NewsExtractionResponse, error) {
	maxRetries := 3
	baseDelay := 2 * time.Second

	for attempt := range maxRetries {
		newsResponse, err := extractStories(subtitle, videoID, channelID)
		if err != nil {
			// Check if it's a retryable error
			if strings.Contains(err.Error(), "unexpected end of JSON input") ||
				strings.Contains(err.Error(), "failed to parse structured response") ||
				strings.Contains(err.Error(), "no content in response") {

				if attempt == maxRetries-1 {
					return NewsExtractionResponse{}, fmt.Errorf("failed to extract stories after %d retries: %w", maxRetries, err)
				}

				delay := baseDelay * time.Duration(attempt+1) // 2s, 4s, 6s
				log.Printf("Transient error for video %s (attempt %d/%d), retrying in %v: %v",
					videoID, attempt+1, maxRetries, delay, err)
				time.Sleep(delay)
				continue
			}
			// For other errors, don't retry
			return NewsExtractionResponse{}, err
		}

		// Success
		return newsResponse, nil
	}

	return NewsExtractionResponse{}, fmt.Errorf("unexpected error in retry loop")
}

// parseSRTFile parses an SRT file and returns a map of index to SRTEntry
func parseSRTFile(srtContent string) map[int]SRTEntry {
	entries := make(map[int]SRTEntry)

	// Use SplitSeq for more efficient iteration over split strings
	for block := range strings.SplitSeq(strings.ReplaceAll(srtContent, "\r\n", "\n"), "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}

		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 3 {
			continue
		}

		// Parse index
		index, err := strconv.Atoi(strings.TrimSpace(lines[0]))
		if err != nil {
			continue
		}

		// Parse time range
		timeRange := strings.TrimSpace(lines[1])
		timeParts := strings.Split(timeRange, " --> ")
		if len(timeParts) != 2 {
			continue
		}

		startTime := convertSRTTimeToMMSS(timeParts[0])
		endTime := convertSRTTimeToMMSS(timeParts[1])

		// Join remaining lines as text
		text := strings.Join(lines[2:], " ")

		entries[index] = SRTEntry{
			Index:     index,
			StartTime: startTime,
			EndTime:   endTime,
			Text:      text,
		}
	}

	return entries
}

// convertSRTTimeToMMSS converts SRT time format (HH:MM:SS,mmm) to MM:SS format
func convertSRTTimeToMMSS(srtTime string) string {
	// Remove milliseconds part
	srtTime = strings.Split(srtTime, ",")[0]

	// Parse HH:MM:SS
	parts := strings.Split(srtTime, ":")
	if len(parts) != 3 {
		return "00:00"
	}

	hours, _ := strconv.Atoi(parts[0])
	minutes, _ := strconv.Atoi(parts[1])
	seconds, _ := strconv.Atoi(parts[2])

	// Convert to total minutes and seconds
	totalMinutes := hours*60 + minutes

	return fmt.Sprintf("%02d:%02d", totalMinutes, seconds)
}

// populateTimesFromSRT populates StartTime and EndTime fields from SRT indices
func populateTimesFromSRT(stories []NewsStory, srtEntries map[int]SRTEntry) {
	for i := range stories {
		story := &stories[i]

		// Get start time from start index
		if startEntry, exists := srtEntries[story.StartIndex]; exists {
			story.StartTime = startEntry.StartTime
		} else {
			story.StartTime = "00:00"
		}

		// Get end time from end index
		if endEntry, exists := srtEntries[story.EndIndex]; exists {
			story.EndTime = endEntry.EndTime
		} else {
			story.EndTime = "00:00"
		}

		// Generate story URL with timestamp
		story.StoryURL = generateStoryURL(story.VideoID, story.StartTime)
	}
}

// generateStoryURL creates a YouTube URL with timestamp from video ID and start time
func generateStoryURL(videoID, startTime string) string {
	// Convert MM:SS format to seconds
	timeSeconds := convertMMSSToSeconds(startTime)
	if timeSeconds > 0 {
		return fmt.Sprintf("https://www.youtube.com/watch?v=%s&t=%ds", videoID, timeSeconds)
	}
	return fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
}

// convertMMSSToSeconds converts MM:SS format to total seconds
func convertMMSSToSeconds(timeStr string) int {
	if timeStr == "" {
		return 0
	}

	parts := strings.Split(timeStr, ":")
	if len(parts) != 2 {
		return 0
	}

	var minutes, seconds int
	if _, err := fmt.Sscanf(parts[0], "%d", &minutes); err != nil {
		return 0
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &seconds); err != nil {
		return 0
	}

	return minutes*60 + seconds
}
