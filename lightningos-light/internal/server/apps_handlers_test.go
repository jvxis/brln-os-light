package server

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateAppUninstallConfirmation(t *testing.T) {
	for _, test := range []struct {
		name string
		req  appUninstallRequest
		ok   bool
	}{
		{name: "confirmed matching app", req: appUninstallRequest{Confirm: true, AppID: "bitcoincore"}, ok: true},
		{name: "missing confirmation", req: appUninstallRequest{AppID: "bitcoincore"}},
		{name: "missing app id", req: appUninstallRequest{Confirm: true}},
		{name: "different app id", req: appUninstallRequest{Confirm: true, AppID: "elements"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateAppUninstallConfirmation("bitcoincore", test.req)
			if test.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !test.ok && !errors.Is(err, errAppUninstallConfirmationRequired) {
				t.Fatalf("error=%v, want confirmation required", err)
			}
		})
	}
}

func TestValidateBitcoinCoreUninstallSource(t *testing.T) {
	remote := bitcoinRPCConfig{Host: "bitcoin.example:8332", User: "rpc-user", Pass: "rpc-pass"}
	if err := validateBitcoinCoreUninstallSource("remote", remote); err != nil {
		t.Fatalf("verified remote source rejected: %v", err)
	}
	if err := validateBitcoinCoreUninstallSource("local", remote); !errors.Is(err, errBitcoinCoreSourceSwitchRequired) {
		t.Fatalf("local source error=%v, want source switch required", err)
	}
	for name, cfg := range map[string]bitcoinRPCConfig{
		"host": {User: "rpc-user", Pass: "rpc-pass"},
		"user": {Host: "bitcoin.example:8332", Pass: "rpc-pass"},
		"pass": {Host: "bitcoin.example:8332", User: "rpc-user"},
	} {
		t.Run("missing "+name, func(t *testing.T) {
			if err := validateBitcoinCoreUninstallSource("remote", cfg); !errors.Is(err, errBitcoinCoreRemoteCredentials) {
				t.Fatalf("error=%v, want remote credentials error", err)
			}
		})
	}
}

type concurrentInfoTestApp struct {
	id      string
	current *atomic.Int32
	maximum *atomic.Int32
}

func (app concurrentInfoTestApp) Definition() appDefinition {
	return appDefinition{ID: app.id, Name: app.id}
}

func (app concurrentInfoTestApp) Info(ctx context.Context) (appInfo, error) {
	current := app.current.Add(1)
	defer app.current.Add(-1)
	for {
		maximum := app.maximum.Load()
		if current <= maximum || app.maximum.CompareAndSwap(maximum, current) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return appInfo{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	return newAppInfo(app.Definition()), nil
}

func (concurrentInfoTestApp) Install(context.Context) error   { return nil }
func (concurrentInfoTestApp) Uninstall(context.Context) error { return nil }
func (concurrentInfoTestApp) Start(context.Context) error     { return nil }
func (concurrentInfoTestApp) Stop(context.Context) error      { return nil }

func TestCollectAppInfosPreservesOrderAndBoundsConcurrency(t *testing.T) {
	var current atomic.Int32
	var maximum atomic.Int32
	apps := make([]appHandler, 12)
	for index := range apps {
		apps[index] = concurrentInfoTestApp{
			id:      fmt.Sprintf("app-%02d", index),
			current: &current,
			maximum: &maximum,
		}
	}
	infos := collectAppInfos(context.Background(), apps, 4)
	if maximum.Load() < 2 || maximum.Load() > 4 {
		t.Fatalf("maximum concurrent app inspections=%d, want 2..4", maximum.Load())
	}
	for index, info := range infos {
		if want := fmt.Sprintf("app-%02d", index); info.ID != want {
			t.Fatalf("result %d id=%q want=%q", index, info.ID, want)
		}
	}
}

func TestCloneAppInfosDoesNotAliasMutableFields(t *testing.T) {
	operation := appOperationInfo{Action: "start"}
	original := []appInfo{{ID: "test", SecurityNotices: []string{"notice"}, Operation: &operation}}
	cloned := cloneAppInfos(original)
	cloned[0].SecurityNotices[0] = "changed"
	cloned[0].Operation.Action = "stop"
	if original[0].SecurityNotices[0] != "notice" || original[0].Operation.Action != "start" {
		t.Fatal("app list clone aliases mutable cache fields")
	}
}
