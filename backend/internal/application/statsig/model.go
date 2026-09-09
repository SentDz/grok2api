package statsig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/settings"
)

func CompleteExternal(ctx context.Context, cfg settings.StatsigBuiltinConfig, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"model": cfg.LLMModel, "messages": []map[string]string{{"role": "user", "content": prompt}}, "stream": false, "max_tokens": 8192, "temperature": 0})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.LLMURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("invalid external model URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.LLMKey)
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return "", errors.New("external model request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", HTTPError(response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 {
		return "", errors.New("external model response exceeds limit")
	}
	var value struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &value) != nil || len(value.Choices) == 0 || value.Choices[0].Message.Content == "" {
		return "", errors.New("external model returned no code")
	}
	return value.Choices[0].Message.Content, nil
}
