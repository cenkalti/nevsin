# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Nevsin is a YouTube news aggregator CLI tool written in Go that fetches, transcribes, summarizes, and compiles daily Turkish news reports. It monitors specific YouTube channels, uses AI to analyze content, and generates consolidated news reports.

## Core Architecture

- **Single-file CLI application** (`main.go`) using cobra for command structure
- **Pipeline-based processing**: fetch → extract → summarize → generate
- **Concurrent processing** with goroutines for video fetching and transcript extraction
- **Azure OpenAI integration** for thumbnail analysis and transcript summarization
- **YouTube Data API v3** for video fetching

### Key Components

- `fetchCmd`: Retrieves videos from configured channels using custom handlers
- `extractCmd`: Downloads transcripts using yt-dlp
- `summarizeCmd`: Creates AI summaries with structured JSON responses
- `generateCmd`: Compiles final markdown reports
- `runCmd`: Executes full pipeline
- `cleanCmd`: Removes old data

### Data Flow

```
YouTube API → videos/*.json → transcripts/*.txt → summaries/*.md → report.md
```

## Development Commands

### Build and Run
```bash
# Build the application
go build

# Run full pipeline
./nevsin run

# Individual commands
./nevsin fetch
./nevsin extract
./nevsin summarize
./nevsin generate
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

## External Dependencies

- **yt-dlp**: Python tool for transcript extraction (`pip install yt-dlp`)
- **Go 1.24.2**: Minimum Go version
- **YouTube Data API v3**: For video metadata
- **Azure OpenAI GPT-4**: For thumbnail analysis and summarization

## Channel Configuration

Currently monitors two hardcoded channels:
- Nevsin Mengu: Uses AI vision to find "Bugün ne oldu?" content
- Fatih Altaylı: Filters videos with "Fatih Altaylı yorumluyor:" prefix

Channel handlers are defined in the `fetchCmd` with custom filtering logic for each channel.