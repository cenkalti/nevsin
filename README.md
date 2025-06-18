# Nevsin

A multi-language YouTube news aggregator CLI tool that automatically fetches, transcribes, summarizes, and compiles daily news reports from Turkish YouTube channels.

## Overview

Nevsin monitors specific Turkish news channels on YouTube, extracts transcripts, generates AI-powered summaries, and creates comprehensive daily news reports. The tool is designed to answer "Bugün ne oldu?" (What happened today?) by aggregating content from multiple news sources.

## Features

- 🎥 Automatic video fetching from predefined YouTube channels
- 🤖 AI-powered thumbnail analysis to identify relevant content
- 📝 Multi-language transcript extraction
- 🧠 Intelligent summarization using Azure OpenAI
- 📊 Consolidated news reports with importance ranking
- 🌍 Multi-language support (Turkish, English, Spanish, etc.)

## Prerequisites

- Go 1.20 or higher
- Python with `yt-dlp` installed (`pip install yt-dlp`)
- YouTube Data API key
- Azure OpenAI API access

## Installation

```bash
git clone https://github.com/yourusername/nevsin.git
cd nevsin
go build
```

## Configuration

Create a `.env` file in the project root:

```bash
# YouTube API Configuration
YOUTUBE_API_KEY=your-youtube-api-key

# Azure OpenAI Configuration
AZURE_OPENAI_ENDPOINT=https://your-resource.openai.azure.com/
AZURE_OPENAI_API_KEY=your-azure-openai-key
AZURE_OPENAI_DEPLOYMENT=gpt-4
AZURE_OPENAI_VISION_DEPLOYMENT=gpt-4-vision

# Language Configuration (default: en)
NEVSIN_LANGUAGE=tr  # Options: tr, en, es, etc.
```

## Usage

### Run Full Pipeline

```bash
./nevsin run
```

This executes the complete workflow: fetch → extract → summarize → generate

### Individual Commands

```bash
# Fetch recent videos from YouTube channels
./nevsin fetch

# Extract transcripts from fetched videos
./nevsin extract

# Generate AI summaries of transcripts
./nevsin summarize

# Create final news report
./nevsin generate

# Clean up old data
./nevsin clean
```

## How It Works

1. **Fetch**: Retrieves videos from the last 48 hours from predefined channels
   - Nevsin Mengu: Uses AI vision to find "Bugün ne oldu?" content
   - Fatih Altaylı: Filters videos starting with "Fatih Altaylı yorumluyor:"

2. **Extract**: Downloads transcripts using yt-dlp
   - Supports multiple languages via `NEVSIN_LANGUAGE`
   - Processes videos concurrently for efficiency

3. **Summarize**: Creates bullet-point summaries with timestamps
   - Uses Azure OpenAI for intelligent summarization
   - Maintains context and key points from each video

4. **Generate**: Compiles final report
   - Groups related news events
   - Ranks by importance (multi-channel coverage = higher priority)
   - Outputs markdown report with links and timestamps

## Output Structure

```
./
├── videos/         # Video metadata (JSON)
├── transcripts/    # Extracted transcripts (TXT)
├── summaries/      # AI summaries (TXT)
└── report.md       # Final news report (Markdown)
```

## Monitored Channels

- [Nevsin Mengu](https://www.youtube.com/@nevsinmengu)
- [Fatih Altaylı](https://www.youtube.com/@fatihaltayli)

## License

[Add your license here]

## Contributing

[Add contribution guidelines if applicable]