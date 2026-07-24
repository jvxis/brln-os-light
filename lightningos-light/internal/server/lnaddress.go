package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	lnurlRequestTimeout = 20 * time.Second
	lnurlResponseLimit  = 1 << 20
)

type lnurlPayResponse struct {
	Callback       string `json:"callback"`
	MinSendable    int64  `json:"minSendable"`
	MaxSendable    int64  `json:"maxSendable"`
	Metadata       string `json:"metadata"`
	Tag            string `json:"tag"`
	CommentAllowed int    `json:"commentAllowed"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
}

type lnurlCallbackResponse struct {
	Pr     string `json:"pr"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func isLightningAddress(value string) bool {
	user, domain, err := splitLightningAddress(value)
	return err == nil && user != "" && domain != ""
}

func splitLightningAddress(value string) (string, string, error) {
	if strings.TrimSpace(value) == "" {
		return "", "", errors.New("lightning address required")
	}
	parts := strings.Split(value, "@")
	if len(parts) != 2 {
		return "", "", errors.New("invalid lightning address")
	}
	user := strings.TrimSpace(parts[0])
	domain := strings.TrimSpace(parts[1])
	if user == "" || domain == "" || strings.ContainsAny(user, "/?#") {
		return "", "", errors.New("invalid lightning address")
	}
	parsed, err := url.Parse("https://" + domain)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("invalid lightning address")
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return "", "", errors.New("lightning address host must be public")
	}
	if ip := net.ParseIP(hostname); ip != nil && !isPublicLNURLIP(ip) {
		return "", "", errors.New("lightning address host must be public")
	}
	return user, domain, nil
}

func isPublicLNURLIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	_, carrierNAT, _ := net.ParseCIDR("100.64.0.0/10")
	return carrierNAT == nil || !carrierNAT.Contains(ip)
}

func safeLNURLClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, item := range ips {
			if !isPublicLNURLIP(item.IP) {
				lastErr = errors.New("lnurl host resolved to a non-public address")
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(item.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = errors.New("lnurl host has no public address")
		}
		return nil, lastErr
	}
	return &http.Client{
		Transport: transport,
		Timeout:   lnurlRequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many lnurl redirects")
			}
			if req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") || req.URL.Hostname() == "" {
				return errors.New("lnurl redirect must use https")
			}
			return nil
		},
	}
}

func decodeLNURLJSON(body io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(body, lnurlResponseLimit))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func validateLNURLCallback(raw string) (*url.URL, error) {
	callbackURL, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || callbackURL.Hostname() == "" || !strings.EqualFold(callbackURL.Scheme, "https") || callbackURL.User != nil {
		return nil, errors.New("invalid lnurl callback")
	}
	if ip := net.ParseIP(callbackURL.Hostname()); ip != nil && !isPublicLNURLIP(ip) {
		return nil, errors.New("lnurl callback host must be public")
	}
	return callbackURL, nil
}

func inspectLightningAddress(ctx context.Context, address string) (lnurlPayResponse, error) {
	user, domain, err := splitLightningAddress(address)
	if err != nil {
		return lnurlPayResponse{}, err
	}
	metadataURL := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", domain, url.PathEscape(user))
	metaCtx, cancel := context.WithTimeout(ctx, lnurlRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(metaCtx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return lnurlPayResponse{}, err
	}
	resp, err := safeLNURLClient().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(metaCtx.Err(), context.DeadlineExceeded) {
			return lnurlPayResponse{}, errors.New("lnurl request timed out")
		}
		return lnurlPayResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return lnurlPayResponse{}, fmt.Errorf("lnurlp returned status %d", resp.StatusCode)
	}
	var payResp lnurlPayResponse
	if err := decodeLNURLJSON(resp.Body, &payResp); err != nil {
		return lnurlPayResponse{}, err
	}
	if strings.EqualFold(payResp.Status, "ERROR") {
		if strings.TrimSpace(payResp.Reason) != "" {
			return lnurlPayResponse{}, errors.New(strings.TrimSpace(payResp.Reason))
		}
		return lnurlPayResponse{}, errors.New("lnurlp request failed")
	}
	if !strings.EqualFold(strings.TrimSpace(payResp.Tag), "payRequest") {
		return lnurlPayResponse{}, errors.New("lnurlp response is not a pay request")
	}
	if _, err := validateLNURLCallback(payResp.Callback); err != nil {
		return lnurlPayResponse{}, err
	}
	if err := validateLNURLPayDescriptor(payResp); err != nil {
		return lnurlPayResponse{}, err
	}
	return payResp, nil
}

