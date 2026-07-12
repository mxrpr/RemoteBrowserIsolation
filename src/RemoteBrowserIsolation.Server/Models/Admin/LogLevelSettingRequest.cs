namespace RemoteBrowserIsolation.Server.Models.Admin;

// PUT /api/admin/settings/log-level body — sets the whole-server minimum log level.
public sealed record LogLevelSettingRequest(LogLevel Level);
