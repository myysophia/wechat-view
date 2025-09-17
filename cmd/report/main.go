package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wechat-view/internal/chatlog"
	"wechat-view/internal/config"
	"wechat-view/internal/insight"
	"wechat-view/internal/render"
	"wechat-view/internal/summarize"
)

func main() {
	var (
		cfgPath   = flag.String("config", "report.config.json", "Optional config file (JSON). Flags override its values.")
		baseURL   = flag.String("base-url", "", "Base URL of local chatlog service (overrides config)")
		dateStr   = flag.String("date", "", "Date to fetch, format YYYY-MM-DD (default: yesterday)")
		talker    = flag.String("talker", "", "Chat room or talker id, e.g., 27587714869@chatroom")
		keyword   = flag.String("keyword", "", "Filter keyword (optional)")
		dataDir   = flag.String("data-dir", "", "Directory to store raw daily JSON (overrides config)")
		siteDir   = flag.String("site-dir", "", "Directory to store generated site (overrides config)")
		imageBase = flag.String("image-base-url", "", "Local image base URL for inline images")
		force     = flag.Bool("force", false, "Force re-fetch even if data exists")
		verbose   = flag.Bool("v", false, "Verbose logging")
	)
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}
	cfg.Defaults()

	resolved := struct {
		baseURL     string
		talker      string
		talkerLabel string
		keyword     string
		dataDir     string
		siteDir     string
		imageBase   string
		recentDays  int
		messageCap  int
	}{
		baseURL:    firstNonEmpty(*baseURL, cfg.Chatlog.BaseURL, "http://127.0.0.1:5030"),
		talker:     firstNonEmpty(*talker, cfg.Chatlog.Talker),
		keyword:    firstNonEmpty(*keyword, cfg.Chatlog.Keyword),
		dataDir:    firstNonEmpty(*dataDir, cfg.Report.DataDir, "data"),
		siteDir:    firstNonEmpty(*siteDir, cfg.Report.SiteDir, "site"),
		imageBase:  firstNonEmpty(*imageBase, cfg.Chatlog.ImageBaseURL),
		recentDays: cfg.Report.RecentDays,
		messageCap: cfg.Report.MessagePreview,
	}
	if resolved.talker == "" {
		log.Fatal("--talker is required (provide via flag or config.chatlog.talker)")
	}
	resolved.talkerLabel = cfg.TalkerLabel(resolved.talker)

	day := *dateStr
	if day == "" {
		// default to yesterday (local time)
		day = time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	}

	if *verbose {
		label := resolved.talker
		if resolved.talkerLabel != "" {
			label = fmt.Sprintf("%s (%s)", resolved.talkerLabel, resolved.talker)
		}
		log.Printf("Fetching for date=%s talker=%s keyword=%s", day, label, resolved.keyword)
	}

	// Ensure folders exist
	mustMkdirAll(resolved.dataDir)
	mustMkdirAll(resolved.siteDir)

	// Prepare paths
	rawPath := filepath.Join(resolved.dataDir, fmt.Sprintf("%s.json", day))
	if fileExists(rawPath) && !*force {
		if *verbose {
			log.Printf("Raw data exists: %s (use --force to refetch)", rawPath)
		}
	} else {
		// Fetch from chatlog API
		client := chatlog.Client{BaseURL: resolved.baseURL}
		msgs, meta, err := client.FetchDay(day, resolved.talker, resolved.keyword)
		if err != nil {
			log.Fatalf("fetch failed: %v", err)
		}
		// Persist raw
		if err := writeJSON(rawPath, map[string]any{"date": day, "talker": resolved.talker, "keyword": resolved.keyword, "meta": meta, "messages": msgs}); err != nil {
			log.Fatalf("write raw json failed: %v", err)
		}
		if *verbose {
			log.Printf("Saved raw: %s (%d messages)", rawPath, len(msgs))
		}
	}

	// Read raw for summarization (ensures idempotency)
	var raw struct {
		Date     string            `json:"date"`
		Talker   string            `json:"talker"`
		Keyword  string            `json:"keyword"`
		Meta     map[string]any    `json:"meta"`
		Messages []chatlog.Message `json:"messages"`
	}
	if err := readJSON(rawPath, &raw); err != nil {
		log.Fatalf("read raw json failed: %v", err)
	}

	// Summarize
	sum := summarize.BuildSummary(raw.Messages)

	// Optional AI insights
	var insights insight.Result
	var haveInsights bool
	if cfg.LLM.Enabled && cfg.LLM.BaseURL != "" && cfg.LLM.Model != "" {
		if *verbose {
			log.Printf("Generating AI insights via %s (%s)", cfg.LLM.BaseURL, cfg.LLM.Model)
		}
		client := insight.Client{
			BaseURL:     cfg.LLM.BaseURL,
			Model:       cfg.LLM.Model,
			APIKey:      cfg.LLM.APIKey,
			Temperature: cfg.LLM.Temperature,
			Timeout:     time.Duration(cfg.LLM.TimeoutSeconds) * time.Second,
			MaxMessages: cfg.LLM.MaxMessages,
			MaxChars:    cfg.LLM.MaxChars,
		}
		if res, err := client.Generate(context.Background(), day, firstNonEmpty(resolved.talkerLabel, raw.Talker, resolved.talker), sum, raw.Messages); err != nil {
			if *verbose {
				log.Printf("llm insights failed: %v", err)
			}
		} else {
			insights = res
			haveInsights = true
		}
	}

	// Render day page and meta
	y, m, d, err := splitDate(day)
	if err != nil {
		log.Fatal(err)
	}
	dayDir := filepath.Join(resolved.siteDir, y, m, d)
	mustMkdirAll(dayDir)

	dayHTML := filepath.Join(dayDir, "index.html")
	dayMeta := filepath.Join(dayDir, "meta.json")

	ctx := render.DayContext{
		Date:         day,
		Talker:       raw.Talker,
		TalkerLabel:  resolved.talkerLabel,
		Keyword:      raw.Keyword,
		Summary:      sum,
		Messages:     raw.Messages,
		ImageBaseURL: resolved.imageBase,
		MessageLimit: resolved.messageCap,
	}
	if haveInsights {
		ctx.AIInsights = &render.AIInsights{
			Overview:      insights.Overview,
			Highlights:    insights.Highlights,
			Opportunities: insights.Opportunities,
			Risks:         insights.Risks,
			Actions:       insights.Actions,
			Spotlight:     insights.Spotlight,
		}
	}
	if err := render.DayHTML(dayHTML, ctx); err != nil {
		log.Fatalf("render day html failed: %v", err)
	}
	metaPayload := map[string]any{
		"date":    day,
		"talker":  raw.Talker,
		"keyword": raw.Keyword,
		"summary": sum,
	}
	if haveInsights {
		metaPayload["aiInsights"] = insights
	}
	if err := writeJSON(dayMeta, metaPayload); err != nil {
		log.Fatalf("write day meta failed: %v", err)
	}

	// Update site index (recent days)
	if err := render.UpdateHomeIndex(resolved.siteDir, resolved.dataDir, resolved.recentDays); err != nil {
		log.Fatalf("update home index failed: %v", err)
	}

	if *verbose {
		log.Printf("Generated: %s and %s", dayHTML, dayMeta)
	}
}

func mustMkdirAll(p string) {
	if err := os.MkdirAll(p, 0o755); err != nil {
		log.Fatalf("mkdir %s failed: %v", p, err)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeJSON(p string, v any) error {
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func splitDate(s string) (string, string, string, error) {
	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return "", "", "", errors.New("invalid date format, expect YYYY-MM-DD")
	}
	return parts[0], parts[1], parts[2], nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
