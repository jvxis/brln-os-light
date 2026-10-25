package server

import (
	"reflect"
	"testing"
)

func TestAutofeeSeedConfigUsesGoDefaults(t *testing.T) {
	svc := &AutofeeService{}
	def := svc.defaultConfig()
	seed := autofeeSeedConfig(def)
	if !seed.NativeSeedEnabled {
		t.Fatal("a new install starts with the native seed on")
	}
	p := autofeeProfiles[autofeeProfileDefault]
	// The SQL column defaults had drifted (cooldown_down 14400, cooldown_up
	// 10800); the seed must carry the profile's values instead.
	if seed.CooldownDownSec != p.CooldownDownSec || seed.CooldownUpSec != p.CooldownUpSec || seed.RunIntervalSec != p.RunIntervalSec {
		t.Fatalf("seed cooldowns/interval = %d/%d/%d, want the %s profile's %d/%d/%d",
			seed.CooldownUpSec, seed.CooldownDownSec, seed.RunIntervalSec, p.Name, p.CooldownUpSec, p.CooldownDownSec, p.RunIntervalSec)
	}
	seed.NativeSeedEnabled = def.NativeSeedEnabled
	if !reflect.DeepEqual(seed, def) {
		t.Fatalf("apart from the native seed the seed must equal the Go defaults:\n seed=%+v\n  def=%+v", seed, def)
	}
}
