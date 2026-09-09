package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/audit"
	"github.com/chenyme/grok2api/backend/internal/domain/clientkey"
	"github.com/google/uuid"
)

// CompleteStatsigRepair uses the normal Build scheduler and billing/audit path.
// The trusted internal identity is restricted to Build, preventing Web recursion.
func (s *Service) CompleteStatsigRepair(ctx context.Context, model, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	publicModel, ok := qualityProbeBuildPublicModel(model)
	if !ok {
		return "", errors.New("Statsig repair requires a Build model")
	}
	key := s.accountTestKey
	key.ProviderScope = clientkey.ProviderScopeBuild
	body, _ := json.Marshal(map[string]any{"model": publicModel, "messages": []map[string]string{{"role": "user", "content": prompt}}, "max_tokens": 8192, "stream": false})
	result, err := s.CreateChatCompletion(ctx, Input{RequestID: "statsig_" + uuid.NewString(), ClientKey: key, PublicModel: publicModel, Body: body, Operation: audit.OperationChat})
	if err != nil {
		return "", errors.New("Build repair request failed; check available Build accounts and model routing")
	}
	defer result.Body.Close()
	usage := Usage{}
	responseID := ""
	errorCode := "statsig_repair_failed"
	defer func() {
		if result.Finalize != nil {
			result.Finalize(usage, responseID, errorCode)
		}
	}()
	if result.StatusCode != 200 {
		return "", fmt.Errorf("Build repair model returned HTTP %d", result.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(result.Body, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 {
		return "", errors.New("Build repair response exceeds limit")
	}
	var value struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int64 `json:"prompt_tokens"`
			Completion int64 `json:"completion_tokens"`
			Total      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &value) != nil || len(value.Choices) == 0 {
		return "", errors.New("invalid Build repair response")
	}
	usage = Usage{Reported: true, InputTokens: value.Usage.Prompt, OutputTokens: value.Usage.Completion, TotalTokens: value.Usage.Total, OutputObserved: value.Choices[0].Message.Content != ""}
	responseID = value.ID
	errorCode = ""
	return value.Choices[0].Message.Content, nil
}
