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
				newsResponse, err := extractSubtitles(string(data), videoID, video.ChannelID)
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

// extractSubtitles extracts multiple news stories from Turkish subtitle using Azure OpenAI
func extractSubtitles(subtitle, videoID, channelID string) (NewsExtractionResponse, error) {
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

	// Make request to Azure OpenAI
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2024-08-01-preview", endpoint, deployment)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to call Azure OpenAI: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return NewsExtractionResponse{}, fmt.Errorf("azure OpenAI error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
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

	// Add video ID and channel ID to each story
	for i := range newsResponse.Stories {
		newsResponse.Stories[i].VideoID = videoID
		newsResponse.Stories[i].ChannelID = channelID
	}

	return newsResponse, nil
}