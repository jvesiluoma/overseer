// Package pairing issues session tokens in exchange for the short code shown in the tray menu.
//
// Brute-force defences: per-client lockout with exponential backoff, a global failure budget
// that rotates the code, rotation after every successful pairing, and constant-time comparison.
package pairing

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"math"
	"math/big"
	"sync"
	"time"
)

type Config struct {
	CodeLength           int
	MaxFailuresPerClient int
	BaseLockout          time.Duration
	MaxLockout           time.Duration
	// Failures from all clients within GlobalWindow that force a new code.
	GlobalFailureBudget int
	GlobalWindow        time.Duration
	MaxTokens           int
}

func DefaultConfig() Config {
	return Config{CodeLength: 6, MaxFailuresPerClient: 5, BaseLockout: 30 * time.Second, MaxLockout: 15 * time.Minute,
		GlobalFailureBudget: 20, GlobalWindow: time.Minute, MaxTokens: 32}
}

// Result of a pairing attempt: exactly one of Token, RemainingAttempts or RetryAfter applies.
type Result struct {
	Token             string
	RemainingAttempts int
	RetryAfter        time.Duration
}

func (r Result) OK() bool { return r.Token != "" }

type clientRecord struct {
	failures    int
	lockouts    int
	lockedUntil time.Time
}

type Manager struct {
	config         Config
	now            func() time.Time
	mu             sync.Mutex
	code           string
	clients        map[string]*clientRecord
	globalFailures []time.Time
	tokens         []string

	// OnCodeChange is called (outside the lock) whenever the code changes.
	OnCodeChange func(code string)
}

func New(config Config, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{config: config, now: now, code: makeCode(config.CodeLength), clients: map[string]*clientRecord{}}
}

func (m *Manager) CurrentCode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.code
}

func (m *Manager) Attempt(candidate, client string) Result {
	now := m.now()
	rotated := ""
	result := func() Result {
		m.mu.Lock()
		defer m.mu.Unlock()
		record := m.clients[client]
		if record == nil {
			record = &clientRecord{}
		}
		if record.lockedUntil.After(now) {
			return Result{RetryAfter: record.lockedUntil.Sub(now)}
		}
		if ConstantTimeEquals(candidate, m.code) {
			delete(m.clients, client)
			token := m.addTokenLocked()
			m.code = makeCode(m.config.CodeLength)
			rotated = m.code
			return Result{Token: token}
		}
		record.failures++
		m.globalFailures = append(m.globalFailures, now)
		kept := m.globalFailures[:0]
		for _, t := range m.globalFailures {
			if now.Sub(t) <= m.config.GlobalWindow {
				kept = append(kept, t)
			}
		}
		m.globalFailures = kept
		if len(m.globalFailures) >= m.config.GlobalFailureBudget {
			m.globalFailures = nil
			m.code = makeCode(m.config.CodeLength)
			rotated = m.code
		}
		if record.failures >= m.config.MaxFailuresPerClient {
			record.failures = 0
			record.lockouts++
			d := time.Duration(math.Min(float64(m.config.BaseLockout)*math.Pow(2, float64(record.lockouts-1)), float64(m.config.MaxLockout)))
			record.lockedUntil = now.Add(d)
			m.clients[client] = record
			return Result{RetryAfter: d}
		}
		m.clients[client] = record
		return Result{RemainingAttempts: m.config.MaxFailuresPerClient - record.failures}
	}()
	if rotated != "" && m.OnCodeChange != nil {
		m.OnCodeChange(rotated)
	}
	return result
}

// IssueUnauthenticatedToken is used only when the user turned pairing off.
func (m *Manager) IssueUnauthenticatedToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addTokenLocked()
}

func (m *Manager) IsValid(token string) bool {
	if len(token) != 32 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	valid := false
	for _, t := range m.tokens {
		if ConstantTimeEquals(t, token) {
			valid = true
		}
	}
	return valid
}

func (m *Manager) RevokeAll() {
	m.mu.Lock()
	m.tokens = nil
	m.code = makeCode(m.config.CodeLength)
	code := m.code
	m.mu.Unlock()
	if m.OnCodeChange != nil {
		m.OnCodeChange(code)
	}
}

func (m *Manager) addTokenLocked() string {
	token := makeToken()
	m.tokens = append(m.tokens, token)
	if len(m.tokens) > m.config.MaxTokens {
		m.tokens = m.tokens[len(m.tokens)-m.config.MaxTokens:]
	}
	return token
}

func ConstantTimeEquals(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func makeCode(length int) string {
	digits := make([]byte, length)
	for i := range digits {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		digits[i] = byte('0' + n.Int64())
	}
	return string(digits)
}

func makeToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
