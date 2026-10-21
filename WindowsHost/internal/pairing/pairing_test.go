package pairing

import (
	"fmt"
	"testing"
	"time"
)

func newTestManager() (*Manager, *time.Time) {
	now := time.Unix(1000, 0)
	return New(DefaultConfig(), func() time.Time { return now }), &now
}

func wrong(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func TestSuccessIssuesTokenAndRotates(t *testing.T) {
	m, _ := newTestManager()
	code := m.CurrentCode()
	if len(code) != 6 {
		t.Fatalf("code %q", code)
	}
	r := m.Attempt(code, "a")
	if !r.OK() || !m.IsValid(r.Token) || len(r.Token) != 32 {
		t.Fatalf("pairing failed: %+v", r)
	}
	if m.Attempt(code, "a").OK() && m.CurrentCode() == code {
		t.Fatal("code did not rotate")
	}
}

func TestLockoutDoublesAndExpires(t *testing.T) {
	m, now := newTestManager()
	var r Result
	for i := 0; i < 5; i++ {
		r = m.Attempt(wrong(m.CurrentCode()), "a")
	}
	if r.RetryAfter != 30*time.Second {
		t.Fatalf("first lockout: %+v", r)
	}
	if r := m.Attempt(m.CurrentCode(), "a"); r.OK() {
		t.Fatal("correct code accepted while locked out")
	}
	if r := m.Attempt(m.CurrentCode(), "b"); !r.OK() {
		t.Fatal("other client affected by the lockout")
	}
	*now = now.Add(31 * time.Second)
	for i := 0; i < 5; i++ {
		r = m.Attempt(wrong(m.CurrentCode()), "a")
	}
	if r.RetryAfter != 60*time.Second {
		t.Fatalf("second lockout: %+v", r)
	}
}

func TestGlobalBudgetRotatesCode(t *testing.T) {
	m, _ := newTestManager()
	before := m.CurrentCode()
	rotated := ""
	m.OnCodeChange = func(c string) { rotated = c }
	for i := 0; i < 20; i++ {
		m.Attempt(wrong(m.CurrentCode()), fmt.Sprintf("client%d", i))
	}
	if rotated == "" {
		t.Fatalf("code %s not rotated after 20 failures", before)
	}
}

func TestRevokeAll(t *testing.T) {
	m, _ := newTestManager()
	token := m.IssueUnauthenticatedToken()
	m.RevokeAll()
	if m.IsValid(token) {
		t.Fatal("token survived revocation")
	}
}

func TestTokenLimit(t *testing.T) {
	m, _ := newTestManager()
	first := m.IssueUnauthenticatedToken()
	for i := 0; i < 32; i++ {
		m.IssueUnauthenticatedToken()
	}
	if m.IsValid(first) {
		t.Fatal("oldest token not evicted")
	}
}
