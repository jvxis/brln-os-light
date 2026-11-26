package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"time"
)

const treasuryAppID = "stablecoin-treasury"
const treasurySignerPath = "/apps/stablecoin-treasury/signer"
const treasurySocket = "/run/lightningos-treasury/bridge.sock"
const treasuryBundle = "/opt/lightningos/treasury-app"

const treasurySchema = `
CREATE TABLE IF NOT EXISTS stablecoin_treasury_settings (
  id integer PRIMARY KEY CHECK (id = 1),
  installed boolean NOT NULL DEFAULT false,
  enabled boolean NOT NULL DEFAULT false,
  api_key text NOT NULL DEFAULT '',
  wallet text NOT NULL DEFAULT '',
  saved_recipient text NOT NULL DEFAULT ''
);
INSERT INTO stablecoin_treasury_settings (id) VALUES (1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS stablecoin_treasury_orders (
  id text PRIMARY KEY,
  session_hash text NOT NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  state text NOT NULL DEFAULT 'quoted',
  reference text NOT NULL DEFAULT '',
  payload jsonb NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS stablecoin_treasury_single_pending
ON stablecoin_treasury_orders ((true))
WHERE state IN ('deposit_pending','delivery_pending','delivery_issue','refund_pending');
`

type treasuryApp struct{ s *Server }

func (a treasuryApp) Definition() appDefinition {
	return appDefinition{ID: treasuryAppID, Name: "BRLN Reserva", Description: "Convert Lightning sats to native USDC on Solana and back."}
}

func treasuryRuntimeReady() bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false
	}
	socket, err := os.Stat(treasurySocket)
	if err != nil || socket.Mode()&os.ModeSocket == 0 {
		return false
	}
	for _, name := range []string{"signer.html", "signer.js", "signer.css", "solana-worker.cjs"} {
		info, err := os.Stat(treasuryBundle + "/" + name)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func (a treasuryApp) Info(ctx context.Context) (appInfo, error) {
	result := newAppInfo(a.Definition())
	if !treasuryRuntimeReady() || a.s.auth == nil || !a.s.auth.Enabled() {
		result.Available = false
		result.UnavailableMessage = "Requires the BRLN Reserva runtime on Linux amd64 and LightningOS login protection."
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return result, nil
	}
	settings, err := a.s.treasurySettings(ctx)
	if err != nil {
		return result, err
	}
	result.Installed = settings.Installed
	if settings.Installed {
		result.Status = "stopped"
		if settings.Enabled && result.Available {
			result.Status = "running"
		}
	}
	return result, nil
}

func (a treasuryApp) set(ctx context.Context, installed, enabled, requireInstalled bool) error {
	if installed && enabled {
		if !treasuryRuntimeReady() || a.s.auth == nil || !a.s.auth.Enabled() {
			return errors.New("BRLN Reserva runtime or login protection unavailable")
		}
		out, err := treasuryCall(ctx, treasuryWorkerRequest{Action: "health"})
		if err != nil || out.Status != "ready" {
			return errors.New("BRLN Reserva worker unavailable")
		}
	}
	if err := a.s.ensureTreasurySchema(ctx); err != nil {
		return err
	}
	result, err := a.s.db.Exec(ctx, "UPDATE stablecoin_treasury_settings SET installed=$1, enabled=$2 WHERE id=1 AND (NOT $3 OR installed)", installed, enabled, requireInstalled)
	if err == nil && result.RowsAffected() != 1 {
		return errors.New("BRLN Reserva is not installed")
	}
	return err
}

func (a treasuryApp) Install(ctx context.Context) error   { return a.set(ctx, true, true, false) }
func (a treasuryApp) Uninstall(ctx context.Context) error { return a.set(ctx, false, false, false) }
func (a treasuryApp) Start(ctx context.Context) error     { return a.set(ctx, true, true, true) }
func (a treasuryApp) Stop(ctx context.Context) error      { return a.set(ctx, true, false, true) }

type treasurySettings struct {
	Installed, Enabled                        bool
	Key, EncryptedKey, Wallet, SavedRecipient string
}

func (s *Server) ensureTreasurySchema(ctx context.Context) error {
	if s.db == nil {
		return errors.New("Treasury database unavailable")
	}
	s.treasurySchemaMu.Lock()
	defer s.treasurySchemaMu.Unlock()
	if s.treasurySchemaReady {
		return nil
	}
	if _, err := s.db.Exec(ctx, treasurySchema); err != nil {
		return err
	}
	s.treasurySchemaReady = true
	return nil
}

func (s *Server) treasurySettings(ctx context.Context) (treasurySettings, error) {
	var settings treasurySettings
	if err := s.ensureTreasurySchema(ctx); err != nil {
		return settings, err
	}
	err := s.db.QueryRow(ctx, "SELECT installed,enabled,api_key,wallet,saved_recipient FROM stablecoin_treasury_settings WHERE id=1").
		Scan(&settings.Installed, &settings.Enabled, &settings.EncryptedKey, &settings.Wallet, &settings.SavedRecipient)
	if err == nil && settings.EncryptedKey != "" {
		settings.Key, err = openTreasuryCredential(settings.EncryptedKey)
	}
	return settings, err
}

type treasuryWorkerRequest struct {
	Action      string          `json:"action"`
	APIKey      string          `json:"api_key,omitempty"`
	Direction   string          `json:"direction,omitempty"`
	Recipient   string          `json:"recipient,omitempty"`
	Refund      string          `json:"refund,omitempty"`
	Amount      int64           `json:"amount,omitempty"`
	Cluster     string          `json:"cluster,omitempty"`
	BridgeID    string          `json:"bridge_id,omitempty"`
	StatusToken string          `json:"status_token,omitempty"`
	Reference   string          `json:"reference,omitempty"`
	Order       json.RawMessage `json:"order,omitempty"`
	Signed      json.RawMessage `json:"signed,omitempty"`
}

type treasuryBridgeFee struct {
	Amount   int64  `json:"amount"`
	Asset    string `json:"asset"`
	Decimals uint32 `json:"decimals"`
}

type treasuryWorkerResult struct {
	BridgeID    string             `json:"bridge_id"`
	StatusToken string             `json:"status_token"`
	Invoice     string             `json:"invoice"`
	Deposit     string             `json:"deposit"`
	AmountIn    int64              `json:"amount_in"`
	AmountOut   int64              `json:"amount_out"`
	ExpiresAt   int64              `json:"expires_at"`
	Status      string             `json:"status"`
	DepositRef  string             `json:"deposit_ref"`
	DeliveryRef string             `json:"delivery_ref"`
	RefundRef   string             `json:"refund_ref"`
	ProviderFee *treasuryBridgeFee `json:"provider_fee"`
	Order       json.RawMessage    `json:"order"`
	SOL         *int64             `json:"sol_lamports"`
	USDC        *int64             `json:"usdc_micros"`
}

func treasuryCall(ctx context.Context, req treasuryWorkerRequest) (treasuryWorkerResult, error) {
	var result treasuryWorkerResult
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", treasurySocket)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(32 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return result, err
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return result, err
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		return result, err
	}
	decoder := json.NewDecoder(io.LimitReader(conn, 16<<10))
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return treasuryWorkerResult{}, errors.New("invalid Treasury worker response")
	}
	return result, nil
}
