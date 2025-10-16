package nevsin

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// parseRetryAfter parses the Retry-After header value and returns duration
func parseRetryAfter(retryAfter string) time.Duration {
	if retryAfter == "" {
		return 0
	}

	// Try to parse as seconds (numeric value)
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try to parse as HTTP date format
	if retryTime, err := time.Parse(time.RFC1123, retryAfter); err == nil {
		return time.Until(retryTime)
	}

	return 0
}

// makeOpenAIRequest makes a request to Azure OpenAI with retry logic for 429 errors
func makeOpenAIRequest(requestBody []byte, endpoint, apiKey, deployment, apiPath string) ([]byte, error) {
	url := fmt.Sprintf("%s/openai/deployments/%s/%s?api-version=2024-08-01-preview", endpoint, deployment, apiPath)
	client := &http.Client{Timeout: 120 * time.Second} // Increased timeout for longer waits

	// Retry configuration - increased for better resilience
	maxRetries := 5
	baseDelay := 5 * time.Second
	maxDelay := 120 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(requestBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api-key", apiKey)

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to call Azure OpenAI: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		// Check for rate limit (429) errors
		if resp.StatusCode == 429 {
			if attempt == maxRetries {
				return nil, fmt.Errorf("azure OpenAI rate limit exceeded after %d retries (status %d): %s", maxRetries, resp.StatusCode, string(body))
			}

			// Check for Retry-After header
			retryAfter := resp.Header.Get("Retry-After")
			retryDelay := parseRetryAfter(retryAfter)

			// If no Retry-After header or invalid, use exponential backoff
			if retryDelay <= 0 {
				retryDelay = baseDelay * time.Duration(1<<attempt) // 5s, 10s, 20s, 40s, 80s
			}

			// Cap the delay to prevent extremely long waits
			if retryDelay > maxDelay {
				retryDelay = maxDelay
			}

			log.Printf("Rate limit hit (attempt %d/%d), retrying in %v (retry-after: %s)...", attempt+1, maxRetries+1, retryDelay, retryAfter)
			time.Sleep(retryDelay)
			continue
		}

		// Handle other non-success status codes
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("azure OpenAI error (status %d): %s", resp.StatusCode, string(body))
		}

		// Success - return the response body
		return body, nil
	}

	// This should never be reached due to the loop logic
	return nil, fmt.Errorf("unexpected error in retry loop")
}
