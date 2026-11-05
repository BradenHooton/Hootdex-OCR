package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// Claude Messages API defaults. The raw net/http client keeps the module on Go 1.23
// (the official Go SDK v2 requires Go 1.25) and inside the approved dependency list.
const (
	DefaultClaudeModel   = "claude-opus-5-5"
	DefaultClaudeBaseURL = "https://api.anthropic.com"
	claudeAPIVersion     = "2026-10-21" // no temperature/top_p/top_k on this version
	claudeFallbackBeta   = "server-side-fallback-2026-07-01"
	claudeMaxTokens      = 16000
	claudeMaxRetries     = 2
)

// ClaudeConfig configures the cloud cleanup adapter.
type ClaudeConfig struct {
	APIKey     string
	BaseURL    string        // default https://api.anthropic.com
	Model      string        // default claude-opus-5-5
	Effort     string        // output_config.effort; default "medium"
	Fallbacks  bool          // server-side refusal fallbacks (recommended; on by default in the server wiring)
	SendImages bool          // attach the cropped front/back faces
	Timeout    time.Duration // default 120s
	HTTPClient *http.Client
}

// ClaudeAPIExtractor resolves card identity with the Claude Messages API using
// structured outputs constrained to the CardExtractionPayload JSON schema.
type ClaudeAPIExtractor struct {
	cfg    ClaudeConfig
	client *http.Client
	sleep  func(time.Duration)
}

var _ ports.OCRExtractor = (*ClaudeAPIExtractor)(nil)

// NewClaudeAPIExtractor constructs the adapter.
func NewClaudeAPIExtractor(cfg ClaudeConfig) *ClaudeAPIExtractor {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultClaudeBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultClaudeModel
	}
	if cfg.Effort == "" {
		cfg.Effort = "medium"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: cfg.Timeout}
	}
	return &ClaudeAPIExtractor{cfg: cfg, client: c, sleep: time.Sleep}
}

// Kind implements ports.OCRExtractor.
func (c *ClaudeAPIExtractor) Kind() domain.ExtractorKind { return domain.ExtractorClaude }

type claudeContent struct {
	Type   string         `json:"type"`
	Text   string         `json:"text,omitempty"`
	Source map[string]any `json:"source,omitempty"`
}

type claudeRequest struct {
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
	System       string `json:"system"`
	OutputConfig struct {
		Effort string         `json:"effort,omitempty"`
		Format map[string]any `json:"format"`
	} `json:"output_config"`
	Fallbacks string `json:"fallbacks,omitempty"`
	Messages  []struct {
		Role    string          `json:"role"`
		Content []claudeContent `json:"content"`
	} `json:"messages"`
}

type claudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason  string `json:"stop_reason"`
	StopDetails *struct {
		Category    string `json:"category"`
		Explanation string `json:"explanation"`
	} `json:"stop_details"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ErrRefused is returned when the model declines (stop_reason "refusal").
var ErrRefused = errors.New("claude refused the request")

// Extract implements ports.OCRExtractor.
func (c *ClaudeAPIExtractor) Extract(ctx context.Context, req ports.ExtractionRequest) (*ports.ExtractionResult, error) {
	wrap := func(err error) error { return fmt.Errorf("vision.ClaudeAPIExtractor [%s]: %w", req.JobID, err) }
	if c.cfg.APIKey == "" {
		return nil, wrap(errors.New("ANTHROPIC_API_KEY not configured"))
	}
	var body claudeRequest
	body.Model, body.MaxTokens, body.System = c.cfg.Model, claudeMaxTokens, SystemPrompt
	body.OutputConfig.Effort = c.cfg.Effort
	body.OutputConfig.Format = map[string]any{"type": "json_schema", "schema": PayloadJSONSchema()}
	if c.cfg.Fallbacks {
		body.Fallbacks = "default"
	}
	var content []claudeContent
	if c.cfg.SendImages {
		for _, p := range faceImages(req) {
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, wrap(err)
			}
			content = append(content, claudeContent{Type: "image", Source: map[string]any{
				"type": "base64", "media_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(raw)}})
		}
	}
	content = append(content, claudeContent{Type: "text", Text: BuildUserPrompt(req)})
	body.Messages = append(body.Messages, struct {
		Role    string          `json:"role"`
		Content []claudeContent `json:"content"`
	}{Role: "user", Content: content})
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, wrap(err)
	}

	out, err := c.send(ctx, buf)
	if err != nil {
		return nil, wrap(err)
	}
	switch out.StopReason {
	case "refusal":
		cat := ""
		if out.StopDetails != nil {
			cat = out.StopDetails.Category
		}
		return nil, wrap(fmt.Errorf("%w (category %q)", ErrRefused, cat))
	case "max_tokens":
		return nil, wrap(errors.New("response truncated at max_tokens"))
	}
	var text strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" { // thinking blocks are skipped
			text.WriteString(b.Text)
		}
	}
	p, err := domain.ParsePayloadJSON([]byte(stripFences(text.String())))
	if err != nil {
		return nil, wrap(err)
	}
	return &ports.ExtractionResult{Payload: *p, Extractor: domain.ExtractorClaude, RawOCRFront: req.RawOCRFront, RawOCRBack: req.RawOCRBack, Candidates: req.Candidates}, nil
}

// send POSTs to /v1/messages, retrying 429 / 5xx / 529 with Retry-After or exponential backoff.
func (c *ClaudeAPIExtractor) send(ctx context.Context, body []byte) (*claudeResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= claudeMaxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", c.cfg.APIKey)
		httpReq.Header.Set("anthropic-version", claudeAPIVersion)
		if c.cfg.Fallbacks {
			httpReq.Header.Set("anthropic-beta", claudeFallbackBeta)
		}
		resp, err := c.client.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			c.backoff(attempt, "")
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		var out claudeResponse
		_ = json.Unmarshal(raw, &out)
		if resp.StatusCode == http.StatusOK {
			return &out, nil
		}
		msg := strings.TrimSpace(string(raw))
		if out.Error != nil {
			msg = out.Error.Type + ": " + out.Error.Message
		}
		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			c.backoff(attempt, resp.Header.Get("retry-after"))
			continue
		}
		return nil, lastErr // 4xx other than 429 are not retryable
	}
	return nil, lastErr
}

func (c *ClaudeAPIExtractor) backoff(attempt int, retryAfter string) {
	if attempt >= claudeMaxRetries {
		return
	}
	d := time.Duration(1<<attempt) * time.Second
	if s, err := strconv.Atoi(retryAfter); err == nil && s >= 0 && s <= 60 {
		d = time.Duration(s) * time.Second
	}
	c.sleep(d)
}
