// Package codieout talks to a codie device (clt01_embed_backend) over its
// signed local API: pairing, the music session, uploads, and whatever else
// the firmware exposes — the contract proven in the M0 spike.
package codieout

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	payloadEmpty     = "empty"
	payloadJSON      = "json"
	payloadMultipart = "multipart"
	bodyHashUnsigned = "UNSIGNED-PAYLOAD"

	apiMusic = "/v1/api/data/user/music"
)

// Creds is a persisted client credential (or the master secret).
type Creds struct {
	Addr      string `json:"addr"`
	Secret    string `json:"secret"`
	SessionID string `json:"session_id,omitempty"`
	UserID    string `json:"user_id"`
	Kind      string `json:"kind"`
	DeviceID  string `json:"device_id,omitempty"`
	Name      string `json:"name,omitempty"`
	PairedAt  int64  `json:"paired_at,omitempty"`
	// QueueRev is the queue revision to hand the device next: the device
	// refuses a queue whose expected revision differs from its own (and
	// bumps its own by one on every accepted install), so the last known
	// one is kept here and re-probed when it goes stale.
	QueueRev int64 `json:"queue_rev,omitempty"`
}

// APIError is a non-2xx device response.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("codie: %s: %s (HTTP %d)", e.Code, e.Message, e.Status)
	}
	return fmt.Sprintf("codie: %s (HTTP %d)", e.Message, e.Status)
}

type Client struct {
	Creds *Creds
	HTTP  *http.Client
	revMu sync.Mutex
}

func New(creds *Creds) *Client {
	return &Client{Creds: creds, HTTP: &http.Client{Timeout: 120 * time.Second}}
}

func baseURL(addr string) string { return "http://" + strings.TrimSpace(addr) }

// DefaultPort is the port every codie device listens on.
const DefaultPort = "8081"

// NormalizeAddr accepts a bare IP, an IP:port, or a URL and answers the
// host:port the device is at: a bare host gets the default port.
func NormalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimSuffix(addr, "/")
	if addr == "" {
		return addr
	}
	if host, port, err := net.SplitHostPort(addr); err == nil && host != "" && port != "" {
		return addr // port already present
	}
	return addr + ":" + DefaultPort
}

// Call performs a signed request (X-Local-Sig = HMAC-SHA256 over
// METHOD|path|ts|nonce|payloadMode|bodyHash|userId).
func (c *Client) Call(method, path string, body []byte, mode, contentType string) ([]byte, error) {
	if contentType == "" && len(body) > 0 && mode != payloadMultipart {
		contentType = "application/json"
	}
	payloadMode, bodyHash := payloadEmpty, sha256Hex(nil)
	if mode == payloadMultipart {
		payloadMode, bodyHash = payloadMultipart, bodyHashUnsigned
	} else if len(body) > 0 {
		payloadMode, bodyHash = payloadJSON, sha256Hex(body)
	}
	req, err := c.buildSigned(method, path, body, payloadMode, bodyHash, contentType)
	if err != nil {
		return nil, err
	}
	return c.doSigned(req)
}

// buildSigned prepares an http.Request with the X-Local-* signature headers.
// fullPath may carry a query (only the path part is signed).
func (c *Client) buildSigned(method, fullPath string, body []byte, payloadMode, bodyHash, contentType string) (*http.Request, error) {
	return c.buildSignedBody(method, fullPath, bytes.NewReader(body), int64(len(body)), payloadMode, bodyHash, contentType)
}

