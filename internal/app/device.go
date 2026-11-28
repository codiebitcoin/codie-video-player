package app

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/i18n"
)

// The codie device: pairing over its signed local API, and the probe that
// asks it, endpoint by endpoint, what it can do — the media session's
// capabilities first, which is where video would say itself if the firmware
// has it.

// pairState is where the pairing dialog is.
type pairState struct {
	addr      string // the device's address, as typed
	challenge string // what the device gave for the code it now shows
	code      string // the 6 digits typed back
	busy      string // what's on its way
	err       string
	// found are the devices a scan of the network turned up, host:8081, for
	// the list the listener picks from. scanning says one is on its way.
	found    []string
	scanning bool
}

// codiePath is where the paired device's credential is kept — with the rest
// of the app's own, so a run that only exists to be looked at keeps its
// pairing to itself too. A var so a test can point it at a folder of its own.
var codiePath = func() string {
	return filepath.Join(userDataDir(""), "codie.json")
}

// loadCodie brings back the devices paired before: the list of them, and the
// one that was active last. A list that has nothing yet takes what codie.json
// holds, so an older run's pairing stays.
func (a *app) loadCodie() {
	a.loadDevices()
	if len(a.devices) == 0 {
		b, err := os.ReadFile(codiePath())
		if err != nil {
			return
		}
		var creds codieout.Creds
		if json.Unmarshal(b, &creds) != nil || creds.Addr == "" || creds.Secret == "" {
			return
		}
		a.devices = append(a.devices, savedDevice{Creds: creds, Online: true})
		a.saveDevices()
	}
	if a.activeDeviceIndex() < 0 {
		a.setActiveDevice(0)
		return
	}
	i := a.activeDeviceIndex()
	a.codie = codieout.New(&a.devices[i].Creds)
	a.syncDeviceSlot()
	a.toolsRefresh()
	a.devicePollStart()
	go a.deviceStatePoll()
}

// saveCodie keeps the credential, or forgets it with the device.
func (a *app) saveCodie(creds *codieout.Creds) {
	path := codiePath()
	if creds == nil {
		os.Remove(path)
		return
	}
	b, _ := json.MarshalIndent(creds, "", "  ")
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, b, 0o600)
}

// openDeviceWindow shows the 设备 window, opening it the first time.
func (a *app) openDeviceWindow() {
	if a.win == nil {
		return
	}
	if a.deviceWin != nil {
		a.deviceWin.Show()
		a.deviceWin.Focus()
		return
	}
	a.deviceWin = mygo.NewWindow(mygo.WindowOptions{
		Title:           i18n.T("Codie Device"),
		Width:           720,
		Height:          560,
		MinWidth:        560,
		MinHeight:       420,
		BackgroundColor: "#141416",
		Content:         ui.View(a.deviceView),
	})
	a.deviceWin.OnClosed(func() { a.deviceWin = nil })
}

// deviceView is the 设备 window: pairing while there's no device, and once
// there is one, how it is and what it says it can do.
func (a *app) deviceView(c *ui.Context) {
	a.applyPalette()
	t := *c.Theme()
	t.Dark = colors.Dark
	t.Accent, t.AccentHover, t.AccentPressed = colors.accent, colors.accent, colors.accent
	c.SetTheme(&t)
	c.Root().Background(colors.bg)
	lead := float32(0)
	if runtime.GOOS == "darwin" {
		lead = 30 // the traffic lights sit over the window's top-left
	}
	ui.Scroll(c.Key("device")).Fill().Padding(24+lead, 28, 28, 28).Children(func() {
		if a.codie == nil {
			a.pairingView(c)
			return
		}
		a.pairedView(c)
	})
}

