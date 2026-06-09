package server

const (
	depixSettingsID                   = 1
	depixProviderBaseDefault          = "https://pix.br-ln.com"
	depixProviderAPIKeyDefault        = "soberania2026"
	depixDefaultTimezone              = "America/Sao_Paulo"
	depixDefaultMinAmountCents  int64 = 10000
	depixDefaultMaxAmountCents  int64 = 50000
	depixDefaultDailyLimitCents int64 = 300000
	depixDefaultEulenFeeCents   int64 = 99
	depixDefaultBrlnFeeBPS      int64 = 150
)

type depixSettings struct {
	AppInstalled    bool
	AppEnabled      bool
	ProviderBaseURL string
	ProviderAPIKey  string
	DefaultTimezone string
	MinAmountCents  int64
	MaxAmountCents  int64
	DailyLimitCents int64
	EulenFeeCents   int64
	BrlnFeeBPS      int64
}

func depixDefaultSettings() depixSettings {
	return depixSettings{
		AppInstalled:    false,
		AppEnabled:      false,
		ProviderBaseURL: depixProviderBaseDefault,
		ProviderAPIKey:  depixProviderAPIKeyDefault,
		DefaultTimezone: depixDefaultTimezone,
		MinAmountCents:  depixDefaultMinAmountCents,
		MaxAmountCents:  depixDefaultMaxAmountCents,
		DailyLimitCents: depixDefaultDailyLimitCents,
		EulenFeeCents:   depixDefaultEulenFeeCents,
		BrlnFeeBPS:      depixDefaultBrlnFeeBPS,
	}
}