// buildSignedBody is buildSigned for a body that streams: a multipart upload
// hands the file's bytes straight from disk to the connection instead of
// holding the whole of it in memory first. A negative bodyLen leaves the
// length to the transport.
func (c *Client) buildSignedBody(method, fullPath string, body io.Reader, bodyLen int64, payloadMode, bodyHash, contentType string) (*http.Request, error) {
	cr := c.Creds
	signPath := fullPath
	if i := strings.IndexByte(signPath, '?'); i >= 0 {
		signPath = signPath[:i]
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := randHex(16)
	if cr.UserID == "" {
		if uid, err := c.bootstrapUserID(); err == nil {
			cr.UserID = uid
		}
	}
	mac := hmac.New(sha256.New, []byte(cr.Secret))
	mac.Write([]byte(strings.ToUpper(method) + "|" + signPath + "|" + ts + "|" + nonce + "|" + payloadMode + "|" + bodyHash + "|" + cr.UserID))
	req, err := http.NewRequest(method, baseURL(cr.Addr)+fullPath, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if bodyLen >= 0 {
		req.ContentLength = bodyLen
	}
	req.Header.Set("X-Local-Sig", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Local-Nonce", nonce)
	req.Header.Set("X-Local-Timestamp", ts)
	req.Header.Set("X-Local-Payload-Mode", payloadMode)
	req.Header.Set("X-Local-Body-Hash", bodyHash)
	req.Header.Set("X-Local-User-Id", cr.UserID)
	req.Header.Set("X-Local-Session-Id", cr.SessionID)
	return req, nil
}

// doSigned sends a request whose headers are already signed.
func (c *Client) doSigned(req *http.Request) ([]byte, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 codie 设备（%v）——检查设备是否在线、地址是否正确", err)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		json.Unmarshal(raw, &e)
		return nil, friendly(codeErr(resp.StatusCode, e.Code, first(e.Message, trunc(raw, 200))))
	}
	return raw, nil
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}

// codeErr builds the raw APIError.
func codeErr(status int, code, msg string) error {
	return &APIError{Status: status, Code: code, Message: msg}
}

// friendly turns device-side failures into actionable Chinese hints; unknown
// errors keep their original text.
func friendly(err error) error {
	ae, ok := err.(*APIError)
	if !ok {
		return err
	}
	switch ae.Code {
	case "MODE_INACTIVE", "MUSIC_MODE_INACTIVE":
		return errors.New("设备的音乐页面没开——先在 codie 设备上打开音乐应用，再播放")
	case "TF_CARD_NOT_READY", "MUSIC_TF_NOT_READY":
		return errors.New("设备的 TF 卡没就绪——插好卡再试")
	case "MEDIA_UPLOAD_BUSY":
		return errors.New("设备还在处理上一次上传；等几秒再试")
	case "MEDIA_STORAGE_FULL", "BODY_TOO_LARGE", "INSUFFICIENT_SPACE":
		return errors.New("设备的 TF 卡满了——清掉设备上的歌曲再试")
	case "INVALID_SIGNATURE", "LOCAL_SESSION_EXPIRED", "LOCAL_SECRET_MISMATCH", "LOCAL_USER_MISMATCH":
		return errors.New("设备凭证过期了——重新配对")
	case "UNAUTHORIZED":
		return errors.New("设备拒绝了请求——可能刚重启；稍等再试或重新配对")
	}
	return err
}

func (c *Client) GetJSON(path string, out any) error { return c.reqJSON("GET", path, nil, out) }

func (c *Client) PostJSON(path string, in, out any) error {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	return c.reqJSON("POST", path, body, out)
}

func (c *Client) reqJSON(method, path string, body []byte, out any) error {
	raw, err := c.Call(method, path, body, payloadJSON, "")
	if err != nil {
		return err
	}
	var env struct {
		Success bool            `json:"success"`
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("codie: bad envelope: %s", trunc(raw, 200))
	}
	if !env.Success {
		return friendly(&APIError{Status: 200, Code: env.Code, Message: env.Message})
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

func (c *Client) bootstrapUserID() (string, error) {
	resp, err := http.Get(baseURL(c.Creds.Addr) + "/v1/api/auth/bootstrap")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var b struct {
		Data struct {
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &b) != nil {
		return "", fmt.Errorf("bad bootstrap")
	}
	return b.Data.UserID, nil
}

// ---- pairing (unsigned endpoints) ----

// RequestPairing makes the device show a 6-digit code; the challenge it returns
// is confirmed by ConfirmPairing.
func (c *Client) RequestPairing() (string, error) {
	resp, err := http.Post(baseURL(c.Creds.Addr)+"/v1/api/auth/pair/request", "application/json", nil)
	if err != nil {
		return "", err
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	var r struct {
		Success bool `json:"success"`
		Data    struct {
			ChallengeID string `json:"challenge_id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &r) != nil || !r.Success || r.Data.ChallengeID == "" {
		return "", fmt.Errorf("pair/request failed: %s", trunc(raw, 200))
	}
	return r.Data.ChallengeID, nil
}

// ConfirmPairing exchanges the code for a master secret and immediately issues
// an app client credential, so the master secret never persists.
func (c *Client) ConfirmPairing(challengeID, code string) (*Creds, error) {
	body, _ := json.Marshal(map[string]string{"pairing_code": code, "challenge_id": challengeID, "auth_scope": "permanent"})
	resp, err := http.Post(baseURL(c.Creds.Addr)+"/v1/api/auth/pair/confirm", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	var pr struct {
		Data struct {
			LocalSecret string `json:"local_secret"`
			DeviceID    string `json:"device_id"`
			Name        string `json:"name"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &pr) != nil || pr.Data.LocalSecret == "" {
		return nil, fmt.Errorf("pair/confirm failed: %s", trunc(raw, 200))
	}
	uid, err := c.bootstrapUserID()
	if err != nil || uid == "" {
		return nil, fmt.Errorf("device reachable but not logged in")
	}
	master := &Creds{Addr: c.Creds.Addr, Secret: pr.Data.LocalSecret, UserID: uid, Kind: "master",
		DeviceID: pr.Data.DeviceID, Name: pr.Data.Name, PairedAt: time.Now().Unix()}
	mc := New(master)
	cb, _ := json.Marshal(map[string]string{"label": "codie-video-player"})
	var cr struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := mc.reqJSON("POST", "/v1/api/auth/credentials", cb, &cr); err == nil && cr.ID != "" {
		return &Creds{Addr: c.Creds.Addr, Secret: cr.Secret, SessionID: cr.ID, UserID: uid, Kind: "client",
			DeviceID: pr.Data.DeviceID, Name: pr.Data.Name, PairedAt: time.Now().Unix()}, nil
	}
	return master, nil
}

// ---- music API ----

type MusicStorage struct {
	IsLoaded  bool   `json:"is_loaded"`
	RootPath  string `json:"root_path"`
	CapacityT uint64 `json:"capacity_total_bytes"`
	CapacityU uint64 `json:"capacity_used_bytes"`
	Files     []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"music_files"`
}

func (c *Client) MusicStorage() (*MusicStorage, error) {
	var st MusicStorage
	if err := c.GetJSON(apiMusic+"/storage", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// DevicePlayback mirrors GET /music/playback.
type DevicePlayback struct {
	RequestID  string `json:"request_id"`
	Status     string `json:"status"`
	TrackID    string `json:"track_id"` // "local:<card path>" once known
	TrackName  string `json:"track_name"`
	DurationMs int64  `json:"duration_ms"`
	PositionMs int64  `json:"position_ms"`
	IsPlaying  bool   `json:"is_playing"`
	Volume     int32  `json:"volume_level"`
	ErrorCode  string `json:"error_code"`
	ErrorMsg   string `json:"error_message"`
	Applied    bool   `json:"applied"`
}

func (c *Client) Playback() (*DevicePlayback, error) {
	var p DevicePlayback
	if err := c.GetJSON(apiMusic+"/playback", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Control sends a music action (PLAY/PAUSE/NEXT/PREVIOUS/STOP/SEEK/SET_VOLUME…).
func (c *Client) Control(action string, extra map[string]any) error {
	payload := map[string]any{"action": action, "source": "LOCAL_TF", "request_id": fmt.Sprintf("cmp-%d", time.Now().UnixNano())}
	for k, v := range extra {
		payload[k] = v
	}
	return c.PostJSON(apiMusic+"/control", payload, nil)
}

// EnsureSource switches the device to LOCAL_TF: left in BLUETOOTH it silently
// ignores TF playback commands.
func (c *Client) EnsureSource() error {
	var src struct {
		Source string `json:"source"`
	}
	if err := c.GetJSON(apiMusic+"/source", &src); err != nil {
		return err
	}
	if src.Source == "LOCAL_TF" {
		return nil
	}
	if err := c.PostJSON(apiMusic+"/source", map[string]any{"source": "LOCAL_TF"}, nil); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

// MaxUploadBytes is what one upload may carry: the device caps a request body
// at 200 MiB, and the multipart envelope takes a little on top.
const MaxUploadBytes = 190 << 20

// UploadAudio uploads one audio file under name (title.ext): oversize files
// are split into <name>_1, _2, … with ffmpeg -c copy, uploaded in order, and
// the on-card paths answered in play order.
func (c *Client) UploadAudio(path, name string) ([]string, error) {
	// Already on the card under this name? Reuse it — the same song plays
	// again without an upload at all.
	if card, ok := c.cardFileByName(name); ok {
		return []string{card}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	// Room check before pushing bytes for nothing: the file (or its split
	// parts, which total about the same) must fit with a little of margin.
	if st, err := c.MusicStorage(); err == nil && st.IsLoaded {
		free := int64(st.CapacityT) - int64(st.CapacityU)
		if info.Size()+4<<20 > free {
			return nil, fmt.Errorf("设备 TF 卡空间不够（剩 %.0fMB，需要 %.0fMB）——清掉设备上的歌曲再试",
				float64(free)/(1<<20), float64(info.Size())/(1<<20))
		}
	}
	if info.Size() <= MaxUploadBytes {
		card, err := c.uploadOne(path, name)
		return []string{card}, err
	}
	// Oversize: split with ffmpeg, stream copy, so no re-encode.
	segs, err := splitAudio(path, name, info.Size())
	if err != nil {
		return nil, fmt.Errorf("超过单次上传上限且分片失败（需要 ffmpeg）: %v", err)
	}
	var cards []string
	for i, seg := range segs {
		card, err := c.uploadOne(seg, fmt.Sprintf("%s_%d%s", name, i+1, filepath.Ext(name)))
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// splitAudio cuts path into ceil(size/MaxUploadBytes) pieces of copy-coded
// audio in a temp dir, answering their paths in order.
func splitAudio(path, name string, size int64) ([]string, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
			if _, e := os.Stat(p); e == nil {
				ffmpeg, err = p, nil
				break
			}
		}
		if err != nil {
			return nil, err
		}
	}
	out, err := os.MkdirTemp("", "cmp-split-*")
	if err != nil {
		return nil, err
	}
	// How long one piece may be: the whole file's seconds spread over the
	// pieces, from ffprobe; fall back to assuming ~1 MB per second of the
	// typical 256 kbit/s stream when ffprobe is missing.
	secs := float64(size) / (256 * 1024 / 8)
	if ffprobe, e := exec.LookPath("ffprobe"); e == nil {
		if b, e := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration",
			"-of", "csv=p=0", path).Output(); e == nil {
			if d, e := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); e == nil && d > 0 {
				secs = d
			}
		}
	}
	pieces := int((size + MaxUploadBytes - 1) / MaxUploadBytes)
	segTime := int(secs/float64(pieces)) + 2
	pattern := filepath.Join(out, "seg-%03d"+filepath.Ext(name))
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-i", path,
		"-c", "copy", "-f", "segment", "-segment_time", strconv.Itoa(segTime), pattern)
	if b, e := cmd.CombinedOutput(); e != nil {
		return nil, fmt.Errorf("%v: %s", e, trunc(b, 200))
	}
	matches, _ := filepath.Glob(filepath.Join(out, "seg-*"))
	if len(matches) == 0 {
		return nil, fmt.Errorf("no segments produced")
	}
	return matches, nil
}

// cardFileByName looks a file up on the card by its exact name.
func (c *Client) cardFileByName(name string) (string, bool) {
	st, err := c.MusicStorage()
	if err != nil {
		return "", false
	}
	for _, f := range st.Files {
		if f.Name == name {
			return f.Path, true
		}
	}
	return "", false
}

// uploadOne uploads one audio file to the TF card as name.
func (c *Client) uploadOne(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	boundary := "cmp" + strconv.FormatInt(time.Now().UnixNano(), 10)
	// skip: an existing file is not an error — if the upload comes back
	// rejected as a duplicate, the file's card path is inferred below.
	buf.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"conflict_policy\"\r\n\r\nskip\r\n")
	buf.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"files\"; filename=\"" + name + "\"\r\nContent-Type: application/octet-stream\r\n\r\n")
	buf.Write(data)
	buf.WriteString("\r\n--" + boundary + "--\r\n")
	raw, err := c.Call("POST", apiMusic+"/upload", buf.Bytes(), payloadMultipart, "multipart/form-data; boundary="+boundary)
	if err != nil {
		return "", fmt.Errorf("上传失败: %v", err)
	}
	var up struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Uploaded []struct {
				Path string `json:"path"`
			} `json:"uploaded"`
			Rejected []struct {
				Filename string `json:"filename"`
				Code     string `json:"code"`
			} `json:"rejected"`
		} `json:"data"`
	}
	var rej []struct {
		Filename string `json:"filename"`
		Code     string `json:"code"`
	}
	if json.Unmarshal(raw, &up) != nil || (!up.Success && up.Code != "UPLOAD_FAILED") {
		return "", friendly(&APIError{Status: 200, Code: up.Code, Message: first(up.Message, trunc(raw, 200))})
	}
	if len(up.Data.Uploaded) > 0 {
		return up.Data.Uploaded[0].Path, nil
	}
	for _, r := range up.Data.Rejected {
		if r.Code == "INSUFFICIENT_SPACE" {
			return "", errors.New("设备的 TF 卡满了——清掉设备上的歌曲再试")
		}
		if r.Code == "DUPLICATE_FILE" && r.Filename == name {
			// It's already there under the default upload directory.
			if st, err := c.MusicStorage(); err == nil && st.IsLoaded {
				return st.RootPath + "/code27/music/" + name, nil
			}
			return "", errors.New("这首歌已经在设备上，但读卡列表失败了；稍等再试")
		}
		_ = rej
	}
	return "", friendly(&APIError{Status: 200, Code: up.Code, Message: first(up.Message, trunc(raw, 200))})
}

// ClearCardUploads deletes every music file on the card's default upload
// directory (code27/music) — what this app and the device's own uploads leave.
func (c *Client) ClearCardUploads() (int, error) {
	var st MusicStorage
	if err := c.GetJSON(apiMusic+"/storage", &st); err != nil {
		return 0, err
	}
	if !st.IsLoaded {
		return 0, errors.New("设备的 TF 卡没就绪")
	}
	const dir = "/code27/music/"
	removed := 0
	var lastErr error
	for _, f := range st.Files {
		if !strings.Contains(f.Path, dir) {
			continue
		}
		if err := c.DeleteCardFile(f.Path); err != nil {
			lastErr = err
			continue
		}
		removed++
	}
	return removed, lastErr
}

// DeleteCardFile removes one file from the card by absolute path.
func (c *Client) DeleteCardFile(cardPath string) error {
	full := apiMusic + "/file?path=" + urlQueryEscape(cardPath)
	req, err := c.buildSigned("DELETE", full, nil, payloadEmpty, sha256Hex(nil), "")
	if err != nil {
		return err
	}
	_, err = c.doSigned(req)
	if err != nil {
		return friendly(err)
	}
	return nil
}

// PlayCard plays one on-card file: SELECT_TRACK by its card path, retried
// over the device's file-cache lag (an upload is invisible to the player for
// a few seconds; a too-early select answers TRACK_NOT_FOUND and the folder —
// which the device queues by itself, outside any revision check — carries
// on from there). autoAdvance is unused here: the device queues the folder.
func (c *Client) PlayCard(cardPath string) error {
	target := "local:" + cardPath
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			time.Sleep(2500 * time.Millisecond)
		}
		requestID := fmt.Sprintf("cmp-%d", time.Now().UnixNano())
		payload := map[string]any{
			"action": "SELECT_TRACK", "source": "LOCAL_TF",
			"request_id": requestID, "track_id": target,
		}
		body, _ := json.Marshal(payload)
		if _, err := c.Call("POST", apiMusic+"/control", body, payloadJSON, ""); err != nil {
			continue
		}
		switch out := c.awaitCommand(requestID, target, 5*time.Second); out {
		case commandApplied:
			return nil
		case commandRejected, commandNoAnswer:
			continue
		}
	}
	return errors.New("设备没有开始播放——确认音乐页面开着、TF 卡插好，再试一次")
}

// command outcome of one control request, judged by the playback snapshot the
// device answers with under the very request_id.
type commandOutcome int

const (
	commandNoAnswer commandOutcome = iota
	commandApplied
	commandRejected
)

// awaitCommand watches the playback snapshot for the request's own answer. An
// applied answer means the command was accepted, but the song only really
// plays once the device lands on its track; a rejection (a stale file, a
// closed page) answers within a beat.
func (c *Client) awaitCommand(requestID, targetTrack string, wait time.Duration) commandOutcome {
	deadline := time.Now().Add(wait)
	accepted := false
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		pb, err := c.Playback()
		if err != nil {
			continue
		}
		if pb.ErrorCode != "" || pb.Status == "FAILED" {
			return commandRejected // the failure state carries our request id
		}
		if pb.RequestID == requestID {
			if pb.ErrorCode != "" || pb.Status == "FAILED" {
				return commandRejected
			}
			if pb.Applied && pb.TrackID == targetTrack {
				return commandApplied // accepted and landed on our song
			}
			if pb.Applied {
				accepted = true // accepted; the switch lands on a later report
			}
			continue
		}
		if accepted && pb.TrackID == targetTrack {
			return commandApplied
		}
		if accepted {
			return commandRejected
		}
	}
	return commandNoAnswer
}

// queuePayload builds one PLAY request over cardPaths at the given revision.
func (c *Client) queuePayload(cardRoot string, cardPaths []string, title string, autoAdvance bool, rev int64, requestID string) map[string]any {
	type entry struct {
		ID      string `json:"id"`
		TrackID string `json:"track_id"`
	}
	entries := make([]entry, 0, len(cardPaths))
	ids := make([]string, 0, len(cardPaths))
	for _, p := range cardPaths {
		id := randHex(16)
		entries = append(entries, entry{ID: id, TrackID: "local:" + p})
		ids = append(ids, id)
	}
	return map[string]any{
		"action": "PLAY", "source": "LOCAL_TF", "request_id": requestID,
		"expected_queue_revision": rev, "queue_entry_id": ids[0],
		"queue": map[string]any{
			"storage_root": cardRoot, "title": first(title, "codie-music-player"), "auto_advance": autoAdvance, "revision": rev,
			"entries": entries, "current_entry_id": ids[0],
		},
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func trunc(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n]
	}
	return s
}