// pairingView asks for the device's address, then for the 6-digit code its
// screen shows.
func (a *app) pairingView(c *ui.Context) {
	ui.Column(c).Gap(14).Children(func() {
		ui.Text(c, i18n.T("Connect a codie device")).FontSize(20).FontWeight(800)
		ui.Text(c, i18n.T("Put the device and this computer on the same network, find the device’s IP (it shows in its settings), and fill it in below. When pairing starts, the device’s screen shows a 6-digit code.")).FontSize(13).TextColor(colors.muted).LineHeight(1.5)
		a.scanRow(c)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(6, 0, 0, 0).Children(func() {
			ui.Box(c).Grow(1).Children(func() {
				field := ui.Row(c.Key("addr-field")).Height(34).Radius(8).AlignItems(ui.Stretch)
				if c.Focused(a.pairAddrBox) {
					field.Background(colors.bg).Shadow(0, 0, 0, 3, colors.accent.Alpha(0.25)).Border(1, colors.accent.Alpha(0.6))
				} else {
					field.Background(colors.hover).Border(1, colors.hairline)
				}
				field.Children(func() {
					ui.TextInputBase(c.Key("addr"), &a.pair.addr).Bind(&a.pairAddrBox).Grow(1).MinWidth(0).
						PaddingX(10).Placeholder(i18n.T("Device IP, e.g. 192.168.1.20")).FontSize(13).Label(i18n.T("Device address"))
				})
			})
			nextLabel := i18n.T("Send Again")
			if a.pair.challenge == "" {
				nextLabel = i18n.T("Next")
			}
			if ui.Button(c.Key("next"), nextLabel).Clicked() {
				a.deviceRequestPair()
			}
		})
		if a.pair.challenge != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(160).Children(func() {
					field := ui.Row(c.Key("code-field")).Height(34).Radius(8).AlignItems(ui.Stretch)
					if c.Focused(a.pairCodeBox) {
						field.Background(colors.bg).Shadow(0, 0, 0, 3, colors.accent.Alpha(0.25)).Border(1, colors.accent.Alpha(0.6))
					} else {
						field.Background(colors.hover).Border(1, colors.hairline)
					}
					field.Children(func() {
						ui.TextInputBase(c.Key("code"), &a.pair.code).Bind(&a.pairCodeBox).Grow(1).MinWidth(0).
							PaddingX(10).Placeholder(i18n.T("6-digit code")).FontSize(13).Label(i18n.T("Pairing code"))
					})
				})
				if ui.Button(c.Key("confirm"), i18n.T("Confirm Pairing")).Clicked() {
					a.deviceConfirmPair()
				}
			})
			ui.Text(c, i18n.T("The 6-digit code on the device’s screen, good for 5 minutes.")).FontSize(12).TextColor(colors.faint)
		}
		if a.pair.busy != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14)
				ui.Text(c, a.pair.busy).FontSize(13).TextColor(colors.muted)
			})
		}
		if a.pair.err != "" {
			ui.Text(c, a.pair.err).FontSize(13).TextColor(ui.Oklch(0.7, 0.19, 25)).Margin(4, 0, 0, 0)
		}
	})
}

