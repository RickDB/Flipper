// Package auth provides password hashing (pure stdlib PBKDF2-HMAC-SHA256)
// and simple in-memory cookie sessions for Flipper.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pbkdf2Iterations = 120_000
	saltLen          = 16
	keyLen           = 32
)

// pbkdf2Key implements PBKDF2 (RFC 2898) using HMAC-SHA256, stdlib only.
func pbkdf2Key(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:])
		u := prf.Sum(nil)

		t := make([]byte, len(u))
		copy(t, u)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for i := range t {
				t[i] ^= u[i]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

// HashPassword returns an encoded string containing the algorithm params,
// salt and derived key, safe to store at rest.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := pbkdf2Key([]byte(password), salt, pbkdf2Iterations, keyLen)
	return fmt.Sprintf("pbkdf2$%d$%s$%s",
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// VerifyPassword checks a plaintext password against an encoded hash
// produced by HashPassword.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got := pbkdf2Key([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken returns a random, hex-encoded session token.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Session represents a logged in user's session.
type Session struct {
	UserID    int
	Username  string
	IsAdmin   bool
	ExpiresAt time.Time
}

// Manager is a simple in-memory session store.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]Session
	ttl      time.Duration
}

func NewManager(ttl time.Duration) *Manager {
	return &Manager{
		sessions: make(map[string]Session),
		ttl:      ttl,
	}
}

func (m *Manager) Create(userID int, username string, isAdmin bool) (string, error) {
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.sessions[token] = Session{
		UserID:    userID,
		Username:  username,
		IsAdmin:   isAdmin,
		ExpiresAt: time.Now().Add(m.ttl),
	}
	m.mu.Unlock()
	return token, nil
}

var ErrNoSession = errors.New("no such session")

func (m *Manager) Get(token string) (Session, error) {
	m.mu.RLock()
	s, ok := m.sessions[token]
	m.mu.RUnlock()
	if !ok {
		return Session{}, ErrNoSession
	}
	if time.Now().After(s.ExpiresAt) {
		m.Delete(token)
		return Session{}, ErrNoSession
	}
	return s, nil
}

func (m *Manager) Delete(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// DeleteForUser invalidates all sessions belonging to a given user id
// (used e.g. when a user is deleted by an admin).
func (m *Manager) DeleteForUser(userID int) {
	m.mu.Lock()
	for tok, s := range m.sessions {
		if s.UserID == userID {
			delete(m.sessions, tok)
		}
	}
	m.mu.Unlock()
}
