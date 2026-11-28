package app

import (
	"os"
	"path/filepath"
	"testing"

	"codie-video-player/internal/codieout"
)

func TestDevicesListLifecycle(t *testing.T) {
	dir := t.TempDir()
	// devicesPath points at the run's own folder; a test wants its own.
	old := devicesPath
	devicesPath = func() string { return filepath.Join(dir, "devices.json") }
	codiePath = func() string { return filepath.Join(dir, "codie.json") }
	defer func() { devicesPath = old; codiePath = func() string { return filepath.Join(userDataDir(""), "codie.json") } }()

	a := &app{settings: &settings{}}
	a.devices = append(a.devices,
		savedDevice{Creds: codieout.Creds{Addr: "10.0.0.2:8081", Secret: "a", UserID: "u", DeviceID: "dev-a", Name: "Codie-b2c37c7c…and-on-and-on"}},
		savedDevice{Creds: codieout.Creds{Addr: "10.0.0.3:8081", Secret: "b", UserID: "u", DeviceID: "dev-b"}},
	)
	a.devices[1].Online = true
	a.saveDevices()

	b := &app{settings: &settings{}}
	b.loadDevices()
	if len(b.devices) != 2 || b.devices[0].Creds.DeviceID != "dev-a" {
		t.Fatalf("loaded = %+v", b.devices)
	}
	// deviceAdd makes the newcomer active and keeps the rest.
	creds := &codieout.Creds{Addr: "10.0.0.9:8081", Secret: "c", UserID: "u", DeviceID: "dev-c"}
	a.deviceAdd(creds)
	if a.activeDeviceIndex() != 2 || len(a.devices) != 3 {
		t.Fatalf("after add: active=%d devices=%d", a.activeDeviceIndex(), len(a.devices))
	}
	if a.codie == nil || a.codie.Creds.DeviceID != "dev-c" {
		t.Fatalf("active client = %+v", a.codie)
	}
	// Adding the same device again replaces, not duplicates.
	a.deviceAdd(creds)
	if len(a.devices) != 3 || a.activeDeviceIndex() != 2 {
		t.Fatalf("after re-add: active=%d devices=%d", a.activeDeviceIndex(), len(a.devices))
	}
	// Forgetting the active one leaves the others, and one of them active.
	a.deviceForget()
	if len(a.devices) != 2 || a.codie == nil {
		t.Fatalf("after forget: devices=%d codie=%v", len(a.devices), a.codie != nil)
	}
	// The label is codie- plus the address's host: no serial numbers.
	if got := deviceLabel(a.devices[0]); got != "codie-10.0.0.2" {
		t.Errorf("label = %q, want codie-10.0.0.2", got)
	}
	if got := deviceLabel(savedDevice{Creds: codieout.Creds{Addr: "codie.local:8081"}}); got != "codie-codie.local" {
		t.Errorf("label = %q, want codie-codie.local", got)
	}
	_ = os.Remove("")
}
