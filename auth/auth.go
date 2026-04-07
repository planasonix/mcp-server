// Package auth handles API key validation and org-scoped context resolution.
// All tenant identity is derived server-side from the credential — never from
// client-supplied parameters. This prevents prompt injection attacks where a
// malicious LLM prompt might try to pass a different org_id as a tool argument.
package auth

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// OrgContext is attached to every authenticated request. Downstream tool
// handlers receive this — never a raw org_id from the request body.
type OrgContext struct {
	OrgID     string
	OrgName   string
	Scopes    []string // e.g. ["pipelines:read", "pipelines:write", "connectors:read"]
	KeyPrefix string   // e.g. "plx_live" vs "plx_test" — useful for env isolation
	RawKey    string   // original API key, used internally for backend forwarding — never exposed to clients
}

// HasScope returns true if the org context includes the requested scope.
func (c *OrgContext) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// KeyStore validates API keys and resolves them to OrgContext.
// In production, replace with a DB-backed implementation.
type KeyStore interface {
	Validate(apiKey string) (*OrgContext, error)
}

// --- In-Memory Implementation (for dev/testing) ---

type keyRecord struct {
	orgID   string
	orgName string
	scopes  []string
	prefix  string
}

type InMemoryKeyStore struct {
	mu   sync.RWMutex
	keys map[string]keyRecord
}

func NewInMemoryKeyStore() *InMemoryKeyStore {
	return &InMemoryKeyStore{keys: make(map[string]keyRecord)}
}

func (s *InMemoryKeyStore) Register(apiKey, orgID, orgName string, scopes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prefix := "plx_live"
	if strings.Contains(apiKey, "_test_") {
		prefix = "plx_test"
	}

	s.keys[apiKey] = keyRecord{
		orgID:   orgID,
		orgName: orgName,
		scopes:  scopes,
		prefix:  prefix,
	}
}

func (s *InMemoryKeyStore) Validate(apiKey string) (*OrgContext, error) {
	if apiKey == "" {
		return nil, errors.New("missing API key")
	}

	// Basic format guard — Planasonix keys start with "plx_" or "flx_"
	if !strings.HasPrefix(apiKey, "plx_") && !strings.HasPrefix(apiKey, "flx_") {
		return nil, errors.New("invalid API key format")
	}

	s.mu.RLock()
	record, ok := s.keys[apiKey]
	s.mu.RUnlock()

	if !ok {
		// Constant-time delay to prevent timing attacks on key enumeration
		time.Sleep(5 * time.Millisecond)
		return nil, errors.New("unauthorized")
	}

	return &OrgContext{
		OrgID:     record.orgID,
		OrgName:   record.orgName,
		Scopes:    record.scopes,
		KeyPrefix: record.prefix,
		RawKey:    apiKey,
	}, nil
}
