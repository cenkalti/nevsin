package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// Global config instance holds all environment variables
var config struct {
	YouTubeAPIKey         string
	AzureOpenAIEndpoint   string
	AzureOpenAIAPIKey     string
	AzureOpenAIDeployment string
}

func getenv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("Missing required environment variable: %s", key)
	}
	return value
}

func main() {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	// Load environment variables into config
	config.YouTubeAPIKey = getenv("YOUTUBE_API_KEY")
	config.AzureOpenAIEndpoint = getenv("AZURE_OPENAI_ENDPOINT")
	config.AzureOpenAIAPIKey = getenv("AZURE_OPENAI_API_KEY")
	config.AzureOpenAIDeployment = getenv("AZURE_OPENAI_DEPLOYMENT")

	rootCmd := &cobra.Command{
		Use:   "nevsin",
		Short: "Multi-Language YouTube News Aggregator CLI",
	}

	rootCmd.AddCommand(fetchCmd)
	rootCmd.AddCommand(extractCmd)
	rootCmd.AddCommand(summarizeCmd)
	rootCmd.AddCommand(generateCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(cleanCmd)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

// fetchCmd: Reads channels.txt, saves videos/videoID.json
var fetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Fetch recent videos from channels",
	Run: func(cmd *cobra.Command, args []string) {
		channels := []ChannelConfig{
			{
				Name: "Nevsin Mengu",
				ID:   "UCrG27KDq7eW4YoEOYsalU9g",
				Handler: func(videos []YouTubeVideo) []YouTubeVideo {
					// Get videos from last 48 hours, analyze thumbnails, find "Bugun ne oldu?"
					for _, v := range videos {
						if time.Since(v.PublishedAt) > 48*time.Hour {
							continue
						}
						// Analyze thumbnail with Azure OpenAI
						extractedTitle, err := analyzeThumbnail(v.ThumbnailURL)
						if err != nil {
							log.Printf("Thumbnail analysis failed: %v", err)
							continue
						}
						// Check if the title contains "Bugün ne oldu" (case insensitive)
						if strings.Contains(strings.ToLower(extractedTitle), "bugün ne oldu") {
							return []YouTubeVideo{v}
						}
					}
					return nil
				},
			},
			{
				Name: "Fatih Altayli",
				ID:   "UCdS7OE5qbJQc7AG4SwlTzKg",
				Handler: func(videos []YouTubeVideo) []YouTubeVideo {
					// Get videos from last 48 hours, title starts with "Fatih Altaylı yorumluyor:"
					for _, v := range videos {
						if time.Since(v.PublishedAt) > 48*time.Hour {
							continue
						}
						if strings.HasPrefix(v.Title, "Fatih Altaylı yorumluyor:") {
							return []YouTubeVideo{v}
						}
					}
					return nil
				},
			},
		}

		var wg sync.WaitGroup
		var mu sync.Mutex
		allVideos := []YouTubeVideo{}
		log.Printf("Processing %d channels...", len(channels))
		for i, ch := range channels {
			wg.Add(1)
			go func(idx int, chInfo ChannelConfig) {
				defer wg.Done()
				log.Printf("Channel %d/%d: %s", idx+1, len(channels), chInfo.Name)
				videos, err := fetchYouTubeVideos(chInfo.ID)
				if err != nil {
					log.Fatalf("Failed to fetch videos for %s: %v", chInfo.Name, err)
				}
				selected := chInfo.Handler(videos)
				log.Printf("Channel %s: Found %d videos", chInfo.Name, len(selected))
				for _, v := range selected {
					saveVideoMetadata(v)
				}
				mu.Lock()
				allVideos = append(allVideos, selected...)
				mu.Unlock()
			}(i, ch)
		}
		wg.Wait()
		log.Println("Fetch complete.")
	},
}

// ChannelConfig represents a YouTube channel configuration
type ChannelConfig struct {
	Name    string
	ID      string
	Handler func([]YouTubeVideo) []YouTubeVideo
}

// YouTubeVideo represents minimal video metadata
type YouTubeVideo struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	PublishedAt  time.Time `json:"published_at"`
	ThumbnailURL string    `json:"thumbnail_url"`
	ChannelID    string    `json:"channel_id"`
	URL          string    `json:"url"`
}

