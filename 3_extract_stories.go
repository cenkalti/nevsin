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
	Title       string `json:"title" jsonschema:"description=Haberin başlığı"`
	Summary     string `json:"summary" jsonschema:"description=Haberin detaylı özeti"`
	StartSecond int    `json:"start_second" jsonschema:"description=Haberin başlangıç saniyesi"`
	EndSecond   int    `json:"end_second" jsonschema:"description=Haberin bitiş saniyesi"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	StoryURL    string `json:"story_url"`
	VideoID     string `json:"video_id"`
	ChannelID   string `json:"channel_id"`
	Reporter    string `json:"reporter"`
}

// NewsExtractionResponse represents the structured response from Azure OpenAI
type NewsExtractionResponse struct {
	Stories []NewsStory `json:"stories"`
}

// SimplifiedSubtitleEntry represents a single subtitle entry from simplified format
type SimplifiedSubtitleEntry struct {
	Second int
	Text   string
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
	newsResponse, err := extractStoriesWithRetry(string(data), videoID, video.ChannelID, video.Chapters)
	if err != nil {
		return fmt.Errorf("failed to extract stories from subtitle: %w", err)
	}

	// Populate start and end times from seconds
	populateTimesFromSeconds(newsResponse.Stories)

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

func extractStories(subtitle, videoID, channelID string, chapters []YouTubeChapter) (NewsExtractionResponse, error) {
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

	// Format chapter information for LLM context
	chapterInfo := formatChapterInfo(chapters)

	// Prepare the request payload
	requestBody := map[string]any{
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "Sen Türkçe haber metinlerini analiz eden bir uzmansın. Verilen altyazıdan birden fazla haber hikayesini çıkarman gerekiyor. Her haber için başlık, özet ve başlangıç/bitiş saniyelerini belirle.\n\nALTYAZI FORMATI: Altyazı basitleştirilmiş formattadır. Her satır şu şekildedir:\n[saniye]: [metin]\n\nÖrnek:\n7: Retro, retro arkadaşlar. Retro. Sorun\n10: retrodan kaynaklanıyor. Merkür retrosu\n13: Aslan burcunda gerçekleşiyormuş. Ben\n\nSadece gerçek haber içeriğini çıkar, reklam veya genel konuşmaları dahil etme. Her haber için start_second ve end_second değerlerini saniye cinsinden belirle.",
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("Bu altyazıdan tüm haber hikayelerini çıkar ve her biri için başlık, detaylı özet ve başlangıç/bitiş saniyelerini belirle. Altyazı formatı: [saniye]: [metin] şeklindedir.\n\n%s\n\nALTYAZI:\n%s", chapterInfo, subtitle),
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
func extractStoriesWithRetry(subtitle, videoID, channelID string, chapters []YouTubeChapter) (NewsExtractionResponse, error) {
	maxRetries := 3
	baseDelay := 2 * time.Second

	for attempt := range maxRetries {
		newsResponse, err := extractStories(subtitle, videoID, channelID, chapters)
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


// populateTimesFromSeconds populates StartTime and EndTime fields from seconds
func populateTimesFromSeconds(stories []NewsStory) {
	for i := range stories {
		story := &stories[i]

		// Convert seconds to MM:SS format
		story.StartTime = convertSecondsToMMSS(story.StartSecond)
		story.EndTime = convertSecondsToMMSS(story.EndSecond)

		// Generate story URL with timestamp
		story.StoryURL = generateStoryURLFromSeconds(story.VideoID, story.StartSecond)
	}
}


// convertSecondsToMMSS converts total seconds to MM:SS format
func convertSecondsToMMSS(totalSeconds int) string {
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

// generateStoryURLFromSeconds creates a YouTube URL with timestamp from video ID and seconds
func generateStoryURLFromSeconds(videoID string, startSeconds int) string {
	if startSeconds > 0 {
		return fmt.Sprintf("https://www.youtube.com/watch?v=%s&t=%ds", videoID, startSeconds)
	}
	return fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
}

// formatChapterInfo formats chapter information for LLM context
func formatChapterInfo(chapters []YouTubeChapter) string {
	if len(chapters) == 0 {
		return "VIDEO BÖLÜMLERI: Bu video için bölüm bilgisi bulunmuyor."
	}
	
	var chapterInfo strings.Builder
	chapterInfo.WriteString("VIDEO BÖLÜMLERI: Bu videoda aşağıdaki bölümler bulunuyor:\n")
	
	for _, chapter := range chapters {
		startSeconds := int(chapter.StartTime)
		endSeconds := int(chapter.EndTime)
		
		chapterInfo.WriteString(fmt.Sprintf("- %d-%d saniye arası: %s\n", 
			startSeconds, endSeconds, chapter.Title))
	}
	
	chapterInfo.WriteString("\nBu bölüm bilgilerini haber hikayelerini çıkarırken referans olarak kullan.")
	
	return chapterInfo.String()
}
