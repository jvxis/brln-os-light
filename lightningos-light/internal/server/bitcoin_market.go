package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	bitcoinMarketCacheTTL   = 5 * time.Minute
	bitcoinMarketStaleTTL   = 30 * time.Minute
	bitcoinMarketFetchDelay = 5 * time.Second
)

type bitcoinMarketPrice struct {
	Currency  string  `json:"currency"`
	Value     float64 `json:"value"`
	Change24H float64 `json:"change_24h,omitempty"`
}

type bitcoinMarketStatus struct {
	Prices    []bitcoinMarketPrice      `json:"prices,omitempty"`
	Fees      *mempoolFeeRecommendation `json:"fees,omitempty"`
	UpdatedAt string                    `json:"updated_at"`
	Partial   bool                      `json:"partial,omitempty"`
	Stale     bool                      `json:"stale,omitempty"`
	PriceErr  string                    `json:"price_error,omitempty"`
	FeeErr    string                    `json:"fee_error,omitempty"`
}

type cachedBitcoinMarketStatus struct {
	value      bitcoinMarketStatus
	expiresAt  time.Time
	staleUntil time.Time
}

type coingeckoSimplePriceResponse struct {
	Bitcoin map[string]float64 `json:"bitcoin"`
}

func (s *Server) handleBitcoinMarket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), bitcoinMarketFetchDelay)
	defer cancel()

	status := s.bitcoinMarketStatusCached(ctx)
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) bitcoinMarketStatusCached(ctx context.Context) bitcoinMarketStatus {
	now := time.Now()
	if s != nil {
		s.bitcoinMarketMu.Lock()
		if now.Before(s.bitcoinMarketCache.expiresAt) {
			value := s.bitcoinMarketCache.value
			s.bitcoinMarketMu.Unlock()
			return value
		}
		s.bitcoinMarketMu.Unlock()
	}

	status := fetchBitcoinMarketStatus(ctx, now)
	if len(status.Prices) == 0 && status.Fees == nil && s != nil {
		s.bitcoinMarketMu.Lock()
		if now.Before(s.bitcoinMarketCache.staleUntil) && (len(s.bitcoinMarketCache.value.Prices) > 0 || s.bitcoinMarketCache.value.Fees != nil) {
			value := s.bitcoinMarketCache.value
			value.Stale = true
			value.Partial = true
			value.PriceErr = status.PriceErr
			value.FeeErr = status.FeeErr
			s.bitcoinMarketMu.Unlock()
			return value
		}
		s.bitcoinMarketMu.Unlock()
	}

	if len(status.Prices) > 0 || status.Fees != nil {
		status.Partial = status.PriceErr != "" || status.FeeErr != ""
		if s != nil {
			s.bitcoinMarketMu.Lock()
			s.bitcoinMarketCache = cachedBitcoinMarketStatus{
				value:      status,
				expiresAt:  now.Add(bitcoinMarketCacheTTL),
				staleUntil: now.Add(bitcoinMarketStaleTTL),
			}
			s.bitcoinMarketMu.Unlock()
		}
	}
	return status
}

func fetchBitcoinMarketStatus(ctx context.Context, now time.Time) bitcoinMarketStatus {
	var (
		wg       sync.WaitGroup
		prices   []bitcoinMarketPrice
		priceErr error
		fees     mempoolFeeRecommendation
		feeErr   error
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		prices, priceErr = fetchBitcoinMarketPrices(ctx)
	}()
	go func() {
		defer wg.Done()
		feeErr = fetchMempoolJSON(ctx, "https://mempool.space/api/v1/fees/recommended", &fees)
	}()
	wg.Wait()

	status := bitcoinMarketStatus{
		Prices:    prices,
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}
	if feeErr == nil {
		status.Fees = &fees
	} else {
		status.FeeErr = feeErr.Error()
	}
	if priceErr != nil {
		status.PriceErr = priceErr.Error()
	}
	status.Partial = status.PriceErr != "" || status.FeeErr != ""
	return status
}

func fetchBitcoinMarketPrices(ctx context.Context) ([]bitcoinMarketPrice, error) {
	url := "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd,brl,eur&include_24hr_change=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "lightningos-light")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("coingecko api error: %s", msg)
	}

	var payload coingeckoSimplePriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return coinGeckoPricesToMarketPrices(payload)
}

func coinGeckoPricesToMarketPrices(payload coingeckoSimplePriceResponse) ([]bitcoinMarketPrice, error) {
	if len(payload.Bitcoin) == 0 {
		return nil, fmt.Errorf("bitcoin price missing")
	}
	currencies := []string{"usd", "brl", "eur"}
	prices := make([]bitcoinMarketPrice, 0, len(currencies))
	for _, currency := range currencies {
		value, ok := payload.Bitcoin[currency]
		if !ok || value <= 0 {
			continue
		}
		prices = append(prices, bitcoinMarketPrice{
			Currency:  strings.ToUpper(currency),
			Value:     value,
			Change24H: payload.Bitcoin[currency+"_24h_change"],
		})
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("bitcoin price missing")
	}
	return prices, nil
}
