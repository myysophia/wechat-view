package insight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"wechat-view/internal/chatlog"
	"wechat-view/internal/summarize"
)

// Client talks to an OpenAI-compatible endpoint to generate richer insights.
type Client struct {
	BaseURL     string
	Model       string
	APIKey      string
	Temperature float64
	Timeout     time.Duration
	HTTP        *http.Client
	MaxMessages int
	MaxChars    int
}

// Result captures structured insight from the language model.
type Result struct {
	Overview      string   `json:"overview"`
	Highlights    []string `json:"highlights"`
	Opportunities []string `json:"opportunities"`
	Risks         []string `json:"risks"`
	Actions       []string `json:"actions"`
	Spotlight     string   `json:"spotlight"`
}

const systemPrompt = `You are an experienced product operations analyst. You receive JSON containing aggregated metrics and sampled Chinese chat messages from a single day. Analyse the tone, themes, blockers and collaboration dynamics. Respond in Simplified Chinese with concise business language.

Your response MUST be valid JSON with the following schema:
{
  "overview": string (1-2 sentences summarising the day),
  "highlights": [string],   // 3-4 positive observations or key facts
  "opportunities": [string],// optional improvements or emerging opportunities
  "risks": [string],        // potential problems, conflicts or blockers
  "actions": [string],      // concrete suggested follow-ups (max 3)
  "spotlight": string       // optional quote or takeaway
}
Keep each bullet within 40 Chinese characters. If you lack information for a section, return an empty array or empty string.`

// Generate calls the model and parses its structured response.
func (c Client) Generate(ctx context.Context, date, talker string, summary summarize.Summary, messages []chatlog.Message) (Result, error) {
	if c.BaseURL == "" || c.Model == "" {
		return Result{}, errors.New("missing llm configuration")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: c.Timeout}
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}

	payload := map[string]any{
		"date":     date,
		"talker":   talker,
		"summary":  summary,
		"messages": sampleMessages(messages, c.MaxMessages, c.MaxChars),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}

	reqBody := map[string]any{
		"model":       c.Model,
		"temperature": c.Temperature,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(body)},
		},
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return Result{}, err
	}

	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return Result{}, fmt.Errorf("llm status %d: %s", resp.StatusCode, string(b))
	}

	var raw struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Result{}, err
	}
	if raw.Error.Message != "" {
		return Result{}, errors.New(raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return Result{}, errors.New("empty llm response")
	}
	content := strings.TrimSpace(raw.Choices[0].Message.Content)
	if content == "" {
		return Result{}, errors.New("empty llm content")
	}
	if i := strings.Index(content, "{"); i >= 0 {
		if j := strings.LastIndex(content, "}"); j >= i {
			content = content[i : j+1]
		}
	}

	var result Result
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return Result{}, fmt.Errorf("parse llm response: %w", err)
	}
	result.normalize()
	return result, nil
}

func (r *Result) normalize() {
	r.Overview = strings.TrimSpace(r.Overview)
	r.Spotlight = strings.TrimSpace(r.Spotlight)
	r.Highlights = cleanSlice(r.Highlights)
	r.Opportunities = cleanSlice(r.Opportunities)
	r.Risks = cleanSlice(r.Risks)
	r.Actions = cleanSlice(r.Actions)
}

func cleanSlice(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func sampleMessages(msgs []chatlog.Message, limit, maxChars int) []map[string]string {
	if limit <= 0 {
		limit = 60
	}
	if maxChars <= 0 {
		maxChars = 260
	}
	out := make([]map[string]string, 0, min(limit, len(msgs)))
	for _, m := range msgs {
		if len(out) >= limit {
			break
		}
		text := strings.TrimSpace(firstNonEmpty(m.Content, m.Text))
		if text == "" {
			if m.MsgType == 3 {
				text = "[图片消息]"
			} else {
				continue
			}
		}
		runes := []rune(text)
		if len(runes) > maxChars {
			text = string(runes[:maxChars]) + "..."
		}
		out = append(out, map[string]string{
			"sender": chooseSender(m),
			"time":   displayTime(m),
			"text":   text,
		})
	}
	return out
}

func chooseSender(m chatlog.Message) string {
	if strings.TrimSpace(m.SenderName) != "" {
		return m.SenderName
	}
	if strings.TrimSpace(m.Nickname) != "" {
		return m.Nickname
	}
	if strings.TrimSpace(m.Sender) != "" {
		return m.Sender
	}
	if strings.TrimSpace(m.From) != "" {
		return m.From
	}
	return "匿名"
}

func displayTime(m chatlog.Message) string {
	if strings.TrimSpace(m.Time) != "" {
		return m.Time
	}
	ts := m.Timestamp
	if ts == 0 {
		ts = m.CreateTime
	}
	if ts <= 0 {
		return ""
	}
	if ts > 1_000_000_000_000 {
		ts = ts / 1000
	}
	return time.Unix(ts, 0).Format("15:04:05")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
