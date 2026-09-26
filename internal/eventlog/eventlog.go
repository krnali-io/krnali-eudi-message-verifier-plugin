package eventlog

import (
	"encoding/json"
	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/session"
	"io"
	"sync"
	"time"
)

// Fixed fields only: no error descriptions, request paths, identities or free text.
type event struct {
	TS      time.Time      `json:"ts"`
	Event   string         `json:"event"`
	Session string         `json:"session,omitempty"`
	Channel string         `json:"channel,omitempty"`
	Origin  string         `json:"origin,omitempty"`
	Preset  string         `json:"preset,omitempty"`
	Status  session.Status `json:"status,omitempty"`
	Format  string         `json:"format,omitempty"`
	Error   string         `json:"error,omitempty"`
	Seconds float64        `json:"seconds_to_complete,omitempty"`
}
type Logger struct {
	mu  sync.Mutex
	out io.Writer
}

func New(w io.Writer) *Logger { return &Logger{out: w} }
func (l *Logger) Session(name string, s session.Session) {
	e := event{TS: time.Now().UTC(), Event: name, Session: s.ID, Channel: s.Channel, Origin: s.Origin, Preset: s.Preset, Status: s.Status, Error: s.ErrorCode}
	if s.Result != nil {
		e.Format = s.Result.Format
	}
	if !s.CompletedAt.IsZero() {
		e.Seconds = s.CompletedAt.Sub(s.CreatedAt).Seconds()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = json.NewEncoder(l.out).Encode(e)
}
func (l *Logger) Enabled(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = json.NewEncoder(l.out).Encode(event{TS: time.Now().UTC(), Event: "channel.enabled", Channel: name})
}
