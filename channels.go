package nevsin

import (
	"log"
	"strings"
	"time"
)

// ChannelConfig represents a YouTube channel configuration
type ChannelConfig struct {
	Name    string
	ID      string
	Handler func([]YouTubeVideo) []YouTubeVideo
}

// ChannelConfigs contains all configured channel configurations
var ChannelConfigs = []ChannelConfig{
	{
		Name: "Nevsin Mengu",
		ID:   "UCrG27KDq7eW4YoEOYsalU9g",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			// Get videos from last 48 hours, analyze thumbnails, find "Bugun ne oldu?"
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 48*time.Hour {
					continue
				}
				// Analyze thumbnail with Azure OpenAI
				extractedTitle, err := analyzeThumbnail(v.ThumbnailURL)
				if err != nil {
					log.Printf("Thumbnail analysis failed: %v", err)
					continue
				}
				// Check if the title contains "Bugün ne oldu" (case insensitive)
				if strings.Contains(strings.ToLower(extractedTitle), "bugün ne oldu") {
					return []YouTubeVideo{v}
				}
			}
			return nil
		},
	},
	{
		Name: "Fatih Altayli",
		ID:   "UCdS7OE5qbJQc7AG4SwlTzKg",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Deniz Zeyrek",
		ID:   "UCR8vMahbDD-23OjGXDQPu2Q",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Serdar Akinan",
		ID:   "UCnVMhMq6nIwhcrHSLhQO-Ow",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Bahar Feyzan",
		ID:   "UCXgRU9vrJmmNNLDyTbdkjSg",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Rusen Cakir",
		ID:   "UCfeZdjH_RKcQgJCebxUpBSw",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Murat Yetkin",
		ID:   "UC2dULuYNHz0LVfqO5SFsgmw",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Ozlem Gurses",
		ID:   "UCojOP7HHZvM2nZz4Rwnd6-Q",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Onlar TV",
		ID:   "UC8n7mdTh0PsepLg43FgXTyw",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Unsal Unlu",
		ID:   "UCzJMy0X4vYivbZHkNccpPhQ",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
	{
		Name: "Hilal Koylu",
		ID:   "UCCANgYjjCDRgzqcLYHGaJEA",
		Handler: func(videos []YouTubeVideo) []YouTubeVideo {
			var selected []YouTubeVideo
			for _, v := range videos {
				if time.Since(v.PublishedAt) > 24*time.Hour {
					continue
				}
				selected = append(selected, v)
			}
			return selected
		},
	},
}