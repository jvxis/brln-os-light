//go:build linux

package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type torReloadRunner struct {
	calls []string
	fail  bool
	pid   string
}

func (r *torReloadRunner) Run(_ context.Context, binary string, args ...string) (string, error) {
	r.calls = append(r.calls, binary+" "+strings.Join(args, " "))
	if args[0] == "show" {
		return r.pid, nil
	}
	if r.fail {
		return "", errors.New("reload failed")
	}
	return "", nil
}
func TestTorLNDRESTReloadDoesNotRestartOrMaskFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		r := &torReloadRunner{pid: "123", fail: fail}
		err := NewNativeTorLNDRESTManager(r).reloadTor(context.Background())
		if (err != nil) != fail {
			t.Fatalf("failure=%v err=%v", fail, err)
		}
		if len(r.calls) != 2 || !strings.HasSuffix(r.calls[1], "reload tor@default.service") {
			t.Fatal(r.calls)
		}
	}
	r := &torReloadRunner{pid: "0"}
	if NewNativeTorLNDRESTManager(r).reloadTor(context.Background()) == nil {
		t.Fatal("oneshot/no daemon accepted")
	}
	for _, call := range r.calls {
		if strings.Contains(call, "reload") {
			t.Fatal(call)
		}
	}
}
func TestTorLNDRESTRejectsParentSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if validateOnionPath(filepath.Join(link, "missing")) == nil {
		t.Fatal("parent link accepted")
	}
	if err := validateOnionPath(filepath.Join(real, "missing")); err != nil {
		t.Fatal(err)
	}
}
