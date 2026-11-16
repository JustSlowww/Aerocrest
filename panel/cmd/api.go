package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alexedwards/argon2id"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type api struct {
	config   config
	dbConfig dbConfig
}

type dbConfig struct {
	dbPass string
	dbName string
	dbPort string
}

type AuthPageData struct {
	ShowError bool
	ErrorMsg  string
	Username  string
}

type config struct {
	addr     string
	wTimeout time.Duration
	rTimeout time.Duration
}

func (app *api) Mount() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.ClientIPFromRemoteAddr)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(90 * time.Second))
	router.Use(httprate.LimitBy(45, time.Minute, func(r *http.Request) (string, error) {
		return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
	}))
	dbUrl := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%s/%s?sslmode=disable", url.QueryEscape(app.dbConfig.dbPass), app.dbConfig.dbPort, app.dbConfig.dbName)
	// change sslmode=require before production
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbUrl)
	if err != nil {
		log.Fatalf("could not create pool. Err: %s", err.Error())
	}
	err = pool.Ping(ctx)
	if err != nil {
		log.Fatalf("could not ping pool. Err: %s", err.Error())
	}
	err = initUsersTable(ctx, pool)
	if err != nil {
		log.Fatalf("could not init users table. Err: %s", err.Error())
	}
	err = initSessionsTable(ctx, pool)
	if err != nil {
		log.Fatalf("could not init sessions table. Err: %s", err.Error())
	}
	tmpl := template.Must(template.ParseGlob("cmd/templates/*.html"))
	router.NotFound(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		err := tmpl.ExecuteTemplate(w, "NotFound.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, err := getSessionValidity(r.Context(), pool, r) // _ is userid
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if !validSession {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		w.Write([]byte("DASHBOARDDDDDD"))
	})
	router.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, _ := getSessionValidity(r.Context(), pool, r)
		if validSession {
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
		errType := r.URL.Query().Get("error")
		var data AuthPageData
		switch errType {
		case "invalid":
			data.ShowError = true
			data.ErrorMsg = "could not sign in. password or username is invalid"
		}
		err := tmpl.ExecuteTemplate(w, "login.html", data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	router.Get("/signup", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, _ := getSessionValidity(r.Context(), pool, r)
		if validSession {
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
		errType := r.URL.Query().Get("error")
		var data AuthPageData
		switch errType {
		case "invalid":
			data.ShowError = true
			data.ErrorMsg = "could not create new account. password or username is invalid"
		case "exists":
			data.ShowError = true
			data.ErrorMsg = "could not create new account. Username is already used"
		case "internal":
			data.ShowError = true
			data.ErrorMsg = "could not create a new account. Internal server error"
		}
		err := tmpl.ExecuteTemplate(w, "signup.html", data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	router.Post("/login", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		err := r.ParseForm()
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		username := r.PostForm.Get("username")
		password := r.PostForm.Get("password")
		if strings.TrimSpace(username) == "" || password == "" || len(password) > 72 || len(username) > 64 {
			http.Redirect(w, r, "/login?error=invalid", http.StatusSeeOther)
			return
		}
		userId, pswdHash, err := getUser(r.Context(), pool, strings.TrimSpace(username))
		if err != nil {
			if err.Error() == "user not found" {
				http.Redirect(w, r, "/login?error=invalid", http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/login?error=invalid", http.StatusSeeOther)
			return
		}
		match, err := argon2id.ComparePasswordAndHash(password, pswdHash)
		if err != nil {
			http.Redirect(w, r, "/login?error=invalid", http.StatusSeeOther)
			return
		}
		if match {
			err := deleteExpiredSessions(r.Context(), pool, userId)
			if err != nil {
				log.Printf("session cleanup failed %s", err.Error())
				return
			}
			rawToken, err := createSession(r.Context(), pool, userId, middleware.GetClientIPAddr(r.Context()).String())
			if err != nil {
				log.Printf("could not create a new session %s", err.Error())
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     "session_token",
				Value:    rawToken,
				Expires:  (time.Now()).AddDate(0, 0, 7),
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteLaxMode,
				Path:     "/",
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/login?error=invalid", http.StatusSeeOther)
			return
		}
	})
	router.Post("/signup", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		err := r.ParseForm()
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		username := r.PostForm.Get("username")
		password := r.PostForm.Get("password")
		if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" || len(password) > 72 || len(username) > 64 {
			http.Redirect(w, r, "/signup?error=invalid", http.StatusSeeOther)
			log.Printf("password or username is invalid")
			return
		}
		// save hash to db
		userId, err := newUser(r.Context(), pool, strings.TrimSpace(username), password)
		if err != nil {
			var pgError *pgconn.PgError
			if errors.As(err, &pgError) && pgError.Code == "23505" {
				http.Redirect(w, r, "/signup?error=exists", http.StatusSeeOther)
				return
			}
			log.Printf("could not create new account, Err: %s", err.Error())
			http.Redirect(w, r, "/signup?error=internal", http.StatusSeeOther)
			return
		}
		rawToken, err := createSession(r.Context(), pool, userId, middleware.GetClientIPAddr(r.Context()).String())
		if err != nil {
			log.Printf("could not create a new session %s", err.Error())
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "session_token",
			Value:    rawToken,
			Expires:  (time.Now()).AddDate(0, 0, 7),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			Path:     "/",
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	return router
}
func (app *api) Run(h http.Handler) error {
	srv := http.Server{
		Handler:      h,
		Addr:         app.config.addr,
		WriteTimeout: app.config.wTimeout,
		ReadTimeout:  app.config.rTimeout,
		IdleTimeout:  time.Minute,
	}
	fmt.Printf("backend api is running at port %s", app.config.addr)
	return srv.ListenAndServe()
}

func initUsersTable(ctx context.Context, pool *pgxpool.Pool) error {
	query := `CREATE TABLE IF NOT EXISTS users (
    user_id UUID PRIMARY KEY,
    username varchar(64) UNIQUE NOT NULL,
    password_hash varchar(255) NOT NULL,
    perms int DEFAULT 0 NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
)`
	_, err := pool.Exec(ctx, query)
	return err
}

func newUser(ctx context.Context, pool *pgxpool.Pool, username, password string) (string, error) {
	userId := uuid.New()
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		return "", err
	}
	query := `INSERT INTO users (user_id, username, password_hash, perms)
	VALUES ($1, $2, $3, $4)
`
	_, err = pool.Exec(ctx, query, userId, username, hash, 0)
	return userId.String(), err
}

func getUser(ctx context.Context, pool *pgxpool.Pool, username string) (string, string, error) {
	query := `SELECT user_id, password_hash FROM users WHERE username=$1;`
	var pswdHash string
	var userId uuid.UUID
	err := pool.QueryRow(ctx, query, username).Scan(&userId, &pswdHash)
	if err != nil {
		if errors.Is(pgx.ErrNoRows, err) {
			return "", "", fmt.Errorf("user not found")
		}
		return "", "", err
	}
	return userId.String(), pswdHash, nil
}

func initSessionsTable(ctx context.Context, pool *pgxpool.Pool) error {
	query := `CREATE TABLE IF NOT EXISTS sessions (
    user_id UUID NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    token_hash varchar(64) PRIMARY KEY,
    ipaddr varchar(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '7 days')
)`
	_, err := pool.Exec(ctx, query)
	return err
}

func generateSessionToken() (string, string, error) {
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", "", err
	}
	rawtoken := base64.RawURLEncoding.EncodeToString(bytes) // for cookie
	hash := sha256.Sum256([]byte(rawtoken))
	tokenHash := hex.EncodeToString(hash[:]) // for table
	return rawtoken, tokenHash, nil
}

func hashToken(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func createSession(ctx context.Context, pool *pgxpool.Pool, userId, ipAddr string) (string, error) {
	rawToken, tokenHash, err := generateSessionToken()
	if err != nil {
		return "", err
	}
	query := `INSERT INTO sessions (user_id, token_hash , ipaddr) VALUES ($1, $2, $3)`

	_, err = pool.Exec(ctx, query, userId, tokenHash, ipAddr)
	return rawToken, err
}

func getSessionValidity(ctx context.Context, pool *pgxpool.Pool, r *http.Request) (string, bool, error) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return "", false, nil
		}
		return "", false, err
	}
	rawToken := cookie.Value
	tokenHash := hashToken(rawToken)

	query := `SELECT token_hash, user_id FROM sessions WHERE token_hash=$1 AND expires_at > NOW()`
	var dbSha string
	var userId string
	err = pool.QueryRow(ctx, query, tokenHash).Scan(&dbSha, &userId)
	if err != nil {
		if errors.Is(pgx.ErrNoRows, err) {
			return "", false, fmt.Errorf("user not found")
		}
		return "", false, err
	}
	return userId, true, nil
}

func deleteExpiredSessions(ctx context.Context, pool *pgxpool.Pool, userId string) error {
	query := `DELETE FROM sessions WHERE user_id=$1 AND expires_at < NOW()`

	_, err := pool.Exec(ctx, query, userId)
	if err != nil {
		return err
	}
	return nil
}
