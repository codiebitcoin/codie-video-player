package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/i18n"
)

// The codie devices paired over time: devices.json beside codie.json, the
// active one of them what a.codie points at. One device's credential lives in
// codie.json still, so an older app reads the same pairing; the list is what
// lets the listener keep several and pick.

// devicesPath is where the paired devices' list is kept. A var so a test can
// point it at a folder of its own.
var devicesPath = func() string {
	return filepath.Join(userDataDir(""), "devices.json")
}

// loadDevices reads the list back, folding in the device of codie.json when
// the list doesn't know it yet (an older run's pairing, or a hand-edited one).
func (a *app) loadDevices() {
	b, err := os.ReadFile(devicesPath())
	if err == nil {
		json.Unmarshal(b, &a.devices)
	}
	if a.codie == nil {
		return
	}
	id := a.codie.Creds.DeviceID
	for i := range a.devices {
		if a.devices[i].Creds.DeviceID == id || (id == "" && a.devices[i].Creds.Addr == a.codie.Creds.Addr) {
			return
		}
	}
	a.devices = append(a.devices, savedDevice{Creds: *a.codie.Creds, Online: true})
	a.saveDevices()
}

// saveDevices keeps the list.
func (a *app) saveDevices() {
	b, _ := json.MarshalIndent(a.devices, "", "  ")
	os.MkdirAll(filepath.Dir(devicesPath()), 0o700)
	os.WriteFile(devicesPath(), b, 0o600)
}

// deviceAdd takes a freshly paired device into the list and makes it the
// active one.
func (a *app) deviceAdd(creds *codieout.Creds) {
	for i := range a.devices {
		if a.devices[i].Creds.DeviceID == creds.DeviceID {
			a.devices[i].Creds = *creds
			a.devices[i].Online = true
			a.devices[i].Err = ""
			a.setActiveDevice(i)
			return
		}
	}
	a.devices = append(a.devices, savedDevice{Creds: *creds, Online: true})
	a.setActiveDevice(len(a.devices) - 1)
}

// deviceUse makes the device at i the active one, keeping the rest. What was
// playing on the old device stops; the output stays where it was.
func (a *app) deviceUse(i int) {
	if i < 0 || i >= len(a.devices) {
		return
	}
	a.setActiveDevice(i)
	if a.win != nil {
		a.win.Invalidate()
	}
}

// setActiveDevice points a.codie at the device at i and settles the rest of
// the app around it: the slot, the tools, the probes.
func (a *app) setActiveDevice(i int) {
	a.stopPlayer()
	creds := a.devices[i].Creds
	a.codie = codieout.New(&creds)
	a.saveCodie(&creds)
	a.deviceNote = ""
	a.probe, a.mediaProbe = nil, nil
	a.syncDeviceSlot()
	a.toolsRefresh()
	a.devicePollStart()
	a.deviceProbe()
}

// deviceLabel is what a device is called: its own name, or its address.
func deviceLabel(d savedDevice) string {
	if d.Creds.Name != "" {
		return d.Creds.Name
	}
	return d.Creds.Addr
}

// deviceState asks every paired device how it is, one to a goroutine, and
// keeps the answers: the header of the device window reads them, and the
// stage shows the active one's.
func (a *app) deviceStatePoll() {
	type answer struct {
		i      int
		online bool
		err    string
		card   bool
	}
	n := len(a.devices)
	if n == 0 {
		return
	}
	ch := make(chan answer, n)
	clients := make([]*codieout.Client, n)
	for i := range a.devices {
		clients[i] = codieout.New(&a.devices[i].Creds)
		go func(i int) {
			c := clients[i]
			if err := codieout.Dial(a.devices[i].Creds.Addr); err != nil {
				ch <- answer{i: i, online: false, err: err.Error()}
				return
			}
			online, card := true, false
			if st, err := c.Status(); err == nil {
				card = st.CardLoaded
			}
			ch <- answer{i: i, online: online, card: card}
		}(i)
	}
	notes := make([]string, n)
	states := make([]savedDevice, n)
	copy(states, a.devices)
	for range n {
		ans := <-ch
		states[ans.i].Online = ans.online
		states[ans.i].Err = ans.err
		if ans.online {
			note := i18n.T("Online")
			if ans.card {
				note += " · " + i18n.T("TF card ready")
			}
			notes[ans.i] = note
		} else {
			notes[ans.i] = i18n.T("Disconnected") + " — " + ans.err
		}
	}
	a.post(func() {
		changed := false
		for i := range states {
			if a.devices[i].Online != states[i].Online {
				changed = true
			}
			a.devices[i].Online = states[i].Online
			a.devices[i].Err = states[i].Err
			// The active device's line keeps its fuller note.
			if a.codie != nil && a.devices[i].Creds.DeviceID == a.codie.Creds.DeviceID {
				a.deviceNote = notes[i] + " · " + i18n.T("Address") + " " + a.devices[i].Creds.Addr
			}
		}
		if changed {
			a.saveDevices()
		}
		if a.deviceWin != nil {
			a.deviceWin.Invalidate()
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	})
}

// devicePollStart keeps the devices asked, a round to a few seconds, while
// anything cares: the device window shows each one's state, and the stage
// says when the active one went away. One poller per run.
func (a *app) devicePollStart() {
	if a.devicePoll {
		return
	}
	a.devicePoll = true
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for range t.C {
			a.post(func() {
				if a.codie == nil && len(a.devices) == 0 {
					return
				}
				go a.deviceStatePoll()
			})
		}
	}()
}

// activeDeviceIndex is where the active device sits in the list, or -1.
func (a *app) activeDeviceIndex() int {
	if a.codie == nil {
		return -1
	}
	for i := range a.devices {
		if a.devices[i].Creds.DeviceID == a.codie.Creds.DeviceID {
			return i
		}
	}
	return -1
}
