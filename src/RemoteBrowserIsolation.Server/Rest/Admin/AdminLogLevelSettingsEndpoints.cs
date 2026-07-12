using RemoteBrowserIsolation.Server.Models.Admin;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Rest.Admin;

// Admin-only read/write for the whole-server minimum log level -- see LogLevelSettingsStore and
// LogLevelState for how a change takes effect immediately (no restart) by overriding the
// appsettings.json Logging:LogLevel config entirely, not just adding a floor on top of it.
public static class AdminLogLevelSettingsEndpoints
{
    // Registers GET/PUT /api/admin/settings/log-level.
    public static void MapAdminLogLevelSettingsEndpoints(this WebApplication app)
    {
        RouteGroupBuilder group = app.MapGroup("/api/admin/settings/log-level").RequireAuthorization();

        group.MapGet("", async (ILogLevelSettingsStore store) =>
        {
            LogLevel level = await store.GetLevelAsync();
            return Results.Ok(new LogLevelSettingResponse(level));
        });

        group.MapPut("", async (LogLevelSettingRequest request, ILogLevelSettingsStore store) =>
        {
            await store.SetLevelAsync(request.Level);
            return Results.Ok(new LogLevelSettingResponse(request.Level));
        });
    }
}
