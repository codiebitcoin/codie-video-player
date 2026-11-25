// Package video is what the player knows of videos: dramas, episodes and the
// like, whichever source they came from, and what a source has to do. A
// source is either built in, like the local folders, or a plugin: see
// internal/plugin.
package video

import "errors"

// Item is a drama as the lists show it: a card in the grid.
type Item struct {
	ID string `json:"id"`
	// Source is the ID of the source the item came from, which plays it. The
	// source's own answers leave it out; it's filled in for them.
	Source   string `json:"source,omitempty"`
	Title    string `json:"title"`
	Cover    string `json:"cover,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	Year     int    `json:"year,omitempty"`
}

// Episode is one part of a drama.
type Episode struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Seconds int    `json:"seconds,omitempty"`
	// Unavailable says the listener can't play it, for a source that knows.
	Unavailable bool `json:"unavailable,omitempty"`
}

// Video is a drama and its episodes, as its page shows.
type Video struct {
	Item
	Intro     string    `json:"intro,omitempty"`
	Episodes  []Episode `json:"episodes,omitempty"`
	Finished  bool      `json:"finished,omitempty"`
	Score     string    `json:"score,omitempty"`
}

// Tab is one of the chips over the grid: a rank, a kind of drama, a year.
type Tab struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// List is a page of what a tab lists.
type List struct {
	Items []Item `json:"items,omitempty"`
	More  bool   `json:"more,omitempty"` // there is a next page
}

// VideoResult is a page of what a search found.
type VideoResult struct {
	Items []Item `json:"items,omitempty"`
	More  bool   `json:"more,omitempty"`
}

// Stream is a video the app plays itself: what a source answers for an
// episode. URL is the picture alone or the whole thing; AudioURL, when set,
// is the sound to play with it, which the app joins through ffmpeg before it
// plays, as a stream that keeps them apart needs.
type Stream struct {
	URL      string            `json:"url"`
	AudioURL string            `json:"audioUrl,omitempty"`
	Type     string            `json:"type"` // hls or mp4
	Headers  map[string]string `json:"headers,omitempty"`
	// SubtitlesURL is where the source's subtitles are, when it has any and
	// the listener asked for them: a WebVTT or SRT file, burnt into the
	// picture as it plays, for the player has no track of its own.
	SubtitlesURL string `json:"subtitlesUrl,omitempty"`
}

// Info is how a source introduces itself.
type Info struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

var (
	// ErrSignIn says the listener has to sign in for this.
	ErrSignIn = errors.New("sign in first")
	// ErrSetup says a setting the source needs is missing.
	ErrSetup = errors.New("the source needs setting up")
	// ErrUnavailable says the episode can't be played.
	ErrUnavailable = errors.New("not available")
	// ErrUnsupported says the source doesn't do this.
	ErrUnsupported = errors.New("not supported by this source")
)

// Fail is an error of a kind above, with what the source said of it, for the
// listener: what to fill in, or what to install. Reason gives it back.
func Fail(kind error, reason string) error {
	if reason == "" {
		return kind
	}
	return &failure{kind, reason}
}

type failure struct {
	kind   error
	reason string
}

func (f *failure) Error() string { return f.kind.Error() + ": " + f.reason }
func (f *failure) Unwrap() error { return f.kind }

// Reason is what the source said of why err happened, if it said anything.
func Reason(err error) string {
	var f *failure
	if errors.As(err, &f) {
		return f.reason
	}
	return ""
}

// VideoSource is where videos come from. Everything but Info and Can may take
// a while and is called off the main thread. What a source doesn't do it
// answers ErrUnsupported to, and Can says so beforehand for the views to
// leave it out.
type VideoSource interface {
	Info() Info
	// Can reports whether the source does what's named, by the name of its
	// plugin's function, such as videoTabs or videoSearch. It answers at
	// once, on the main thread, without starting anything.
	Can(what string) bool

	// Tabs are the chips the grid shows over what this source lists, its own
	// ranks and kinds of drama.
	Tabs() ([]Tab, error)
	// List is a page of what a tab lists, by the tab's ID, pages from one.
	List(tabID string, page int) (*List, error)

	Search(query string, offset int) (*VideoResult, error)

	// Video is a drama and its episodes.
	Video(id string) (*Video, error)

	// Play answers the stream of an episode of a drama.
	Play(id, ep string) (*Stream, error)
}
