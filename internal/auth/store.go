package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session is an upstream Emby login that was accepted through proxemby.
type Session struct {
	Route     string    `json:"route"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name"`
	CreatedAt time.Time `json:"created_at"`
}

// Store keeps accepted access tokens and the secret used to sign resource URLs.
// When path is empty the store lives only in memory.
type Store struct {
	mu       sync.RWMutex
	path     string
	secret   []byte
	sessions map[string]Session
}

type storeFile struct {
	Secret   string             `json:"secret"`
	Sessions map[string]Session `json:"sessions"`
}

func NewMemoryStore() *Store {
	store, _ := OpenStore("")
	return store
}

func OpenStore(path string) (*Store, error) {
	store := &Store{
		path:     path,
		sessions: make(map[string]Session),
	}
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			var file storeFile
			if err := json.Unmarshal(data, &file); err != nil {
				return nil, fmt.Errorf("load auth state %s: %w", path, err)
			}
			secret, err := base64.StdEncoding.DecodeString(file.Secret)
			if err != nil {
				return nil, fmt.Errorf("load auth state %s: invalid secret: %w", path, err)
			}
			store.secret = secret
			if file.Sessions != nil {
				store.sessions = file.Sessions
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, fmt.Errorf("load auth state %s: %w", path, err)
		}
	}
	if len(store.secret) == 0 {
		store.secret = make([]byte, 32)
		if _, err := rand.Read(store.secret); err != nil {
			return nil, err
		}
		if err := store.save(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) Add(token string, session Session) error {
	if token == "" {
		return errors.New("empty access token")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = session
	return s.save()
}

func (s *Store) Lookup(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	s.mu.RLock()
	session, ok := s.sessions[token]
	s.mu.RUnlock()
	return session, ok
}

func (s *Store) Remove(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[token]; !ok {
		return nil
	}
	delete(s.sessions, token)
	return s.save()
}

// Sign returns a URL-safe signature for the given parts.
func (s *Store) Sign(parts ...string) string {
	mac := hmac.New(sha256.New, s.secret)
	for _, part := range parts {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

func (s *Store) Verify(signature string, parts ...string) bool {
	return hmac.Equal([]byte(signature), []byte(s.Sign(parts...)))
}

// save must be called with s.mu held for writing.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(storeFile{
		Secret:   base64.StdEncoding.EncodeToString(s.secret),
		Sessions: s.sessions,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp*")
	if err != nil {
		return fmt.Errorf("save auth state: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("save auth state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save auth state: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("save auth state: %w", err)
	}
	return nil
}