// fetchYouTubeVideos fetches recent videos for a channel using the YouTube Data API v3
func fetchYouTubeVideos(channelID string) ([]YouTubeVideo, error) {
	apiKey := config.YouTubeAPIKey

	// Fetch the latest 10 videos from the channel
	url := fmt.Sprintf(
		"https://www.googleapis.com/youtube/v3/search?key=%s&channelId=%s&part=snippet,id&order=date&maxResults=10&type=video",
		apiKey, channelID,
	)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch videos: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("YouTube API error: %s", string(body))
	}

	var searchResult struct {
		Items []struct {
			ID struct {
				VideoID string `json:"videoId"`
			} `json:"id"`
			Snippet struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				PublishedAt string `json:"publishedAt"`
				Thumbnails  struct {
					High struct {
						URL string `json:"url"`
					} `json:"high"`
					Default struct {
						URL string `json:"url"`
					} `json:"default"`
				} `json:"thumbnails"`
			} `json:"snippet"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchResult); err != nil {
		return nil, fmt.Errorf("failed to decode YouTube API response: %w", err)
	}

	videos := make([]YouTubeVideo, 0, len(searchResult.Items))
	for _, item := range searchResult.Items {
		publishedAt, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
		if err != nil {
			publishedAt = time.Time{}
		}
		thumbURL := item.Snippet.Thumbnails.High.URL
		if thumbURL == "" {
			thumbURL = item.Snippet.Thumbnails.Default.URL
		}
		video := YouTubeVideo{
			ID:           item.ID.VideoID,
			Title:        item.Snippet.Title,
			Description:  item.Snippet.Description,
			PublishedAt:  publishedAt,
			ThumbnailURL: thumbURL,
			ChannelID:    channelID,
			URL:          "https://www.youtube.com/watch?v=" + item.ID.VideoID,
		}
		videos = append(videos, video)
	}

	return videos, nil
}

// analyzeThumbnail analyzes a thumbnail with Azure OpenAI GPT-4 Vision
func analyzeThumbnail(thumbnailURL string) (string, error) {
	endpoint := config.AzureOpenAIEndpoint
	apiKey := config.AzureOpenAIAPIKey
	deployment := config.AzureOpenAIDeployment

	// Download the thumbnail image
	resp, err := http.Get(thumbnailURL)
	if err != nil {
		return "", fmt.Errorf("failed to download thumbnail: %w", err)
	}
	defer resp.Body.Close()

	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read thumbnail data: %w", err)
	}

	// Prepare the request payload for Azure OpenAI GPT-4 Vision
	requestBody := map[string]any{
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "You are an expert at analyzing YouTube video thumbnails. Extract and return ONLY the title text shown in the thumbnail. If there is no visible title text, return 'NO_TITLE'.",
			},
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": "What is the title text shown in this thumbnail? Return only the title text, nothing else.",
					},
					{
						"type": "image_url",
						"image_url": map[string]string{
							"url": fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(imageData)),
						},
					},
				},
			},
		},
		"max_tokens":  100,
		"temperature": 0,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make request to Azure OpenAI
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2024-08-01-preview", endpoint, deployment)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp2, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Azure OpenAI: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		return "", fmt.Errorf("azure OpenAI error (status %d): %s", resp2.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp2.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Choices) > 0 && result.Choices[0].Message.Content != "" {
		extractedTitle := strings.TrimSpace(result.Choices[0].Message.Content)
		log.Printf("Extracted thumbnail title: %s", extractedTitle)
		return extractedTitle, nil
	}

	return "", fmt.Errorf("no title extracted from thumbnail")
}

// saveVideoMetadata saves video metadata as videos/videoID.json
func saveVideoMetadata(video YouTubeVideo) {
	data, _ := json.MarshalIndent(video, "", "  ")
	path := filepath.Join("videos", video.ID+".json")
	_ = os.WriteFile(path, data, 0644)
}

// extractCmd: Reads videos/, saves transcripts/videoID.txt
var extractCmd = &cobra.Command{
	Use:   "extract",
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
				// Find the .vtt file
				files2, _ := os.ReadDir(tmpDir)
				for _, f := range files2 {
					if strings.HasPrefix(f.Name(), video.ID) && strings.HasSuffix(f.Name(), ".vtt") {
						vttPath := filepath.Join(tmpDir, f.Name())
						vttData, _ := os.ReadFile(vttPath)
						// Save as .txt (could convert to plain text here)
						if err := os.WriteFile(outPath, vttData, 0644); err != nil {
							log.Printf("Failed to write transcript file: %v", err)
						}
						if err := os.Remove(vttPath); err != nil {
							log.Printf("Failed to remove temp file: %v", err)
						}
					}
				}
			}(file.Name())
		}
		wg.Wait()
		log.Println("Transcript extraction complete.")
	},
}

var summarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Summarize transcripts",
	Run: func(cmd *cobra.Command, args []string) {
		files, err := os.ReadDir("transcripts")
		if err != nil {
			log.Printf("Failed to read transcripts directory: %v", err)
			return
		}
		var wg sync.WaitGroup
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".txt") {
				continue
			}
			wg.Add(1)
			go func(filename string) {
				defer wg.Done()
				data, err := os.ReadFile(filepath.Join("transcripts", filename))
				if err != nil {
					log.Printf("Failed to read %s: %v", filename, err)
					return
				}
				// Call Azure OpenAI to summarize transcript
				summary := summarizeTranscript(string(data))
				// Change extension from .txt to .json for JSON output
				baseFilename := strings.TrimSuffix(filename, ".txt")
				outPath := filepath.Join("summaries", baseFilename+".json")
				if err := os.WriteFile(outPath, []byte(summary), 0644); err != nil {
					log.Printf("Failed to write summary file: %v", err)
				}
			}(file.Name())
		}
		wg.Wait()
		log.Println("Summarization complete.")
	},
}

// NewsStory represents a single news story extracted from transcript
type NewsStory struct {
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Reporter  string `json:"reporter"`
}

// MergedNewsStory represents a news story merged from multiple sources
type MergedNewsStory struct {
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	Reporters []string `json:"reporters"`
	Priority  int      `json:"priority"`
}

// ReportGenerationResponse represents the structured response from Azure OpenAI for report generation
type ReportGenerationResponse struct {
	Stories []MergedNewsStory `json:"stories"`
}

// NewsExtractionResponse represents the structured response from Azure OpenAI
type NewsExtractionResponse struct {
	Stories []NewsStory `json:"stories"`
}

// summarizeTranscript extracts multiple news stories from Turkish transcript using Azure OpenAI
func summarizeTranscript(transcript string) string {
	endpoint := config.AzureOpenAIEndpoint
	apiKey := config.AzureOpenAIAPIKey
	deployment := config.AzureOpenAIDeployment

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
				"content": "Sen Türkçe haber metinlerini analiz eden bir uzmansın. Verilen transkriptten birden fazla haber hikayesini çıkarman gerekiyor. Her haber için başlık, özet ve zaman damgalarını belirle. Sadece gerçek haber içeriğini çıkar, reklam veya genel konuşmaları dahil etme.",
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("Bu transkriptten tüm haber hikayelerini çıkar ve her biri için başlık, detaylı özet ve zaman aralığını belirle:\n\n%s", transcript),
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

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate final news report",
	Run: func(cmd *cobra.Command, args []string) {
		files, err := os.ReadDir("summaries")
		if err != nil {
			log.Printf("Failed to read summaries directory: %v", err)
			return
		}
		summaries := make(map[string]string)
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join("summaries", file.Name()))
			if err != nil {
				log.Printf("Failed to read %s: %v", file.Name(), err)
				continue
			}
			summaries[file.Name()] = string(data)
		}
		report := generateReport(summaries)
		if err := os.WriteFile("report.md", []byte(report), 0644); err != nil {
			log.Printf("Failed to write report file: %v", err)
			return
		}
		log.Println("Report generated: report.md")
	},
}

// getReporterName maps video ID to reporter name using video metadata
func getReporterName(videoID string) string {
	videoPath := filepath.Join("videos", videoID+".json")

	data, err := os.ReadFile(videoPath)
	if err != nil {
		log.Printf("Failed to read video metadata %s: %v", videoPath, err)
		return "Bilinmeyen Muhabir"
	}

	var video YouTubeVideo
	if err := json.Unmarshal(data, &video); err != nil {
		log.Printf("Failed to parse video metadata %s: %v", videoPath, err)
		return "Bilinmeyen Muhabir"
	}

	// Map channel ID to reporter name
	switch video.ChannelID {
	case "UCrG27KDq7eW4YoEOYsalU9g":
		return "Nevsin Mengu"
	case "UCdS7OE5qbJQc7AG4SwlTzKg":
		return "Fatih Altaylı"
	default:
		return "Bilinmeyen Muhabir"
	}
}

// generateReport uses Azure OpenAI to merge and group news stories from multiple reporters
func generateReport(summaries map[string]string) string {
	endpoint := config.AzureOpenAIEndpoint
	apiKey := config.AzureOpenAIAPIKey
	deployment := config.AzureOpenAIDeployment

	// Parse JSON summaries and add reporter attribution
	var allStories []NewsStory
	for filename, jsonContent := range summaries {
		videoID := strings.TrimSuffix(filename, ".json")

		// Get reporter name from video metadata
		reporterName := getReporterName(videoID)

		// Parse JSON content
		var newsResponse NewsExtractionResponse
		if err := json.Unmarshal([]byte(jsonContent), &newsResponse); err != nil {
			log.Printf("Failed to parse JSON summary %s: %v", filename, err)
			continue
		}

		// Add reporter attribution to each story
		for _, story := range newsResponse.Stories {
			story.Reporter = reporterName
			allStories = append(allStories, story)
		}
	}

	if len(allStories) == 0 {
		return "# Bugün Ne Oldu?\n\nBugün için haber bulunamadı.\n"
	}

	// Convert stories to JSON for the prompt
	storiesJSON, err := json.MarshalIndent(allStories, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal stories: %v", err)
		return "# Bugün Ne Oldu?\n\nHata: Haber verilerini hazırlarken hata oluştu\n"
	}

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
							"description": "Birleştirilmiş haberin başlığı",
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Tüm kaynaklardan birleştirilmiş detaylı haber özeti",
						},
						"reporters": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "string",
							},
							"description": "Bu haberi kapsayan muhabirlerin listesi",
						},
						"priority": map[string]any{
							"type":        "integer",
							"description": "Haberin önem derecesi (1=en önemli, 10=en az önemli)",
						},
					},
					"required": []string{"title", "summary", "reporters", "priority"},
				},
			},
		},
		"required": []string{"stories"},
	}

	// Prepare the request payload
	requestBody := map[string]any{
		"messages": []map[string]any{
			{
				"role": "system",
				"content": `Sen Türkçe haber metinlerini analiz eden ve birleştiren bir uzmansın. Farklı muhabirlerden gelen haber hikayelerini analiz edip, benzer konuları birleştir ve önem sırasına göre sırala. 

Görevlerin:
1. Benzer/ilgili haberleri bir araya getir ve tek bir haber olarak birleştir
2. Her birleştirilmiş haber için hangi muhabirlerin bu konuyu kapsadığını belirt
3. Haberleri önem derecesine göre sırala (1=en önemli, 10=en az önemli)
4. Her haber için kapsamlı bir özet oluştur
5. Türkiye gündemindeki önemli konulara öncelik ver

Dikkat edilecek noktalar:
- Aynı konuyu farklı muhabirler farklı açılardan ele almış olabilir, bunları birleştir
- Her muhabirin katkısını göz önünde bulundur
- Objektif ve tarafsız bir dil kullan
- Haberlerin önem sıralamasını Türkiye gündemi açısından yap`,
			},
			{
				"role": "user",
				"content": fmt.Sprintf(`Bu haber hikayelerini analiz et, benzer konuları birleştir ve önem sırasına göre sırala. Her birleştirilmiş haber için hangi muhabirlerin kapsadığını belirt:

%s`, string(storiesJSON)),
			},
		},
		"max_tokens":  4000,
		"temperature": 0.2,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "report_generation",
				"schema": schema,
			},
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		log.Printf("Failed to marshal request: %v", err)
		return "# Bugün Ne Oldu?\n\nHata: İstek hazırlanamadı\n"
	}

	// Make request to Azure OpenAI
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2024-08-01-preview", endpoint, deployment)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Failed to create request: %v", err)
		return "# Bugün Ne Oldu?\n\nHata: HTTP isteği oluşturulamadı\n"
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to call Azure OpenAI: %v", err)
		return "# Bugün Ne Oldu?\n\nHata: Azure OpenAI çağrısı başarısız\n"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Azure OpenAI error (status %d): %s", resp.StatusCode, string(body))
		return "# Bugün Ne Oldu?\n\nHata: Azure OpenAI API hatası\n"
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
		return "# Bugün Ne Oldu?\n\nHata: Yanıt çözümlenemedi\n"
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		log.Printf("No content in response")
		return "# Bugün Ne Oldu?\n\nHata: Boş yanıt alındı\n"
	}

	// Parse the structured JSON response
	var reportResponse ReportGenerationResponse
	if err := json.Unmarshal([]byte(result.Choices[0].Message.Content), &reportResponse); err != nil {
		log.Printf("Failed to parse structured response: %v", err)
		return "# Bugün Ne Oldu?\n\nHata: Yapılandırılmış yanıt çözümlenemedi\n"
	}

	// Generate final markdown report
	return formatFinalReport(reportResponse.Stories)
}

// formatFinalReport converts merged stories to final markdown format
func formatFinalReport(stories []MergedNewsStory) string {
	if len(stories) == 0 {
		return "# Bugün Ne Oldu?\n\nBugün için haber bulunamadı.\n"
	}

	// Sort stories by priority (lower number = higher priority)
	for i := 0; i < len(stories)-1; i++ {
		for j := i + 1; j < len(stories); j++ {
			if stories[i].Priority > stories[j].Priority {
				stories[i], stories[j] = stories[j], stories[i]
			}
		}
	}

	report := "# Bugün Ne Oldu?\n\n"
	report += fmt.Sprintf("*%s tarihli günlük haber raporu - %d haber birleştirildi*\n\n", time.Now().Format("2 January 2006"), len(stories))

	for i, story := range stories {
		report += fmt.Sprintf("## %d. %s\n\n", i+1, story.Title)
		report += fmt.Sprintf("%s\n\n", story.Summary)

		// Add reporter attribution
		if len(story.Reporters) > 0 {
			report += "**Bu haberi kapsayan muhabirler:**\n"
			for _, reporter := range story.Reporters {
				report += fmt.Sprintf("- %s\n", reporter)
			}
			report += "\n"
		}

		report += "---\n\n"
	}

	return report
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the full pipeline: fetch -> extract -> summarize -> generate",
	Run: func(cmd *cobra.Command, args []string) {
		log.Println("Running full pipeline...")
		fetchCmd.Run(cmd, args)
		extractCmd.Run(cmd, args)
		summarizeCmd.Run(cmd, args)
		generateCmd.Run(cmd, args)
		log.Println("Pipeline complete.")
	},
}

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean old videos, transcripts, summaries, and report",
	Run: func(cmd *cobra.Command, args []string) {
		dirs := []string{"videos", "transcripts", "summaries"}
		for _, dir := range dirs {
			files, err := os.ReadDir(dir)
			if err != nil {
				log.Printf("Failed to read %s: %v", dir, err)
				continue
			}
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				err := os.Remove(filepath.Join(dir, file.Name()))
				if err != nil {
					log.Printf("Failed to remove %s: %v", file.Name(), err)
				}
			}
		}

		// Remove report.md file
		if err := os.Remove("report.md"); err != nil {
			if !os.IsNotExist(err) {
				log.Printf("Failed to remove report.md: %v", err)
			}
		}

		log.Println("Cleaned videos, transcripts, summaries directories and report.md.")
	},
}
