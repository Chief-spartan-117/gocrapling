package spider

import (
	"encoding/json"
	"errors"
	"os"
)

type checkpoint struct {
	Spider   string     `json:"spider"`
	Requests []*Request `json:"requests"`
	Seen     []string   `json:"seen"`
}

func saveCheckpoint(path string, c checkpoint) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic: never leaves a torn file
}

func loadCheckpoint(path string) (*checkpoint, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c checkpoint
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
