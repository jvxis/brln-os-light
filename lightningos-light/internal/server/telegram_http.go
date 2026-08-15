package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type telegramHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func doTelegramRequest(client telegramHTTPDoer, request *http.Request, operation string) (*http.Response, error) {
	response, err := client.Do(request)
	if err == nil {
		return response, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(request.Context().Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("telegram %s request timed out", operation)
	}
	return nil, fmt.Errorf("telegram %s request failed", operation)
}

func telegramAPIStatusError(operation string, statusCode int) error {
	return fmt.Errorf("telegram %s api returned status %d", operation, statusCode)
}

func telegramRequestBuildError(operation string) error {
	return fmt.Errorf("telegram %s request could not be created", operation)
}
