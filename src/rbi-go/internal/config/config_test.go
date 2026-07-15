package config

import (
	"os"
	"slices"
	"testing"
)

// TestLoad_Defaults_Port verifies that the default port is 5139 when no config file or env vars are set.
func TestLoad_Defaults_Port(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	t.Setenv("RBI_PORT", "")
	t.Setenv("RBI_WWWROOT", "")
	t.Setenv("RBI_LOG_LEVEL", "")
	t.Setenv("RBI_FFMPEG_LIB_PATH", "")
	t.Setenv("RBI_DB_PATH", "")
	t.Setenv("RBI_JWT_KEY", "")
	t.Setenv("RBI_JWT_ISSUER", "")
	t.Setenv("RBI_JWT_AUDIENCE", "")
	t.Setenv("RBI_JWT_TTL_MINUTES", "")
	t.Setenv("RBI_PROXY_PORT", "")
	t.Setenv("RBI_PROXY_BIND", "")
	t.Setenv("RBI_PROXY_INTERCEPT_PORTS", "")
	t.Setenv("RBI_PROXY_SELF_HOSTS", "")
	t.Setenv("RBI_WEBRTC_ADVERTISED_IP", "")
	t.Setenv("RBI_WEBRTC_UDP_PORT_START", "")
	t.Setenv("RBI_WEBRTC_UDP_PORT_END", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Port != 5139 {
		t.Errorf("Expected Port=5139, got %d", cfg.Port)
	}
}

// TestLoad_Defaults_WwwRoot verifies the default WwwRoot path.
func TestLoad_Defaults_WwwRoot(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.WwwRoot != "../RemoteBrowserIsolation.Server/wwwroot" {
		t.Errorf("Expected default WwwRoot, got %s", cfg.WwwRoot)
	}
}

// TestLoad_Defaults_JwtFields verifies default JWT configuration.
func TestLoad_Defaults_JwtFields(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Jwt.Key != "CHANGE_ME_dev_only_signing_key_at_least_32_bytes_long" {
		t.Errorf("Expected default JWT Key, got %s", cfg.Jwt.Key)
	}
	if cfg.Jwt.Issuer != "RemoteBrowserIsolation.Server" {
		t.Errorf("Expected default JWT Issuer, got %s", cfg.Jwt.Issuer)
	}
	if cfg.Jwt.Audience != "RemoteBrowserIsolation.Admin" {
		t.Errorf("Expected default JWT Audience, got %s", cfg.Jwt.Audience)
	}
	if cfg.Jwt.TtlMinutes != 60 {
		t.Errorf("Expected default JWT TtlMinutes=60, got %d", cfg.Jwt.TtlMinutes)
	}
}

// TestLoad_Defaults_ProxySlices verifies default Proxy InterceptPorts and SelfHosts.
func TestLoad_Defaults_ProxySlices(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.InterceptPorts) != 1 || cfg.Proxy.InterceptPorts[0] != 443 {
		t.Errorf("Expected default InterceptPorts=[443], got %v", cfg.Proxy.InterceptPorts)
	}
	if len(cfg.Proxy.SelfHosts) != 2 || !slices.Contains(cfg.Proxy.SelfHosts, "localhost") ||
		!slices.Contains(cfg.Proxy.SelfHosts, "127.0.0.1") {
		t.Errorf("Expected default SelfHosts=[localhost, 127.0.0.1], got %v", cfg.Proxy.SelfHosts)
	}
}

// TestLoad_Defaults_WebRtcPorts verifies default WebRTC configuration.
func TestLoad_Defaults_WebRtcPorts(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.WebRtc.AdvertisedIp != "127.0.0.1" {
		t.Errorf("Expected AdvertisedIp=127.0.0.1, got %s", cfg.WebRtc.AdvertisedIp)
	}
	if cfg.WebRtc.UdpPortStart != 40000 {
		t.Errorf("Expected UdpPortStart=40000, got %d", cfg.WebRtc.UdpPortStart)
	}
	if cfg.WebRtc.UdpPortEnd != 40009 {
		t.Errorf("Expected UdpPortEnd=40009, got %d", cfg.WebRtc.UdpPortEnd)
	}
}

