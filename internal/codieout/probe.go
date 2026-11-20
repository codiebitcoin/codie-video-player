package codieout

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ProbeResult is what one endpoint of the device answered.
type ProbeResult struct {
	Path   string
	Status int
	Note   string // the beginning of what it answered, when anything
}

// OK says the endpoint is there and answered.
func (r ProbeResult) OK() bool { return r.Status >= 200 && r.Status < 300 }

// ProbeMedia asks the device, endpoint by endpoint, what it serves beyond the
// music session: the media session's own capabilities first, for that's the
// device saying with its own voice what it can play, video included if its
// firmware has it.
func (c *Client) ProbeMedia() []ProbeResult {
	paths := []string{
		"/v1/api/data/user/media/capabilities",
		"/v1/api/data/user/media/sessions",
		"/v1/api/data/user/music/storage",
		"/v1/api/data/user/music/playback",
		"/v1/api/data/user/device/state",
		"/v1/api/data/user/slot",
		"/v1/api/data/user/system",
	}
	out := make([]ProbeResult, 0, len(paths))
	for _, p := range paths {
		r := ProbeResult{Path: p}
		body, err := c.Call("GET", p, nil, payloadEmpty, "")
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				r.Status = apiErr.Status
				r.Note = first(apiErr.Code, apiErr.Message)
			} else {
				r.Status = 0
				r.Note = err.Error()
			}
		} else {
			r.Status = 200
			r.Note = summarize(body)
		}
		out = append(out, r)
	}
	return out
}

// summarize is the beginning of an answer, its JSON tidied if it is one, for
// the probe's table.
func summarize(body []byte) string {
	var pretty json.RawMessage
	if err := json.Unmarshal(body, &pretty); err == nil {
		body = pretty
	}
	s := strings.TrimSpace(string(body))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 400 {
		s = s[:400] + " …"
	}
	return s
}

// Status is how the device is, as the settings of the device window say it.
type Status struct {
	Addr       string
	Name       string
	CardLoaded bool
	CardRoot   string
}

// Status asks the device how it is: reachable, and what its card says.
func (c *Client) Status() (*Status, error) {
	st, err := c.MusicStorage()
	if err != nil {
		return nil, err
	}
	return &Status{CardLoaded: st.IsLoaded, CardRoot: st.RootPath}, nil
}

// Dial checks an address answers as a codie device before pairing: its
// bootstrap needs no signature, and only a codie answers it with a user id.
func Dial(addr string) error {
	resp, err := http.Get("http://" + NormalizeAddr(addr) + "/v1/api/auth/bootstrap")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var b struct {
		Data struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &b) != nil || b.Data.UserID == "" {
		return fmt.Errorf("%s 不像一台 codie 设备", addr)
	}
	return nil
}
