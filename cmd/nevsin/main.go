package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/cenkalti/nevsin"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

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

	// Set configuration for the nevsin package
	nevsin.Config.YouTubeAPIKey = getenv("YOUTUBE_API_KEY")
	nevsin.Config.AzureOpenAIEndpoint = getenv("AZURE_OPENAI_ENDPOINT")
	nevsin.Config.AzureOpenAIAPIKey = getenv("AZURE_OPENAI_API_KEY")
	nevsin.Config.AzureOpenAIDeployment = getenv("AZURE_OPENAI_DEPLOYMENT")

	rootCmd := &cobra.Command{
		Use:   "nevsin",
		Short: "Multi-Language YouTube News Aggregator CLI",
	}

	// Add all commands from the nevsin package
	rootCmd.AddCommand(nevsin.FetchVideosCmd)
	rootCmd.AddCommand(nevsin.FetchSubtitlesCmd)
	rootCmd.AddCommand(nevsin.ExtractStoriesCmd)
	rootCmd.AddCommand(nevsin.GenerateReportCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(cleanCmd)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the full pipeline: fetch-videos -> fetch-subtitles -> extract-stories -> generate-report",
	Run: func(cmd *cobra.Command, args []string) {
		log.Println("Running full pipeline...")
		nevsin.FetchVideosCmd.Run(cmd, args)
		nevsin.FetchSubtitlesCmd.Run(cmd, args)
		nevsin.ExtractStoriesCmd.Run(cmd, args)
		nevsin.GenerateReportCmd.Run(cmd, args)
		log.Println("Pipeline complete.")
	},
}

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean old videos, subtitles, stories, and report",
	Run: func(cmd *cobra.Command, args []string) {
		dirs := []string{"videos", "subtitles", "stories"}
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

		log.Println("Cleaned videos, subtitles, stories directories and report.md.")
	},
}