// TestLoad_JSONFileOverridesPort verifies that a JSON file overrides the Port default.
func TestLoad_JSONFileOverridesPort(t *testing.T) {
	dir := t.TempDir()
	cfgFile := dir + "/config.json"
	t.Setenv("RBI_CONFIG_FILE", cfgFile)
	clearAllEnvs(t)

	jsonContent := `{"Port": 9999}`
	if err := os.WriteFile(cfgFile, []byte(jsonContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Expected Port=9999 from JSON, got %d", cfg.Port)
	}
}

// TestLoad_JSONFileOverridesNestedField verifies that JSON overrides nested fields like Jwt.Key.
func TestLoad_JSONFileOverridesNestedField(t *testing.T) {
	dir := t.TempDir()
	cfgFile := dir + "/config.json"
	t.Setenv("RBI_CONFIG_FILE", cfgFile)
	clearAllEnvs(t)

	jsonContent := `{"Jwt": {"Key": "custom-jwt-key-from-json"}}`
	if err := os.WriteFile(cfgFile, []byte(jsonContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Jwt.Key != "custom-jwt-key-from-json" {
		t.Errorf("Expected Jwt.Key from JSON, got %s", cfg.Jwt.Key)
	}
}

// TestLoad_MissingJSONFileIsIgnored verifies that missing config file doesn't error; defaults are used.
func TestLoad_MissingJSONFileIsIgnored(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should succeed for missing file, got error: %v", err)
	}
	if cfg.Port != 5139 {
		t.Errorf("Expected default Port after missing JSON, got %d", cfg.Port)
	}
}

// TestLoad_MalformedJSONReturnsError verifies that invalid JSON causes an error.
func TestLoad_MalformedJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfgFile := dir + "/config.json"
	t.Setenv("RBI_CONFIG_FILE", cfgFile)
	clearAllEnvs(t)

	jsonContent := `{"Port": 9999 INVALID JSON`
	if err := os.WriteFile(cfgFile, []byte(jsonContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatalf("Load() should fail for malformed JSON, got nil error")
	}
}

// TestLoad_EnvPort_Scalar verifies RBI_PORT env override works.
func TestLoad_EnvPort_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PORT", "7777")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Port != 7777 {
		t.Errorf("Expected Port=7777 from env, got %d", cfg.Port)
	}
}

// TestLoad_EnvWwwRoot_Scalar verifies RBI_WWWROOT env override works.
func TestLoad_EnvWwwRoot_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_WWWROOT", "/custom/wwwroot")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.WwwRoot != "/custom/wwwroot" {
		t.Errorf("Expected WwwRoot=/custom/wwwroot, got %s", cfg.WwwRoot)
	}
}

// TestLoad_EnvLogLevel_Scalar verifies RBI_LOG_LEVEL env override works.
func TestLoad_EnvLogLevel_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_LOG_LEVEL", "Debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Logging.LogLevel.Default != "Debug" {
		t.Errorf("Expected LogLevel=Debug, got %s", cfg.Logging.LogLevel.Default)
	}
}

// TestLoad_EnvFFmpegLibPath_Scalar verifies RBI_FFMPEG_LIB_PATH env override works.
func TestLoad_EnvFFmpegLibPath_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_FFMPEG_LIB_PATH", "/usr/local/lib/ffmpeg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.FFmpeg.LibPath != "/usr/local/lib/ffmpeg" {
		t.Errorf("Expected LibPath=/usr/local/lib/ffmpeg, got %s", cfg.FFmpeg.LibPath)
	}
}

