# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Nevsin is a YouTube news aggregator CLI tool written in Go that fetches, transcribes, summarizes, and compiles daily Turkish news reports. It monitors specific YouTube channels, uses AI to analyze content, and generates consolidated news reports.

## Core Architecture

- **Modular CLI application** with main function in `cmd/nevsin/main.go` using cobra for command structure
- **Convention over configuration**: No flags, pure file-based operations in working directory
- **Pipeline-based processing**: fetch-videos → fetch-subtitles → extract-stories → generate-report
- **Concurrent processing** with goroutines for video fetching and transcript extraction
- **Azure OpenAI integration** for thumbnail analysis and transcript summarization
- **YouTube Data API v3** for video fetching

### Key Components

- `fetchVideosCmd`: Retrieves videos from hardcoded channels using custom handlers
- `fetchSubtitlesCmd`: Downloads transcripts using yt-dlp
- `extractStoriesCmd`: Creates AI summaries with structured JSON responses
- `generateReportCmd`: Compiles final markdown reports with AI-sorted importance
- `runCmd`: Executes full pipeline
- `cleanCmd`: Removes old data

### File Structure Convention

```
./
├── .env                        # API keys only
├── videos/
│   ├── abc123.json            # Individual video metadata
│   └── def456.json
├── transcripts/
│   ├── abc123.txt             # Individual transcripts
│   └── def456.txt
├── stories/
│   ├── abc123.json            # Individual stories (JSON format)
│   └── def456.json
└── report.md                  # Final daily report
```

### Data Flow

```
YouTube API → videos/*.json → transcripts/*.txt → stories/*.json → report.md
```

## Development Commands

### Build and Run
```bash
# Build the application
go build ./cmd/nevsin

# Run full pipeline
./nevsin run

# Individual commands
./nevsin fetch-videos
./nevsin fetch-subtitles
./nevsin extract-stories
./nevsin generate-report
./nevsin clean
```

### Linting
```bash
golangci-lint run
```

## Configuration

Requires `.env` file with:
- `YOUTUBE_API_KEY`: YouTube Data API v3 key
- `AZURE_OPENAI_ENDPOINT`: Azure OpenAI endpoint URL
- `AZURE_OPENAI_API_KEY`: Azure OpenAI API key
- `AZURE_OPENAI_DEPLOYMENT`: GPT-4 deployment name
- `AZURE_OPENAI_VISION_DEPLOYMENT`: GPT-4 Vision deployment (optional, for thumbnail analysis)

All environment variables are checked at startup with fail-fast error handling.

## External Dependencies

- **yt-dlp**: Python tool for transcript extraction (`pip install yt-dlp`)
- **Go 1.24.2**: Minimum Go version
- **YouTube Data API v3**: For video metadata
- **Azure OpenAI GPT-4**: For thumbnail analysis and summarization

## Channel Configuration

Currently monitors two hardcoded channels with specific selection criteria:

### Nevsin Mengu (UCrG27KDq7eW4YoEOYsalU9g)
- Gets videos from last 48 hours
- Uses Azure OpenAI GPT-4 Vision to analyze thumbnails
- Extracts text from thumbnails to find "Bugün ne oldu?" content
- Fails entire process if thumbnail analysis fails

### Fatih Altaylı (UCdS7OE5qbJQc7AG4SwlTzKg)
- Gets videos from last 48 hours
- Filters for videos with titles starting with "Fatih Altaylı yorumluyor:"
- Takes first matching video

Channel handlers are defined in the `fetchVideosCmd` with custom filtering logic for each channel. Processing happens concurrently with progress reporting.

## Logging Best Practices

- Always use log.Print* function to log something instead of fmt.Println.