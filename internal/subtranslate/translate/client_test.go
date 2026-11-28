package translate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A fake llama-server: /v1/chat/completions with timings, /v1/models, /props,
// /health and /metrics.
func fakeServer(t *testing.T, check func(body map[string]any)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			data, _ := io.ReadAll(r.Body)
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Error(err)
			}
			if check != nil {
				check(body)
			}
			if body["model"] == "broken" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"template error near: Where were you?"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"id\":\"b1\",\"t\":[[0,\"Hola\"]]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":120,"completion_tokens":12},
				"timings":{"cache_n":20,"prompt_n":100,"prompt_ms":80.5,"predicted_n":12,"predicted_ms":240.0}}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"Qwen_Qwen3-4B-Instruct-2507-Q4_K_M"}]}`))
		case "/props":
			_, _ = w.Write([]byte(`{"build_info":"b8545-7ca0c9cca","model_path":"/models/x.gguf","total_slots":2,"default_generation_settings":{"n_ctx":4096}}`))
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/metrics":
			_, _ = w.Write([]byte("# HELP x\nllamacpp:prompt_tokens_total 1234\nllamacpp:tokens_predicted_total{slot=\"0\"} 56\n"))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestClientComplete(t *testing.T) {
	var got map[string]any
	srv := fakeServer(t, func(b map[string]any) { got = b })
	defer srv.Close()
	c := NewClient(srv.URL + "/v1/") // a trailing /v1 is accepted
	resp, err := c.Complete(context.Background(), ChatRequest{
		Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Temperature: 0.2, TopP: 0.9, Seed: 42,
		Schema: batchSchema("b1", []int{0}), NoThink: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content == "" || resp.PromptTokens != 120 || resp.CompletionTokens != 12 || resp.PromptMS != 80.5 || resp.PredictedMS != 240 || resp.CachedTokens != 20 {
		t.Errorf("response %+v", resp)
	}
	rf := got["response_format"].(map[string]any)
	if rf["type"] != "json_schema" || rf["json_schema"].(map[string]any)["strict"] != true {
		t.Errorf("response_format %v", rf)
	}
	if got["chat_template_kwargs"].(map[string]any)["enable_thinking"] != false {
		t.Errorf("thinking not disabled: %v", got["chat_template_kwargs"])
	}
	if got["seed"].(float64) != 42 || got["stream"] != false {
		t.Errorf("body %v", got)
	}
	// No Ollama-only fields.
	for _, k := range []string{"keep_alive", "options", "think"} {
		if _, ok := got[k]; ok {
			t.Errorf("unexpected field %s", k)
		}
	}
}

func TestClientErrorHidesDetail(t *testing.T) {
	srv := fakeServer(t, nil)
	defer srv.Close()
	_, err := NewClient(srv.URL).Complete(context.Background(), ChatRequest{Model: "broken", Messages: []Message{{Role: "user", Content: "x"}}})
	var he *HTTPError
	if !errors.As(err, &he) || !strings.Contains(he.Detail, "Where were you?") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), "Where were you") {
		t.Errorf("Error() leaks the server message: %s", err)
	}
}

func TestClientServerInfo(t *testing.T) {
	srv := fakeServer(t, nil)
	defer srv.Close()
	c := NewClient(srv.URL)
	ctx := context.Background()
	models, err := c.Models(ctx)
	if err != nil || len(models) != 1 {
		t.Fatalf("%v %v", models, err)
	}
	p, err := c.Props(ctx)
	if err != nil || p.BuildInfo != "b8545-7ca0c9cca" || p.Ctx != 4096 || p.TotalSlots != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := c.WaitHealthy(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	m, err := c.Metrics(ctx)
	if err != nil || m["llamacpp:prompt_tokens_total"] != 1234 || m["llamacpp:tokens_predicted_total"] != 56 {
		t.Fatalf("%v %v", m, err)
	}
}