// TestLoad_EnvDBPath_Scalar verifies RBI_DB_PATH env override works.
func TestLoad_EnvDBPath_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_DB_PATH", "custom.db")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.ConnectionStrings.Sqlite != "custom.db" {
		t.Errorf("Expected Sqlite=custom.db, got %s", cfg.ConnectionStrings.Sqlite)
	}
}

// TestLoad_EnvJwtKey_Scalar verifies RBI_JWT_KEY env override works.
func TestLoad_EnvJwtKey_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_JWT_KEY", "custom-key-64-bytes-long-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Jwt.Key != "custom-key-64-bytes-long-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" {
		t.Errorf("Expected custom JWT Key, got %s", cfg.Jwt.Key)
	}
}

// TestLoad_EnvJwtIssuer_Scalar verifies RBI_JWT_ISSUER env override works.
func TestLoad_EnvJwtIssuer_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_JWT_ISSUER", "custom-issuer")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Jwt.Issuer != "custom-issuer" {
		t.Errorf("Expected Issuer=custom-issuer, got %s", cfg.Jwt.Issuer)
	}
}

// TestLoad_EnvJwtAudience_Scalar verifies RBI_JWT_AUDIENCE env override works.
func TestLoad_EnvJwtAudience_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_JWT_AUDIENCE", "custom-audience")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Jwt.Audience != "custom-audience" {
		t.Errorf("Expected Audience=custom-audience, got %s", cfg.Jwt.Audience)
	}
}

// TestLoad_EnvJwtTTLValid_Scalar verifies RBI_JWT_TTL_MINUTES with valid number works.
func TestLoad_EnvJwtTTLValid_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_JWT_TTL_MINUTES", "120")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Jwt.TtlMinutes != 120 {
		t.Errorf("Expected TtlMinutes=120, got %d", cfg.Jwt.TtlMinutes)
	}
}

// TestLoad_EnvJwtTTLInvalid_SkippedSilently verifies that invalid RBI_JWT_TTL_MINUTES is silently skipped.
func TestLoad_EnvJwtTTLInvalid_SkippedSilently(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_JWT_TTL_MINUTES", "not-a-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Jwt.TtlMinutes != 60 {
		t.Errorf("Expected TtlMinutes to remain default (60) after invalid env, got %d", cfg.Jwt.TtlMinutes)
	}
}

// TestLoad_EnvProxyPort_Scalar verifies RBI_PROXY_PORT env override works.
func TestLoad_EnvProxyPort_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_PORT", "8888")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Proxy.Port != 8888 {
		t.Errorf("Expected Proxy.Port=8888, got %d", cfg.Proxy.Port)
	}
}

// TestLoad_EnvProxyBind_Scalar verifies RBI_PROXY_BIND env override works.
func TestLoad_EnvProxyBind_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_BIND", "0.0.0.0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Proxy.Bind != "0.0.0.0" {
		t.Errorf("Expected Proxy.Bind=0.0.0.0, got %s", cfg.Proxy.Bind)
	}
}

// TestLoad_EnvWebRtcAdvertisedIP_Scalar verifies RBI_WEBRTC_ADVERTISED_IP env override works.
func TestLoad_EnvWebRtcAdvertisedIP_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_WEBRTC_ADVERTISED_IP", "192.168.1.1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.WebRtc.AdvertisedIp != "192.168.1.1" {
		t.Errorf("Expected AdvertisedIp=192.168.1.1, got %s", cfg.WebRtc.AdvertisedIp)
	}
}

// TestLoad_EnvWebRtcUdpPortStart_Scalar verifies RBI_WEBRTC_UDP_PORT_START env override works.
func TestLoad_EnvWebRtcUdpPortStart_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_WEBRTC_UDP_PORT_START", "50000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.WebRtc.UdpPortStart != 50000 {
		t.Errorf("Expected UdpPortStart=50000, got %d", cfg.WebRtc.UdpPortStart)
	}
}

