package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// DBKeyStore validates API keys against the user_api_keys table in Postgres.
type DBKeyStore struct {
	db *sql.DB
}

func NewDBKeyStore(databaseURL string) (*DBKeyStore, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	return &DBKeyStore{db: db}, nil
}

func (s *DBKeyStore) Validate(apiKey string) (*OrgContext, error) {
	if apiKey == "" {
		return nil, errors.New("missing API key")
	}

	if !strings.HasPrefix(apiKey, "plx_") && !strings.HasPrefix(apiKey, "flx_") {
		return nil, errors.New("invalid API key format")
	}

	keyHash := hashKey(apiKey)

	var (
		keyID           string
		orgID           sql.NullString
		orgName         sql.NullString
		permissionsJSON []byte
		isActive        bool
		expiresAt       sql.NullTime
		keyPrefix       string
	)

	err := s.db.QueryRow(`
		SELECT k.id, k.organization_id, COALESCE(o.name, ''), k.permissions,
		       k.is_active, k.expires_at, k.key_prefix
		FROM user_api_keys k
		LEFT JOIN organizations o ON o.id = k.organization_id
		WHERE k.key_hash = $1 AND k.revoked_at IS NULL
	`, keyHash).Scan(&keyID, &orgID, &orgName, &permissionsJSON, &isActive, &expiresAt, &keyPrefix)

	if err == sql.ErrNoRows {
		time.Sleep(5 * time.Millisecond) // constant-time to prevent enumeration
		return nil, errors.New("unauthorized")
	}
	if err != nil {
		return nil, errors.New("unauthorized")
	}

	if !isActive {
		return nil, errors.New("unauthorized")
	}

	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return nil, errors.New("unauthorized")
	}

	var scopes []string
	if len(permissionsJSON) > 0 {
		if err := json.Unmarshal(permissionsJSON, &scopes); err != nil {
			log.Printf("[mcp] malformed permissions JSON for key %s: %v", keyPrefix, err)
		}
	}

	// Update last_used_at and use_count asynchronously
	go func() {
		_, err := s.db.Exec(`
			UPDATE user_api_keys
			SET last_used_at = NOW(), use_count = use_count + 1
			WHERE id = $1
		`, keyID)
		if err != nil {
			log.Printf("[mcp] failed to update key usage: %v", err)
		}
	}()

	return &OrgContext{
		OrgID:     orgID.String,
		OrgName:   orgName.String,
		Scopes:    scopes,
		KeyPrefix: keyPrefix,
		RawKey:    apiKey,
	}, nil
}

func (s *DBKeyStore) Close() error {
	return s.db.Close()
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
