package nevsin

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/spf13/cobra"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed templates/report.html
var htmlTemplate string

//go:embed templates/styles.css
var cssStyles string

// VideoSource represents a video source for a news story
type VideoSource struct {
	Reporter  string
	VideoID   string
	StartTime string
	EndTime   string
	StoryURL  string
}

// MergedNewsStory represents a news story merged from multiple sources
type MergedNewsStory struct {
	Title        string        `json:"title" jsonschema:"description=Birleştirilmiş haberin başlığı"`
	Summary      string        `json:"summary" jsonschema:"description=Tüm kaynaklardan birleştirilmiş detaylı haber özeti"`
	Reporters    []string      `json:"reporters" jsonschema:"description=Bu haberi kapsayan muhabirlerin listesi"`
	Priority     int           `json:"priority" jsonschema:"description=Haberin önem derecesi (1=en önemli, 10=en az önemli)"`
	VideoSources []VideoSource `json:"-"` // Not included in JSON schema
}

// ReportGenerationResponse represents the structured response from OpenAI for report generation
type ReportGenerationResponse struct {
	Stories []MergedNewsStory `json:"stories"`
}

var GenerateReportCmd = &cobra.Command{
	Use:   "generate-report",
	Short: "Generate final news report in both markdown and HTML formats",
	Run: func(cmd *cobra.Command, args []string) {
		report := generateReportFromClusters()
		if err := os.WriteFile("report.md", []byte(report), 0644); err != nil {
			log.Printf("Failed to write report file: %v", err)
			return
		}
		log.Println("Report generated: report.md")

		// Generate HTML version
		htmlContent := generateCompleteHTML(report)
		if err := os.WriteFile("report.html", []byte(htmlContent), 0644); err != nil {
			log.Printf("Failed to write HTML file: %v", err)
			return
		}
		log.Println("HTML report generated: report.html")
	},
}

// generateReportFromClusters generates a report from clustered stories
func generateReportFromClusters() string {
	// Read clustered stories
	clusters, err := loadClusters()
	if err != nil {
		log.Printf("DEBUG: Detailed error loading clusters: %v", err)
		return "# Bugün Ne Oldu?\n\nKümelenmiş hikayeler yüklenemedi.\n"
	}

	if len(clusters.Clusters) == 0 {
		return "# Bugün Ne Oldu?\n\nBugün için haber bulunamadı.\n"
	}

	// Convert clusters to merged stories using AI
	mergedStories := convertClustersToMergedStories(clusters)

	// Generate final markdown report
	return formatFinalReport(mergedStories)
}

// loadClusters loads clustering results from file
func loadClusters() (ClusteringResult, error) {
	data, err := os.ReadFile("clusters/clusters.json")
	if err != nil {
		return ClusteringResult{}, fmt.Errorf("failed to read clusters file: %w", err)
	}

	log.Printf("DEBUG: Successfully read clusters file, size: %d bytes", len(data))

	var clusters ClusteringResult
	if err := json.Unmarshal(data, &clusters); err != nil {
		log.Printf("DEBUG: JSON unmarshal error: %v", err)
		maxLen := min(len(data), 200)
		log.Printf("DEBUG: First 200 chars of JSON: %s", string(data[:maxLen]))
		return ClusteringResult{}, fmt.Errorf("failed to parse clusters: %w", err)
	}

	log.Printf("DEBUG: Successfully parsed clusters: %d clusters found", len(clusters.Clusters))
	return clusters, nil
}

// convertClustersToMergedStories converts story clusters to merged stories using OpenAI API
func convertClustersToMergedStories(clusters ClusteringResult) []MergedNewsStory {
	var mergedStories []MergedNewsStory

	for _, cluster := range clusters.Clusters {
		if len(cluster.Stories) == 0 {
			continue
		}

		// If cluster has only one story, use it directly
		if len(cluster.Stories) == 1 {
			story := cluster.Stories[0]
			mergedStory := MergedNewsStory{
				Title:     story.Title,
				Summary:   story.Summary,
				Reporters: []string{story.Reporter},
				Priority:  5, // Default priority
				VideoSources: []VideoSource{
					{
						Reporter:  story.Reporter,
						VideoID:   story.VideoID,
						StartTime: getStoryStartTime(story.StoryID),
						EndTime:   getStoryEndTime(story.StoryID),
						StoryURL:  getStoryURL(story.StoryID),
					},
				},
			}
			mergedStories = append(mergedStories, mergedStory)
			continue
		}

		// For multiple stories, use AI to merge them
		mergedStory := mergeClusterWithAI(cluster)
		mergedStories = append(mergedStories, mergedStory)
	}

	// Sort by priority using AI
	if len(mergedStories) > 1 {
		mergedStories = prioritizeStoriesWithAI(mergedStories)
	}

	return mergedStories
}

