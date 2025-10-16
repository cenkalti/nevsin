package nevsin

import (
	"context"
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

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/sosodev/duration"
	"github.com/spf13/cobra"
)

// YouTubeChapter represents a video chapter
type YouTubeChapter struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

// YouTubeVideo represents minimal video metadata
type YouTubeVideo struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	PublishedAt  time.Time        `json:"published_at"`
	ThumbnailURL string           `json:"thumbnail_url"`
	ChannelID    string           `json:"channel_id"`
	ChannelName  string           `json:"channel_name"`
	Duration     string           `json:"duration"`
	URL          string           `json:"url"`
	Chapters     []YouTubeChapter `json:"chapters"`
}

// FetchVideosCmd: Reads channels.txt, saves videos/videoID.json
var FetchVideosCmd = &cobra.Command{
	Use:   "fetch-videos [channel-name]",
	Short: "Fetch recent videos from channels",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var channelsToProcess []ChannelConfig

		// If channel name is provided, filter to that channel
		if len(args) > 0 {
			channelName := args[0]
			found := false
			for _, ch := range ChannelConfigs {
				if strings.EqualFold(ch.Name, channelName) {
					channelsToProcess = []ChannelConfig{ch}
					found = true
					break
				}
			}
			if !found {
				log.Fatalf("Channel '%s' not found. Available channels: %s", channelName, getChannelNames())
			}
		} else {
			channelsToProcess = ChannelConfigs
		}

		var wg sync.WaitGroup
		log.Printf("Processing %d channels...", len(channelsToProcess))
		for i, ch := range channelsToProcess {
			wg.Add(1)
			go func(idx int, chInfo ChannelConfig) {
				defer wg.Done()
				log.Printf("Channel %d/%d: %s", idx+1, len(channelsToProcess), chInfo.Name)
				videos, err := fetchYouTubeVideos(chInfo.ID, chInfo.Name)
				if err != nil {
					log.Fatalf("Failed to fetch videos for %s: %v", chInfo.Name, err)
				}
				selected := chInfo.Handler(videos)
				log.Printf("Channel %s: Found %d videos", chInfo.Name, len(selected))
				for _, v := range selected {
					// Print video info in single line with channel name
					log.Printf("📺 [%s] %s - %s", chInfo.Name, v.Title, v.URL)

					// Fetch chapters for the video
					chapters, err := fetchVideoChapters(v.ID)
					if err != nil {
						log.Printf("Failed to fetch chapters for video %s: %v", v.ID, err)
					} else {
						v.Chapters = chapters
						log.Printf("Found %d chapters for video %s", len(chapters), v.ID)
					}
					saveVideoMetadata(v)
				}
			}(i, ch)
		}
		wg.Wait()
		log.Println("Fetch complete.")
	},
}

// getChannelNames returns a comma-separated list of available channel names
func getChannelNames() string {
	var names []string
	for _, ch := range ChannelConfigs {
		names = append(names, ch.Name)
	}
	return strings.Join(names, ", ")
}

