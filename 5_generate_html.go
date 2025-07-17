package nevsin

import (
	"bytes"
	_ "embed"
	"html/template"
	"log"
	"os"
	"strings"
	"time"

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

var GenerateHTMLCmd = &cobra.Command{
	Use:   "generate-html",
	Short: "Generate HTML version of the news report",
	Run: func(cmd *cobra.Command, args []string) {
		// Check if report.md exists
		reportData, err := os.ReadFile("report.md")
		if err != nil {
			log.Printf("Failed to read report.md: %v", err)
			return
		}

		// Generate complete HTML with goldmark
		htmlContent := generateCompleteHTML(string(reportData))

		// Write to file
		if err := os.WriteFile("report.html", []byte(htmlContent), 0644); err != nil {
			log.Printf("Failed to write HTML file: %v", err)
			return
		}

		log.Println("HTML report generated: report.html")
	},
}

// generateCompleteHTML generates a complete HTML document with embedded CSS
func generateCompleteHTML(markdownContent string) string {
	// Remove the duplicate title and date from the markdown content
	lines := strings.Split(markdownContent, "\n")
	var filteredLines []string

	for i, line := range lines {
		// Skip the first h1 line
		if i == 0 && strings.HasPrefix(line, "# Bugün Ne Oldu?") {
			continue
		}
		// Skip the date line (contains "tarihli günlük haber raporu")
		if strings.Contains(line, "tarihli günlük haber raporu") && strings.Contains(line, "haber birleştirildi") {
			continue
		}
		// Skip empty lines at the beginning
		if len(filteredLines) == 0 && strings.TrimSpace(line) == "" {
			continue
		}
		filteredLines = append(filteredLines, line)
	}

	cleanMarkdown := strings.Join(filteredLines, "\n")

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

