package vision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hootyeah/hootdex/internal/domain"
	"github.com/hootyeah/hootdex/internal/ports"
)

const stroudJSON = `{"category":"FOOTBALL","release_year":2023,"manufacturer":"Panini","set_name":"Prizm","card_number":"339",
"player_or_character":"C.J. Stroud","team_or_type":"Houston Texans","parallel_variant":"Silver Prizm","serial_number":null,
"serial_max":null,"is_rookie":true,"is_autograph":false,"is_memorabilia":false,"is_graded":false,"grading_company":"RAW",
"grade_value":null,"cert_number":null,"estimated_raw_condition":"NM-MT","centering_ratio_lr":"51/49","centering_ratio_tb":"53/47",
"confidence_score":0.94,"extraction_notes":"ok"}`

func llmRequest(t *testing.T) ports.ExtractionRequest {
	t.Helper()
	dir := t.TempDir()
	front, back := filepath.Join(dir, "front.jpg"), filepath.Join(dir, "back.jpg")
	_ = os.WriteFile(front, []byte("FRONTJPEG"), 0o644)
	_ = os.WriteFile(back, []byte("BACKJPEG"), 0o644)
	return ports.ExtractionRequest{
		JobID:       "job-1",
		Preprocess:  &domain.PreprocessResult{CardType: domain.CardTypeRaw, CroppedFrontPath: front, CroppedBackPath: back, CenteringLR: "51/49"},
		RawOCRFront: "PRI2M C.J. STR0UD", RawOCRBack: "#339 ©2023 Panini",
		Candidates: []domain.ChecklistCandidate{{Score: 0.97, Entry: domain.ChecklistEntry{ID: 1, Category: domain.CategoryFootball,
			ReleaseYear: 2023, Manufacturer: "Panini", SetName: "Prizm", CardNumber: "339", PlayerOrCharacter: "C.J. Stroud"}}},
	}
}

func TestPayloadSchemaIsStrict(t *testing.T) {
	s := PayloadJSONSchema()
	if s["additionalProperties"] != false {
		t.Fatal("schema must forbid additional properties")
	}
	props := s["properties"].(map[string]any)
	req := s["required"].([]string)
	if len(props) != len(req) || len(req) != 22 {
		t.Fatalf("props=%d required=%d", len(props), len(req))
	}
	for _, k := range req {
		if _, ok := props[k]; !ok {
			t.Fatalf("required %q missing from properties", k)
		}
	}
	// The canonical example must decode into exactly these keys.
	var m map[string]any
	_ = json.Unmarshal([]byte(stroudJSON), &m)
	if len(m) != len(req) {
		t.Fatalf("contract drift: example has %d keys", len(m))
	}
}

func TestBuildUserPromptIncludesOCRAndTop15(t *testing.T) {
	req := llmRequest(t)
	for i := 0; i < 20; i++ {
		req.Candidates = append(req.Candidates, req.Candidates[0])
	}
	p := BuildUserPrompt(req)
	if !strings.Contains(p, "PRI2M C.J. STR0UD") || !strings.Contains(p, "centering_ratio_lr=51/49") || !strings.Contains(p, "15. (match score") || strings.Contains(p, "16. (match score") {
		t.Fatalf("prompt:\n%s", p)
	}
	if !strings.Contains(SystemPrompt, "Respond ONLY with a valid JSON object matching the CardExtractionPayload schema.") {
		t.Fatal("system prompt drifted from OCR_DOMAIN_CONTEXT.md")
	}
}

func TestOllamaExtractor(t *testing.T) {
	var got ollamaRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"response": "```json\n" + stroudJSON + "\n```", "done": true})
	}))
	defer srv.Close()
	ex := NewOllamaVLMExtractor(OllamaConfig{BaseURL: srv.URL, SendImages: true})
	res, err := ex.Extract(context.Background(), llmRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Payload.PlayerOrCharacter != "C.J. Stroud" || res.Extractor != domain.ExtractorOllama || res.RawOCRFront == "" {
		t.Fatalf("result %+v", res)
	}
	if got.Model != DefaultOllamaModel || got.Stream || len(got.Images) != 2 || got.Format["type"] != "object" || got.System != SystemPrompt {
		t.Fatalf("request shape: model=%s stream=%v images=%d", got.Model, got.Stream, len(got.Images))
	}
}

