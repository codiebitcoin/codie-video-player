package app

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// The app asks its repo, at most once a day, whether a newer release is out.
// When there is one, the top bar's settings dot shows it, and the settings
// window has a button that opens the release's page.

// updateRepo is where the releases live.
const updateRepo = "https://api.github.com/repos/codiebitcoin/codie-video-player"

// checkForUpdates asks the repo for a newer release, when the last ask is a
// day old or more.
func (a *app) checkForUpdates() {
	if time.Since(a.settings.UpdatesChecked) < 24*time.Hour {
		return
	}
	a.checkUpdatesNow()
}

// checkUpdatesNow asks the repo for a newer release, whatever the last ask
// was.
func (a *app) checkUpdatesNow() {
	if a.updateChecking {
		return
	}
	a.updateChecking = true
	go func() {
		tag, url, err := latestRelease()
		a.post(func() {
			a.updateChecking = false
			a.settings.UpdatesChecked = time.Now()
			a.settings.save()
			if err != nil {
				log.Printf("updates: %v", err)
				return
			}
			if newer(tag, mygo.App.Version()) {
				a.updateTag, a.updateURL = tag, url
			} else {
				a.updateTag, a.updateURL = "", ""
			}
		})
	}()
}

// release is what the repo's latest-release answer holds of use.
type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// latestRelease asks GitHub what the repo's newest release is.
func latestRelease() (tag, url string, err error) {
	c := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", updateRepo+"/releases/latest", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := c.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", errors.New("the repo answered " + res.Status)
	}
	var r release
	if json.NewDecoder(res.Body).Decode(&r) != nil || r.TagName == "" {
		return "", "", errors.New("the repo's answer made no sense")
	}
	return r.TagName, r.HTMLURL, nil
}

// newer says whether a release tag is of a version after v: the tags go by
// the app's own vX.Y.Z.
func newer(tag, v string) bool {
	t, ok := semver(tag)
	if !ok {
		return false
	}
	c, ok := semver(v)
	if !ok {
		return false
	}
	if t[0] != c[0] {
		return t[0] > c[0]
	}
	if t[1] != c[1] {
		return t[1] > c[1]
	}
	return t[2] > c[2]
}

// semver reads the three numbers of a version out of a tag, leaving words
// like v or prefixes behind.
func semver(s string) ([3]int, bool) {
	var out [3]int
	at := 0
	for _, part := range strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".") {
		if at > 2 {
			break
		}
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				return out, false
			}
			n = n*10 + int(r-'0')
		}
		out[at] = n
		at++
	}
	if at < 3 {
		return out, false
	}
	return out, true
}
