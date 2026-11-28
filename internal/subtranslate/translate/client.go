package translate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is what the translator asks of the model.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Temperature float64
	TopP        float64
	Seed        int
	MaxTokens   int
	// Schema, when set, forces JSON output matching it (llama-server turns it into a
	// grammar).
	Schema json.RawMessage
	// NoThink disables the thinking mode of hybrid models through the chat template.
	NoThink bool
	// RawPrompt, when set, is sent as is to llama-server's /completion (no chat
	// template); Messages and Schema are ignored.
	RawPrompt string
}

// ChatResponse is the model's answer with its cost.
type ChatResponse struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	// PromptMS and PredictedMS come from llama-server's "timings" (0 when the
	// server does not report them).
	PromptMS     float64
	PredictedMS  float64
	CachedTokens int
	Wall         time.Duration
}

// Completer sends chat requests. Client implements it; tests use a fake.
type Completer interface {
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// Client talks to an OpenAI-compatible server: llama.cpp's llama-server (directly or
// behind llama-swap), and anything else that speaks /v1/chat/completions.
type Client struct {
	// BaseURL is the server root, e.g. http://127.0.0.1:8080 (a trailing /v1 is fine).
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewClient returns a client with a generous timeout (a cold model load can take a while).
func NewClient(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

func (c *Client) root() string {
	u := strings.TrimRight(c.BaseURL, "/")
	return strings.TrimSuffix(u, "/v1")
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.root()+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	return c.HTTP.Do(req)
}

type chatBody struct {
	Model              string         `json:"model,omitempty"`
	Messages           []Message      `json:"messages"`
	Temperature        float64        `json:"temperature"`
	TopP               float64        `json:"top_p,omitempty"`
	Seed               int            `json:"seed,omitempty"`
	MaxTokens          int            `json:"max_tokens,omitempty"`
	Stream             bool           `json:"stream"`
	ResponseFormat     any            `json:"response_format,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
	CachePrompt        bool           `json:"cache_prompt"`
}

type chatReply struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Timings *struct {
		CacheN      int     `json:"cache_n"`
		PromptN     int     `json:"prompt_n"`
		PromptMS    float64 `json:"prompt_ms"`
		PredictedN  int     `json:"predicted_n"`
		PredictedMS float64 `json:"predicted_ms"`
	} `json:"timings"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one chat completion (no streaming).
func (c *Client) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if req.RawPrompt != "" {
		return c.completeRaw(ctx, req)
	}
	body := chatBody{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Seed:        req.Seed,
		MaxTokens:   req.MaxTokens,
		CachePrompt: true,
	}
	if len(req.Schema) > 0 {
		body.ResponseFormat = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "subtitles", "strict": true, "schema": req.Schema},
		}
	}
	if req.NoThink {
		body.ChatTemplateKwargs = map[string]any{"enable_thinking": false}
	}
	start := time.Now()
	resp, err := c.do(ctx, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return ChatResponse{}, err
	}
	var reply chatReply
	if jerr := json.Unmarshal(data, &reply); jerr != nil && resp.StatusCode == http.StatusOK {
		return ChatResponse{}, fmt.Errorf("decoding the reply: %w", jerr)
	}
	if resp.StatusCode != http.StatusOK {
		e := &HTTPError{Status: resp.Status}
		if reply.Error != nil {
			e.Detail = firstLine(reply.Error.Message, 300)
		}
		return ChatResponse{}, e
	}
	if len(reply.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("chat completion: no choices")
	}
	out := ChatResponse{
		Content:          reply.Choices[0].Message.Content,
		PromptTokens:     reply.Usage.PromptTokens,
		CompletionTokens: reply.Usage.CompletionTokens,
		Wall:             time.Since(start),
	}
	if t := reply.Timings; t != nil {
		out.PromptMS, out.PredictedMS, out.CachedTokens = t.PromptMS, t.PredictedMS, t.CacheN
		if out.CompletionTokens == 0 {
			out.CompletionTokens = t.PredictedN
		}
		if out.PromptTokens == 0 {
			out.PromptTokens = t.PromptN + t.CacheN
		}
	}
	return out, nil
}

// HTTPError is a non-200 answer. Error() only has the status: the server's message
// can quote the prompt (template errors), so Detail is only logged with the debug
// text flag.
type HTTPError struct {
	Status string
	Detail string
}

func (e *HTTPError) Error() string { return "chat completion: " + e.Status }

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// Models lists the model ids the server announces (/v1/models).
func (c *Client) Models(ctx context.Context) ([]string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/v1/models: %s", resp.Status)
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(r.Data))
	for _, m := range r.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

// ServerProps is the part of llama-server's /props the reports record.
type ServerProps struct {
	BuildInfo    string `json:"build_info"`
	ModelPath    string `json:"model_path"`
	TotalSlots   int    `json:"total_slots"`
	ChatTemplate string `json:"chat_template"`
	Ctx          int    `json:"n_ctx"`
}

// Props reads /props (llama-server only; other servers return an error).
func (c *Client) Props(ctx context.Context) (ServerProps, error) {
	var p ServerProps
	resp, err := c.do(ctx, http.MethodGet, "/props", nil)
	if err != nil {
		return p, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return p, fmt.Errorf("/props: %s", resp.Status)
	}
	var raw struct {
		ServerProps
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return p, err
	}
	p = raw.ServerProps
	if p.Ctx == 0 {
		p.Ctx = raw.DefaultGenerationSettings.NCtx
	}
	return p, nil
}

// WaitHealthy polls /health until the model is loaded (200) and returns how long it
// waited.
func (c *Client) WaitHealthy(ctx context.Context, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		resp, err := c.do(ctx, http.MethodGet, "/health", nil)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(start), nil
			}
		}
		select {
		case <-ctx.Done():
			return time.Since(start), fmt.Errorf("server not healthy after %s", timeout)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Metrics reads llama-server's Prometheus /metrics (needs --metrics) as name -> value.
func (c *Client) Metrics(ctx context.Context) (map[string]float64, error) {
	resp, err := c.do(ctx, http.MethodGet, "/metrics", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/metrics: %s (start llama-server with --metrics)", resp.Status)
	}
	out := map[string]float64{}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			out[name] = v
		}
	}
	return out, sc.Err()
}

