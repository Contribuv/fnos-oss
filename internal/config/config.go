package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func bytesTrimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

type Config struct {
	ListenHost     string `json:"listen_host"`
	Port           int    `json:"port"`
	SocketPath     string `json:"socket_path"`
	GatewayPrefix  string `json:"gateway_prefix"`
	DataDir        string `json:"data_dir"`
	Region         string `json:"region"`
	PublicEndpoint string `json:"public_endpoint"`
	LogLevel       string `json:"log_level"`
	MaxUploadSize  int64  `json:"max_upload_size"`
	DevMode        bool   `json:"dev_mode"`
}

func Default() Config {
	return Config{
		ListenHost:    "0.0.0.0",
		Port:          9100,
		GatewayPrefix: "/app/fnos-oss",
		DataDir:       "./data",
		Region:        "us-east-1",
		LogLevel:      "info",
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return cfg, err
			}
		} else if err := json.Unmarshal(bytesTrimBOM(raw), &cfg); err != nil {
			return cfg, err
		}
	}
	applyEnv(&cfg)
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		cfg.Port = 9100
	}
	cfg.GatewayPrefix = "/" + strings.Trim(cfg.GatewayPrefix, "/")
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("OSS_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	if v := os.Getenv("OSS_SOCKET"); v != "" {
		cfg.SocketPath = v
	}
	if v := os.Getenv("OSS_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("OSS_GATEWAY_PREFIX"); v != "" {
		cfg.GatewayPrefix = v
	}
	if v := os.Getenv("OSS_REGION"); v != "" {
		cfg.Region = v
	}
	if v := os.Getenv("OSS_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("OSS_DEV"); v == "1" || strings.EqualFold(v, "true") {
		cfg.DevMode = true
	}
}

// ConfigPath returns the config file location used by the fnOS package scripts.
func ConfigPath() string {
	if v := os.Getenv("TRIM_PKGETC"); v != "" {
		return filepath.Join(v, "oss.json")
	}
	return "oss.json"
}
