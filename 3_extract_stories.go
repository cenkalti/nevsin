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

	"github.com/spf13/cobra"
)

// NewsStory represents a single news story extracted from subtitle
type NewsStory struct {
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	VideoID   string `json:"video_id"`
	ChannelID string `json:"channel_id"`
	Reporter  string `json:"reporter"`
}

// NewsExtractionResponse represents the structured response from Azure OpenAI
type NewsExtractionResponse struct {
	Stories []NewsStory `json:"stories"`
}

var ExtractStoriesCmd = &cobra.Command{
	Use:   "extract-stories",
	Short: "Summarize subtitles",
	Run: func(cmd *cobra.Command, args []string) {
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
			wg.Add(1)
			go func(filename string) {
				defer wg.Done()
				data, err := os.ReadFile(filepath.Join("subtitles", filename))
				if err != nil {
					log.Printf("Failed to read %s: %v", filename, err)
					return
				}
				// Change extension from .srt to get video ID
				baseFilename := strings.TrimSuffix(filename, ".srt")
				videoID := baseFilename

				// Read video video to get channel ID
				video, err := readVideo(videoID)
				if err != nil {
					log.Printf("Failed to read video metadata for %s: %v", videoID, err)
					return
				}

				// Call Azure OpenAI to extract stories from subtitle
				newsResponse, err := extractStories(string(data), videoID, video.ChannelID)
				if err != nil {
					log.Printf("Failed to extract stories from subtitle for %s: %v", videoID, err)
					return
				}

				// Marshal the response to JSON
				jsonData, err := json.MarshalIndent(newsResponse, "", "  ")
				if err != nil {
					log.Printf("Failed to marshal news response for %s: %v", videoID, err)
					return
				}

				outPath := filepath.Join("stories", baseFilename+".json")
				if err := os.WriteFile(outPath, jsonData, 0644); err != nil {
					log.Printf("Failed to write summary file: %v", err)
				}
			}(file.Name())
		}
		wg.Wait()
		log.Println("Story extraction complete.")
	},
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

	// Define JSON schema for structured output
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"stories": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title": map[string]any{
							"type":        "string",
							"description": "Haberin başlığı",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Haberin detaylı özeti",
						},
						"start_time": map[string]any{
							"type":        "string",
							"description": "Haberin başlangıç zamanı (MM:SS formatında)",
						},
						"end_time": map[string]any{
							"type":        "string",
							"description": "Haberin bitiş zamanı (MM:SS formatında)",
						},
					},
					"required": []string{"title", "summary", "start_time", "end_time"},
				},
			},
		},
		"required": []string{"stories"},
	}

	// Prepare the request payload
	requestBody := map[string]any{
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "Sen Türkçe haber metinlerini analiz eden bir uzmansın. Verilen altyazıdan birden fazla haber hikayesini çıkarman gerekiyor. Her haber için başlık, özet ve zaman damgalarını belirle. Sadece gerçek haber içeriğini çıkar, reklam veya genel konuşmaları dahil etme.",
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("Bu altyazıdan tüm haber hikayelerini çıkar ve her biri için başlık, detaylı özet ve zaman aralığını belirle:\n\n%s", subtitle),
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