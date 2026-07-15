// Package config loads and holds all server configuration, mirroring the key
// names and shape from the C# appsettings.json so the same config concepts
// can be shared across both backends.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LoggingConfig holds log-level settings matching the appsettings.json Logging section.
type LoggingConfig struct {
	LogLevel struct {
		Default string `json:"Default"`
	} `json:"LogLevel"`
}

// FFmpegConfig holds the path to the FFmpeg native libraries.
type FFmpegConfig struct {
	// LibPath is the directory containing FFmpeg shared libraries (libavcodec, etc.).
	LibPath string `json:"LibPath"`
}

// ConnectionStringsConfig holds database connection strings.
type ConnectionStringsConfig struct {
	// Sqlite is the SQLite connection string (e.g. "Data Source=rbi-go.db").
	Sqlite string `json:"Sqlite"`
}

// JwtConfig holds the signing key and token parameters for HS256 JWT auth.
type JwtConfig struct {
	Key        string `json:"Key"`
	Issuer     string `json:"Issuer"`
	Audience   string `json:"Audience"`
	TtlMinutes int    `json:"TtlMinutes"`
}

// ProxyConfig holds settings for the TLS-intercepting forward proxy listener.
type ProxyConfig struct {
	Port           int      `json:"Port"`
	Bind           string   `json:"Bind"`
	InterceptPorts []int    `json:"InterceptPorts"`
	SelfHosts      []string `json:"SelfHosts"`
}

// WebRtcConfig holds ICE/media transport settings for the WebRTC session manager.
type WebRtcConfig struct {
	AdvertisedIp string `json:"AdvertisedIp"`
	UdpPortStart int    `json:"UdpPortStart"`
	UdpPortEnd   int    `json:"UdpPortEnd"`
}

// BrowserConfig holds settings for the headless Chromium session manager.
type BrowserConfig struct {
	// ChromiumPath is the absolute path to the Chromium executable. If empty,
	// chromedp auto-detects the binary using the system PATH (suitable for both
	// dev environments and Docker images where chromium is on PATH).
	ChromiumPath string `json:"ChromiumPath"`
}

// Config is the top-level configuration struct. Fields mirror the C# appsettings.json
// sections so both backends share the same conceptual config shape.
type Config struct {
	// Port is the HTTP server listen port (not the proxy port).
	Port int `json:"Port"`
	// WwwRoot is the path to the directory of static files to serve.
	WwwRoot string `json:"WwwRoot"`

	Logging           LoggingConfig           `json:"Logging"`
	FFmpeg            FFmpegConfig            `json:"FFmpeg"`
	ConnectionStrings ConnectionStringsConfig  `json:"ConnectionStrings"`
	Jwt               JwtConfig               `json:"Jwt"`
	Proxy             ProxyConfig             `json:"Proxy"`
	WebRtc            WebRtcConfig            `json:"WebRtc"`
	Browser           BrowserConfig           `json:"Browser"`
}

// defaults returns a Config populated with the same defaults as the C# appsettings.json.
func defaults() Config {
	return Config{
		Port:    5139,
		WwwRoot: "../RemoteBrowserIsolation.Server/wwwroot",
		Logging: LoggingConfig{
			LogLevel: struct {
				Default string `json:"Default"`
			}{Default: "Information"},
		},
		FFmpeg: FFmpegConfig{
			LibPath: "",
		},
		ConnectionStrings: ConnectionStringsConfig{
			Sqlite: "Data Source=rbi-go.db",
		},
		Jwt: JwtConfig{
			Key:        "CHANGE_ME_dev_only_signing_key_at_least_32_bytes_long",
			Issuer:     "RemoteBrowserIsolation.Server",
			Audience:   "RemoteBrowserIsolation.Admin",
			TtlMinutes: 60,
		},
		Proxy: ProxyConfig{
			Port:           8080,
			Bind:           "127.0.0.1",
			InterceptPorts: []int{443},
			SelfHosts:      []string{"localhost", "127.0.0.1"},
		},
		WebRtc: WebRtcConfig{
			AdvertisedIp: "127.0.0.1",
			UdpPortStart: 40000,
			UdpPortEnd:   40009,
		},
		Browser: BrowserConfig{
			ChromiumPath: "", // empty = auto-detect via system PATH
		},
	}
}

