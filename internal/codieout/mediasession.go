package codieout

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// The media session API (firmware 1.6.0): a temporary MP4, image or audio
// file the device plays on its own screen, in a rectangle of it the layout
// names. A session is made with one multipart POST, driven by actions, and
// closed when done; the device keeps at most 256 MiB of them at once, and
// one file may take 128 MiB. Video is H.264 with AAC, up to 1080p.

const apiMedia = "/v1/api/data/user/media"

// MediaLayout is where on the device's screen the session shows, in
// percentages of it.
type MediaLayout struct {
	XPercent      float64 `json:"x_percent"`
	YPercent      float64 `json:"y_percent"`
	WidthPercent  float64 `json:"width_percent"`
	HeightPercent float64 `json:"height_percent"`
	Fit           string  `json:"fit"` // contain, cover or stretch
}

// FullscreenLayout is the whole screen, the picture contained in it.
func FullscreenLayout() *MediaLayout {
	return &MediaLayout{WidthPercent: 100, HeightPercent: 100, Fit: "contain"}
}

// MediaPlayback is how a session starts playing.
type MediaPlayback struct {
	Loop            bool    `json:"loop,omitempty"`
	Muted           bool    `json:"muted,omitempty"`
	Volume          float64 `json:"volume,omitempty"` // 0 to 1
	StartPositionMS int64   `json:"start_position_ms,omitempty"`
}

// MediaSessionRequest is the request part of a session's creation.
type MediaSessionRequest struct {
	RequestID      string         `json:"request_id"`
	Layout         *MediaLayout   `json:"layout,omitempty"`
	Playback       *MediaPlayback `json:"playback,omitempty"`
	MaxDurationMS  *int64         `json:"max_duration_ms,omitempty"`
	ConflictPolicy string         `json:"conflict_policy,omitempty"` // reject or replace
}

// MediaSessionState is what the device says a session is doing.
type MediaSessionState struct {
	SessionID  string
	RequestID  string
	Kind       string
	PositionMS int64
	DurationMS int64
	IsPlaying  bool
	Buffering  bool
}

// newRequestID is an id of our own for one request, as the device asks of
// every command.
func newRequestID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "cvp-" + strconv.FormatInt(time.Now().UnixNano()/1e6, 36) + "-" + hex.EncodeToString(b)
}

// CreateMediaSession uploads one file and opens a session of it on the
// device's screen. It answers the session's id.
func (c *Client) CreateMediaSession(path string, req MediaSessionRequest) (string, error) {
	if req.RequestID == "" {
		req.RequestID = newRequestID()
	}
	if req.ConflictPolicy == "" {
		req.ConflictPolicy = "replace"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	boundary := "cvp" + strconv.FormatInt(time.Now().UnixNano(), 10)
	request, _ := json.Marshal(req)
	buf.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"request\"\r\nContent-Type: application/json\r\n\r\n" + string(request) + "\r\n")
	buf.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + filepath.Base(path) + "\"\r\nContent-Type: video/mp4\r\n\r\n")
	buf.Write(data)
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	raw, err := c.Call("POST", apiMedia+"/sessions", buf.Bytes(), payloadMultipart, "multipart/form-data; boundary="+boundary)
	if err != nil {
		return "", err
	}
	return mediaSessionID(raw)
}

// MediaAction drives a session: pause, resume, seek, set_volume, set_muted
// or set_layout, with the arguments the action takes.
func (c *Client) MediaAction(sessionID, action string, extra map[string]any) error {
	body := map[string]any{"request_id": newRequestID(), "action": action}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := c.postRaw(apiMedia+"/sessions/"+sessionID+"/actions", body)
	if err != nil {
		return err
	}
	_, err = mediaSessionID(raw)
	return err
}

// MediaSessionState asks the device what a session is doing now.
func (c *Client) MediaSessionState(sessionID string) (*MediaSessionState, error) {
	raw, err := c.Call("GET", apiMedia+"/sessions/"+sessionID, nil, payloadEmpty, "")
	if err != nil {
		return nil, err
	}
	return mediaSessionState(raw)
}

// CloseMediaSession ends the session and frees the file, window and player
// of it on the device.
func (c *Client) CloseMediaSession(sessionID string) error {
	_, err := c.Call("DELETE", apiMedia+"/sessions/"+sessionID, nil, payloadEmpty, "")
	return err
}

// ---- the shapes of the device's answers ----

// mediaSessionID reads the id out of an answer, whose envelope says how the
// Android host took it.
func mediaSessionID(raw []byte) (string, error) {
	var env struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		HTTPStatus int    `json:"http_status"`
		Data       struct {
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("设备回答读不懂: %s", trunc(raw, 200))
	}
	if env.Data.SessionID == "" {
		msg := firstNonEmpty(env.Message, env.Code, trunc(raw, 200))
		return "", errors.New(msg)
	}
	return env.Data.SessionID, nil
}

// mediaSessionState reads a session out of the device's answer, of one
// session or of one in the list.
func mediaSessionState(raw []byte) (*MediaSessionState, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	one := func(b []byte) *MediaSessionState {
		var d struct {
			SessionID  string `json:"session_id"`
			RequestID  string `json:"request_id"`
			Kind       string `json:"kind"`
			PositionMS *int64 `json:"position_ms"`
			DurationMS *int64 `json:"duration_ms"`
			IsPlaying  *bool  `json:"is_playing"`
			Buffering  *bool  `json:"buffering"`
			State      struct {
				PositionMS int64 `json:"position_ms"`
				DurationMS int64 `json:"duration_ms"`
				IsPlaying  bool  `json:"is_playing"`
			} `json:"state"`
		}
		if json.Unmarshal(b, &d) != nil || d.SessionID == "" {
			return nil
		}
		st := &MediaSessionState{SessionID: d.SessionID, RequestID: d.RequestID, Kind: d.Kind}
		if d.PositionMS != nil {
			st.PositionMS = *d.PositionMS
		}
		if d.DurationMS != nil {
			st.DurationMS = *d.DurationMS
		}
		if d.IsPlaying != nil {
			st.IsPlaying = *d.IsPlaying
		}
		if d.Buffering != nil {
			st.Buffering = *d.Buffering
		}
		// Some firmwares fold the facts under a state of their own.
		if d.PositionMS == nil && d.State.DurationMS != 0 {
			st.PositionMS, st.DurationMS, st.IsPlaying = d.State.PositionMS, d.State.DurationMS, d.State.IsPlaying
		}
		return st
	}
	if st := one(env.Data); st != nil {
		return st, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(env.Data, &list); err == nil {
		for _, b := range list {
			if st := one(b); st != nil {
				return st, nil
			}
		}
	}
	return nil, errors.New("设备没有报这个会话")
}

// postRaw is a signed POST whose answer comes back as it is.
func (c *Client) postRaw(path string, body any) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.Call("POST", path, encoded, payloadJSON, "")
}