// fetchYouTubeVideos fetches recent videos for a channel using the YouTube Data API v3
func fetchYouTubeVideos(channelID string, channelName string) ([]YouTubeVideo, error) {
	apiKey := Config.YouTubeAPIKey

	// First, fetch the latest 10 videos from the channel
	searchURL := fmt.Sprintf(
		"https://www.googleapis.com/youtube/v3/search?key=%s&channelId=%s&part=snippet,id&order=date&maxResults=20&type=video",
		apiKey, channelID,
	)

	resp, err := http.Get(searchURL)
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

	// Extract video IDs for detailed lookup
	videoIDs := make([]string, 0, len(searchResult.Items))
	for _, item := range searchResult.Items {
		videoIDs = append(videoIDs, item.ID.VideoID)
	}

	if len(videoIDs) == 0 {
		return []YouTubeVideo{}, nil
	}

	// Fetch detailed video information including live broadcast status and content details
	videoIDsStr := strings.Join(videoIDs, ",")
	videosURL := fmt.Sprintf(
		"https://www.googleapis.com/youtube/v3/videos?key=%s&id=%s&part=snippet,liveStreamingDetails,contentDetails",
		apiKey, videoIDsStr,
	)

	resp2, err := http.Get(videosURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch video details: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		return nil, fmt.Errorf("YouTube API error for video details: %s", string(body))
	}

	var videosResult struct {
		Items []struct {
			ID      string `json:"id"`
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
			ContentDetails struct {
				Duration string `json:"duration"`
			} `json:"contentDetails"`
			LiveStreamingDetails struct {
				ScheduledStartTime string `json:"scheduledStartTime"`
				ActualStartTime    string `json:"actualStartTime"`
				ActualEndTime      string `json:"actualEndTime"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp2.Body).Decode(&videosResult); err != nil {
		return nil, fmt.Errorf("failed to decode video details response: %w", err)
	}

	videos := make([]YouTubeVideo, 0, len(videosResult.Items))
	for _, item := range videosResult.Items {
		// Skip videos that are scheduled premieres (have scheduledStartTime but no actualStartTime)
		if item.LiveStreamingDetails.ScheduledStartTime != "" && item.LiveStreamingDetails.ActualStartTime == "" {
			log.Printf("[%s] Skipping premiere video: %s", channelName, item.Snippet.Title)
			continue
		}

		// Skip videos shorter than 10 minutes
		if item.ContentDetails.Duration != "" {
			dur, err := duration.Parse(item.ContentDetails.Duration)
			if err == nil {
				durationSeconds := int(dur.ToTimeDuration().Seconds())
				if durationSeconds < 600 {
					log.Printf("[%s] Skipping short video (%ds): %s", channelName, durationSeconds, item.Snippet.Title)
					continue
				}
			}
		}

		publishedAt, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
		if err != nil {
			publishedAt = time.Time{}
		}
		thumbURL := item.Snippet.Thumbnails.High.URL
		if thumbURL == "" {
			thumbURL = item.Snippet.Thumbnails.Default.URL
		}
		video := YouTubeVideo{
			ID:           item.ID,
			Title:        item.Snippet.Title,
			Description:  item.Snippet.Description,
			PublishedAt:  publishedAt,
			ThumbnailURL: thumbURL,
			ChannelID:    channelID,
			ChannelName:  channelName,
			Duration:     item.ContentDetails.Duration,
			URL:          "https://www.youtube.com/watch?v=" + item.ID,
		}
		videos = append(videos, video)
	}

	return videos, nil
}

// analyzeThumbnail analyzes a thumbnail with OpenAI GPT-4o Vision
func analyzeThumbnail(thumbnailURL string) (string, error) {
	apiKey := Config.OpenAIAPIKey

	// Create OpenAI client
	client := openai.NewClient(option.WithAPIKey(apiKey))

	// Create chat completion with vision using image URL directly
	chatCompletion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are an expert at analyzing YouTube video thumbnails. Extract and return ONLY the title text shown in the thumbnail. If there is no visible title text, return 'NO_TITLE'."),
			openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
				openai.TextContentPart("What is the title text shown in this thumbnail? Return only the title text, nothing else."),
				openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
					URL: thumbnailURL,
				}),
			}),
		},
		Model:       openai.ChatModelGPT4o,
		MaxTokens:   openai.Int(100),
		Temperature: openai.Float(0),
	})
	if err != nil {
		return "", fmt.Errorf("failed to call OpenAI API: %w", err)
	}

	if len(chatCompletion.Choices) > 0 && chatCompletion.Choices[0].Message.Content != "" {
		extractedTitle := strings.TrimSpace(chatCompletion.Choices[0].Message.Content)
		log.Printf("Extracted thumbnail title: %s", extractedTitle)
		return extractedTitle, nil
	}

	return "", fmt.Errorf("no title extracted from thumbnail")
}

// fetchVideoChapters fetches video chapters using yt-dlp
func fetchVideoChapters(videoID string) ([]YouTubeChapter, error) {
	videoURL := "https://www.youtube.com/watch?v=" + videoID

	cmd := exec.Command("yt-dlp", "--dump-json", "--no-download", videoURL)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %w", err)
	}

	var ytdlpData struct {
		Chapters []struct {
			Title     string  `json:"title"`
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		} `json:"chapters"`
	}

	if err := json.Unmarshal(output, &ytdlpData); err != nil {
		return nil, fmt.Errorf("failed to parse yt-dlp output: %w", err)
	}

	chapters := make([]YouTubeChapter, len(ytdlpData.Chapters))
	for i, ch := range ytdlpData.Chapters {
		chapters[i] = YouTubeChapter{
			Title:     ch.Title,
			StartTime: ch.StartTime,
			EndTime:   ch.EndTime,
		}
	}

	return chapters, nil
}

// saveVideoMetadata saves video metadata as videos/videoID.json
func saveVideoMetadata(video YouTubeVideo) {
	data, _ := json.MarshalIndent(video, "", "  ")
	path := filepath.Join("videos", video.ID+".json")
	_ = os.WriteFile(path, data, 0644)
}