// TestLoad_EnvWebRtcUdpPortEnd_Scalar verifies RBI_WEBRTC_UDP_PORT_END env override works.
func TestLoad_EnvWebRtcUdpPortEnd_Scalar(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_WEBRTC_UDP_PORT_END", "50009")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.WebRtc.UdpPortEnd != 50009 {
		t.Errorf("Expected UdpPortEnd=50009, got %d", cfg.WebRtc.UdpPortEnd)
	}
}

// TestLoad_ProxyInterceptPorts_Single verifies RBI_PROXY_INTERCEPT_PORTS with a single port.
func TestLoad_ProxyInterceptPorts_Single(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_INTERCEPT_PORTS", "8443")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.InterceptPorts) != 1 || cfg.Proxy.InterceptPorts[0] != 8443 {
		t.Errorf("Expected InterceptPorts=[8443], got %v", cfg.Proxy.InterceptPorts)
	}
}

// TestLoad_ProxyInterceptPorts_Multiple verifies RBI_PROXY_INTERCEPT_PORTS with multiple ports.
func TestLoad_ProxyInterceptPorts_Multiple(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_INTERCEPT_PORTS", "443,8443,9443")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.InterceptPorts) != 3 || !slices.Contains(cfg.Proxy.InterceptPorts, 443) ||
		!slices.Contains(cfg.Proxy.InterceptPorts, 8443) || !slices.Contains(cfg.Proxy.InterceptPorts, 9443) {
		t.Errorf("Expected InterceptPorts with 443,8443,9443, got %v", cfg.Proxy.InterceptPorts)
	}
}

// TestLoad_ProxyInterceptPorts_Whitespace verifies that whitespace around ports is trimmed.
func TestLoad_ProxyInterceptPorts_Whitespace(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_INTERCEPT_PORTS", "  443  ,  8443  ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.InterceptPorts) != 2 || !slices.Contains(cfg.Proxy.InterceptPorts, 443) ||
		!slices.Contains(cfg.Proxy.InterceptPorts, 8443) {
		t.Errorf("Expected InterceptPorts=[443,8443] after whitespace trimming, got %v", cfg.Proxy.InterceptPorts)
	}
}

// TestLoad_ProxyInterceptPorts_InvalidTokensSkipped verifies that non-numeric tokens are skipped.
func TestLoad_ProxyInterceptPorts_InvalidTokensSkipped(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_INTERCEPT_PORTS", "443,invalid,8443")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.InterceptPorts) != 2 || !slices.Contains(cfg.Proxy.InterceptPorts, 443) ||
		!slices.Contains(cfg.Proxy.InterceptPorts, 8443) {
		t.Errorf("Expected InterceptPorts=[443,8443] with invalid skipped, got %v", cfg.Proxy.InterceptPorts)
	}
}

// TestLoad_ProxyInterceptPorts_AllInvalidPreservesDefault verifies that all-invalid tokens preserve the default.
func TestLoad_ProxyInterceptPorts_AllInvalidPreservesDefault(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_INTERCEPT_PORTS", "invalid,invalid2,invalid3")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.InterceptPorts) != 1 || cfg.Proxy.InterceptPorts[0] != 443 {
		t.Errorf("Expected default InterceptPorts=[443] when all tokens invalid, got %v", cfg.Proxy.InterceptPorts)
	}
}

// TestLoad_ProxySelfHosts_Single verifies RBI_PROXY_SELF_HOSTS with a single host.
func TestLoad_ProxySelfHosts_Single(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_SELF_HOSTS", "localhost")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.SelfHosts) != 1 || cfg.Proxy.SelfHosts[0] != "localhost" {
		t.Errorf("Expected SelfHosts=[localhost], got %v", cfg.Proxy.SelfHosts)
	}
}

