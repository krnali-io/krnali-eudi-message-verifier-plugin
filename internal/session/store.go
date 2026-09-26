// Package session owns all mutable session state. No pointers to stored data escape.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/krnali-io/krnali-eudi-message-verifier-plugin/internal/claims"
)

type Status string

const (
	Created   Status = "created"
	Sent      Status = "sent"
	Verifying Status = "verifying"
	Verified  Status = "verified"
	Mismatch  Status = "mismatch"
	Declined  Status = "declined"
	Failed    Status = "failed"
	Expired   Status = "expired"
	Cancelled Status = "cancelled"
)

func (s Status) Terminal() bool {
	return s == Verified || s == Mismatch || s == Declined || s == Failed || s == Expired || s == Cancelled
}

var ErrGone = errors.New("link_unavailable")
var ErrCapacity = errors.New("capacity_reached")

type Session struct {
	ID                string         `json:"id"`
	TokenHash         [32]byte       `json:"-"`
	PollKey           string         `json:"-"`
	Channel           string         `json:"channel"`
	Origin            string         `json:"origin"`
	RecipientMasked   string         `json:"recipient"`
	RecipientRef      string         `json:"-"`
	Preset            string         `json:"preset"`
	ExpectedName      string         `json:"-"`
	SentBy            string         `json:"sent_by"`
	CaseRef           string         `json:"case_ref,omitempty"`
	Status            Status         `json:"status"`
	ErrorCode         string         `json:"error_code,omitempty"`
	TransactionID     string         `json:"-"`
	Nonce             string         `json:"-"`
	CreatedAt         time.Time      `json:"created_at"`
	OpenedAt          time.Time      `json:"opened_at"`
	StartedAt         time.Time      `json:"started_at"`
	CompletedAt       time.Time      `json:"completed_at"`
	ExpiresAt         time.Time      `json:"expires_at"`
	Result            *claims.Result `json:"result"`
	ResultWipeAt      time.Time      `json:"result_wipe_at"`
	ResultWiped       bool           `json:"result_wiped"`
	SecondsToVerified float64        `json:"seconds_to_verified,omitempty"`
}
type Store struct {
	mu                               sync.Mutex
	items                            map[string]*Session
	tokens                           map[[32]byte]string
	polls                            map[[32]byte]string
	Now                              func() time.Time
	LinkTTL, VerifyWindow, ClaimsTTL time.Duration
}

func New(now func() time.Time, link, verify, ttl time.Duration) *Store {
	return &Store{items: map[string]*Session{}, tokens: map[[32]byte]string{}, polls: map[[32]byte]string{}, Now: now, LinkTTL: link, VerifyWindow: verify, ClaimsTTL: ttl}
}
func Random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func clone(s *Session) Session {
	v := *s
	if s.Result != nil {
		r := *s.Result
		if r.AgeOver18 != nil {
			b := *r.AgeOver18
			r.AgeOver18 = &b
		}
		v.Result = &r
	}
	return v
}
func (st *Store) expire(s *Session, now time.Time) {
	if ((s.Status == Created || s.Status == Sent) && !now.Before(s.ExpiresAt)) || (s.Status == Verifying && !now.Before(s.StartedAt.Add(st.VerifyWindow))) {
		st.finish(s, Expired, nil, "", now)
	}
	if s.Result != nil && !now.Before(s.ResultWipeAt) {
		s.Result = nil
		s.ResultWiped = true
		s.ExpectedName = ""
	}
}
func (st *Store) Create(s Session) (Session, string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.items) >= 10000 {
		return Session{}, "", ErrCapacity
	}
	token := Random(32)
	s.ID = Random(16)
	s.TokenHash = sha256.Sum256([]byte(token))
	s.CreatedAt = st.Now()
	s.ExpiresAt = s.CreatedAt.Add(st.LinkTTL)
	s.Status = Created
	st.items[s.ID] = &s
	st.tokens[s.TokenHash] = s.ID
	return clone(&s), token, nil
}
func (st *Store) Get(id string) (Session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.items[id]
	if !ok {
		return Session{}, false
	}
	st.expire(s, st.Now())
	return clone(s), true
}
func (st *Store) List() []Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []Session{}
	for _, s := range st.items {
		st.expire(s, st.Now())
		out = append(out, clone(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (st *Store) MarkSent(id string) Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.items[id]
	st.expire(s, st.Now())
	if s.Status == Created {
		s.Status = Sent
	}
	return clone(s)
}
func (st *Store) ByToken(token string, start bool) (Session, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	h := sha256.Sum256([]byte(token))
	s := st.items[st.tokens[h]]
	if s == nil || subtle.ConstantTimeCompare(h[:], s.TokenHash[:]) != 1 {
		return Session{}, ErrGone
	}
	st.expire(s, st.Now())
	if s.Status != Sent {
		return Session{}, ErrGone
	}
	if s.OpenedAt.IsZero() {
		s.OpenedAt = st.Now()
	}
	if start {
		s.Status = Verifying
		s.StartedAt = st.Now()
		s.Nonce = Random(32)
		s.PollKey = Random(32)
		st.polls[sha256.Sum256([]byte(s.PollKey))] = s.ID
		delete(st.tokens, h)
	}
	return clone(s), nil
}
func (st *Store) SetTransaction(id, tx string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.items[id]
	if s == nil {
		return false
	}
	st.expire(s, st.Now())
	if s.Status != Verifying {
		return false
	}
	s.TransactionID = tx
	return true
}
func (st *Store) Poll(key string) (Status, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.items[st.polls[sha256.Sum256([]byte(key))]]
	if s == nil {
		return "", false
	}
	st.expire(s, st.Now())
	return s.Status, true
}
func (st *Store) finish(s *Session, status Status, r *claims.Result, code string, now time.Time) {
	s.Status = status
	s.CompletedAt = now
	s.ErrorCode = code
	delete(st.tokens, s.TokenHash)
	if r != nil {
		c := *r
		if r.AgeOver18 != nil {
			age := *r.AgeOver18
			c.AgeOver18 = &age
		}
		s.Result = &c
		s.ResultWipeAt = now.Add(st.ClaimsTTL)
		s.SecondsToVerified = now.Sub(s.CreatedAt).Seconds()
	}
	// Names are needed only for the check, except when a retry can reuse them.
	if status == Verified || status == Mismatch || status == Cancelled {
		s.ExpectedName = ""
		s.RecipientRef = ""
	}
	s.Nonce = ""
}
func (st *Store) Complete(id string, status Status, r *claims.Result, code string) (Session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.items[id]
	if s == nil {
		return Session{}, false
	}
	st.expire(s, st.Now())
	if s.Status.Terminal() {
		return clone(s), false
	}
	if !status.Terminal() {
		return clone(s), false
	}
	st.finish(s, status, r, code, st.Now())
	return clone(s), true
}
func (st *Store) Cancel(id string) (Session, bool) { return st.Complete(id, Cancelled, nil, "") }
func (st *Store) Sweep() {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.Now()
	for id, s := range st.items {
		st.expire(s, now)
		if s.Status.Terminal() && !now.Before(s.CompletedAt.Add(24*time.Hour)) {
			delete(st.tokens, s.TokenHash)
			delete(st.polls, sha256.Sum256([]byte(s.PollKey)))
			delete(st.items, id)
		}
	}
}
