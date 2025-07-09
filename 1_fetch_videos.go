package nevsin

import (
	"bytes"
	"encoding/base64"
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

// FetchVideosCmd: Reads channels.txt, saves videos/videoID.json
var FetchVideosCmd = &cobra.Command{
	Use:   "fetch-videos",
	Short: "Fetch recent videos from channels",
	Run: func(cmd *cobra.Command, args []string) {
		var wg sync.WaitGroup
		log.Printf("Processing %d channels...", len(ChannelConfigs))
		for i, ch := range ChannelConfigs {
			wg.Add(1)
			go func(idx int, chInfo ChannelConfig) {
				defer wg.Done()
				log.Printf("Channel %d/%d: %s", idx+1, len(ChannelConfigs), chInfo.Name)
				videos, err := fetchYouTubeVideos(chInfo.ID)
				if err != nil {
					log.Fatalf("Failed to fetch videos for %s: %v", chInfo.Name, err)
				}
				selected := chInfo.Handler(videos)
				log.Printf("Channel %s: Found %d videos", chInfo.Name, len(selected))
				for _, v := range selected {
					saveVideoMetadata(v)
				}
			}(i, ch)
		}
		wg.Wait()
		log.Println("Fetch complete.")
	},
}

// fetchYouTubeVideos fetches recent videos for a channel using the YouTube Data API v3
func fetchYouTubeVideos(channelID string) ([]YouTubeVideo, error) {
	apiKey := Config.YouTubeAPIKey

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
	endpoint := Config.AzureOpenAIEndpoint
	apiKey := Config.AzureOpenAIAPIKey
	deployment := Config.AzureOpenAIDeployment

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