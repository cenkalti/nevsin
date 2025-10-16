package nevsin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/spf13/cobra"
)

// EmbeddingRecord represents a story embedding in the database
type EmbeddingRecord struct {
	StoryID   string    `json:"story_id"`
	VideoID   string    `json:"video_id"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Reporter  string    `json:"reporter"`
	Embedding []float64 `json:"embedding"`
	CreatedAt time.Time `json:"created_at"`
}

var EmbedStoriesCmd = &cobra.Command{
	Use:   "embed-stories",
	Short: "Generate embeddings for all stories",
	Run: func(cmd *cobra.Command, args []string) {
		if err := embedAllStories(); err != nil {
			log.Printf("Failed to embed stories: %v", err)
			return
		}
		log.Println("Story embedding complete.")
	},
}

// embedAllStories processes all story files and generates embeddings
func embedAllStories() error {
	// Initialize database
	db, err := initEmbeddingDB()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("Failed to close database: %v", err)
		}
	}()

	// Read all story files
	files, err := os.ReadDir("stories")
	if err != nil {
		return fmt.Errorf("failed to read stories directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		videoID := strings.TrimSuffix(file.Name(), ".json")

		if err := processVideoEmbeddings(db, videoID); err != nil {
			log.Printf("Failed to process embeddings for video %s: %v", videoID, err)
			continue
		}

		log.Printf("Processed embeddings for video: %s", videoID)
	}

	return nil
}

// initEmbeddingDB initializes the SQLite database for embeddings
func initEmbeddingDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "embeddings.db")
	if err != nil {
		return nil, err
	}

	// Create table if not exists
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS embeddings (
		story_id TEXT PRIMARY KEY,
		video_id TEXT NOT NULL,
		title TEXT NOT NULL,
		summary TEXT NOT NULL,
		reporter TEXT NOT NULL,
		embedding_json TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_video_id ON embeddings(video_id);
	CREATE INDEX IF NOT EXISTS idx_reporter ON embeddings(reporter);
	`

	if _, err := db.Exec(createTableSQL); err != nil {
		if err := db.Close(); err != nil {
			log.Printf("Failed to close database: %v", err)
		}
		return nil, err
	}

	return db, nil
}

// processVideoEmbeddings processes embeddings for a single video's stories
func processVideoEmbeddings(db *sql.DB, videoID string) error {
	// Read story file
	storyPath := filepath.Join("stories", videoID+".json")
	data, err := os.ReadFile(storyPath)
	if err != nil {
		return fmt.Errorf("failed to read story file: %w", err)
	}

	// Parse stories
	var newsResponse NewsExtractionResponse
	if err := json.Unmarshal(data, &newsResponse); err != nil {
		return fmt.Errorf("failed to parse story JSON: %w", err)
	}

	// Process each story
	for i, story := range newsResponse.Stories {
		storyID := fmt.Sprintf("%s_%d", videoID, i)

		// Check if embedding already exists
		exists, err := embeddingExists(db, storyID)
		if err != nil {
			return fmt.Errorf("failed to check existing embedding: %w", err)
		}

		if exists {
			log.Printf("Embedding already exists for story: %s", storyID)
			continue
		}

		// Generate embedding from title + summary for richer semantic representation
		combinedText := story.Title + " " + story.Summary
		embedding, err := generateEmbedding(combinedText)
		if err != nil {
			return fmt.Errorf("failed to generate embedding for story %s: %w", storyID, err)
		}

		// Save to database
		if err := saveEmbedding(db, storyID, story, embedding); err != nil {
			return fmt.Errorf("failed to save embedding: %w", err)
		}

		log.Printf("Generated embedding for story: %s", storyID)

		// Small delay to avoid rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// embeddingExists checks if an embedding already exists in the database
func embeddingExists(db *sql.DB, storyID string) (bool, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM embeddings WHERE story_id = ?", storyID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// generateEmbedding calls OpenAI API to generate embeddings for text
func generateEmbedding(text string) ([]float64, error) {
	apiKey := Config.OpenAIAPIKey

	// Create OpenAI client
	client := openai.NewClient(option.WithAPIKey(apiKey))

	// Generate embedding using text-embedding-3-large for superior semantic quality
	embedding, err := client.Embeddings.New(context.TODO(), openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: openai.String(text),
		},
		Model:          openai.EmbeddingModelTextEmbedding3Large,
		EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call OpenAI API: %w", err)
	}

	if len(embedding.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}

	return embedding.Data[0].Embedding, nil
}

// saveEmbedding saves an embedding to the database
func saveEmbedding(db *sql.DB, storyID string, story NewsStory, embedding []float64) error {
	embeddingJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("failed to marshal embedding: %w", err)
	}

	insertSQL := `
	INSERT INTO embeddings (story_id, video_id, title, summary, reporter, embedding_json)
	VALUES (?, ?, ?, ?, ?, ?)
	`

	_, err = db.Exec(insertSQL, storyID, story.VideoID, story.Title, story.Summary, story.Reporter, string(embeddingJSON))
	if err != nil {
		return fmt.Errorf("failed to insert embedding: %w", err)
	}

	return nil
}