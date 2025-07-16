package nevsin

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"
)

// VideoSource represents a video source for a news story
type VideoSource struct {
	Reporter  string
	VideoID   string
	StartTime string
	EndTime   string
}

// MergedNewsStory represents a news story merged from multiple sources
type MergedNewsStory struct {
	Title        string        `json:"title" jsonschema:"description=Birleştirilmiş haberin başlığı"`
	Summary      string        `json:"summary" jsonschema:"description=Tüm kaynaklardan birleştirilmiş detaylı haber özeti"`
	Reporters    []string      `json:"reporters" jsonschema:"description=Bu haberi kapsayan muhabirlerin listesi"`
	Priority     int           `json:"priority" jsonschema:"description=Haberin önem derecesi (1=en önemli, 10=en az önemli)"`
	VideoSources []VideoSource `json:"-"` // Not included in JSON schema
}

// ReportGenerationResponse represents the structured response from Azure OpenAI for report generation
type ReportGenerationResponse struct {
	Stories []MergedNewsStory `json:"stories"`
}

var GenerateReportCmd = &cobra.Command{
	Use:   "generate-report",
	Short: "Generate final news report",
	Run: func(cmd *cobra.Command, args []string) {
		files, err := os.ReadDir("stories")
		if err != nil {
			log.Printf("Failed to read stories directory: %v", err)
			return
		}
		stories := make(map[string]string)
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join("stories", file.Name()))
			if err != nil {
				log.Printf("Failed to read %s: %v", file.Name(), err)
				continue
			}
			stories[file.Name()] = string(data)
		}
		report := generateReport(stories)
		if err := os.WriteFile("report.md", []byte(report), 0644); err != nil {
			log.Printf("Failed to write report file: %v", err)
			return
		}
		log.Println("Report generated: report.md")
	},
}

// generateReport uses Azure OpenAI to merge and group news stories from multiple reporters
func generateReport(stories map[string]string) string {
	endpoint := Config.AzureOpenAIEndpoint
	apiKey := Config.AzureOpenAIAPIKey
	deployment := Config.AzureOpenAIDeployment

	// Parse JSON stories and collect video sources
	var allStories []NewsStory
	videoSources := make(map[string][]VideoSource) // Map story title to video sources
	for filename, jsonContent := range stories {
		// Parse JSON content
		var newsResponse NewsExtractionResponse
		if err := json.Unmarshal([]byte(jsonContent), &newsResponse); err != nil {
			log.Printf("Failed to parse JSON story %s: %v", filename, err)
			continue
		}

		// Add stories to the collection and collect video sources
		for _, story := range newsResponse.Stories {
			allStories = append(allStories, story)
			// Track video source for this story
			videoSource := VideoSource{
				Reporter:  story.Reporter,
				VideoID:   story.VideoID,
				StartTime: story.StartTime,
				EndTime:   story.EndTime,
			}
			videoSources[story.Title] = append(videoSources[story.Title], videoSource)
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

	// Generate JSON schema for structured output
	reflector := jsonschema.Reflector{}
	schema := reflector.Reflect(&ReportGenerationResponse{})

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

	// Make request to Azure OpenAI with retry logic
	responseBody, err := makeOpenAIRequest(jsonBody, endpoint, apiKey, deployment)
	if err != nil {
		log.Printf("Failed to call Azure OpenAI: %v", err)
		return "# Bugün Ne Oldu?\n\nHata: Azure OpenAI çağrısı başarısız\n"
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(responseBody, &result); err != nil {
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

	// Add video sources to merged stories
	for i := range reportResponse.Stories {
		story := &reportResponse.Stories[i]
		// Find video sources for this story's reporters
		for _, reporter := range story.Reporters {
			for _, sources := range videoSources {
				for _, source := range sources {
					if source.Reporter == reporter {
						story.VideoSources = append(story.VideoSources, source)
					}
				}
			}
		}
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

		// Add reporter attribution with video links
		if len(story.Reporters) > 0 {
			report += "**Bu haberi kapsayan muhabirler:**\n"
			for _, reporter := range story.Reporters {
				// Find video sources for this reporter
				var reporterSources []VideoSource
				for _, source := range story.VideoSources {
					if source.Reporter == reporter {
						reporterSources = append(reporterSources, source)
					}
				}
				
				if len(reporterSources) > 0 {
					// Use the first video source for this reporter
					source := reporterSources[0]
					videoURL := formatVideoURL(source.VideoID, source.StartTime)
					report += fmt.Sprintf("- [%s](%s) (⏱️ %s-%s)\n", reporter, videoURL, source.StartTime, source.EndTime)
				} else {
					report += fmt.Sprintf("- %s\n", reporter)
				}
			}
			report += "\n"
		}

		report += "---\n\n"
	}

	return report
}

// formatVideoURL creates a YouTube URL with timestamp from video ID and start time
func formatVideoURL(videoID, startTime string) string {
	// Convert MM:SS format to seconds
	timeSeconds := convertTimeToSeconds(startTime)
	if timeSeconds > 0 {
		return fmt.Sprintf("https://www.youtube.com/watch?v=%s&t=%ds", videoID, timeSeconds)
	}
	return fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
}

// convertTimeToSeconds converts MM:SS format to total seconds
func convertTimeToSeconds(timeStr string) int {
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