// pairedView is how the device is, and what it says it can do: the devices
// paired over time with each one's state, the active one at work.
func (a *app) pairedView(c *ui.Context) {
	ui.Column(c).Gap(14).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			label := i18n.T("codie device connected")
			if a.activeDeviceIndex() >= 0 {
				label = deviceLabel(a.devices[a.activeDeviceIndex()])
			}
			ui.Text(c, label).FontSize(20).FontWeight(800).Grow(1)
			if ui.Button(c.Key("forget"), i18n.T("Forget Device")).Clicked() {
				a.deviceForget()
			}
		})
		a.deviceList(c)
		if a.deviceNote != "" {
			ui.Text(c, a.deviceNote).FontSize(13).TextColor(colors.muted)
		}
		ui.Row(c).Gap(10).Children(func() {
			probeLabel := i18n.T("Probe Device Capabilities")
			if a.probing {
				probeLabel = i18n.T("Probing…")
			}
			if ui.Button(c.Key("probe"), probeLabel).Clicked() && !a.probing {
				a.deviceProbe()
			}
		})
		ui.Text(c, i18n.T("The probe asks the device every media endpoint it might have — what it reports (media/capabilities) is whether this firmware can play video.")).FontSize(12.5).TextColor(colors.faint).LineHeight(1.5)
		if len(a.mediaProbe) > 0 {
			ui.Row(c).Margin(8, 0, 0, 0).Children(func() {
				ui.Text(c, i18n.T("Live test: media sessions")).FontSize(15).FontWeight(700)
			})
			ui.Column(c).Gap(6).Children(func() {
				for i := range a.mediaProbe {
					r := &a.mediaProbe[i]
					row := ui.Column(c.Key("ms"+strconv.Itoa(i))).Gap(2).Padding(10, 12).Radius(9).Background(colors.press)
					row.Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							mark := ui.Text(c.Key("mark"), strconv.Itoa(r.Status)).FontSize(12.5).FontWeight(700)
							if r.OK() {
								mark.TextColor(ui.Oklch(0.75, 0.17, 145))
							} else if r.Status == 0 {
								mark.TextColor(colors.faint)
							} else {
								mark.TextColor(ui.Oklch(0.7, 0.19, 25))
							}
							ui.Text(c, r.Name).FontSize(13).FontWeight(600)
						})
						if r.Note != "" {
							ui.Text(c, r.Note).FontSize(11.5).TextColor(colors.muted).MaxLines(8).LineHeight(1.4)
						}
					})
				}
			})
		}
		if len(a.probe) > 0 {
			ui.Column(c).Gap(6).Margin(4, 0, 0, 0).Children(func() {
				for i := range a.probe {
					r := &a.probe[i]
					row := ui.Column(c.Key(r.Path)).Gap(2).Padding(10, 12).Radius(9).Background(colors.press)
					row.Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							status := itoa(r.Status)
							mark := ui.Text(c.Key("mark"), status).FontSize(12.5).FontWeight(700)
							if r.OK() {
								mark.TextColor(ui.Oklch(0.75, 0.17, 145))
							} else if r.Status == 404 {
								mark.TextColor(colors.faint)
							} else {
								mark.TextColor(ui.Oklch(0.7, 0.19, 25))
							}
							ui.Text(c, "GET "+strings.TrimPrefix(r.Path, "/v1/api/data/user/")).FontSize(13).FontWeight(600)
						})
						if r.Note != "" {
							ui.Text(c, r.Note).FontSize(11.5).TextColor(colors.muted).MaxLines(6).LineHeight(1.4)
						}
					})
				}
			})
		}
	})
}

// deviceList is every paired device: its name or address, whether it answers
// now, and a switch to it for all but the active one. The active one carries
// the play; the rest sit ready.
func (a *app) deviceList(c *ui.Context) {
	if len(a.devices) == 0 {
		return
	}
	active := a.activeDeviceIndex()
	ui.Column(c.Key("devices")).Gap(6).Children(func() {
		ui.Text(c, i18n.T("Paired devices")).FontSize(12).FontWeight(700).TextColor(colors.faint)
		for i := range a.devices {
			i, d := i, &a.devices[i]
			row := ui.Row(c.Key("dev"+strconv.Itoa(i))).Gap(10).AlignItems(ui.Center).
				Padding(10, 12).Radius(9)
			if i == active {
				row.Background(colors.accent.Alpha(0.14)).Border(1, colors.accent.Alpha(0.5))
			} else {
				row.Background(colors.press)
			}
			row.Children(func() {
				dot := ui.Box(c.Key("dot"+strconv.Itoa(i))).Size(8, 8).Radius(4).Shrink(0)
				if d.Online {
					dot.Background(ui.Oklch(0.75, 0.17, 145))
				} else {
					dot.Background(ui.Oklch(0.7, 0.19, 25))
				}
				ui.Column(c).Gap(1).Grow(1).Children(func() {
					ui.Text(c, deviceLabel(*d)).FontSize(13).FontWeight(600).SingleLine()
					state := i18n.T("Disconnected")
					if d.Online {
						state = i18n.T("Online")
					}
					t := ui.Text(c, state).FontSize(11.5)
					if d.Online {
						t.TextColor(ui.Oklch(0.75, 0.17, 145))
					} else {
						t.TextColor(colors.faint)
					}
				})
				if i == active {
					ui.Text(c, i18n.T("In use")).FontSize(12).FontWeight(700).TextColor(colors.accent)
				} else if b := ui.Button(c.Key("use"+strconv.Itoa(i)), i18n.T("Use")); d.Online && b.Clicked() {
					a.deviceUse(i)
				}
			})
		}
	})
}

