using RemoteBrowserIsolation.Server.Models;

namespace RemoteBrowserIsolation.Server.Data.Entities;

// One admin-managed rule: "this host (and its subdomains) may be viewed in this mode." Absence of
// a matching row for a host means deny — see PolicyEngine.Resolve.
public sealed class SitePolicy
{
    public int Id { get; set; }

    // Bare host, e.g. "example.com". Matching (in PolicyEngine) also covers subdomains such as
    // "www.example.com". Unique so there's exactly one rule per registered host.
    public string HostPattern { get; set; } = string.Empty;

    public ViewMode ViewMode { get; set; }

    public DateTime CreatedAt { get; set; }

    public DateTime UpdatedAt { get; set; }
}
