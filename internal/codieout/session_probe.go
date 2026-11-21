package codieout

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"
)

// MediaSessionProbe is what the device said of a media session we opened with
// our own hands.
type MediaSessionProbe struct {
	Name   string // what we tried
	Status int    // 0 = the device wasn't reachable at all
	Note   string // its exact words, or the session it gave
}

// OK says the device took the session.
func (r MediaSessionProbe) OK() bool { return r.Status >= 200 && r.Status < 300 }

// TryMediaSessions opens media sessions with two files of our own making: a
// half second of silence as WAV, which the docs say a session takes, and a
// stub as MP4, which it shouldn't. What comes back — a session, or the
// refusal's exact words — is the firmware answering the video question itself.
func (c *Client) TryMediaSessions() []MediaSessionProbe {
	const path = "/v1/api/data/user/media/sessions"
	silence := wavSilence(time.Millisecond * 500)
	mp4 := []byte{
		0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0,
		'm', 'p', '4', '2', 'i', 's', 'o', 'm', 0x00, 0x00, 0x00, 0x08, 'f', 'r', 'e', 'e',
	}
	tries := []struct {
		name    string
		file    string
		data    []byte
		request map[string]any
	}{
		{"WAV 0.5s（音频，无 layout）", "probe.wav", silence, map[string]any{"request_id": newRequestID(), "max_duration_ms": 2000}},
		{"MP4 stub（视频，带全屏 layout）", "probe.mp4", mp4, map[string]any{
			"request_id": newRequestID(),
			"layout":     map[string]any{"x_percent": 0.0, "y_percent": 0.0, "width_percent": 100.0, "height_percent": 100.0, "fit": "contain"},
			"playback":   map[string]any{"volume": 1.0},
		}},
	}
	var out []MediaSessionProbe
	for _, t := range tries {
		r := MediaSessionProbe{Name: t.name}
		status, body, err := c.postMultipart(path, t.file, t.data, t.request)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				r.Status = apiErr.Status
				r.Note = firstNonEmpty(apiErr.Code, apiErr.Message, err.Error())
				if apiErr.Code == "MEDIA_SESSION_BLOCKED" {
					r.Note += "\n设备的屏幕此刻不接受画面:点亮屏幕、解锁、退出主界面后再试。具体原因见 reason 字段。"
				}
			} else {
				r.Note = err.Error()
			}
		} else {
			r.Status = status
			r.Note = summarize(body)
		}
		log.Printf("codie probe: %s -> %d %s", t.file, r.Status, r.Note)
		out = append(out, r)
	}
	return out
}

// postMultipart is a multipart POST the device's signature accepts, with one
// file and one JSON part named request.
func (c *Client) postMultipart(path, filename string, data []byte, request map[string]any) (int, []byte, error) {
	var buf bytes.Buffer
	boundary := "cvp" + strconv.FormatInt(time.Now().UnixNano(), 10)
	reqJSON, _ := json.Marshal(request)
	buf.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"request\"\r\nContent-Type: application/json\r\n\r\n" + string(reqJSON) + "\r\n")
	buf.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + filename + "\"\r\nContent-Type: application/octet-stream\r\n\r\n")
	buf.Write(data)
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	body, err := c.Call("POST", path, buf.Bytes(), payloadMultipart, "multipart/form-data; boundary="+boundary)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return apiErr.Status, []byte(firstNonEmpty(apiErr.Message, apiErr.Code)), err
		}
		return 0, nil, err
	}
	return 200, body, nil
}

// wavSilence is a legal WAV of n seconds of silence, 8 kHz mono 16-bit.
func wavSilence(n time.Duration) []byte {
	const rate = 8000
	samples := int(n.Seconds()*rate) + 1
	dataLen := samples * 2
	b := &bytes.Buffer{}
	b.WriteString("RIFF")
	writeU32(b, 36+dataLen)
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	writeU32(b, 16)
	writeU16(b, 1) // PCM
	writeU16(b, 1) // mono
	writeU32(b, rate)
	writeU32(b, rate*2)
	writeU16(b, 2)
	writeU16(b, 16)
	b.WriteString("data")
	writeU32(b, dataLen)
	b.Write(make([]byte, dataLen))
	return b.Bytes()
}

func writeU16(b *bytes.Buffer, v int) {
	b.WriteByte(byte(v))
	b.WriteByte(byte(v >> 8))
}

func writeU32(b *bytes.Buffer, v int) {
	b.WriteByte(byte(v))
	b.WriteByte(byte(v >> 8))
	b.WriteByte(byte(v >> 16))
	b.WriteByte(byte(v >> 24))
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
