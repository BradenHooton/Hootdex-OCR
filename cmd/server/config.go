package main

import (
	"fmt"
	"log/slog"

	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the server configuration, read from environment variables.
type Config struct {
	DataDir          string
	DBPath           string
	HTTPAddr         string
	WebDir           string
	Workers          int
	PollInterval     time.Duration
	EnableLLMCleanup bool
	EnableOllama     bool
	OllamaURL        string
	OllamaModel      string
	AnthropicAPIKey  string
	ClaudeModel      string
	ClaudeFallbacks  bool
	TesseractBin     string
	TesseractLang    string
	PriceChartingKey string
	MarkupPct        float64
	LogLevel         slog.Level
}

func (c Config) InboxDir() string     { return filepath.Join(c.DataDir, "inbox") }
func (c Config) ArchiveDir() string   { return filepath.Join(c.DataDir, "archive") }
func (c Config) ProcessedDir() string { return filepath.Join(c.DataDir, "processed") }

// LoadConfig reads the environment (getenv is injectable for tests).
func LoadConfig(getenv func(string) string) (Config, error) {
	str := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	var errs []string
	boolean := func(k string, def bool) bool {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			return def
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q is not a boolean", k, v))
		}
		return b
	}
	integer := func(k string, def int) int {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q is not an integer", k, v))
		}
		return n
	}
	c := Config{
		DataDir:          str("HOOTDEX_DATA_DIR", "./data"),
		HTTPAddr:         str("HTTP_ADDR", ":8080"),
		WebDir:           str("WEB_DIR", "./web/dist"),
		Workers:          integer("OCR_WORKERS", 2),
		PollInterval:     time.Duration(integer("POLL_INTERVAL_MS", 1000)) * time.Millisecond,
		EnableLLMCleanup: boolean("ENABLE_LLM_CLEANUP", false),
		EnableOllama:     boolean("ENABLE_OLLAMA", true),
		OllamaURL:        str("OLLAMA_URL", "http://localhost:11434"),
		OllamaModel:      str("OLLAMA_MODEL", ""),
		AnthropicAPIKey:  getenv("ANTHROPIC_API_KEY"),
		ClaudeModel:      str("CLAUDE_MODEL", ""),
		ClaudeFallbacks:  boolean("CLAUDE_FALLBACKS", true),
		TesseractBin:     str("TESSERACT_BIN", "tesseract"),
		TesseractLang:    str("TESSERACT_LANG", "eng"),
		PriceChartingKey: getenv("PRICECHARTING_TOKEN"),
		LogLevel:         slog.LevelInfo,
	}
	c.DBPath = str("HOOTDEX_DB", filepath.Join(c.DataDir, "hootdex.db"))
	if v := str("ASKING_MARKUP_PCT", ""); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			errs = append(errs, fmt.Sprintf("ASKING_MARKUP_PCT=%q is not a number", v))
		}
		c.MarkupPct = f
	}
	if err := c.LogLevel.UnmarshalText([]byte(str("LOG_LEVEL", "INFO"))); err != nil {
		errs = append(errs, err.Error())
	}
	if c.Workers < 1 || c.Workers > 8 {
		errs = append(errs, fmt.Sprintf("OCR_WORKERS=%d must be 1..8 (2 protects the 8GB VRAM)", c.Workers))
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("config: %s", strings.Join(errs, "; "))
	}
	return c, nil
}
