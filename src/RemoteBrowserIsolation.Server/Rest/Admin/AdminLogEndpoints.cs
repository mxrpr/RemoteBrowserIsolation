using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Models.Admin;

namespace RemoteBrowserIsolation.Server.Rest.Admin;

// Read-only access to the RequestLog audit trail written by RequestLogService. Requires a valid
// bearer JWT.
public static class AdminLogEndpoints
{
    // Default/maximum page size for GET /api/admin/logs — bounds an unbounded ?limit= from turning
    // into an accidental full-table scan/response.
    private const int DefaultLimit = 50;
    private const int MaxLimit = 500;

    // Registers GET /api/admin/logs?limit=&offset=, newest first.
    public static void MapAdminLogEndpoints(this WebApplication app)
    {
        app.MapGet("/api/admin/logs", async (int? limit, int? offset, AppDbContext db) =>
        {
            int take = Math.Clamp(limit ?? DefaultLimit, 1, MaxLimit);
            int skip = Math.Max(offset ?? 0, 0);

            List<RequestLog> logs = await db.RequestLogs
                .AsNoTracking()
                .OrderByDescending(l => l.Timestamp)
                .Skip(skip)
                .Take(take)
                .ToListAsync();

            return Results.Ok(logs.Select(ToResponse));
        }).RequireAuthorization();
    }

    private static RequestLogResponse ToResponse(RequestLog log)
    {
        return new RequestLogResponse(log.Id, log.Timestamp, log.Url, log.Host, log.Decision, log.Allowed, log.ClientIp);
    }
}
