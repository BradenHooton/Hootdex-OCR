package main

import (
	"log/slog"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfigDefaults(t *testing.T) {
	c, err := LoadConfig(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Workers != 2 || c.DBPath != "data/hootdex.db" || c.EnableLLMCleanup || !c.ClaudeFallbacks || c.HTTPAddr != ":8080" || c.LogLevel != slog.LevelInfo {
		t.Fatalf("defaults %+v", c)
	}
	if c.InboxDir() != "data/inbox" || c.ProcessedDir() != "data/processed" {
		t.Fatalf("dirs %s %s", c.InboxDir(), c.ProcessedDir())
	}
}

func TestLoadConfigOverridesAndErrors(t *testing.T) {
	c, err := LoadConfig(env(map[string]string{"HOOTDEX_DATA_DIR": "/srv/hd", "ENABLE_LLM_CLEANUP": "true", "OCR_WORKERS": "1", "LOG_LEVEL": "debug", "ASKING_MARKUP_PCT": "15"}))
	if err != nil || c.DBPath != "/srv/hd/hootdex.db" || !c.EnableLLMCleanup || c.Workers != 1 || c.LogLevel != slog.LevelDebug || c.MarkupPct != 15 {
		t.Fatalf("overrides %+v %v", c, err)
	}
	if _, err := LoadConfig(env(map[string]string{"OCR_WORKERS": "16"})); err == nil {
		t.Fatal("too many workers must be rejected")
	}
	if _, err := LoadConfig(env(map[string]string{"ENABLE_LLM_CLEANUP": "maybe"})); err == nil {
		t.Fatal("bad bool must be rejected")
	}
}
