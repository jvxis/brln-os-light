package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const telegramSentinelToken = "123456789:SUPER_SECRET_TELEGRAM_TOKEN_ABCDEFGHIJKLMNOPQRSTUVWXYZ"

type telegramRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn telegramRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestTelegramTransportErrorsNeverExposeCredentialBearingURL(t *testing.T) {
	client := &http.Client{Transport: telegramRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("transport failed for %s", request.URL.String())
	})}
	tests := []struct {
		name string
		call func() error
	}{
		{name: "setMyCommands", call: func() error {
			return setTelegramBotCommandsWithClient(context.Background(), telegramSentinelToken, client)
		}},
		{name: "getUpdates", call: func() error {
			_, err := fetchTelegramUpdatesWithClient(context.Background(), telegramSentinelToken, 1, client)
			return err
		}},
		{name: "sendMessage", call: func() error {
			return sendTelegramMessageWithClient(context.Background(), telegramSentinelToken, "123", "test", client)
		}},
		{name: "sendDocument", call: func() error {
			return sendTelegramDocumentWithClient(context.Background(), telegramSentinelToken, "123", "scb.backup", []byte("backup"), "test", client)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if err == nil {
				t.Fatal("expected transport failure")
			}
			assertTelegramErrorSecretFree(t, err)
		})
	}
}

func TestTelegramUpstreamBodiesNeverCrossErrorBoundary(t *testing.T) {
	client := &http.Client{Transport: telegramRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader("upstream echoed " + telegramSentinelToken)),
			Header:     make(http.Header),
		}, nil
	})}
	for _, call := range []func() error{
		func() error {
			return setTelegramBotCommandsWithClient(context.Background(), telegramSentinelToken, client)
		},
		func() error {
			_, err := fetchTelegramUpdatesWithClient(context.Background(), telegramSentinelToken, 1, client)
			return err
		},
		func() error {
			return sendTelegramMessageWithClient(context.Background(), telegramSentinelToken, "123", "test", client)
		},
		func() error {
			return sendTelegramDocumentWithClient(context.Background(), telegramSentinelToken, "123", "scb.backup", []byte("backup"), "test", client)
		},
	} {
		err := call()
		if err == nil {
			t.Fatal("expected upstream status failure")
		}
		assertTelegramErrorSecretFree(t, err)
	}
}

func assertTelegramErrorSecretFree(t *testing.T, err error) {
	t.Helper()
	message := err.Error()
	for _, forbidden := range []string{telegramSentinelToken, "api.telegram.org/bot", "SUPER_SECRET"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("telegram secret escaped error boundary: %q", message)
		}
	}
}
