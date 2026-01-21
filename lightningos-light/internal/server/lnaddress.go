package server

import (
  "context"
  "encoding/json"
  "errors"
  "fmt"
  "net/http"
  "net/url"
  "strconv"
  "strings"
  "time"
)

const lnurlRequestTimeout = 10 * time.Second

type lnurlPayResponse struct {
  Callback string `json:"callback"`
  MinSendable int64 `json:"minSendable"`
  MaxSendable int64 `json:"maxSendable"`
  Metadata string `json:"metadata"`
  Tag string `json:"tag"`
  CommentAllowed int `json:"commentAllowed"`
  Status string `json:"status"`
  Reason string `json:"reason"`
}

type lnurlCallbackResponse struct {
  Pr string `json:"pr"`
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
  if user == "" || domain == "" {
    return "", "", errors.New("invalid lightning address")
  }
  return user, domain, nil
}

func resolveLightningAddress(ctx context.Context, address string, amountSat int64, comment string) (string, error) {
  if amountSat <= 0 {
    return "", errors.New("amount must be positive")
  }
  user, domain, err := splitLightningAddress(address)
  if err != nil {
    return "", err
  }

  reqCtx, cancel := context.WithTimeout(ctx, lnurlRequestTimeout)
  defer cancel()

  metadataURL := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", domain, url.PathEscape(user))
  metaReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, metadataURL, nil)
  if err != nil {
    return "", err
  }
  metaResp, err := http.DefaultClient.Do(metaReq)
  if err != nil {
    return "", err
  }
  defer metaResp.Body.Close()
  if metaResp.StatusCode != http.StatusOK {
    return "", fmt.Errorf("lnurlp returned status %d", metaResp.StatusCode)
  }

  var payResp lnurlPayResponse
  if err := json.NewDecoder(metaResp.Body).Decode(&payResp); err != nil {
    return "", err
  }
  if strings.EqualFold(payResp.Status, "ERROR") {
    if payResp.Reason != "" {
      return "", errors.New(payResp.Reason)
    }
    return "", errors.New("lnurlp request failed")
  }
  if payResp.Callback == "" {
    return "", errors.New("lnurlp callback missing")
  }

  amountMsat := amountSat * 1000
  if payResp.MinSendable > 0 && amountMsat < payResp.MinSendable {
    return "", fmt.Errorf("amount below minimum (%d sats)", payResp.MinSendable/1000)
  }
  if payResp.MaxSendable > 0 && amountMsat > payResp.MaxSendable {
    return "", fmt.Errorf("amount above maximum (%d sats)", payResp.MaxSendable/1000)
  }

  callbackURL, err := url.Parse(payResp.Callback)
  if err != nil {
    return "", fmt.Errorf("invalid lnurl callback: %w", err)
  }

  q := callbackURL.Query()
  q.Set("amount", strconv.FormatInt(amountMsat, 10))
  if comment != "" {
    if payResp.CommentAllowed <= 0 {
      return "", errors.New("comments not allowed for this address")
    }
    if len(comment) > payResp.CommentAllowed {
      return "", fmt.Errorf("comment too long (max %d chars)", payResp.CommentAllowed)
    }
    q.Set("comment", comment)
  }
  callbackURL.RawQuery = q.Encode()

  cbReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, callbackURL.String(), nil)
  if err != nil {
    return "", err
  }
  cbResp, err := http.DefaultClient.Do(cbReq)
  if err != nil {
    return "", err
  }
  defer cbResp.Body.Close()
  if cbResp.StatusCode != http.StatusOK {
    return "", fmt.Errorf("lnurl callback returned status %d", cbResp.StatusCode)
  }

  var cb lnurlCallbackResponse
  if err := json.NewDecoder(cbResp.Body).Decode(&cb); err != nil {
    return "", err
  }
  if strings.EqualFold(cb.Status, "ERROR") {
    if cb.Reason != "" {
      return "", errors.New(cb.Reason)
    }
    return "", errors.New("lnurl callback failed")
  }
  if cb.Pr == "" {
    return "", errors.New("payment request missing from callback")
  }

  return cb.Pr, nil
}
