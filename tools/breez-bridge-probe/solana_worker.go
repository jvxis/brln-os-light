package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func solanaWorker(req bridgeCommand, output io.Writer) error {
	if req.APIKey != "" {
		return errors.New("unexpected credential")
	}
	executable, err := os.Executable()
	if err != nil {
		return errors.New("worker path unavailable")
	}
	input, err := json.Marshal(req)
	if err != nil {
		return errors.New("invalid Solana request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/node", filepath.Join(filepath.Dir(executable), "solana-worker.cjs"))
	cmd.Stdin = bytes.NewReader(input)
	// The bundled helper is trusted and returns one bounded result. Its diagnostics
	// stay private and never attach to Manager errors.
	data, err := cmd.Output()
	if err != nil || len(data) > 16<<10 || !json.Valid(data) {
		return errors.New("Solana operation unavailable")
	}
	_, err = output.Write(data)
	return err
}
