package settings

import settingsdomain "github.com/chenyme/grok2api/backend/internal/domain/settings"

func statsigBuiltinPointer(value settingsdomain.StatsigBuiltinConfig) *settingsdomain.StatsigBuiltinConfig {
	return &value
}

func publicStatsigBuiltin(value settingsdomain.StatsigBuiltinConfig) *settingsdomain.StatsigBuiltinConfig {
	value.LLMKeyConfigured = value.LLMKey != ""
	value.LLMKey = ""
	value.ClearLLMKey = false
	return &value
}

// StatsigRuntime is for application wiring only; HTTP uses the redacted snapshot.
func (s *Service) StatsigRuntime() (string, settingsdomain.StatsigBuiltinConfig) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Provider.Web.StatsigMode, s.cfg.Provider.Web.StatsigBuiltin
}
