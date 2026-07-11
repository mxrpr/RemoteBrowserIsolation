using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Models.Admin;

namespace RemoteBrowserIsolation.Server.Rest.Admin;

// CRUD for SitePolicy rows — the admin-managed source of truth PolicyEngine reads from. All
// routes require a valid bearer JWT (RequireAuthorization on the group).
public static class AdminSiteEndpoints
{
    // Registers GET/POST/PUT/DELETE /api/admin/sites[/{id}].
    public static void MapAdminSiteEndpoints(this WebApplication app)
    {
        RouteGroupBuilder group = app.MapGroup("/api/admin/sites").RequireAuthorization();

        group.MapGet("", async (AppDbContext db) =>
        {
            List<SitePolicy> policies = await db.SitePolicies
                .AsNoTracking()
                .OrderBy(p => p.HostPattern)
                .ToListAsync();

            return Results.Ok(policies.Select(ToResponse));
        });

        group.MapPost("", async (SitePolicyRequest request, AppDbContext db) =>
        {
            if (string.IsNullOrWhiteSpace(request.HostPattern))
            {
                return Results.BadRequest(new { error = "hostPattern is required." });
            }

            string normalizedHost = ExtractHost(request.HostPattern);
            bool alreadyExists = await db.SitePolicies.AnyAsync(p => p.HostPattern == normalizedHost);
            if (alreadyExists)
            {
                return Results.Conflict(new { error = $"A policy for '{normalizedHost}' already exists." });
            }

            DateTime now = DateTime.UtcNow;
            SitePolicy policy = new SitePolicy
            {
                HostPattern = normalizedHost,
                ViewMode = request.ViewMode,
                CreatedAt = now,
                UpdatedAt = now,
            };

            db.SitePolicies.Add(policy);
            await db.SaveChangesAsync();

            return Results.Created($"/api/admin/sites/{policy.Id}", ToResponse(policy));
        });

        group.MapPut("/{id:int}", async (int id, SitePolicyRequest request, AppDbContext db) =>
        {
            if (string.IsNullOrWhiteSpace(request.HostPattern))
            {
                return Results.BadRequest(new { error = "hostPattern is required." });
            }

            SitePolicy? policy = await db.SitePolicies.FindAsync(id);
            if (policy is null)
            {
                return Results.NotFound();
            }

            policy.HostPattern = ExtractHost(request.HostPattern);
            policy.ViewMode = request.ViewMode;
            policy.UpdatedAt = DateTime.UtcNow;

            await db.SaveChangesAsync();
            return Results.Ok(ToResponse(policy));
        });

        group.MapDelete("/{id:int}", async (int id, AppDbContext db) =>
        {
            SitePolicy? policy = await db.SitePolicies.FindAsync(id);
            if (policy is null)
            {
                return Results.NotFound();
            }

            // Deletion is the only way to revert a site to the default-deny posture — there's no
            // separate "disabled" state.
            db.SitePolicies.Remove(policy);
            await db.SaveChangesAsync();

            return Results.NoContent();
        });
    }

    // Normalizes whatever an admin types — a bare host ("index.hu"), a full URL
    // ("https://index.hu/path"), or a host with a trailing path ("index.hu/path") — down to just
    // the host, so PolicyEngine's host-match logic always compares against a clean value regardless
    // of how the site was entered. Uri.TryCreate needs a scheme for UriKind.Absolute, so a
    // schemeless input is given a throwaway "https://" prefix purely to make it parseable; falls
    // back to the trimmed/lowercased raw input if it still doesn't parse as a URI.
    private static string ExtractHost(string input)
    {
        string trimmed = input.Trim();
        string candidate = trimmed.Contains("://") ? trimmed : $"https://{trimmed}";

        return Uri.TryCreate(candidate, UriKind.Absolute, out Uri? uri) && !string.IsNullOrEmpty(uri.Host)
            ? uri.Host.ToLowerInvariant()
            : trimmed.ToLowerInvariant();
    }

    // Maps the persistence entity to the wire DTO; kept separate so the entity's EF-required
    // parameterless-constructible shape doesn't leak into the API contract.
    private static SitePolicyResponse ToResponse(SitePolicy policy)
    {
        return new SitePolicyResponse(policy.Id, policy.HostPattern, policy.ViewMode, policy.CreatedAt, policy.UpdatedAt);
    }
}
