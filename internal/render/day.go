package render

import (
	"embed"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"wechat-view/internal/chatlog"
	"wechat-view/internal/summarize"
)

//go:embed templates/*
var tplFS embed.FS

type DayContext struct {
	Date               string
	Talker             string
	TalkerLabel        string
	Keyword            string
	Summary            summarize.Summary
	Messages           []chatlog.Message
	ImageBaseURL       string
	MessageLimit       int
	HiddenMessageCount int
	ActivitySeries     []HourSlot
	SenderViews        []SenderView
	LinkViews          []LinkView
	KeywordViews       []KeywordView
	AIInsights         *AIInsights
}

func DayHTML(outPath string, ctx DayContext) error {
	ctx.ActivitySeries = buildActivitySeries(ctx.Summary.HourlyHistogram)
	ctx.SenderViews = buildSenderViews(ctx.Summary.TopSenders, ctx.Summary.TotalMessages)
	ctx.LinkViews = buildLinkViews(ctx.Summary.TopLinks, ctx.Messages)
	ctx.KeywordViews = buildKeywordViews(ctx.Summary.Keywords, 20)
	if ctx.MessageLimit > 0 && len(ctx.Messages) > ctx.MessageLimit {
		start := len(ctx.Messages) - ctx.MessageLimit
		if start < 0 {
			start = 0
		}
		ctx.HiddenMessageCount = start
		ctx.Messages = append([]chatlog.Message(nil), ctx.Messages[start:]...)
	}

	funcMap := template.FuncMap{
		"imageURL": func(base string, m chatlog.Message) string {
			if base == "" || m.MediaPath == "" || m.MediaMD5 == "" {
				return ""
			}
			// keep backslashes in path per local API requirement
			return strings.TrimRight(base, "/") + "/image/" + m.MediaMD5 + "," + m.MediaPath
		},
		"isImage":         func(m chatlog.Message) bool { return m.MsgType == 3 },
		"host":            hostOnly,
		"formatTimestamp": formatTimestamp,
		"percent":         func(v float64) string { return fmt.Sprintf("%.0f%%", v*100) },
		"join":            strings.Join,
	}
	t, err := template.New("day").Funcs(funcMap).ParseFS(tplFS, "templates/day.html")
	if err != nil {
		return err
	}
	f, err := createAtomic(outPath)
	if err != nil {
		return err
	}
	defer f.abort()
	if err := t.Execute(f.tmp, ctx); err != nil {
		return err
	}
	return f.commit()
}

func UpdateHomeIndex(siteDir, dataDir string, recentDays int) error {
	// Scan dataDir for YYYY-MM-DD.json files and pick the most recent N
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return err
	}
	days := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) == 15 && name[4] == '-' && name[7] == '-' && name[10:] == ".json" {
			days = append(days, name[:10])
		}
	}
	sort.Strings(days)
	if len(days) > recentDays {
		days = days[len(days)-recentDays:]
	}
	// Build items for template
	type item struct{ Date, URL, Label string }
	items := make([]item, 0, len(days))
	for i := len(days) - 1; i >= 0; i-- { // newest first
		day := days[i]
		y, m, d := day[:4], day[5:7], day[8:10]
		items = append(items, item{
			Date:  day,
			URL:   filepath.ToSlash(filepath.Join(y, m, d, "index.html")),
			Label: mustFormatLabel(day),
		})
	}

	t, err := template.ParseFS(tplFS, "templates/index.html")
	if err != nil {
		return err
	}
	f, err := createAtomic(filepath.Join(siteDir, "index.html"))
	if err != nil {
		return err
	}
	defer f.abort()
	data := map[string]any{"Items": items, "GeneratedAt": time.Now().Format(time.RFC3339)}
	if err := t.Execute(f.tmp, data); err != nil {
		return err
	}
	return f.commit()
}

type atomicFile struct {
	tmp   *os.File
	final string
}

func createAtomic(final string) (*atomicFile, error) {
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), ".tmp-*")
	if err != nil {
		return nil, err
	}
	return &atomicFile{tmp: tmp, final: final}, nil
}

func (a *atomicFile) commit() error {
	if err := a.tmp.Close(); err != nil {
		return err
	}
	return os.Rename(a.tmp.Name(), a.final)
}

