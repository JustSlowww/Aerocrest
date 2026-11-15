package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/alexedwards/argon2id"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(90 * time.Second))

	dbUrl := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%s/%s?sslmode=disable", url.QueryEscape(app.dbConfig.dbPass), app.dbConfig.dbPort, app.dbConfig.dbName)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbUrl)
	if err != nil {
		log.Printf("could not create pool. Err: %s", err.Error())
	}
	err = pool.Ping(ctx)
	if err != nil {
		log.Printf("could not ping pool. Err: %s", err.Error())
	}
	err = initUsersTable(ctx, pool)
	if err != nil {
		log.Printf("could not ping pool. Err: %s", err.Error())
	}
	tmpl := template.Must(template.ParseGlob("cmd/templates/*.html"))
	r.NotFound(func(w http.ResponseWriter, request *http.Request) {
		err := tmpl.ExecuteTemplate(w, "NotFound.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("DASHBOARDDDDDD"))
	})
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.ExecuteTemplate(w, "signin.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	r.Get("/signup", func(w http.ResponseWriter, r *http.Request) {
		errType := r.URL.Query().Get("error")
		var data AuthPageData
		switch errType {
		case "invalid":
			data.ShowError = true
			data.ErrorMsg = "could not create new account. password or username is invalid"
		case "exists":
			data.ShowError = true
			data.ErrorMsg = "could not create new account. Username is already used"
		}
		err := tmpl.ExecuteTemplate(w, "signup.html", data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)

		err := r.ParseForm()
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		username := r.PostForm.Get("username")
		password := r.PostForm.Get("password")
		if username == "" || password == "" || len(password) > 72 || len(username) > 64 {
			http.Error(w, "invalid username or password", http.StatusUnauthorized)
			return
		}
		// compare hash instead
		//_, err = argon2id.CreateHash(password, argon2id.DefaultParams)
		//if err != nil {
		//	return
		//}
		// save hash to db
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	r.Post("/signup", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		err := r.ParseForm()
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		username := r.PostForm.Get("username")
		password := r.PostForm.Get("password")
		if username == "" || password == "" || len(password) > 72 || len(username) > 64 {
			http.Redirect(w, r, "/signup?error=invalid", http.StatusSeeOther)
			log.Printf("password or username is invalid")
			return
		}
		// save hash to db
		err = newUsers(ctx, pool, username, password)
		if err != nil {
			http.Redirect(w, r, "/signup?error=exists", http.StatusSeeOther)
			log.Printf("could not create new account, Err: %s", err.Error())
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	return r
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
    user_id varchar(255) PRIMARY KEY,
    username varchar(64) UNIQUE NOT NULL,
    password_hash varchar(255) NOT NULL,
    perms int DEFAULT 0 NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
)`
	_, err := pool.Exec(ctx, query)
	return err
}

func newUsers(ctx context.Context, pool *pgxpool.Pool, username, password string) error {
	userId := uuid.New()
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		return err
	}
	query := `INSERT INTO users (user_id, username, password_hash, perms)
	VALUES ($1, $2, $3, $4)
`
	_, err = pool.Exec(ctx, query, userId, username, hash, 0)
	return err
}