// scanRow is the way to find a device instead of typing its address: a scan of
// the network, then one button to a device it found, which fills the address.
func (a *app) scanRow(c *ui.Context) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(6, 0, 0, 0).Children(func() {
		label := i18n.T("Scan for devices")
		if a.pair.scanning {
			label = i18n.T("Scanning…")
		}
		if ui.Button(c.Key("scan"), label).Clicked() && !a.pair.scanning {
			a.deviceScan()
		}
		if a.pair.scanning {
			ui.Spinner(c).Size(14, 14)
		}
	})
	if len(a.pair.found) == 0 {
		return
	}
	ui.Column(c).Gap(6).Children(func() {
		for i, addr := range a.pair.found {
			host := addr
			b := ui.ButtonBase(c.Key("found" + strconv.Itoa(i))).Height(32).PaddingX(12).Radius(8).
				AlignItems(ui.Center).Justify(ui.Start).FocusRing(false).Label(host).Background(colors.hover)
			b.Children(func() {
				ui.Text(c, host).FontSize(13).FontWeight(600)
			})
			if b.Clicked() {
				a.pair.addr = host
				a.deviceRequestPair()
			}
		}
	})
}

// deviceScan looks for codie devices on this computer's own networks, each at
// port 8081, and lists the ones that answer.
func (a *app) deviceScan() {
	a.pair.scanning = true
	a.pair.found = nil
	a.pair.err = ""
	go func() {
		found := scanDevices()
		a.post(func() {
			a.pair.scanning = false
			a.pair.found = found
			if len(found) == 0 {
				a.pair.err = i18n.T("No device found on this network")
			}
		})
	}()
}

// scanDevices asks every address of this computer's own networks whether a
// codie device listens there, on 8081, and answers the ones that do.
func scanDevices() []string {
	nets := localNets()
	if len(nets) == 0 {
		return nil
	}
	var mu sync.Mutex
	var found []string
	var wg sync.WaitGroup
	gate := make(chan struct{}, 64)
	for _, n := range nets {
		for ip := range hostsOf(n) {
			wg.Add(1)
			gate <- struct{}{}
			go func(ip string) {
				defer wg.Done()
				defer func() { <-gate }()
				addr := ip + ":" + codieout.DefaultPort
				if portOpen(addr) && codieout.Dial(addr) == nil {
					mu.Lock()
					found = append(found, addr)
					mu.Unlock()
				}
			}(ip)
		}
	}
	wg.Wait()
	sort.Strings(found)
	return found
}

// portOpen says whether anything answers at addr within a short while, so a
// scan doesn't wait on every address that has nothing there.
func portOpen(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// localNets are the networks this computer sits on, the private ones, each
// cut down to what a scan can finish: a /24 at the widest.
func localNets() []net.IPNet {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IPNet
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			ones, _ := ipn.Mask.Size()
			if ones < 24 {
				ipn.Mask = net.CIDRMask(24, 32)
				ones = 24
			}
			out = append(out, *ipn)
		}
	}
	return out
}