// TestLoad_ProxySelfHosts_Multiple verifies RBI_PROXY_SELF_HOSTS with multiple hosts.
func TestLoad_ProxySelfHosts_Multiple(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_SELF_HOSTS", "localhost,127.0.0.1,myhost")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.SelfHosts) != 3 || !slices.Contains(cfg.Proxy.SelfHosts, "localhost") ||
		!slices.Contains(cfg.Proxy.SelfHosts, "127.0.0.1") || !slices.Contains(cfg.Proxy.SelfHosts, "myhost") {
		t.Errorf("Expected SelfHosts with localhost,127.0.0.1,myhost, got %v", cfg.Proxy.SelfHosts)
	}
}

// TestLoad_ProxySelfHosts_Whitespace verifies that whitespace around hosts is trimmed.
func TestLoad_ProxySelfHosts_Whitespace(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_SELF_HOSTS", "  localhost  ,  127.0.0.1  ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.SelfHosts) != 2 || !slices.Contains(cfg.Proxy.SelfHosts, "localhost") ||
		!slices.Contains(cfg.Proxy.SelfHosts, "127.0.0.1") {
		t.Errorf("Expected SelfHosts=[localhost,127.0.0.1] after whitespace trimming, got %v", cfg.Proxy.SelfHosts)
	}
}

// TestLoad_ProxySelfHosts_EmptyTokensDropped verifies that empty tokens are dropped.
func TestLoad_ProxySelfHosts_EmptyTokensDropped(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_SELF_HOSTS", "localhost,,127.0.0.1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.SelfHosts) != 2 || !slices.Contains(cfg.Proxy.SelfHosts, "localhost") ||
		!slices.Contains(cfg.Proxy.SelfHosts, "127.0.0.1") {
		t.Errorf("Expected SelfHosts=[localhost,127.0.0.1] with empty dropped, got %v", cfg.Proxy.SelfHosts)
	}
}

// TestLoad_ProxySelfHosts_AllEmptyPreservesDefault verifies that all-empty tokens preserve the default.
func TestLoad_ProxySelfHosts_AllEmptyPreservesDefault(t *testing.T) {
	t.Setenv("RBI_CONFIG_FILE", "/nonexistent/path/config.json")
	clearAllEnvs(t)
	t.Setenv("RBI_PROXY_SELF_HOSTS", ",,")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(cfg.Proxy.SelfHosts) != 2 || !slices.Contains(cfg.Proxy.SelfHosts, "localhost") ||
		!slices.Contains(cfg.Proxy.SelfHosts, "127.0.0.1") {
		t.Errorf("Expected default SelfHosts=[localhost,127.0.0.1] when all empty, got %v", cfg.Proxy.SelfHosts)
	}
}

// TestLoad_EnvOverrideWinsOverJSONFile verifies that env var takes precedence over JSON file.
func TestLoad_EnvOverrideWinsOverJSONFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := dir + "/config.json"
	t.Setenv("RBI_CONFIG_FILE", cfgFile)
	clearAllEnvs(t)
	t.Setenv("RBI_PORT", "8888")

	jsonContent := `{"Port": 7777}`
	if err := os.WriteFile(cfgFile, []byte(jsonContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Port != 8888 {
		t.Errorf("Expected env override (8888) to win over JSON (7777), got %d", cfg.Port)
	}
}

// clearAllEnvs clears all RBI-related environment variables used in overrides.
func clearAllEnvs(t *testing.T) {
	envVars := []string{
		"RBI_PORT", "RBI_WWWROOT", "RBI_LOG_LEVEL", "RBI_FFMPEG_LIB_PATH",
		"RBI_DB_PATH", "RBI_JWT_KEY", "RBI_JWT_ISSUER", "RBI_JWT_AUDIENCE",
		"RBI_JWT_TTL_MINUTES", "RBI_PROXY_PORT", "RBI_PROXY_BIND",
		"RBI_PROXY_INTERCEPT_PORTS", "RBI_PROXY_SELF_HOSTS",
		"RBI_WEBRTC_ADVERTISED_IP", "RBI_WEBRTC_UDP_PORT_START", "RBI_WEBRTC_UDP_PORT_END",
	}
	for _, v := range envVars {
		t.Setenv(v, "")
	}
}
