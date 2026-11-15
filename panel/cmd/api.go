package main

import (
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type api struct {
	config config
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
	tmpl := template.Must(template.ParseGlob("cmd/templates/*.html"))
	r.NotFound(func(w http.ResponseWriter, request *http.Request) {
		err := tmpl.ExecuteTemplate(w, "NotFound.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.ExecuteTemplate(w, "index.html", map[string]string{
			"Title": "Aerocrest",
			"Msg":   "Welcome to the aerocrest homepage",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	r.Post("/login", func(w http.ResponseWriter, request *http.Request) {
		username := request.FormValue("username")
		password := request.FormValue("password")
		msg := fmt.Sprintf("Login info: \n Username: %s \n Password: %s", username, password)

		w.Write([]byte(msg))
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
