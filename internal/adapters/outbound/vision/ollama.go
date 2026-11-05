package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

// DefaultOllamaModel is a ~3B-parameter VLM (~3.2GB at Q4) that fits comfortably inside the
// 6.5GB VRAM budget of the RX 5700 XT (8GB) under ROCm. Override with OLLAMA_MODEL.
const DefaultOllamaModel = "qwen2.5vl:3b"

// OllamaConfig configures the local VLM adapter.
type OllamaConfig struct {
	BaseURL    string        // default http://localhost:11434
	Model      string        // default DefaultOllamaModel
	Timeout    time.Duration // default 180s (first call loads the model into VRAM)
	NumCtx     int           // default 8192
	KeepAlive  string        // default "10m"
	SendImages bool
	HTTPClient *http.Client
}

// OllamaVLMExtractor calls Ollama's /api/generate with JSON-schema format enforcement.
type OllamaVLMExtractor struct {
	cfg    OllamaConfig
	client *http.Client
}

var _ ports.OCRExtractor = (*OllamaVLMExtractor)(nil)

// NewOllamaVLMExtractor constructs the adapter.
func NewOllamaVLMExtractor(cfg OllamaConfig) *OllamaVLMExtractor {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}
	if cfg.Model == "" {
		cfg.Model = DefaultOllamaModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 180 * time.Second
	}
	if cfg.NumCtx <= 0 {
		cfg.NumCtx = 8192
	}
	if cfg.KeepAlive == "" {
		cfg.KeepAlive = "10m"
	}
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: cfg.Timeout}
	}
	return &OllamaVLMExtractor{cfg: cfg, client: c}
}

// Kind implements ports.OCRExtractor.
func (o *OllamaVLMExtractor) Kind() domain.ExtractorKind { return domain.ExtractorOllama }

type ollamaRequest struct {
	Model     string         `json:"model"`
	System    string         `json:"system"`
	Prompt    string         `json:"prompt"`
	Images    []string       `json:"images,omitempty"`
	Format    map[string]any `json:"format"`
	Stream    bool           `json:"stream"`
	KeepAlive string         `json:"keep_alive"`
	Options   map[string]any `json:"options"`
}

type ollamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error"`
}

// Extract implements ports.OCRExtractor.
func (o *OllamaVLMExtractor) Extract(ctx context.Context, req ports.ExtractionRequest) (*ports.ExtractionResult, error) {
	wrap := func(err error) error { return fmt.Errorf("vision.OllamaVLMExtractor [%s]: %w", req.JobID, err) }
	body := ollamaRequest{
		Model: o.cfg.Model, System: SystemPrompt, Prompt: BuildUserPrompt(req), Format: PayloadJSONSchema(),
		Stream: false, KeepAlive: o.cfg.KeepAlive,
		Options: map[string]any{"temperature": 0, "num_ctx": o.cfg.NumCtx},
	}
	if o.cfg.SendImages {
		for _, p := range faceImages(req) {
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, wrap(err)
			}
			body.Images = append(body.Images, base64.StdEncoding.EncodeToString(raw))
		}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, wrap(err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.cfg.BaseURL, "/")+"/api/generate", bytes.NewReader(buf))
	if err != nil {
		return nil, wrap(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, wrap(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, wrap(err)
	}
	var out ollamaResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, wrap(fmt.Errorf("decode response (HTTP %d): %w", resp.StatusCode, err))
	}
	if resp.StatusCode != http.StatusOK || out.Error != "" {
		return nil, wrap(fmt.Errorf("HTTP %d: %s", resp.StatusCode, out.Error))
	}
	p, err := domain.ParsePayloadJSON([]byte(stripFences(out.Response)))
	if err != nil {
		return nil, wrap(err)
	}
	return &ports.ExtractionResult{Payload: *p, Extractor: domain.ExtractorOllama, RawOCRFront: req.RawOCRFront, RawOCRBack: req.RawOCRBack, Candidates: req.Candidates}, nil
}
