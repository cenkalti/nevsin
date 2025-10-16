# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Nevsin is a YouTube news aggregator CLI tool written in Go that fetches, transcribes, summarizes, clusters, and compiles daily Turkish news reports using AI. It monitors specific YouTube channels, uses OpenAI API for all AI operations, and generates consolidated news reports published to GitHub Pages.

## Core Architecture

### Modular Pipeline Design
The application follows a **sequential pipeline architecture** with numbered command modules:
1. `fetch-videos` → Retrieves videos from configured channels
2. `fetch-subtitles` → Downloads subtitles using yt-dlp
3. `extract-stories` → Creates AI summaries with structured JSON
4. `embed-stories` → Generates semantic embeddings
5. `cluster-stories` → Groups similar stories using ML clustering
6. `generate-report` → Compiles final markdown/HTML reports
7. `upload-site` → Publishes to GitHub Pages

Each command is **file-based** and operates on a working directory convention (no flags).

### Key Design Principles
- **Convention over configuration**: Pure file-based operations, no flags
- **Concurrent processing**: Goroutines for video fetching and subtitle extraction
- **AI-powered**: OpenAI API for all AI operations (extraction, embeddings, vision)
- **Clustering-first**: Uses DBSCAN/K-means with stability analysis to group stories
- **Fail-fast validation**: Environment variables checked at startup

### Directory Structure
```
./
├── videos/           # Video metadata JSON files
├── subtitles/        # Simplified subtitle files (second: text format)
├── stories/          # AI-extracted news stories with timestamps
├── clusters/         # Clustering results and quality metrics
├── report.md         # Final markdown report
├── report.html       # HTML version for GitHub Pages
└── embeddings.db     # SQLite database for story embeddings
```

### Data Flow
```
YouTube API → videos/*.json →
subtitles/*.srt (simplified format) →
stories/*.json (AI extracted) →
embeddings.db (vector embeddings) →
clusters/clusters.json (ML clustering) →
report.md / report.html →
GitHub Pages
```

## Development Commands

### Build and Run
```bash
# Build the application
go build ./cmd/nevsin

# Run full pipeline
./nevsin run

# Run individual commands (in order)
./nevsin fetch-videos
./nevsin fetch-subtitles
./nevsin extract-stories
./nevsin embed-stories
./nevsin cluster-stories
./nevsin generate-report
./nevsin upload-site

# Clean all generated files
./nevsin clean

# Process specific channel only
./nevsin fetch-videos "Nevsin Mengu"
```

### Linting
```bash
golangci-lint run
```

### Testing
While there are no formal tests yet, you can verify the pipeline:
```bash
# Test individual stages
./nevsin fetch-videos && ls videos/
./nevsin fetch-subtitles && ls subtitles/
./nevsin extract-stories && ls stories/
```

## Configuration

### Environment Variables (required in `.env`)
- `YOUTUBE_API_KEY` - YouTube Data API v3 key
- `OPENAI_API_KEY` - OpenAI API key (for all AI operations)

All variables are validated at startup with fail-fast errors.

### Channel Configuration
Channels are hardcoded in `channels.go` with custom filtering logic. Each channel has:
- Name, ID (YouTube channel ID)
- Handler function for custom video selection logic

Example: Nevsin Mengu uses GPT-4o Vision to analyze thumbnails for "Bugün ne oldu?" text.

## External Dependencies

### Runtime Dependencies
- **yt-dlp** (Python tool): Required for subtitle extraction
  ```bash
  pip install yt-dlp
  ```
- **YouTube Data API v3**: For video metadata
- **OpenAI API**: For all AI operations
  - Story extraction: `gpt-4.1` model with strict JSON schema validation
  - Embeddings: `text-embedding-3-large` model for semantic representation
  - Vision: `gpt-4o` model for thumbnail analysis
  - Implemented via `github.com/openai/openai-go/v3` SDK

