package app

import (
	"context"
	"errors"

	"github.com/chenyme/grok2api/backend/internal/application/gateway"
	settingsapp "github.com/chenyme/grok2api/backend/internal/application/settings"
	statsigapp "github.com/chenyme/grok2api/backend/internal/application/statsig"
	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/settings"
	"github.com/chenyme/grok2api/backend/internal/domain/statsig"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	webprovider "github.com/chenyme/grok2api/backend/internal/infra/provider/web"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

func newBuiltinStatsig(ctx context.Context, db *relational.Database, cipher *security.Cipher, lock repository.DistributedLock, settingsService *settingsapp.Service, accounts repository.AccountRepository, web *webprovider.Adapter, gatewayService *gateway.Service) (*statsigapp.Service, error) {
	selectAccount := func(ctx context.Context, id uint64) (account.Credential, error) {
		if id != 0 {
			value, err := accounts.Get(ctx, id)
			if err != nil || value.Provider != account.ProviderWeb || !value.Enabled || value.AuthStatus != account.AuthStatusActive {
				return account.Credential{}, errors.New("capture account must be an enabled active Web account")
			}
			return value, nil
		}
		values, err := accounts.ListEnabled(ctx, account.ProviderWeb)
		if err != nil {
			return account.Credential{}, errors.New("cannot read Web accounts")
		}
		for _, value := range values {
			if value.AuthStatus == account.AuthStatusActive {
				return value, nil
			}
		}
		return account.Credential{}, errors.New("no active Web account available for signature capture")
	}
	return statsigapp.New(ctx, relational.NewStatsigRepository(db, cipher), lock, settingsService.StatsigRuntime,
		func(ctx context.Context, id uint64) (statsig.Capture, error) {
			value, err := selectAccount(ctx, id)
			if err != nil {
				return statsig.Capture{}, err
			}
			return web.CaptureStatsig(ctx, value)
		},
		func(ctx context.Context, id uint64, sample statsig.Sample) error {
			value, err := selectAccount(ctx, id)
			if err != nil {
				return err
			}
			return web.VerifyStatsig(ctx, value, sample)
		},
		func(ctx context.Context, cfg settings.StatsigBuiltinConfig, prompt string) (string, error) {
			if cfg.LLMProvider == "external" {
				return statsigapp.CompleteExternal(ctx, cfg, prompt)
			}
			return gatewayService.CompleteStatsigRepair(ctx, cfg.LLMModel, prompt)
		}, webprovider.SignBuiltinSample)
}