// mergeClusterWithAI uses OpenAI API to merge stories in a cluster
func mergeClusterWithAI(cluster StoryCluster) MergedNewsStory {
	apiKey := Config.OpenAIAPIKey

	// Prepare cluster data for AI processing
	clusterJSON, err := json.MarshalIndent(cluster.Stories, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal cluster: %v", err)
		// Return first story as fallback
		story := cluster.Stories[0]
		return MergedNewsStory{
			Title:     story.Title,
			Summary:   story.Summary,
			Reporters: []string{story.Reporter},
			Priority:  5,
		}
	}

	// Generate JSON schema for structured output
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	schemaObj := reflector.Reflect(&MergedNewsStory{})

	if schemaObj.Type == "" {
		schemaObj.Type = "object"
	}

	// Convert to interface{} for OpenAI SDK
	schemaBytes, err := json.Marshal(schemaObj)
	if err != nil {
		log.Printf("Failed to marshal schema: %v", err)
		return MergedNewsStory{Title: "Hata", Summary: "Schema hatası"}
	}
	var schema any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		log.Printf("Failed to unmarshal schema: %v", err)
		return MergedNewsStory{Title: "Hata", Summary: "Schema hatası"}
	}

	// Collect unique reporters
	reporterSet := make(map[string]bool)
	for _, story := range cluster.Stories {
		reporterSet[story.Reporter] = true
	}
	var reporters []string
	for reporter := range reporterSet {
		reporters = append(reporters, reporter)
	}

	// Create OpenAI client
	client := openai.NewClient(option.WithAPIKey(apiKey))

	// Prepare system and user messages
	systemContent := `Sen benzer haber hikayelerini birleştiren bir uzmansın. Verilen hikayeleri analiz et ve tek bir tutarlı haber haline getir.

Görevlerin:
1. Ortak bir başlık oluştur
2. Tüm hikayelerdeki bilgileri birleştirerek kapsamlı özet yaz
3. Haberin önem derecesini belirle (1=en önemli, 10=en az önemli)
4. Türkiye gündemindeki yerini değerlendir

ÖZET YAZIM KURALLARI:
• Özeti basit cümleler halinde yaz
• Madde işaretleri kullan
• Her madde kısa ve net olsun
• Karmaşık cümleler kurma
• Teknik terimler varsa basit açıkla`
	userContent := fmt.Sprintf("Bu benzer haber hikayelerini birleştir ve tek bir tutarlı haber haline getir:\n\n%s", string(clusterJSON))

	// Create chat completion with structured outputs
	chatCompletion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemContent),
			openai.UserMessage(userContent),
		},
		Model:       openai.ChatModelGPT4_1,
		MaxTokens:   openai.Int(2000),
		Temperature: openai.Float(0.1),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        "merged_story",
					Description: openai.String("Merge similar news stories into a single coherent story"),
					Schema:      schema,
					Strict:      openai.Bool(true),
				},
			},
		},
	})
	if err != nil {
		log.Printf("Failed to call OpenAI API for merging: %v", err)
		return MergedNewsStory{Title: "Hata", Summary: "AI çağrısı hatası"}
	}

	if len(chatCompletion.Choices) == 0 || chatCompletion.Choices[0].Message.Content == "" {
		log.Printf("No content in merge response")
		return MergedNewsStory{Title: "Hata", Summary: "Boş yanıt"}
	}

	var mergedStory MergedNewsStory
	if err := json.Unmarshal([]byte(chatCompletion.Choices[0].Message.Content), &mergedStory); err != nil {
		log.Printf("Failed to parse merged story: %v", err)
		return MergedNewsStory{Title: "Hata", Summary: "Ayrıştırma hatası"}
	}

	// Set reporters and video sources
	mergedStory.Reporters = reporters
	for _, story := range cluster.Stories {
		videoSource := VideoSource{
			Reporter:  story.Reporter,
			VideoID:   story.VideoID,
			StartTime: getStoryStartTime(story.StoryID),
			EndTime:   getStoryEndTime(story.StoryID),
			StoryURL:  getStoryURL(story.StoryID),
		}
		mergedStory.VideoSources = append(mergedStory.VideoSources, videoSource)
	}

	return mergedStory
}