### Go Dependencies
- `github.com/spf13/cobra` - CLI framework
- `github.com/joho/godotenv` - .env file loading
- `github.com/invopop/jsonschema` - JSON schema generation for structured AI outputs
- `github.com/openai/openai-go/v3` - Official OpenAI Go SDK
- `github.com/mattn/go-sqlite3` - SQLite for embedding storage
- `gonum.org/v1/gonum/mat` - Matrix operations for clustering
- `github.com/yuin/goldmark` - Markdown to HTML conversion

## Key Implementation Details

### Subtitle Processing
Subtitles are converted from SRT format to simplified format for LLM processing:
```
[second]: [text]
```
Example:
```
7: Retro, retro arkadaşlar. Retro. Sorun
10: retrodan kaynaklanıyor. Merkür retrosu
```

### Story Extraction
Uses OpenAI API (GPT-4o) with structured output and strict JSON schema validation. Stories include:
- Title, summary (with bullet-point formatting rules)
- Start/end seconds and timestamps
- YouTube URLs with timestamp parameters
- Reporter attribution
- Implemented using the official `github.com/openai/openai-go/v3` SDK

### Clustering Algorithm
The system uses adaptive clustering with stability analysis:
1. **Normalization**: L2 normalization of embeddings
2. **Outlier removal**: Statistical outlier detection (2σ threshold)
3. **Primary method**: DBSCAN with stability validation (3 runs with parameter variations)
4. **Fallback**: K-means with optimal K selection (evaluated using Silhouette, Davies-Bouldin, Calinski-Harabasz scores)
5. **Post-processing**: Merge single-story clusters into most similar clusters

Quality metrics tracked:
- Silhouette score (cluster separation)
- Davies-Bouldin index (cluster definition)
- Cross-reporter effectiveness (stories from different reporters in same cluster)
- Cluster importance scores (size + reporter diversity)

### Report Generation
- Clusters are merged using AI to create coherent news stories
- Stories prioritized by AI-assigned importance (1=most important, 10=least)
- Final output: Markdown with clickable YouTube links (with timestamps)
- HTML generated using goldmark with embedded CSS

### GitHub Pages Upload
- Creates/updates `gh-pages` branch in temp directory
- Commits `index.html` and `index.md`
- Pushes to remote with proper cleanup

## Logging Conventions
- **Always use `log.Print*` functions** instead of `fmt.Println`
- Use emoji prefixes for key operations in clustering (🔍, ✅, ⚠️, etc.)
- Log progress for concurrent operations with channel names
- Include detailed error context with `log.Printf("Failed to X: %v", err)`

## Code Organization Notes
- Main entry point: `cmd/nevsin/main.go` (sets up cobra, loads config)
- Commands exported as package-level variables: `nevsin.FetchVideosCmd`, etc.
- Shared types: `YouTubeVideo`, `NewsStory`, `ClusteredStory`, `MergedNewsStory`
- Retry logic: Built into `makeOpenAIRequest` with exponential backoff for rate limits
- Embedding storage: SQLite with JSON serialization for float64 arrays

## Important Implementation Patterns

### AI Integration
- **OpenAI SDK** (`github.com/openai/openai-go/v3`):
  - Story Extraction: GPT-4.1 with structured output and strict JSON schema validation (Temperature=0.1)
  - Embeddings: text-embedding-3-large model for semantic representation
  - Vision: GPT-4o for thumbnail analysis (Temperature=0)
  - Report Generation: GPT-4.1 for merging and prioritizing stories (Temperature=0.1)
  - SDK handles retries automatically

### Concurrency Patterns
- Goroutines + WaitGroups for video/subtitle processing
- Individual goroutine per channel/video for parallel processing
- Progress logging includes index (e.g., "Channel 2/5: Fatih Altayli")

### Error Handling
- Fail-fast for missing environment variables
- Log and continue for individual video/story failures
- Detailed error context in logs for debugging
