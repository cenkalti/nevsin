package nevsin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
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

func extractStories(subtitle, videoID, channelID string, chapters []YouTubeChapter) (NewsExtractionResponse, error) {
	apiKey := Config.OpenAIAPIKey

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

	// Convert to interface{} for OpenAI SDK
	schemaBytes, err := json.Marshal(schemaObj)
	if err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to marshal schema: %w", err)
	}
	var schema any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	// Format chapter information for LLM context
	chapterInfo := formatChapterInfo(chapters)

	// Create OpenAI client
	client := openai.NewClient(option.WithAPIKey(apiKey))

	// Prepare system and user messages
	systemContent := "Sen Türkçe haber metinlerini analiz eden bir uzmansın. Verilen altyazıdan birden fazla haber hikayesini çıkarman gerekiyor. Her haber için başlık, özet ve başlangıç/bitiş saniyelerini belirle.\n\nALTYAZI FORMATI: Altyazı basitleştirilmiş formattadır. Her satır şu şekildedir:\n[saniye]: [metin]\n\nÖrnek:\n7: Retro, retro arkadaşlar. Retro. Sorun\n10: retrodan kaynaklanıyor. Merkür retrosu\n13: Aslan burcunda gerçekleşiyormuş. Ben\n\nÖZET YAZIM KURALLARI:\n• Özeti basit cümleler halinde yaz\n• Madde işaretleri kullan\n• Her madde kısa ve net olsun\n• Karmaşık cümleler kurma\n• Teknik terimler varsa basit açıkla\n\nSadece gerçek haber içeriğini çıkar, reklam veya genel konuşmaları dahil etme. Her haber için start_second ve end_second değerlerini saniye cinsinden belirle."
	userContent := fmt.Sprintf("Bu altyazıdan tüm haber hikayelerini çıkar ve her biri için başlık, detaylı özet ve başlangıç/bitiş saniyelerini belirle. Altyazı formatı: [saniye]: [metin] şeklindedir.\n\n%s\n\nALTYAZI:\n%s", chapterInfo, subtitle)

	// Create chat completion with structured outputs
	chatCompletion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemContent),
			openai.UserMessage(userContent),
		},
		Model:       openai.ChatModelGPT4_1,
		MaxTokens:   openai.Int(4000),
		Temperature: openai.Float(0.1),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        "news_extraction",
					Description: openai.String("Extract news stories from subtitles"),
					Schema:      schema,
					Strict:      openai.Bool(true),
				},
			},
		},
	})
	if err != nil {
		return NewsExtractionResponse{}, fmt.Errorf("failed to call OpenAI API: %w", err)
	}

	if len(chatCompletion.Choices) == 0 || chatCompletion.Choices[0].Message.Content == "" {
		return NewsExtractionResponse{}, fmt.Errorf("no content in response")
	}

	// Parse the structured JSON response
	var newsResponse NewsExtractionResponse
	if err := json.Unmarshal([]byte(chatCompletion.Choices[0].Message.Content), &newsResponse); err != nil {
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
