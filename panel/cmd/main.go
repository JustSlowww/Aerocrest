package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/ini.v1"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	appDir := filepath.Join("/etc/aerocrest/")
	_, err := os.Stat(filepath.Join(appDir, "aerocrest.cfg"))
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("Please Run sudo ./setup.sh first")
		}
		logger.Error("Failed to load config", "error", err)
	}

	cfgFile, err := ini.Load(filepath.Join(appDir, "aerocrest.cfg"))
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	port := cfgFile.Section("").Key("port").String()

	// dbConfig
	dbPass := cfgFile.Section("").Key("dbPass").String()
	dbName := cfgFile.Section("").Key("dbName").String()
	dbPort := cfgFile.Section("").Key("dbPort").String()

	dbCfg := dbConfig{
		dbPass: dbPass,
		dbName: dbName,
		dbPort: dbPort,
	}
	cfg := config{
		addr:     ":" + port,
		wTimeout: 120 * time.Second,
		rTimeout: 60 * time.Second,
	}

	app := api{config: cfg, dbConfig: dbCfg}
	h := app.Mount()
	err = app.Run(h)
	if err != nil {
		logger.Error("Failed to run server", "error", err)
		os.Exit(1)
	}
}