// prioritizeStoriesWithAI uses AI to prioritize merged stories
func prioritizeStoriesWithAI(stories []MergedNewsStory) []MergedNewsStory {
	// For now, just sort by existing priority. Could enhance with AI later.
	for i := 0; i < len(stories)-1; i++ {
		for j := i + 1; j < len(stories); j++ {
			if stories[i].Priority > stories[j].Priority {
				stories[i], stories[j] = stories[j], stories[i]
			}
		}
	}
	return stories
}

// Helper functions to get story metadata from original story files
func getStoryStartTime(storyID string) string {
	parts := strings.Split(storyID, "_")
	if len(parts) < 2 {
		return "00:00"
	}
	videoID := parts[0]
	storyIndex := parts[1]

	// Read original story file
	storyPath := filepath.Join("stories", videoID+".json")
	data, err := os.ReadFile(storyPath)
	if err != nil {
		return "00:00"
	}

	var newsResponse NewsExtractionResponse
	if err := json.Unmarshal(data, &newsResponse); err != nil {
		return "00:00"
	}

	// Parse story index
	var idx int
	if _, err := fmt.Sscanf(storyIndex, "%d", &idx); err != nil {
		return "00:00"
	}

	if idx >= 0 && idx < len(newsResponse.Stories) {
		return newsResponse.Stories[idx].StartTime
	}

	return "00:00"
}

func getStoryEndTime(storyID string) string {
	parts := strings.Split(storyID, "_")
	if len(parts) < 2 {
		return "00:00"
	}
	videoID := parts[0]
	storyIndex := parts[1]

	storyPath := filepath.Join("stories", videoID+".json")
	data, err := os.ReadFile(storyPath)
	if err != nil {
		return "00:00"
	}

	var newsResponse NewsExtractionResponse
	if err := json.Unmarshal(data, &newsResponse); err != nil {
		return "00:00"
	}

	var idx int
	if _, err := fmt.Sscanf(storyIndex, "%d", &idx); err != nil {
		return "00:00"
	}

	if idx >= 0 && idx < len(newsResponse.Stories) {
		return newsResponse.Stories[idx].EndTime
	}

	return "00:00"
}

func getStoryURL(storyID string) string {
	parts := strings.Split(storyID, "_")
	if len(parts) < 2 {
		return ""
	}
	videoID := parts[0]
	storyIndex := parts[1]

	storyPath := filepath.Join("stories", videoID+".json")
	data, err := os.ReadFile(storyPath)
	if err != nil {
		return ""
	}

	var newsResponse NewsExtractionResponse
	if err := json.Unmarshal(data, &newsResponse); err != nil {
		return ""
	}

	var idx int
	if _, err := fmt.Sscanf(storyIndex, "%d", &idx); err != nil {
		return ""
	}

	if idx >= 0 && idx < len(newsResponse.Stories) {
		return newsResponse.Stories[idx].StoryURL
	}

	return ""
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
			report += "**Bu haberi kapsayan muhabirler:**\n\n"
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
					report += fmt.Sprintf("- [%s](%s) (⏱️ %s-%s)\n", reporter, source.StoryURL, source.StartTime, source.EndTime)
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

// generateCompleteHTML generates a complete HTML document with embedded CSS
func generateCompleteHTML(markdownContent string) string {
	// Remove the duplicate title and date from the markdown content
	lines := strings.Split(markdownContent, "\n")

	cleanMarkdown := strings.Join(lines, "\n")

	// Configure goldmark with extensions
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Table,
			extension.Linkify,
			extension.Strikethrough,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			html.WithXHTML(),
			html.WithUnsafe(),
		),
	)

	// Convert markdown to HTML
	var buf bytes.Buffer
	if err := md.Convert([]byte(cleanMarkdown), &buf); err != nil {
		log.Printf("Failed to convert markdown to HTML: %v", err)
		return ""
	}

	// Parse the HTML template
	tmpl, err := template.New("report").Parse(htmlTemplate)
	if err != nil {
		log.Printf("Failed to parse HTML template: %v", err)
		return ""
	}

	// Prepare template data
	data := struct {
		Title string
		Date  string
		Body  template.HTML
		CSS   template.CSS
	}{
		Title: "Bugün Ne Oldu?",
		Date:  time.Now().Format("2 January 2006"),
		Body:  template.HTML(buf.String()),
		CSS:   template.CSS(cssStyles),
	}

	// Execute template
	var result bytes.Buffer
	if err := tmpl.Execute(&result, data); err != nil {
		log.Printf("Failed to execute template: %v", err)
		return ""
	}

	return result.String()
}