type rawReply struct {
	Content         string `json:"content"`
	TokensEvaluated int    `json:"tokens_evaluated"`
	TokensPredicted int    `json:"tokens_predicted"`
	Timings         *struct {
		CacheN      int     `json:"cache_n"`
		PromptMS    float64 `json:"prompt_ms"`
		PredictedMS float64 `json:"predicted_ms"`
	} `json:"timings"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// completeRaw uses llama-server's native /completion endpoint with a prompt that is
// already in the model's turn format.
func (c *Client) completeRaw(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body := map[string]any{
		"prompt": req.RawPrompt, "temperature": req.Temperature, "seed": req.Seed,
		"cache_prompt": true, "stream": false,
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if req.MaxTokens > 0 {
		body["n_predict"] = req.MaxTokens
	}
	start := time.Now()
	resp, err := c.do(ctx, http.MethodPost, "/completion", body)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return ChatResponse{}, err
	}
	var r rawReply
	jerr := json.Unmarshal(data, &r)
	if resp.StatusCode != http.StatusOK {
		e := &HTTPError{Status: resp.Status}
		if r.Error != nil {
			e.Detail = firstLine(r.Error.Message, 300)
		}
		return ChatResponse{}, e
	}
	if jerr != nil {
		return ChatResponse{}, fmt.Errorf("decoding the reply: %w", jerr)
	}
	out := ChatResponse{Content: r.Content, PromptTokens: r.TokensEvaluated, CompletionTokens: r.TokensPredicted, Wall: time.Since(start)}
	if t := r.Timings; t != nil {
		out.PromptMS, out.PredictedMS, out.CachedTokens = t.PromptMS, t.PredictedMS, t.CacheN
	}
	return out, nil
}
