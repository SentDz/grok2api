package settings

import (
	"errors"
	"net/url"
	"strings"
)

type StatsigBuiltinConfig struct {
	AutoUpdate           bool   `json:"autoUpdate"`
	CheckIntervalSeconds int    `json:"checkIntervalSeconds"`
	CaptureAccountID     uint64 `json:"captureAccountID"`
	LLMEnabled           bool   `json:"llmEnabled"`
	LLMProvider          string `json:"llmProvider"`
	LLMURL               string `json:"llmURL"`
	LLMKey               string `json:"llmKey,omitempty"`
	LLMKeyConfigured     bool   `json:"llmKeyConfigured"`
	ClearLLMKey          bool   `json:"clearLLMKey,omitempty"`
	LLMModel             string `json:"llmModel"`
	LLMMaxAttempts       int    `json:"llmMaxAttempts"`
}

func DefaultStatsigBuiltin() StatsigBuiltinConfig {
	return StatsigBuiltinConfig{AutoUpdate: true, CheckIntervalSeconds: 1800, LLMEnabled: true, LLMProvider: "build", LLMModel: "grok-4.6", LLMMaxAttempts: 3}
}

func (c StatsigBuiltinConfig) Validate() error {
	if c.CheckIntervalSeconds < 300 || c.CheckIntervalSeconds > 86400 {
		return errors.New("Statsig check interval must be 300-86400 seconds")
	}
	if c.LLMProvider != "build" && c.LLMProvider != "external" {
		return errors.New("Statsig model provider must be build or external")
	}
	if c.LLMMaxAttempts < 1 || c.LLMMaxAttempts > 8 {
		return errors.New("Statsig model attempts must be 1-8")
	}
	if len(c.LLMKey) > 8192 || len(c.LLMModel) > 256 || len(c.LLMURL) > 2048 {
		return errors.New("Statsig model configuration is too long")
	}
	if c.LLMEnabled && strings.TrimSpace(c.LLMModel) == "" {
		return errors.New("Statsig model name is required")
	}
	if c.LLMProvider == "external" && (c.LLMEnabled || c.LLMURL != "") {
		u, err := url.ParseRequestURI(c.LLMURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("Statsig model URL must be an HTTP(S) base URL without credentials, query or fragment")
		}
		if c.LLMEnabled && c.LLMKey == "" {
			return errors.New("Statsig external model API key is required")
		}
	}
	return nil
}
