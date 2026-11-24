package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alexedwards/argon2id"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed templates/*.html templates/app/*.html templates/partials/*.html
var templateFS embed.FS

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

type nodes struct {
	NodeId    string `db:"nodeId"`
	NodeName  string `db:"name"`
	IpAddress string `db:"ipaddr"`
	Port      string `db:"port"`
	DiskTotal string `db:"total_disk_mb"`
	RamTotal  string `db:"total_ram_mb"`
	Token     string `db:"token"`
}

type servers struct {
	ServerId string `db:"serverid"`
	NodeId   string `db:"nodeid"`
	Name     string `db:"name"`
	Image    string `db:"image"`
	RamMb    string `db:"ram_mb"`
	CpuLimit string `db:"cpu_limit"`
	DiskMb   string `db:"disk_mb"`
	Node     nodes  `db:"-"`
}

type newServer struct {
	Id   string
	Port string
}

type powerState struct {
	State int `json:"state"`
}

func renderPage(w http.ResponseWriter, pageFile string, data any) error {
	tmpl := template.Must(template.ParseFS(templateFS,
		"templates/app/base.html",
		"templates/partials/*.html",
		"templates/app/"+pageFile,
	))
	return tmpl.ExecuteTemplate(w, "base", data)
}

func (app *api) Mount() http.Handler {
	// init router + middleware
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.ClientIPFromRemoteAddr)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(90 * time.Second)) //timeout
	router.Use(httprate.LimitBy(60, time.Minute, func(r *http.Request) (string, error) {
		return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
	})) // ratelimit
	dbUrl := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%s/%s?sslmode=disable", url.QueryEscape(app.dbConfig.dbPass), app.dbConfig.dbPort, app.dbConfig.dbName)
	// change sslmode=require before production
	ctx := context.Background()

	// create db pool and init tables
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
	err = initNodesTable(ctx, pool)
	if err != nil {
		log.Fatalf("could not init nodes table. Err: %s", err.Error())
	}
	err = initServersTable(ctx, pool)
	if err != nil {
		log.Fatalf("could not init servers table. Err: %s", err.Error())
	}
	err = initAllocationsTable(ctx, pool)
	if err != nil {
		log.Fatalf("could not init allocations table. Err: %s", err.Error())
	}

	// templates for login logout notfound
	tmpl := template.Must(template.ParseFS(templateFS,
		"templates/*.html",
	))

	// Endpoints
	router.NotFound(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		err := tmpl.ExecuteTemplate(w, "NotFound.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	// GET render new pages and check validity of token
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, err := getSessionValidity(r.Context(), pool, r) // _ is userid
		if err != nil {
			log.Printf("session validity check failed, error : %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !validSession {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		tokenMap, err := getUserInfoFromSessionToken(r.Context(), pool, r)
		if err != nil {
			log.Printf("failed to get user info from session token: %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		err = renderPage(w, "home.html", map[string]any{
			"Title":    "Dashboard",
			"Username": tokenMap["username"].(string),
			"Perms":    tokenMap["perms"],
		})
		if err != nil {
			log.Printf("failed to render template: %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	router.Get("/servers", func(w http.ResponseWriter, r *http.Request) {
		userId, validSession, err := getSessionValidity(r.Context(), pool, r) // _ is userid
		if err != nil {
			log.Printf("session validity check failed, error : %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !validSession {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		tokenMap, err := getUserInfoFromSessionToken(r.Context(), pool, r)
		if err != nil {
			log.Printf("failed to get user info from session token: %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		serversList, err := getUserServers(r.Context(), pool, userId)
		if err != nil {
			log.Printf("failed to get servers List from user id: %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		err = renderPage(w, "serversList.html", map[string]any{
			"Title":    "Servers",
			"Username": tokenMap["username"].(string),
			"Perms":    tokenMap["perms"],
			"servers":  serversList,
		})
		if err != nil {
			log.Printf("failed to render template: %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	router.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, _ := getSessionValidity(r.Context(), pool, r)
		if validSession {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		errType := r.URL.Query().Get("error")
		var data AuthPageData
		switch errType {
		case "invalid":
			data.ShowError = true
			data.ErrorMsg = "password or username is invalid"
		}
		err := tmpl.ExecuteTemplate(w, "login", data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	router.Get("/signup", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, _ := getSessionValidity(r.Context(), pool, r)
		if validSession {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		errType := r.URL.Query().Get("error")
		var data AuthPageData
		switch errType {
		case "invalid":
			data.ShowError = true
			data.ErrorMsg = "password or username is invalid"
		case "exists":
			data.ShowError = true
			data.ErrorMsg = "Username is already used"
		case "internal":
			data.ShowError = true
			data.ErrorMsg = "could not create a new account. Internal server error"
		}
		err := tmpl.ExecuteTemplate(w, "signup", data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})
	router.Route("/server", func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			userId, validSession, err := getSessionValidity(r.Context(), pool, r) // _ is userid
			if err != nil {
				log.Printf("session validity check failed, error : %s", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !validSession {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			serverId := r.URL.Query().Get("uuid")
			server, err := getServer(r.Context(), pool, serverId, userId)
			if err != nil {
				log.Printf("failed to server from uuid: %s", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			tokenMap, err := getUserInfoFromSessionToken(r.Context(), pool, r)
			if err != nil {
				log.Printf("failed to get user info from session token: %s", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			nodeInfo, err := getNodeById(r.Context(), pool, server.NodeId)
			if err != nil {
				log.Printf("failed to get node info: %s", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			request, err := http.NewRequest("GET", "http://"+nodeInfo.IpAddress+":"+nodeInfo.Port+"/api/servers/"+server.ServerId+"/power", nil)
			if err != nil {
				log.Printf("failed get server state: %s", err.Error())
				http.Error(w, "failed get server state", http.StatusBadRequest)
				return
			}
			request.Header.Set("Authorization", "Bearer "+nodeInfo.Token)
			do, err := http.DefaultClient.Do(request)
			if err != nil {
				log.Printf("failed get server state: %s", err.Error())
				http.Error(w, "failed get server state", http.StatusBadRequest)
				return
			}
			var pwrJson powerState
			err = json.NewDecoder(do.Body).Decode(&pwrJson)
			if err != nil {
				log.Printf("failed get server state: %s", err.Error())
				http.Error(w, "failed get server state", http.StatusBadRequest)
				return
			}
			defer do.Body.Close()
			err = renderPage(w, "serverInfo.html", map[string]any{
				"Title":          server.Name,
				"Username":       tokenMap["username"].(string),
				"Perms":          tokenMap["perms"],
				"server":         server,
				"containerState": pwrJson.State,
			})
			if err != nil {
				log.Printf("failed to render template: %s", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		})
		r.Post("/pwr", func(w http.ResponseWriter, r *http.Request) {
			userId, validSession, err := getSessionValidity(r.Context(), pool, r) // _ is userid
			if err != nil {
				log.Printf("session validity check failed, error : %s", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if !validSession {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			serverId := r.URL.Query().Get("uuid")
			instruction := r.URL.Query().Get("instruction")
			server, err := getServer(r.Context(), pool, serverId, userId)
			if err != nil {
				log.Printf("failed to server from uuid: %s", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			nodeInfo, err := getNodeById(r.Context(), pool, server.NodeId)
			if err != nil {
				log.Printf("failed to get node info: %s", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			q := url.Values{}
			q.Add("instruction", instruction)
			request, err := http.NewRequest("POST", "http://"+nodeInfo.IpAddress+":"+nodeInfo.Port+"/api/servers/"+server.ServerId+"/power?"+q.Encode(), nil)
			if err != nil {
				log.Printf("failed get server state: %s", err.Error())
				http.Error(w, "failed get server state", http.StatusBadRequest)
				return
			}
			request.Header.Set("Authorization", "Bearer "+nodeInfo.Token)
			do, err := http.DefaultClient.Do(request)
			if err != nil {
				log.Printf("failed get server state: %s", err.Error())
				http.Error(w, "failed get server state", http.StatusBadRequest)
				return
			}
			defer do.Body.Close()
			if do.StatusCode == http.StatusOK {
				w.Header().Set("HX-Redirect", "/server?uuid="+server.ServerId)
				w.WriteHeader(http.StatusOK)
			}
		})
	})

	router.Get("/admin", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, err := getSessionValidity(r.Context(), pool, r) // _ is userid
		if err != nil {
			log.Printf("session validity check failed, error : %s", err.Error())
			http.Error(w, fmt.Sprintf("session validity check failed, error : %s", err.Error()), http.StatusInternalServerError)
			return
		}
		if !validSession {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		tokenMap, err := getUserInfoFromSessionToken(r.Context(), pool, r)
		if err != nil {
			log.Printf("failed to get user info from session token: %s", err.Error())
			http.Error(w, fmt.Sprintf("failed to get user info from session token: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		if tokenMap["perms"] != 1 {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		nodesList, err := getNodes(r.Context(), pool)
		if err != nil {
			log.Printf("failed to get nodes list: %s", err.Error())
			http.Error(w, fmt.Sprintf("failed to get nodes list: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		// render admin page
		err = renderPage(w, "admin.html", map[string]any{
			"Title":    "Admin Settings",
			"Username": tokenMap["username"].(string),
			"Perms":    tokenMap["perms"],
			"nodes":    nodesList,
		})
		if err != nil {
			log.Printf("failed to render template: %s", err.Error())
			http.Error(w, fmt.Sprintf("failed to render template: %s", err.Error()), http.StatusInternalServerError)
			return
		}
	})
	router.Get("/newServer", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, err := getSessionValidity(r.Context(), pool, r) // _ is userid
		if err != nil {
			log.Printf("session validity check failed, error : %s", err.Error())
			http.Error(w, fmt.Sprintf("session validity check failed, error : %s", err.Error()), http.StatusInternalServerError)
			return
		}
		if !validSession {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		tokenMap, err := getUserInfoFromSessionToken(r.Context(), pool, r)
		if err != nil {
			log.Printf("failed to get user info from session token: %s", err.Error())
			http.Error(w, fmt.Sprintf("failed to get user info from session token: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		nodesList, err := getNodes(r.Context(), pool)
		if err != nil {
			log.Printf("failed to get nodes list: %s", err.Error())
			http.Error(w, fmt.Sprintf("failed to get nodes list:: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		err = renderPage(w, "createServer.html", map[string]any{
			"Title":    "New Server",
			"Username": tokenMap["username"].(string),
			"Perms":    tokenMap["perms"],
			"nodes":    nodesList,
		})
		if err != nil {
			log.Printf("failed to render template: %s", err.Error())
			http.Error(w, fmt.Sprintf("failed to render template: %s", err.Error()), http.StatusInternalServerError)
			return
		}
	})
	router.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(240 * time.Second))
		r.Post("/newServer", func(w http.ResponseWriter, r *http.Request) {
			userId, validSession, err := getSessionValidity(r.Context(), pool, r)
			if err != nil {
				log.Printf("session validity check failed, error : %s", err.Error())
				http.Error(w, fmt.Sprintf("session validity check failed, error : %s", err.Error()), http.StatusInternalServerError)
				return
			}
			if !validSession {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusOK)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			err = r.ParseForm()
			if err != nil {
				log.Printf("failed to parse form: %s", err.Error())
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			name := r.PostForm.Get("name")
			disk := r.PostForm.Get("disk")
			ram := r.PostForm.Get("ram")
			cpu := r.PostForm.Get("cpu")
			game := r.PostForm.Get("game")
			allowedImages := map[string]bool{
				"itzg/minecraft-server:latest": true,
			}
			if !allowedImages[game] {
				log.Printf("invalid game")
				http.Error(w, "invalid game", http.StatusBadRequest)
				return
			}
			node := r.PostForm.Get("node")
			nodeInfo, err := getNode(r.Context(), pool, node)
			if err != nil {
				log.Printf("failed get node info: %s", err.Error())
				http.Error(w, "failed get node info", http.StatusBadRequest)
				return
			}
			var serverI newServer
			var servId string
			if strings.Contains(game, "itzg/minecraft-server") {
				q := url.Values{}
				q.Add("image", game)
				q.Add("ram", ram)
				q.Add("cpu", cpu)
				q.Add("version", "26.3")
				q.Add("ssd", disk+"M")
				request, err := http.NewRequest("POST", "http://"+nodeInfo.IpAddress+":"+nodeInfo.Port+"/api/servers/minecraft?"+q.Encode(), nil)
				if err != nil {
					log.Printf("failed create server: %s", err.Error())
					http.Error(w, "failed create server", http.StatusBadRequest)
					return
				}
				request.Header.Set("Authorization", "Bearer "+nodeInfo.Token)
				doReq, err := http.DefaultClient.Do(request)
				if err != nil {
					log.Printf("failed create server: %s", err.Error())
					http.Error(w, "failed create server", http.StatusBadRequest)
					return
				}
				defer doReq.Body.Close()
				if doReq.StatusCode == http.StatusOK {
					err = json.NewDecoder(doReq.Body).Decode(&serverI)
					if err != nil {
						log.Printf("failed create server: %s", err.Error())
						http.Error(w, "failed create server", http.StatusBadRequest)
						return
					}
					servId, err = createServer(r.Context(), pool, userId, nodeInfo.NodeName, servers{
						NodeId:   nodeInfo.NodeId,
						Name:     name,
						Image:    game,
						RamMb:    ram,
						CpuLimit: cpu,
						DiskMb:   disk,
						Node:     *nodeInfo,
						ServerId: serverI.Id,
					})
					if err != nil {
						log.Printf("failed create server: %s", err.Error())
						http.Error(w, "failed create server", http.StatusBadRequest)
						return
					}
				} else {
					log.Printf("failed create server: %v", doReq.StatusCode)
					http.Error(w, "failed create server", http.StatusInternalServerError)
					return
				}
			}
			if strings.TrimSpace(servId) == "" {
				log.Print("failed create server: empty server id")
				http.Error(w, "failed create server", http.StatusInternalServerError)
				return
			}
			w.Header().Set("HX-Redirect", "/server?uuid="+url.QueryEscape(servId))
			w.WriteHeader(http.StatusOK)
		})
	})
	router.Post("/addNode", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, err := getSessionValidity(r.Context(), pool, r)
		if err != nil {
			log.Printf("session validity check failed, error : %s", err.Error())
			http.Error(w, fmt.Sprintf("session validity check failed, error : %s", err.Error()), http.StatusInternalServerError)
			return
		}
		if !validSession {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		tokenMap, err := getUserInfoFromSessionToken(r.Context(), pool, r)
		if err != nil {
			log.Printf("failed to get user info from session token: %s", err.Error())
			http.Error(w, fmt.Sprintf("failed to get user info from session token: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		if tokenMap["perms"] != 1 {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		err = r.ParseForm()
		if err != nil {
			log.Printf("failed to parse form: %s", err.Error())
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		name := r.PostForm.Get("name")
		ip := r.PostForm.Get("ip")
		port := r.PostForm.Get("port")
		token := r.PostForm.Get("token")
		disk := r.PostForm.Get("disk")
		ram := r.PostForm.Get("ram")
		request, err := http.NewRequest("GET", fmt.Sprintf("http://%s:%s/ping", ip, port), nil)
		if err != nil {
			log.Printf("node did not respond: %s", err.Error())
			http.Error(w, fmt.Sprintf("node did not respond: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		request.Header.Add("Authorization", "Bearer "+token)
		do, err := http.DefaultClient.Do(request)
		if err != nil {
			log.Printf("node did not respond: %s", err.Error())
			http.Error(w, fmt.Sprintf("node did not respond: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		defer do.Body.Close()
		if do.StatusCode == http.StatusOK {
			all, err := io.ReadAll(do.Body)
			if err != nil {
				log.Printf("node did not respond")
				http.Error(w, "server did not respond correctly", http.StatusInternalServerError)
				return
			}
			if string(all) != "pong" {
				log.Printf("node did not respond")
				http.Error(w, "server did not respond correctly", http.StatusInternalServerError)
				return
			}
		} else {
			log.Printf("node did not respond: %s", err.Error())
			http.Error(w, "server did not respond correctly", http.StatusInternalServerError)
			return
		}
		_, err = createNode(r.Context(), pool, name, ip, port, token, disk, ram)
		if err != nil {
			log.Printf("failed to create node in db: %s", err.Error())
			http.Error(w, fmt.Sprintf("failed to create node in db: %s", err.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Redirect", "/admin")
		w.WriteHeader(http.StatusOK)
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
			}
			rawToken, err := createSession(r.Context(), pool, userId, middleware.GetClientIPAddr(r.Context()).String())
			if err != nil {
				log.Printf("could not create a new session %s", err.Error())
				http.Error(w, "internal server error", http.StatusInternalServerError)
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
	// Signup/login forms post
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
			http.Error(w, "internal server error", http.StatusInternalServerError)
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
	router.Post("/logout", func(w http.ResponseWriter, r *http.Request) {
		_, validSession, err := getSessionValidity(r.Context(), pool, r)
		if err != nil {
			log.Printf("session validity check failed, error : %s", err.Error())
			http.Error(w, fmt.Sprintf("session validity check failed, error : %s", err.Error()), http.StatusInternalServerError)
			return
		}
		if !validSession {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		err = logout(r.Context(), pool, r)
		if err != nil {
			log.Printf("could not logout %s", err.Error())
			http.Error(w, "internal server error. Logout failed", http.StatusInternalServerError)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "session_token",
			Value:    "",
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			Path:     "/",
		})
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
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

func createSession(ctx context.Context, pool *pgxpool.Pool, userId, ipAddr string) (string, error) {
	rawToken, tokenHash, err := generateSessionToken()
	if err != nil {
		return "", err
	}
	query := `INSERT INTO sessions (user_id, token_hash , ipaddr) VALUES ($1, $2, $3)`

	_, err = pool.Exec(ctx, query, userId, tokenHash, ipAddr)
	return rawToken, err
}

func deleteExpiredSessions(ctx context.Context, pool *pgxpool.Pool, userId string) error {
	query := `DELETE FROM sessions WHERE user_id=$1 AND expires_at < NOW()`

	_, err := pool.Exec(ctx, query, userId)
	if err != nil {
		return err
	}
	return nil
}

func createNode(ctx context.Context, pool *pgxpool.Pool, nodeName, ipAddr, port, token, totalDisk, totalRam string) (string, error) {
	query := `INSERT INTO nodes (nodeId, name , ipaddr, port, token, total_disk_mb, total_ram_mb) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	nodeId := uuid.New().String()
	_, err := pool.Exec(ctx, query, nodeId, nodeName, ipAddr, port, token, totalDisk, totalRam)
	return nodeId, err
}

func createServer(ctx context.Context, pool *pgxpool.Pool, userId, nodeName string, sInfo servers) (string, error) {
	node, err := getNode(ctx, pool, nodeName)
	if err != nil {
		return "", err
	}
	query := `INSERT INTO servers (serverid, nodeid, name, image, ram_mb, disk_mb, cpu_limit, user_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err = pool.Exec(ctx, query, sInfo.ServerId, node.NodeId, sInfo.Name, sInfo.Image, sInfo.RamMb, sInfo.DiskMb, sInfo.CpuLimit, userId)
	if err != nil {
		return "", err
	}
	return sInfo.ServerId, nil
}

func logout(ctx context.Context, pool *pgxpool.Pool, r *http.Request) error {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		return err
	}
	rawToken := cookie.Value
	tokenHash := hashToken(rawToken)
	query := `SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at > NOW()`
	var userId string
	err = pool.QueryRow(ctx, query, tokenHash).Scan(&userId)
	if err != nil {
		return err
	}
	query = `DELETE FROM sessions WHERE user_id=$1`
	_, err = pool.Exec(ctx, query, userId)
	if err != nil {
		return err
	}

	return nil
}

// table inits

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

func initNodesTable(ctx context.Context, pool *pgxpool.Pool) error {
	query := `CREATE TABLE IF NOT EXISTS nodes (
    nodeId varchar(255) PRIMARY KEY,
    name varchar(255) UNIQUE NOT NULL,
    ipaddr varchar(15) NOT NULL,
    port varchar(5) NOT NULL,
    token varchar(255) UNIQUE NOT NULL,
    total_disk_mb varchar(255) NOT NULL,
    total_ram_mb varchar(255) NOT NULL,
    min_port varchar(6) NOT NULL,
    max_port varchar(6) NOT NULL
)`
	_, err := pool.Exec(ctx, query)
	return err
}

func initServersTable(ctx context.Context, pool *pgxpool.Pool) error {
	query := `CREATE TABLE IF NOT EXISTS servers (
    serverId varchar(255) PRIMARY KEY,
    nodeId varchar(255) NOT NULL REFERENCES nodes(nodeId) ON DELETE CASCADE,
    name varchar(255) NOT NULL,
    image varchar(255) NOT NULL,
    ram_mb varchar(255) NOT NULL,
    cpu_limit varchar(255) NOT NULL,
    disk_mb varchar(255) NOT NULL,
    user_id UUID NOT NULL REFERENCES users(user_id) ON DELETE CASCADE
)`
	_, err := pool.Exec(ctx, query)
	return err
}

func initAllocationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	query := `CREATE TABLE IF NOT EXISTS allocations (
    id SERIAL PRIMARY KEY,
    serverid varchar(255) NOT NULL REFERENCES servers(serverid) ON DELETE CASCADE,
    nodeid varchar(255) NOT NULL REFERENCES nodes(nodeId) ON DELETE CASCADE,
    nodeip varchar(15) NOT NULL,
    used_port varchar(5) NOT NULL,
    UNIQUE (nodeid, nodeip, used_port)
)`

	_, err := pool.Exec(ctx, query)
	return err
}

// getSmth funcs

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
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return userId, true, nil
}

func getUserInfoFromSessionToken(ctx context.Context, pool *pgxpool.Pool, r *http.Request) (map[string]any, error) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		return nil, err
	}
	rawToken := cookie.Value
	tokenHash := hashToken(rawToken)
	query := `SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at > NOW()`
	var userId string
	err = pool.QueryRow(ctx, query, tokenHash).Scan(&userId)
	if err != nil {
		return nil, err
	}
	query = `SELECT username,perms,created_at FROM users WHERE user_id=$1`
	var username string
	var perms int
	var createdAt pgtype.Timestamp
	err = pool.QueryRow(ctx, query, userId).Scan(&username, &perms, &createdAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"username":  username,
		"userId":    userId,
		"perms":     perms,
		"createdAt": createdAt,
	}, nil
}

func getUser(ctx context.Context, pool *pgxpool.Pool, username string) (string, string, error) {
	query := `SELECT user_id, password_hash FROM users WHERE username=$1;`
	var pswdHash string
	var userId uuid.UUID
	err := pool.QueryRow(ctx, query, username).Scan(&userId, &pswdHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", fmt.Errorf("user not found")
		}
		return "", "", err
	}
	return userId.String(), pswdHash, nil
}

func getNodes(ctx context.Context, pool *pgxpool.Pool) ([]nodes, error) {
	query := `SELECT nodeId, name, ipaddr, port, total_disk_mb, total_ram_mb FROM nodes`
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	nodesList, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[nodes])
	if err != nil {
		return nil, err
	}
	return nodesList, nil
}

func getNode(ctx context.Context, pool *pgxpool.Pool, nodeName string) (*nodes, error) {
	query := `SELECT nodeId, name, ipaddr, port, total_disk_mb, total_ram_mb, token FROM nodes WHERE name = $1`
	rows, err := pool.Query(ctx, query, nodeName)
	if err != nil {
		return nil, err
	}
	node, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[nodes])
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func getNodeById(ctx context.Context, pool *pgxpool.Pool, nodeId string) (*nodes, error) {
	query := `SELECT nodeId, name, ipaddr, port, total_disk_mb, total_ram_mb, token FROM nodes WHERE nodeId = $1`
	rows, err := pool.Query(ctx, query, nodeId)
	if err != nil {
		return nil, err
	}
	node, err := pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[nodes])
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func getUserServers(ctx context.Context, pool *pgxpool.Pool, userId string) ([]servers, error) {
	query := `SELECT serverId, nodeId, name, image, ram_mb, cpu_limit, disk_mb FROM servers WHERE user_id = $1`
	rows, err := pool.Query(ctx, query, userId)
	if err != nil {
		return nil, err
	}
	serversList, err := pgx.CollectRows(rows, pgx.RowToStructByName[servers])
	if err != nil {
		return nil, err
	}
	var newServersList []servers
	for _, s := range serversList {
		query = `SELECT nodeid, name, ipaddr FROM nodes WHERE nodeid = $1`
		rows, err = pool.Query(ctx, query, s.NodeId)
		if err != nil {
			return nil, err
		}
		nodesList, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[nodes])
		if err != nil {
			return nil, err
		}
		cpuLimit, err := strconv.ParseFloat(s.CpuLimit, 32)
		if err != nil {
			return nil, err
		}
		if len(nodesList) > 0 {
			s.Node = nodesList[0]
		} else {
			s.Node = nodes{}
		}
		newCpuLimit := cpuLimit * 100
		percentCpuLimit := fmt.Sprintf("%.0f%%", newCpuLimit)
		s.CpuLimit = percentCpuLimit
		newServersList = append(newServersList, s)
	}
	return newServersList, nil
}

func getServer(ctx context.Context, pool *pgxpool.Pool, serverId, userId string) (servers, error) {
	query := `SELECT nodeid, name, image, ram_mb, cpu_limit, disk_mb, serverid FROM servers WHERE serverid = $1 AND user_id = $2`
	var currentServer servers
	err := pool.QueryRow(ctx, query, serverId, userId).Scan(
		&currentServer.NodeId,
		&currentServer.Name,
		&currentServer.Image,
		&currentServer.RamMb,
		&currentServer.CpuLimit,
		&currentServer.DiskMb,
		&currentServer.ServerId,
	)
	if err != nil {
		return servers{}, err
	}
	return currentServer, nil
}

// helpers

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

func getFreePort(ctx context.Context, pool *pgxpool.Pool, nodeId string) (string, error) {
	query := `SELECT used_port FROM allocations WHERE nodeid = $1`
	rows, err := pool.Query(ctx, query, nodeId)
	if err != nil {
		return "0", err
	}
	uPorts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "0", err
	}
	query = `SELECT min_port, max_port FROM nodes WHERE nodeid = $1`
	var minPort, maxPort string
	err = pool.QueryRow(ctx, query, nodeId).Scan(&minPort, &maxPort)
	if err != nil {
		return "0", err
	}
	iminPort, err := strconv.Atoi(minPort)
	if err != nil {
		return "0", err
	}
	imaxPort, err := strconv.Atoi(maxPort)
	if err != nil {
		return "0", err
	}
	var intUPorts []int
	for _, p := range uPorts {
		usedPort, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		intUPorts = append(intUPorts, usedPort)
	}
	var freePort int
	for port := iminPort; port <= imaxPort; port++ {
		if !slices.Contains(intUPorts, port) {
			freePort = port
			break
		}
	}
	return strconv.Itoa(freePort), nil
}
