# Nevsin: Multi-Language YouTube News Aggregator CLI

## Overview
A Go CLI tool with individual subcommands that work with local files in current directory, using language from environment variable (no flags needed).

## CLI Commands Structure

### Core Commands (no flags - pure convention over configuration)
```bash
nevsin fetch                    # Reads channels.txt, saves videos/videoID.json
nevsin extract                  # Reads videos/, saves transcripts/videoID.txt  
nevsin summarize                # Reads transcripts/, saves summaries/videoID.txt
nevsin generate                 # Reads summaries/, saves report.md
nevsin run                      # Executes full pipeline: fetch -> extract -> summarize -> generate
```

### Language Configuration via Environment
- Language determined by `NEVSIN_LANGUAGE` environment variable
- Defaults to "en" if not set
- Set in .env file: `NEVSIN_LANGUAGE=tr` or `NEVSIN_LANGUAGE=en`
- Affects: subtitle extraction, AI prompts, and report language

## File Structure Convention
```
./
├── .env                        # API keys + language preference
├── videos/
│   ├── abc123.json            # Individual video metadata  
│   └── def456.json
├── transcripts/
│   ├── abc123.txt             # Individual transcripts
│   └── def456.txt
├── summaries/
│   ├── abc123.txt             # Individual summaries
│   └── def456.txt
└── report.md                  # Final daily report
```

## Environment Variables (.env file)
```bash
# API Configuration
YOUTUBE_API_KEY=your-youtube-api-key
AZURE_OPENAI_ENDPOINT=https://your-resource.openai.azure.com/
AZURE_OPENAI_API_KEY=your-azure-openai-key  
AZURE_OPENAI_DEPLOYMENT=gpt-4
AZURE_OPENAI_VISION_DEPLOYMENT=gpt-4-vision  # For thumbnail analysis

# Language Configuration
NEVSIN_LANGUAGE=en              # Default: en, can be tr, es, etc.
```

## Command Details

### `nevsin fetch`
- **Input**: Channels defined in Go code with custom selection criteria
- **Output**: `videos/videoID.json` with all available metadata
- **Function**: Fetch recent videos using channel-specific logic:
  - **Nevsin Mengu (UCrG27KDq7eW4YoEOYsalU9g)**: 
    - Get videos from last 48 hours
    - Analyze thumbnails with GPT 4.1 (Azure AI Foundry)
    - Find "Bugun ne oldu?" by text extraction
    - Skip channel if not found
  - **Fatih Altayli (UCdS7OE5qbJQc7AG4SwlTzKg)**:
    - Get videos from last 48 hours
    - Find first video with title starting "Fatih Altaylı yorumluyor:"
- **Concurrency**: Process channels in parallel using goroutines
- **Progress**: Show "Processing 3 channels... Channel 1/3: Found 2 videos..."
- **Error Handling**: Fail entire process if thumbnail analysis fails

### `nevsin extract`
- **Input**: `videos/` directory + `NEVSIN_LANGUAGE` env var
- **Output**: `transcripts/videoID.txt` files
- **Function**: Extract transcripts using yt-dlp for language from env
- **Language**: Uses `NEVSIN_LANGUAGE` (defaults to "en")
- **Concurrency**: Process multiple videos in parallel
- **Error Handling**: Skip videos without subtitles in requested language

### `nevsin summarize`
- **Input**: `transcripts/` directory + `NEVSIN_LANGUAGE` env var
- **Output**: `summaries/videoID.txt` files with bullet points and timestamps
- **Function**: AI summarization with language-specific prompts from env
- **Language**: Uses `NEVSIN_LANGUAGE` for prompts and output
- **Concurrency**: Process multiple transcripts in parallel
- **Format**: Bullet points with timestamps (start/end) for key points
- **Length**: No maximum length restriction

### `nevsin generate`
- **Input**: `summaries/` directory + `NEVSIN_LANGUAGE` env var
- **Output**: `report.md` with grouped news events
- **Function**: Compile final news report with AI-sorted importance
- **Language**: Uses `NEVSIN_LANGUAGE` for headers and formatting
- **Format**: 
  ```markdown
  # Bugun ne oldu?
  
  ## {event_title}
  {event description}
  
  **Covered by:**
  - [Video Title](https://link-with-timestamp)
  - [Video Title 2](https://link-with-timestamp)
  ```
- **Sorting**: AI sorts by importance (events in multiple channels ranked higher)

### `nevsin run`
- **Function**: Execute complete pipeline using `NEVSIN_LANGUAGE` for all steps

### `nevsin clean`
- **Function**: Remove old videos, transcripts, and summaries from directories
- **Output**: Cleaned directories

## Dependencies
- `yt-dlp` for transcript extraction: `pip install yt-dlp`
- Go packages: `cobra`, `godotenv`

## Key Features
- Pure convention over configuration - no flags at all
- Language from environment variable only
- Individual testable commands
- Automatic .env file loading
- Clean separation of concerns
- Stateless operations on working directory
- Simple setup: just set NEVSIN_LANGUAGE=tr in .env file
- Concurrent processing where possible (fetching, extracting, summarizing)
- Channel-specific selection logic in code

## Error Handling
- All environment variables are required
- Check all required env vars at program startup
- Fail fast with clear error messages
- Lower layers don't need to re-check env vars

## Implementation Notes
- Each command reads language from `NEVSIN_LANGUAGE` environment variable
- Falls back to "en" if `NEVSIN_LANGUAGE` is not set
- Language affects subtitle language codes, AI prompt language, and report headers
- All file operations happen in current working directory
- No configuration files needed beyond .env for credentials and language preference