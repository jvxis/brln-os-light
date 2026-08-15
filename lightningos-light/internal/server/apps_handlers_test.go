package server

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

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
