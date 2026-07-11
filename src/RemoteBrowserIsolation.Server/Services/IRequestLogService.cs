namespace RemoteBrowserIsolation.Server.Services;

public interface IRequestLogService
{
    // Writes one RequestLog row for a browse decision. "decision" is "deny" or the resolved
    // ViewMode's name; callers pass the same value they used to decide whether to proceed.
    // clientIp is best-effort (from HttpContext.Connection.RemoteIpAddress) and may be null.
    Task LogAsync(Uri url, string decision, bool allowed, string? clientIp, CancellationToken cancellationToken = default);
}
