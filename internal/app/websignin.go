package app

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"codie-video-player/internal/i18n"
	"codie-video-player/internal/plugin"
)

// Plugins of services that have no way in but their website sign in on a page
// the app shows: see plugin.WebSignIn.

const (
	// signInPagePoll is how often the page's cookies are looked at for those
	// that say the listener signed in.
	signInPagePoll = 400 * time.Millisecond
	// signInPageFor is how long the page waits for them, as long as the
	// plugin waits for a sign-in.
	signInPageFor = 5 * time.Minute
	// signInCookieGrace is how long a page has to make its own cookies
	// before those UntilChanged names are watched for a change: a site that
	// renews its session at sign-in makes the first ones as it loads.
	signInCookieGrace = 2 * time.Second
)

// webSignIn shows a plugin's sign-in page in a window of its own, over the
// main one, in a session of its own: it sees none of the listener's other
// sign-ins, and is forgotten once the window closes. It answers the Cookie
// header of the address the plugin asked for once the cookies it named are
// all there, and closes the window. Closing it first is giving up.
func (a *app) webSignIn(o plugin.WebSignIn) (string, error) {
	var available bool
	mygo.RunOnMain(func() { available = mygo.WebViewAvailable() })
	if !available {
		return "", errors.New(i18n.T("This computer can't show the page to sign in on"))
	}
	session := mygo.NewEphemeralSession()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     i18n.Tf("Sign in to %s", o.Name),
		URL:       o.URL,
		Width:     480,
		Height:    680,
		MinWidth:  360,
		MinHeight: 480,
		Parent:    a.win,
		Page:      mygo.PageOptions{Session: session, UserAgent: o.UserAgent},
	})
	closed := make(chan struct{})
	win.OnClosed(func() { close(closed) })
	a.post(func() {
		if a.signInPages == nil {
			a.signInPages = map[string]*mygo.Window{}
		}
		a.signInPages[o.Plugin] = win
	})
	defer a.post(func() {
		if a.signInPages[o.Plugin] == win {
			delete(a.signInPages, o.Plugin)
		}
	})
	tick := time.NewTicker(signInPagePoll)
	defer tick.Stop()
	timeout := time.NewTimer(signInPageFor)
	defer timeout.Stop()
	opened := time.Now()
	var was map[string]string // the cookies named in UntilChanged, as the page made them
	for {
		select {
		case <-closed:
			return "", errors.New(i18n.T("The sign-in window was closed before you finished signing in"))
		case <-timeout.C:
			win.Close()
			return "", errors.New(i18n.T("Signing in took too long. Try again."))
		case <-tick.C:
			cookies, err := session.Cookies.List(mygo.CookieFilter{URL: o.CookiesOf})
			if err != nil {
				log.Printf("sign-in page of %s: %v", o.Plugin, err)
				continue
			}
			if len(o.UntilChanged) > 0 && was == nil && time.Since(opened) >= signInCookieGrace {
				// The page's own cookies are what a sign-in renews: these are
				// the ones it made as it loaded.
				if values := cookieValues(cookies); len(values) > 0 {
					was = values
				}
			}
			if header, ok := cookieHeader(cookies, o.Until, o.UntilChanged, was); ok {
				win.Close()
				return header, nil
			}
		}
	}
}

// cookieValues is the cookies by name, as they are now.
func cookieValues(cookies []mygo.Cookie) map[string]string {
	values := make(map[string]string, len(cookies))
	for _, c := range cookies {
		values[c.Name] = c.Value
	}
	return values
}

// cookieHeader is the Cookie header that sends cookies, once those named in
// until are all among them, and any of those in changed has a value other
// than the one it had in was. A nil was answers false: the page hasn't had
// its moment to make them yet.
func cookieHeader(cookies []mygo.Cookie, until, changed []string, was map[string]string) (string, bool) {
	have := map[string]string{}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Value == "" {
			continue
		}
		have[c.Name] = c.Value
		parts = append(parts, c.Name+"="+c.Value)
	}
	for _, name := range until {
		if have[name] == "" {
			return "", false
		}
	}
	if len(changed) > 0 {
		if was == nil {
			return "", false
		}
		differs := false
		for _, name := range changed {
			if have[name] != "" && have[name] != was[name] {
				differs = true
				break
			}
		}
		if !differs {
			return "", false
		}
	}
	return strings.Join(parts, "; "), true
}

// closeSignInPage gives up on a plugin's sign-in page, if it shows.
func (a *app) closeSignInPage(id string) {
	if w := a.signInPages[id]; w != nil {
		w.Close()
	}
}
