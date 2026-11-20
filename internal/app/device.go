package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/codieout"
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
}

// codiePath is where the paired device's credential is kept.
func codiePath() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		dir = ""
	}
	return filepath.Join(dir, "codie.json")
}

// loadCodie brings back the device paired before, if there was one.
func (a *app) loadCodie() {
	b, err := os.ReadFile(codiePath())
	if err != nil {
		return
	}
	var creds codieout.Creds
	if json.Unmarshal(b, &creds) != nil || creds.Addr == "" || creds.Secret == "" {
		return
	}
	a.codie = codieout.New(&creds)
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
		Title:           "Codie 设备",
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
	t := *c.Theme()
	t.Dark = true
	c.SetTheme(&t)
	c.Root().Background(colors.bg)
	ui.Scroll(c.Key("device")).Fill().Padding(24, 28, 28, 28).Children(func() {
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
		ui.Text(c, "连接 codie 设备").FontSize(20).FontWeight(800)
		ui.Text(c, "设备和电脑连同一个局域网，在设备上找到它的 IP（设置里看），填在下面。配对时设备屏幕会弹出 6 位配对码。").FontSize(13).TextColor(colors.muted).LineHeight(1.5)
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
						PaddingX(10).Placeholder("设备的 IP，如 192.168.1.20").FontSize(13).Label("设备地址")
				})
			})
			nextLabel := "重新发码"
			if a.pair.challenge == "" {
				nextLabel = "下一步"
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
							PaddingX(10).Placeholder("6 位配对码").FontSize(13).Label("配对码")
					})
				})
				if ui.Button(c.Key("confirm"), "确认配对").Clicked() {
					a.deviceConfirmPair()
				}
			})
			ui.Text(c, "设备屏幕上显示的 6 位码，5 分钟内有效。").FontSize(12).TextColor(colors.faint)
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

// pairedView is how the device is, and what it says it can do.
func (a *app) pairedView(c *ui.Context) {
	ui.Column(c).Gap(14).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已连接 codie 设备").FontSize(20).FontWeight(800).Grow(1)
			if ui.Button(c.Key("forget"), "解除配对").Clicked() {
				a.deviceForget()
			}
		})
		if a.deviceNote != "" {
			ui.Text(c, a.deviceNote).FontSize(13).TextColor(colors.muted)
		}
		ui.Row(c).Gap(10).Children(func() {
			probeLabel := "探测设备能力"
			if a.probing {
				probeLabel = "探测中…"
			}
			if ui.Button(c.Key("probe"), probeLabel).Clicked() && !a.probing {
				a.deviceProbe()
			}
		})
		ui.Text(c, "探测会问设备每一个可能的媒体端点——它自己报上来的能力（media/capabilities）就是这台固件能不能放视频的答案。").FontSize(12.5).TextColor(colors.faint).LineHeight(1.5)
		if len(a.mediaProbe) > 0 {
			ui.Row(c).Margin(8, 0, 0, 0).Children(func() {
				ui.Text(c, "实测:媒体会话").FontSize(15).FontWeight(700)
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

// deviceRequestPair checks the address, and has the device show its code.
func (a *app) deviceRequestPair() {
	addr := codieout.NormalizeAddr(a.pair.addr)
	if addr == "" {
		a.pair.err = "先填设备的 IP"
		return
	}
	a.pair.addr = addr
	a.pair.busy, a.pair.err = "正在找设备…", ""
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
		a.pair.err = "先填设备上显示的 6 位配对码"
		return
	}
	a.pair.busy, a.pair.err = "正在配对…", ""
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
			a.codie = codieout.New(creds)
			a.saveCodie(creds)
			a.pair.challenge, a.pair.code, a.pair.err = "", "", ""
			a.deviceProbe()
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
			note = "地址 " + client.Creds.Addr
			if st.CardLoaded {
				note += " · TF 卡就绪（" + st.CardRoot + "）"
			} else {
				note += " · TF 卡未挂载"
			}
			if client.Creds.Name != "" {
				note = client.Creds.Name + " · " + note
			}
		} else {
			note = "设备状态问不动：" + err.Error()
		}
		a.post(func() {
			a.probing = false
			a.probe = results
			a.mediaProbe = sessions
			a.deviceNote = note
		})
	}()
}

// deviceForget unmates the device.
func (a *app) deviceForget() {
	a.stopPlayer()
	a.codie = nil
	a.saveCodie(nil)
	a.probe, a.deviceNote = nil, ""
}
