# Nevsin - Turkish News Aggregator

Nevsin is a YouTube news aggregator CLI tool written in Go that fetches, transcribes, summarizes, and compiles daily Turkish news reports. It monitors specific YouTube channels, uses AI to analyze content, and generates consolidated news reports.

## Features

- **Automated news collection** from YouTube channels
- **AI-powered transcript analysis** using Azure OpenAI
- **Structured story extraction** with timestamps and source links
- **Daily report generation** in markdown format
- **Automatic HTML deployment** to GitHub Pages

## Architecture

### Core Components

- `fetch-videos`: Retrieves videos from configured channels
- `fetch-subtitles`: Downloads subtitles using yt-dlp
- `extract-stories`: Creates AI summaries with structured JSON responses
- `generate-report`: Compiles final markdown reports with AI-sorted importance
- `run`: Executes full pipeline
- `clean`: Removes old data

### Data Flow

```
YouTube API → videos/*.json → subtitles/*.srt → stories/*.json → report.md → index.html
```

## Installation

### Prerequisites

- Go 1.24.2 or higher
- Python with `yt-dlp` installed: `pip install yt-dlp`
- YouTube Data API v3 key
- Azure OpenAI API access

### Setup

1. Clone the repository:
```bash
git clone https://github.com/cenkalti/nevsin.git
cd nevsin
```

2. Build the application:
```bash
go build ./cmd/nevsin
```

3. Create a `.env` file with your API keys:
```env
YOUTUBE_API_KEY=your_youtube_api_key
AZURE_OPENAI_ENDPOINT=https://your-resource.openai.azure.com/
AZURE_OPENAI_API_KEY=your_azure_openai_key
AZURE_OPENAI_DEPLOYMENT=your_gpt4_deployment_name
AZURE_OPENAI_VISION_DEPLOYMENT=your_gpt4_vision_deployment_name
```

## Usage

### Manual Execution

```bash
# Run the complete pipeline
./nevsin run

# Or run individual steps
./nevsin fetch-videos
./nevsin fetch-subtitles
./nevsin extract-stories [video-id]  # Optional video ID for single processing
./nevsin generate-report
```

### GitHub Actions Deployment

The project includes automated deployment to GitHub Pages using GitHub Actions.

#### Setup GitHub Pages Deployment

1. **Enable GitHub Pages**:
   - Go to repository Settings → Pages
   - Set source to "GitHub Actions"

2. **Add Repository Secrets**:
   - Go to repository Settings → Secrets and Variables → Actions
   - Add the following secrets:
     - `YOUTUBE_API_KEY`
     - `AZURE_OPENAI_ENDPOINT`
     - `AZURE_OPENAI_API_KEY`
     - `AZURE_OPENAI_DEPLOYMENT`
     - `AZURE_OPENAI_VISION_DEPLOYMENT`

3. **Automatic Deployment**:
   - The workflow runs daily at 12 PM EST (17:00 UTC)
   - Manual runs can be triggered from the Actions tab
   - Generated reports are automatically deployed as HTML to GitHub Pages

#### Workflow Features

- **Daily Schedule**: Runs automatically at 12 PM EST
- **Manual Trigger**: Can be run manually from GitHub Actions
- **HTML Generation**: Converts markdown reports to styled HTML
- **Responsive Design**: Mobile-friendly layout with embedded CSS
- **Turkish Language Support**: Proper encoding and typography

## Configuration

### Monitored Channels

Currently monitors these channels with specific criteria:

#### Nevsin Mengu (UCrG27KDq7eW4YoEOYsalU9g)
- Fetches videos from last 48 hours
- Uses AI vision to analyze thumbnails for "Bugün ne oldu?" content
- Fails if thumbnail analysis fails

#### Fatih Altaylı (UCdS7OE5qbJQc7AG4SwlTzKg)
- Fetches videos from last 48 hours
- Filters for titles starting with "Fatih Altaylı yorumluyor:"
- Takes first matching video

## File Structure

```
./
├── .env                        # API keys (local only)
├── .github/workflows/          # GitHub Actions workflows
├── templates/                  # HTML templates for deployment
├── videos/                     # Video metadata (JSON)
├── subtitles/                  # Subtitle files (SRT)
├── stories/                    # Extracted stories (JSON)
├── report.md                   # Generated daily report
└── index.html                  # Generated HTML (deployment)
```

## Development

### Linting

```bash
golangci-lint run
```

### Testing Individual Components

```bash
# Extract stories from a specific video
./nevsin extract-stories VIDEO_ID

# Generate report from existing stories
./nevsin generate-report
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests and linting
5. Submit a pull request

## License

[Add your license here]

## Support

For issues and questions, please open an issue on GitHub.