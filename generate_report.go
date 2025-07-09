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
	"time"

	"github.com/spf13/cobra"
)

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

var GenerateReportCmd = &cobra.Command{
	Use:   "generate-report",
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

// generateReport uses Azure OpenAI to merge and group news stories from multiple reporters
func generateReport(summaries map[string]string) string {
	endpoint := Config.AzureOpenAIEndpoint
	apiKey := Config.AzureOpenAIAPIKey
	deployment := Config.AzureOpenAIDeployment

	// Parse JSON summaries
	var allStories []NewsStory
	for filename, jsonContent := range summaries {
		// Parse JSON content
		var newsResponse NewsExtractionResponse
		if err := json.Unmarshal([]byte(jsonContent), &newsResponse); err != nil {
			log.Printf("Failed to parse JSON summary %s: %v", filename, err)
			continue
		}

		// Add stories to the collection
		allStories = append(allStories, newsResponse.Stories...)
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