func TestOllamaExtractorErrors(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "model not found"})
	}))
	defer notFound.Close()
	if _, err := NewOllamaVLMExtractor(OllamaConfig{BaseURL: notFound.URL}).Extract(context.Background(), llmRequest(t)); err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"response": `{"category":"CRICKET"}`, "done": true})
	}))
	defer invalid.Close()
	if _, err := NewOllamaVLMExtractor(OllamaConfig{BaseURL: invalid.URL}).Extract(context.Background(), llmRequest(t)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("expected contract violation, got %v", err)
	}
}

func TestClaudeExtractorRequestShapeAndParsing(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []any{map[string]any{"type": "thinking", "thinking": ""}, map[string]any{"type": "text", "text": stroudJSON}},
		})
	}))
	defer srv.Close()
	ex := NewClaudeAPIExtractor(ClaudeConfig{APIKey: "sk-test", BaseURL: srv.URL, Fallbacks: true, SendImages: true})
	res, err := ex.Extract(context.Background(), llmRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Payload.CardNumber != "339" || res.Extractor != domain.ExtractorClaude {
		t.Fatalf("result %+v", res.Payload)
	}
	if hdr.Get("x-api-key") != "sk-test" || hdr.Get("anthropic-version") != "2026-10-21" || hdr.Get("anthropic-beta") != "server-side-fallback-2026-07-01" {
		t.Fatalf("headers %v", hdr)
	}
	if body["model"] != "claude-opus-5-5" || body["fallbacks"] != "default" || body["system"] != SystemPrompt {
		t.Fatalf("body model/fallbacks/system wrong: %v %v", body["model"], body["fallbacks"])
	}
	for _, forbidden := range []string{"temperature", "top_p", "top_k", "thinking"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("%s must not be sent on API version 2026-10-21 / Opus 5.5", forbidden)
		}
	}
	oc := body["output_config"].(map[string]any)
	format := oc["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil || oc["effort"] != "medium" {
		t.Fatalf("output_config %v", oc)
	}
	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 3 || content[0].(map[string]any)["type"] != "image" || content[2].(map[string]any)["type"] != "text" {
		t.Fatalf("content blocks %v", content)
	}
}

func TestClaudeExtractorRefusalRetryAndErrors(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		switch r.Header.Get("x-api-key") {
		case "retry":
			if n == 1 {
				w.Header().Set("retry-after", "0")
				w.WriteHeader(529)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": stroudJSON}}})
		case "refuse":
			_ = json.NewEncoder(w).Encode(map[string]any{"stop_reason": "refusal", "stop_details": map[string]any{"category": "cyber"}, "content": []any{}})
		default:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad schema"}}`))
		}
	}))
	defer srv.Close()
	mk := func(key string) *ClaudeAPIExtractor {
		e := NewClaudeAPIExtractor(ClaudeConfig{APIKey: key, BaseURL: srv.URL})
		e.sleep = func(time.Duration) {}
		return e
	}
	if _, err := mk("retry").Extract(context.Background(), llmRequest(t)); err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	if _, err := mk("refuse").Extract(context.Background(), llmRequest(t)); !errors.Is(err, ErrRefused) {
		t.Fatalf("expected refusal, got %v", err)
	}
	atomic.StoreInt32(&calls, 0)
	if _, err := mk("bad").Extract(context.Background(), llmRequest(t)); err == nil || !strings.Contains(err.Error(), "bad schema") || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("400 must not retry: %v calls=%d", err, calls)
	}
	if _, err := NewClaudeAPIExtractor(ClaudeConfig{}).Extract(context.Background(), llmRequest(t)); err == nil {
		t.Fatal("missing API key must error")
	}
}