func (a *atomicFile) abort() {
	if a.tmp != nil {
		_ = a.tmp.Close()
		_ = os.Remove(a.tmp.Name())
	}
}

func mustFormatLabel(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.Format("2006-01-02 Mon")
}

type HourSlot struct {
	Label   string
	Count   int
	Percent float64
}

type SenderView struct {
	Name    string
	Count   int
	Percent float64
}

type LinkView struct {
	URL     string
	Host    string
	Title   string
	Desc    string
	Snippet string
}

type KeywordView struct {
	Text  string
	Count int
}

type AIInsights struct {
	Overview      string
	Highlights    []string
	Opportunities []string
	Risks         []string
	Actions       []string
	Spotlight     string
}

func buildActivitySeries(hist [24]int) []HourSlot {
	slots := make([]HourSlot, 0, len(hist))
	max := 0
	for _, v := range hist {
		if v > max {
			max = v
		}
	}
	for hour, count := range hist {
		percent := 0.0
		if max > 0 {
			percent = float64(count) / float64(max) * 100
		}
		slots = append(slots, HourSlot{
			Label:   fmt.Sprintf("%02d", hour),
			Count:   count,
			Percent: percent,
		})
	}
	return slots
}

func buildSenderViews(items []summarize.KV, total int) []SenderView {
	views := make([]SenderView, 0, len(items))
	tot := float64(total)
	for _, kv := range items {
		percent := 0.0
		if tot > 0 {
			percent = float64(kv.Count) / tot * 100
		}
		views = append(views, SenderView{
			Name:    kv.Key,
			Count:   kv.Count,
			Percent: percent,
		})
	}
	return views
}

func buildLinkViews(urls []string, messages []chatlog.Message) []LinkView {
	freq := make(map[string]int)
	ordered := make([]string, 0, len(urls))
	for _, u := range urls {
		if _, ok := freq[u]; !ok {
			ordered = append(ordered, u)
		}
		freq[u]++
	}
	meta := make(map[string]LinkView)
	for _, msg := range messages {
		if msg.Share != nil && msg.Share.URL != "" {
			u := msg.Share.URL
			entry := meta[u]
			entry.URL = u
			entry.Host = hostOnly(u)
			if entry.Title == "" && msg.Share.Title != "" {
				entry.Title = msg.Share.Title
			}
			if entry.Desc == "" && msg.Share.Desc != "" {
				entry.Desc = msg.Share.Desc
			}
			meta[u] = entry
		}
		text := firstNonEmptyStr(msg.Content, msg.Text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		snippet := buildLinkSnippet(text)
		if snippet == "" {
			continue
		}
		for _, u := range urls {
			if strings.Contains(text, u) {
				entry := meta[u]
				if entry.URL == "" {
					entry.URL = u
					entry.Host = hostOnly(u)
				}
				if entry.Snippet == "" {
					entry.Snippet = snippet
				}
				if entry.Title == "" {
					entry.Title = entry.Host
				}
				meta[u] = entry
				break
			}
		}
	}
	out := make([]LinkView, 0, len(ordered))
	for _, u := range ordered {
		if entry, ok := meta[u]; ok {
			if entry.Title == "" {
				entry.Title = hostOnly(u)
			}
			out = append(out, entry)
			continue
		}
		out = append(out, LinkView{URL: u, Host: hostOnly(u), Title: hostOnly(u)})
	}
	return out
}

var linkURLRegexp = regexp.MustCompile(`https?://[^\s]+`)

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func buildLinkSnippet(text string) string {
	clean := strings.TrimSpace(linkURLRegexp.ReplaceAllString(text, ""))
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return ""
	}
	runes := []rune(clean)
	if len(runes) > 80 {
		clean = string(runes[:80]) + "…"
	}
	return clean
}

func hostOnly(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

func buildKeywordViews(items []summarize.KV, limit int) []KeywordView {
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]KeywordView, 0, len(items))
	for _, kv := range items {
		out = append(out, KeywordView{Text: kv.Key, Count: kv.Count})
	}
	return out
}

func formatTimestamp(ts int64) string {
	if ts <= 0 {
		return ""
	}
	if ts > 1_000_000_000_000 {
		ts = ts / 1000
	}
	return time.Unix(ts, 0).Format("15:04:05")
}
