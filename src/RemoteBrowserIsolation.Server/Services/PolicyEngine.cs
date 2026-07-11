using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Models;

namespace RemoteBrowserIsolation.Server.Services;

// Resolves a URL's host against admin-managed SitePolicy rows. Deny-by-default: an unmatched host
// resolves to null rather than falling back to any implicit mode. Scoped (matches AppDbContext).
public sealed class PolicyEngine(AppDbContext db) : IPolicyEngine
{
    public async Task<ViewMode?> ResolveAsync(Uri url, CancellationToken cancellationToken = default)
    {
        string host = url.Host;

        // The policy table is expected to stay small (admin-curated, not per-request data), so
        // loading it in full and matching in memory is simpler than trying to express
        // "exact match or subdomain of" as a single SQL predicate/index lookup in SQLite.
        List<SitePolicy> policies = await db.SitePolicies.AsNoTracking().ToListAsync(cancellationToken);

        SitePolicy? bestMatch = null;
        foreach (SitePolicy policy in policies)
        {
            if (!IsHostMatch(host, policy.HostPattern))
            {
                continue;
            }

            // Longest pattern wins so a specific rule (e.g. "app.example.com") overrides a broader
            // one ("example.com") when both would otherwise match.
            if (bestMatch is null || policy.HostPattern.Length > bestMatch.HostPattern.Length)
            {
                bestMatch = policy;
            }
        }

        return bestMatch?.ViewMode;
    }

    // A host matches a pattern if it's an exact (case-insensitive) match, or a subdomain of it —
    // e.g. "www.example.com" and "app.example.com" both match pattern "example.com".
    private static bool IsHostMatch(string host, string pattern)
    {
        return string.Equals(host, pattern, StringComparison.OrdinalIgnoreCase)
            || host.EndsWith("." + pattern, StringComparison.OrdinalIgnoreCase);
    }
}