func validateLNURLPayDescriptor(payResp lnurlPayResponse) error {
	if payResp.MinSendable <= 0 {
		return errors.New("lnurlp response has an invalid minimum amount")
	}
	if payResp.MaxSendable <= 0 {
		return errors.New("lnurlp response has an invalid maximum amount")
	}
	if payResp.MinSendable > payResp.MaxSendable {
		return errors.New("lnurlp response has an invalid payment range")
	}
	return nil
}

func resolveLightningAddress(ctx context.Context, address string, amountSat int64, comment string) (string, error) {
	if amountSat <= 0 {
		return "", errors.New("amount must be positive")
	}
	payResp, err := inspectLightningAddress(ctx, address)
	if err != nil {
		return "", err
	}
	if err := validateLNURLPayParameters(payResp, amountSat, comment); err != nil {
		return "", err
	}
	amountMsat := amountSat * 1000
	callbackURL, err := validateLNURLCallback(payResp.Callback)
	if err != nil {
		return "", err
	}
	q := callbackURL.Query()
	q.Set("amount", strconv.FormatInt(amountMsat, 10))
	if comment != "" {
		q.Set("comment", comment)
	}
	callbackURL.RawQuery = q.Encode()
	cbCtx, cancel := context.WithTimeout(ctx, lnurlRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cbCtx, http.MethodGet, callbackURL.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := safeLNURLClient().Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(cbCtx.Err(), context.DeadlineExceeded) {
			return "", errors.New("lnurl request timed out")
		}
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("lnurl callback returned status %d", resp.StatusCode)
	}
	var cb lnurlCallbackResponse
	if err := decodeLNURLJSON(resp.Body, &cb); err != nil {
		return "", err
	}
	if strings.EqualFold(cb.Status, "ERROR") {
		if strings.TrimSpace(cb.Reason) != "" {
			return "", errors.New(strings.TrimSpace(cb.Reason))
		}
		return "", errors.New("lnurl callback failed")
	}
	if strings.TrimSpace(cb.Pr) == "" {
		return "", errors.New("payment request missing from callback")
	}
	return strings.TrimSpace(cb.Pr), nil
}

func validateLNURLPayParameters(payResp lnurlPayResponse, amountSat int64, comment string) error {
	if amountSat <= 0 {
		return errors.New("amount must be positive")
	}
	if amountSat > math.MaxInt64/1000 {
		return errors.New("amount is too large")
	}
	amountMsat := amountSat * 1000
	if (payResp.MinSendable > 0 && amountMsat < payResp.MinSendable) || (payResp.MaxSendable > 0 && amountMsat > payResp.MaxSendable) {
		minSat := int64(0)
		maxSat := int64(0)
		if payResp.MinSendable > 0 {
			minSat = (payResp.MinSendable + 999) / 1000
		}
		if payResp.MaxSendable > 0 {
			maxSat = payResp.MaxSendable / 1000
		}
		if minSat > 0 && maxSat > 0 {
			return fmt.Errorf("amount out of range. Minimum is %d sats; maximum is %d sats", minSat, maxSat)
		}
		if minSat > 0 {
			return fmt.Errorf("amount too small. Minimum is %d sats", minSat)
		}
		if maxSat > 0 {
			return fmt.Errorf("amount too large. Maximum is %d sats", maxSat)
		}
	}
	if comment != "" {
		if payResp.CommentAllowed <= 0 {
			return errors.New("comments not allowed for this address")
		}
		if len(comment) > payResp.CommentAllowed {
			return fmt.Errorf("comment too long (max %d chars)", payResp.CommentAllowed)
		}
	}
	return nil
}