// hostsOf walks the addresses of a network, leaving out the network's own and
// its broadcast.
func hostsOf(n net.IPNet) func(func(string) bool) {
	return func(yield func(string) bool) {
		ip := n.IP.To4()
		if ip == nil {
			return
		}
		cur := make(net.IP, len(ip))
		copy(cur, ip.Mask(n.Mask))
		inc(cur)
		for n.Contains(cur) {
			next := make(net.IP, len(cur))
			copy(next, cur)
			inc(next)
			if !n.Contains(next) {
				return // the broadcast
			}
			if !yield(cur.String()) {
				return
			}
			cur = next
		}
	}
}

// inc steps an address up by one.
func inc(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			return
		}
	}
}

// deviceRequestPair checks the address, and has the device show its code.
func (a *app) deviceRequestPair() {
	addr := codieout.NormalizeAddr(a.pair.addr)
	if addr == "" {
		a.pair.err = i18n.T("Fill in the device’s IP first")
		return
	}
	a.pair.addr = addr
	a.pair.busy, a.pair.err = i18n.T("Looking for the device…"), ""
	go func() {
		if err := codieout.Dial(addr); err != nil {
			a.post(func() { a.pair.busy, a.pair.err = "", err.Error() })
			return
		}
		c := codieout.New(&codieout.Creds{Addr: addr})
		challenge, err := c.RequestPairing()
		a.post(func() {
			a.pair.busy = ""
			if err != nil {
				a.pair.err = err.Error()
				return
			}
			a.pair.challenge = challenge
		})
	}()
}

// deviceConfirmPair trades the code for a credential, keeps it, and asks the
// device what it can do.
func (a *app) deviceConfirmPair() {
	if a.pair.challenge == "" || len(a.pair.code) != 6 {
		a.pair.err = i18n.T("Fill in the 6-digit code the device shows first")
		return
	}
	a.pair.busy, a.pair.err = i18n.T("Pairing…"), ""
	addr := a.pair.addr
	challenge, code := a.pair.challenge, a.pair.code
	go func() {
		c := codieout.New(&codieout.Creds{Addr: addr})
		creds, err := c.ConfirmPairing(challenge, code)
		a.post(func() {
			a.pair.busy = ""
			if err != nil {
				a.pair.err = err.Error()
				return
			}
			a.deviceAdd(creds)
			a.pair.challenge, a.pair.code, a.pair.err = "", "", ""
			a.pair.addr = ""
		})
	}()
}

// deviceProbe asks the device what it can do, off the main thread.
func (a *app) deviceProbe() {
	if a.codie == nil || a.probing {
		return
	}
	a.probing = true
	client := a.codie
	go func() {
		results := client.ProbeMedia()
		sessions := client.TryMediaSessions()
		note := ""
		if st, err := client.Status(); err == nil {
			note = i18n.T("Address") + " " + client.Creds.Addr
			if st.CardLoaded {
				note += " · " + i18n.T("TF card ready")
			} else {
				note += " · " + i18n.T("TF card not mounted (video doesn’t need it)")
			}
		} else {
			note = i18n.T("Couldn’t ask the device’s state") + ": " + err.Error()
		}
		a.post(func() {
			a.probing = false
			a.probe = results
			a.mediaProbe = sessions
			a.deviceNote = note
		})
	}()
}

// deviceForget unmates the active device, taking it off the list. Another
// paired one, when there is one, becomes the active device.
func (a *app) deviceForget() {
	a.stopPlayer()
	id := ""
	if a.codie != nil {
		id = a.codie.Creds.DeviceID
	}
	kept := a.devices[:0]
	for _, d := range a.devices {
		if d.Creds.DeviceID != id {
			kept = append(kept, d)
		}
	}
	a.devices = kept
	a.saveDevices()
	a.codie = nil
	a.toolsRefresh()
	a.saveCodie(nil)
	a.probe, a.mediaProbe, a.deviceNote = nil, nil, ""
	if len(a.devices) > 0 {
		a.setActiveDevice(0)
	}
}