// Load reads configuration from an optional JSON file and then applies
// environment variable overrides. The JSON file path is taken from the
// RBI_CONFIG_FILE env var, defaulting to "config.json" in the working directory.
// Missing config file is silently ignored (defaults are used). Env var names
// follow the pattern RBI_<SECTION>_<KEY> (e.g. RBI_JWT_KEY, RBI_PROXY_PORT).
func Load() (*Config, error) {
	cfg := defaults()

	// Determine config file path.
	cfgFile := os.Getenv("RBI_CONFIG_FILE")
	if cfgFile == "" {
		cfgFile = "config.json"
	}

	// Read and parse the JSON config file if it exists.
	if data, err := os.ReadFile(cfgFile); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", cfgFile, err)
		}
	}

	// Apply environment variable overrides.
	applyEnvOverrides(&cfg)

	return &cfg, nil
}

// applyEnvOverrides overwrites fields in cfg with values from environment variables.
// Each env var is optional; absent vars leave the config field unchanged.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("RBI_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	if v := os.Getenv("RBI_WWWROOT"); v != "" {
		cfg.WwwRoot = v
	}
	if v := os.Getenv("RBI_LOG_LEVEL"); v != "" {
		cfg.Logging.LogLevel.Default = v
	}
	if v := os.Getenv("RBI_FFMPEG_LIB_PATH"); v != "" {
		cfg.FFmpeg.LibPath = v
	}
	if v := os.Getenv("RBI_DB_PATH"); v != "" {
		cfg.ConnectionStrings.Sqlite = v
	}
	if v := os.Getenv("RBI_JWT_KEY"); v != "" {
		cfg.Jwt.Key = v
	}
	if v := os.Getenv("RBI_JWT_ISSUER"); v != "" {
		cfg.Jwt.Issuer = v
	}
	if v := os.Getenv("RBI_JWT_AUDIENCE"); v != "" {
		cfg.Jwt.Audience = v
	}
	if v := os.Getenv("RBI_JWT_TTL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Jwt.TtlMinutes = n
		}
	}
	if v := os.Getenv("RBI_PROXY_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Proxy.Port = n
		}
	}
	if v := os.Getenv("RBI_PROXY_BIND"); v != "" {
		cfg.Proxy.Bind = v
	}
	// RBI_PROXY_INTERCEPT_PORTS accepts a comma-separated list of port numbers,
	// e.g. "443,8443". Invalid tokens are silently skipped.
	if v := os.Getenv("RBI_PROXY_INTERCEPT_PORTS"); v != "" {
		var ports []int
		for _, tok := range strings.Split(v, ",") {
			tok = strings.TrimSpace(tok)
			if n, err := strconv.Atoi(tok); err == nil {
				ports = append(ports, n)
			}
		}
		if len(ports) > 0 {
			cfg.Proxy.InterceptPorts = ports
		}
	}
	// RBI_PROXY_SELF_HOSTS accepts a comma-separated list of hostnames/IPs,
	// e.g. "localhost,127.0.0.1,myhost".
	if v := os.Getenv("RBI_PROXY_SELF_HOSTS"); v != "" {
		var hosts []string
		for _, tok := range strings.Split(v, ",") {
			if h := strings.TrimSpace(tok); h != "" {
				hosts = append(hosts, h)
			}
		}
		if len(hosts) > 0 {
			cfg.Proxy.SelfHosts = hosts
		}
	}
	if v := os.Getenv("RBI_WEBRTC_ADVERTISED_IP"); v != "" {
		cfg.WebRtc.AdvertisedIp = v
	}
	if v := os.Getenv("RBI_WEBRTC_UDP_PORT_START"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.WebRtc.UdpPortStart = n
		}
	}
	if v := os.Getenv("RBI_WEBRTC_UDP_PORT_END"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.WebRtc.UdpPortEnd = n
		}
	}
	if v := os.Getenv("RBI_BROWSER_CHROMIUM_PATH"); v != "" {
		cfg.Browser.ChromiumPath = v
	}
}
