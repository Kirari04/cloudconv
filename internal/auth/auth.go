package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/kirari04/cloudconv/internal/store"
)

const CookieName = "cloudconv_session"

type Service struct {
	store        *store.Store
	cookieSecure bool
}

type SessionUser struct {
	Session *store.Session `json:"-"`
	User    *store.User    `json:"user,omitempty"`
	CSRF    string         `json:"csrfToken,omitempty"`
}

func New(s *store.Store, cookieSecure bool) *Service {
	return &Service{store: s, cookieSecure: cookieSecure}
}

func (a *Service) SetupNeeded(ctx context.Context) (bool, error) {
	hasAdmin, err := a.store.HasAdmin(ctx)
	return !hasAdmin, err
}

func (a *Service) CreateUser(ctx context.Context, email, password, role string) (*store.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("valid email is required")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	if role != "admin" && role != "user" {
		return nil, errors.New("role must be admin or user")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	u := store.User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: hash,
		Role:         role,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := a.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	return &u, nil
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (a *Service) Login(ctx context.Context, w http.ResponseWriter, r *http.Request, email, password string) (*SessionUser, error) {
	u, err := a.store.UserByEmail(ctx, email)
	if err != nil {
		return nil, errors.New("invalid email or password")
	}
	if u.Disabled {
		return nil, errors.New("account is disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid email or password")
	}
	token := randomToken(32)
	csrf := randomToken(32)
	now := time.Now().UTC()
	session := store.Session{
		ID:        uuid.NewString(),
		TokenHash: HashToken(token),
		UserID:    u.ID,
		CSRFToken: csrf,
		ExpiresAt: now.Add(14 * 24 * time.Hour),
		CreatedAt: now,
		IPAddress: ClientIP(r, false),
		UserAgent: r.UserAgent(),
	}
	if err := a.store.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	_ = a.store.MarkLogin(ctx, u.ID)
	http.SetCookie(w, a.Cookie(token, session.ExpiresAt))
	return &SessionUser{Session: &session, User: u, CSRF: csrf}, nil
}

func (a *Service) Logout(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(CookieName)
	if err == nil && cookie.Value != "" {
		_ = a.store.DeleteSession(ctx, HashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (a *Service) Current(ctx context.Context, r *http.Request) (*SessionUser, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return &SessionUser{}, nil
	}
	session, user, err := a.store.SessionByTokenHash(ctx, HashToken(cookie.Value))
	if err != nil {
		return &SessionUser{}, nil
	}
	if user.Disabled {
		return &SessionUser{}, nil
	}
	return &SessionUser{Session: session, User: user, CSRF: session.CSRFToken}, nil
}

func (a *Service) Cookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func NewAnonymousToken() (plain, hash string) {
	plain = randomToken(32)
	return plain, HashToken(plain)
}

func randomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			return strings.TrimSpace(parts[0])
		}
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			return realIP
		}
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx > -1 {
		return host[:idx]
	}
	return host
}
