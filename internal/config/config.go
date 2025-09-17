package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Config collects optional defaults for the report generator.
type Config struct {
	Chatlog ChatlogConfig `json:"chatlog"`
	Report  ReportConfig  `json:"report"`
	LLM     LLMConfig     `json:"llm"`
}

// ChatlogConfig controls how daily data is fetched.
type ChatlogConfig struct {
	BaseURL      string            `json:"baseURL"`
	Talker       string            `json:"talker"`
	TalkerName   string            `json:"talkerName"`
	TalkerAlias  map[string]string `json:"talkerAliases"`
	Keyword      string            `json:"keyword"`
	ImageBaseURL string            `json:"imageBaseURL"`
}

// ReportConfig customises local output.
type ReportConfig struct {
	DataDir        string `json:"dataDir"`
	SiteDir        string `json:"siteDir"`
	RecentDays     int    `json:"recentDays"`
	MessagePreview int    `json:"messagePreview"`
}

// LLMConfig configures the AI insight generation.
type LLMConfig struct {
	Enabled        bool    `json:"enabled"`
	BaseURL        string  `json:"baseURL"`
	Model          string  `json:"model"`
	APIKey         string  `json:"apiKey"`
	Temperature    float64 `json:"temperature"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
	MaxMessages    int     `json:"maxMessages"`
	MaxChars       int     `json:"maxChars"`
}

// Load reads configuration from JSON. Missing files are treated as empty config.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// TalkerLabel returns a friendly name for the talker id if known.
func (c Config) TalkerLabel(id string) string {
	if id == "" {
		return ""
	}
	if c.Chatlog.TalkerAlias != nil {
		if v, ok := c.Chatlog.TalkerAlias[id]; ok && v != "" {
			return v
		}
	}
	if id == c.Chatlog.Talker && c.Chatlog.TalkerName != "" {
		return c.Chatlog.TalkerName
	}
	return ""
}

// Defaults ensures minimal sane defaults.
func (c *Config) Defaults() {
	if c.Report.RecentDays == 0 {
		c.Report.RecentDays = 14
	}
	if c.Report.MessagePreview == 0 {
		c.Report.MessagePreview = 120
	}
	if c.LLM.Temperature == 0 {
		c.LLM.Temperature = 0.4
	}
	if c.LLM.TimeoutSeconds == 0 {
		c.LLM.TimeoutSeconds = 25
	}
	if c.LLM.MaxMessages == 0 {
		c.LLM.MaxMessages = 60
	}
	if c.LLM.MaxChars == 0 {
		c.LLM.MaxChars = 260
	}
}
