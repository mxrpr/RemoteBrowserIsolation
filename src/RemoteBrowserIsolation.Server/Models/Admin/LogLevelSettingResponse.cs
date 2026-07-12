namespace RemoteBrowserIsolation.Server.Models.Admin;

// Full read-side shape returned by GET/PUT /api/admin/settings/log-level.
public sealed record LogLevelSettingResponse(LogLevel Level);
