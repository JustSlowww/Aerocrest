package rims

import (
	"embed"
	"encoding/json"
	"io/fs"
)

//go:embed configs/*.json
var configs embed.FS

type Rim struct {
	Name           string   `json:"name"`
	Image          string   `json:"image"`
	Env            []string `json:"env"`
	StartupCommand string   `json:"startup_command"`
}

func LoadConfigs() ([]Rim, error) {
	entries, err := fs.ReadDir(configs, "configs")
	if err != nil {
		return nil, err
	}
	var rims []Rim
	for _, e := range entries {
		var rim Rim
		file, err := configs.ReadFile("configs/" + e.Name())
		if err != nil {
			return nil, err
		}
		err = json.Unmarshal(file, &rim)
		if err != nil {
			return nil, err
		}
		rims = append(rims, rim)
	}
	return rims, nil
}
