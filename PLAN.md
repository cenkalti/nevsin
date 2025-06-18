# Nevsin: Turkish YouTube News Aggregator CLI

## Overview
A Go CLI tool with individual subcommands that work with local files in current directory.

## CLI Commands Structure

### Core Commands (no flags - pure convention over configuration)
```bash
nevsin fetch                    # Reads channels.txt, saves videos/videoID.json
nevsin extract                  # Reads videos/, saves transcripts/videoID.txt
nevsin summarize                # Reads transcripts/, saves summaries/videoID.txt
nevsin generate                 # Reads summaries/, saves report.md
nevsin run                      # Executes full pipeline: fetch -> extract -> summarize -> generate
```

## File Structure Convention
```
./
├── .env                        # API keys only
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
- **Input**: `videos/` directory
- **Output**: `transcripts/videoID.txt` files
- **Function**: Extract transcripts using yt-dlp
- **Concurrency**: Process multiple videos in parallel
- **Error Handling**: Skip videos without subtitles

### `nevsin summarize`
- **Input**: `transcripts/` directory
- **Output**: `summaries/videoID.txt` files with bullet points and timestamps
- **Function**: AI summarization
- **Concurrency**: Process multiple transcripts in parallel
- **Format**: Bullet points with timestamps (start/end) for key points
- **Length**: No maximum length restriction

### `nevsin generate`
- **Input**: `summaries/` directory
- **Output**: `report.md` with grouped news events
- **Function**: Compile final news report with AI-sorted importance
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
- **Function**: Execute complete pipeline

### `nevsin clean`
- **Function**: Remove old videos, transcripts, and summaries from directories
- **Output**: Cleaned directories

## Dependencies
- `yt-dlp` for transcript extraction: `pip install yt-dlp`
- Go packages: `cobra`, `godotenv`

## Key Features
- Pure convention over configuration - no flags at all
- Individual testable commands
- Automatic .env file loading
- Clean separation of concerns
- Stateless operations on working directory
- Simple setup with convention over configuration
- Concurrent processing where possible (fetching, extracting, summarizing)
- Channel-specific selection logic in code

## Error Handling
- All environment variables are required
- Check all required env vars at program startup
- Fail fast with clear error messages
- Lower layers don't need to re-check env vars

## Implementation Notes
- All file operations happen in current working directory
- No configuration files needed beyond .env for API credentials