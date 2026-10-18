package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChatJSONCapsTheReplyAndDecodesIt(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"cause\":\"app_down\"}"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "gemma3:4b", 5*time.Second, 2)
	var out struct {
		Cause string `json:"cause"`
	}
	if err := c.ChatJSON(context.Background(), []Message{{Role: "user", Content: "hi"}}, map[string]any{}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Cause != "app_down" {
		t.Fatalf("decoded %+v", out)
	}
	opts := sent["options"].(map[string]any)
	if opts["num_predict"] != float64(400) || opts["num_thread"] != float64(2) {
		t.Fatalf("options sent: %v", opts)
	}
}

func TestATruncatedReplyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// What Render's Ollama returned when Gemma ran on until the context was full.
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"cause\":\"payload_changed\",\"summary\":\"The shop"}}`))
	}))
	defer srv.Close()
	var out map[string]any
	if err := New(srv.URL, "m", time.Second, 0).ChatJSON(context.Background(), nil, nil, &out); err == nil {
		t.Fatal("a cut-off JSON reply must be an error, so the rules answer instead")
	}
}
