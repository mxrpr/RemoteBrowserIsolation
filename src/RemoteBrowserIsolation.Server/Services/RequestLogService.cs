using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;

namespace RemoteBrowserIsolation.Server.Services;

// Appends one RequestLog row per browse decision so every request (allowed or denied, any mode) is
// auditable via GET /api/admin/logs, per policy_plan's "all site requests must be logged"
// requirement. Scoped (matches AppDbContext).
public sealed class RequestLogService(AppDbContext db) : IRequestLogService
{
    public async Task LogAsync(Uri url, string decision, bool allowed, string? clientIp, CancellationToken cancellationToken = default)
    {
        RequestLog entry = new RequestLog
        {
            Timestamp = DateTime.UtcNow,
            Url = url.ToString(),
            Host = url.Host,
            Decision = decision,
            Allowed = allowed,
            ClientIp = clientIp,
        };

        db.RequestLogs.Add(entry);
        await db.SaveChangesAsync(cancellationToken);
    }
}
