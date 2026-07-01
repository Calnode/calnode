// Package llm is a minimal, dependency-free client for any OpenAI-compatible
// chat-completions endpoint (cloud frontier model, a self-hosted/sovereign endpoint, or
// a local runtime). Provider and model are configuration, not code — see PRD §8.11.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/netutil"
)

// Config points the client at an endpoint. Endpoint is the API base (e.g.
// "https://api.openai.com/v1"); the client appends "/chat/completions". APIKey is
// optional — local/sovereign endpoints often need none.
type Config struct {
	Endpoint string
	Model    string
	APIKey   string
}

// Client talks to one configured endpoint.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a client. timeout caps a single request. Endpoint is operator-configured
// and may legitimately point at a self-hosted/local runtime — see
// netutil.MetadataSafeTransport for why only cloud-metadata addresses are blocked,
// not private ranges.
func New(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{
		Timeout:   60 * time.Second,
		Transport: netutil.MetadataSafeTransport(slog.Default(), "llm: SSRF block"),
	}}
}

// Model returns the configured model id.
func (c *Client) Model() string { return c.cfg.Model }

// Message is an OpenAI-compatible chat message.
type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // set on role=tool replies
	Name       string     `json:"name,omitempty"`
}

// ToolCall is the model asking to invoke a function.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall is the called function name + raw JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool advertises a function the model may call.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a callable function; Parameters is a JSON Schema value.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ChatRequest is one chat-completions call.
type ChatRequest struct {
	Messages    []Message
	Tools       []Tool
	Temperature *float64
	MaxTokens   int
}

// ChatResult is the assistant's reply (content and/or tool calls).
type ChatResult struct {
	Message Message
}

type chatBody struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

// Chat performs one (non-streaming) chat-completions call.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResult, error) {
	buf, err := json.Marshal(chatBody{
		Model:       c.cfg.Model,
		Messages:    req.Messages,
		Tools:       req.Tools,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/chat/completions"), bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return nil, fmt.Errorf("llm endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("llm decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("llm endpoint returned no choices")
	}
	return &ChatResult{Message: out.Choices[0].Message}, nil
}

// ChatStream performs a streaming chat-completions call. onContent is invoked with each
// content fragment as it arrives (may be nil). It returns the fully assembled message
// (content + any tool calls, with streamed tool-call argument fragments concatenated),
// so callers get the same result shape as Chat plus live deltas.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onContent func(string)) (*ChatResult, error) {
	buf, err := json.Marshal(chatBody{
		Model: c.cfg.Model, Messages: req.Messages, Tools: req.Tools,
		Temperature: req.Temperature, MaxTokens: req.MaxTokens, Stream: true,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/chat/completions"), bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm stream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return nil, fmt.Errorf("llm endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	msg := Message{Role: "assistant"}
	toolAcc := map[int]*ToolCall{}
	var toolOrder []int

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20) // tolerate long SSE lines
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			msg.Content += d.Content
			if onContent != nil {
				onContent(d.Content)
			}
		}
		for _, tc := range d.ToolCalls {
			acc := toolAcc[tc.Index]
			if acc == nil {
				acc = &ToolCall{Type: "function"}
				toolAcc[tc.Index] = acc
				toolOrder = append(toolOrder, tc.Index)
			}
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Type != "" {
				acc.Type = tc.Type
			}
			if tc.Function.Name != "" {
				acc.Function.Name = tc.Function.Name
			}
			acc.Function.Arguments += tc.Function.Arguments
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("llm stream read: %w", err)
	}
	for _, idx := range toolOrder {
		msg.ToolCalls = append(msg.ToolCalls, *toolAcc[idx])
	}
	return &ChatResult{Message: msg}, nil
}

// Ping issues a tiny completion to verify the endpoint, model, and key work. Used by the
// settings "test connection" button.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := c.Chat(ctx, ChatRequest{
		Messages:  []Message{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	return err
}

func (c *Client) url(path string) string {
	return strings.TrimRight(c.cfg.Endpoint, "/") + path
}
