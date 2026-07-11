namespace RemoteBrowserIsolation.Server.Models.Admin;

// One row returned by GET /api/admin/logs — mirrors the RequestLog entity for the admin UI's log
// viewer.
public sealed record RequestLogResponse(int Id, DateTime Timestamp, string Url, string Host, string Decision, bool Allowed, string? ClientIp);
