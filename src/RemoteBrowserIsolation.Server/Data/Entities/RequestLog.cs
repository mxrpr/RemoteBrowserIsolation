namespace RemoteBrowserIsolation.Server.Data.Entities;

// An append-only audit row for one browse decision (allowed or denied), written by
// RequestLogService. Satisfies the "all site requests must be logged" requirement.
public sealed class RequestLog
{
    public int Id { get; set; }

    public DateTime Timestamp { get; set; }

    public string Url { get; set; } = string.Empty;

    public string Host { get; set; } = string.Empty;

    // "deny" or the resolved ViewMode's name (e.g. "HtmlAllowInput"), stored as text so this table
    // reads sensibly on its own without joining back to the enum.
    public string Decision { get; set; } = string.Empty;

    public bool Allowed { get; set; }

    // Best-effort caller IP; null when it can't be determined (e.g. no HttpContext available).
    public string? ClientIp { get; set; }
}
