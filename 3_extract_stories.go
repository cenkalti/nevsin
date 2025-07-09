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
				// Call Azure OpenAI to summarize subtitle
				summary := summarizeSubtitle(string(data))
				// Change extension from .srt to .json for JSON output
				baseFilename := strings.TrimSuffix(filename, ".srt")
				outPath := filepath.Join("stories", baseFilename+".json")
				if err := os.WriteFile(outPath, []byte(summary), 0644); err != nil {
					log.Printf("Failed to write summary file: %v", err)
				}
			}(file.Name())
		}
		wg.Wait()
		log.Println("Story extraction complete.")
	},
}

// summarizeSubtitle extracts multiple news stories from Turkish subtitle using Azure OpenAI
func summarizeSubtitle(subtitle string) string {
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
		log.Printf("Failed to marshal request: %v", err)
		return "{\"stories\":[]}"
	}

	// Make request to Azure OpenAI
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2024-08-01-preview", endpoint, deployment)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Failed to create request: %v", err)
		return "{\"stories\":[]}"
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to call Azure OpenAI: %v", err)
		return "{\"stories\":[]}"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Azure OpenAI error (status %d): %s", resp.StatusCode, string(body))
		return "{\"stories\":[]}"
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("Failed to decode response: %v", err)
		return "{\"stories\":[]}"
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		log.Printf("No content in response")
		return "{\"stories\":[]}"
	}

	// Parse the structured JSON response
	var newsResponse NewsExtractionResponse
	if err := json.Unmarshal([]byte(result.Choices[0].Message.Content), &newsResponse); err != nil {
		log.Printf("Failed to parse structured response: %v", err)
		return "{\"stories\":[]}"
	}

	// Return structured JSON response
	jsonData, err := json.MarshalIndent(newsResponse, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal news response: %v", err)
		return "{\"stories\":[]}"
	}

	return string(jsonData)
}