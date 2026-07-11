using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Rest;

// Public (unauthenticated) policy-resolution surface. Browsing has no end-user identity in this
// iteration — the per-site SitePolicy row is the only gate — so this endpoint is intentionally not
// [Authorize]'d. It's advisory only: the client uses the returned mode to pick a UI path, but every
// endpoint that actually does something (offer/fetch) re-resolves policy itself rather than trusting
// this call happened or that the URL hasn't changed since.
public static class PolicyEndpoints
{
    // Registers GET /api/policy/resolve?url=.
    public static void MapPolicyEndpoints(this WebApplication app)
    {
        app.MapGet("/api/policy/resolve", async (string? url, IPolicyEngine policyEngine, IRequestLogService requestLog, HttpContext httpContext) =>
        {
            if (string.IsNullOrWhiteSpace(url) || !Uri.TryCreate(url, UriKind.Absolute, out Uri? targetUrl))
            {
                return Results.BadRequest(new { error = "A valid absolute url query parameter is required." });
            }

            string? clientIp = httpContext.Connection.RemoteIpAddress?.ToString();
            Models.ViewMode? mode = await policyEngine.ResolveAsync(targetUrl);

            if (mode is null)
            {
                await requestLog.LogAsync(targetUrl, "deny", allowed: false, clientIp);
                return Results.Json(new { error = "This site is not permitted by policy." }, statusCode: StatusCodes.Status403Forbidden);
            }

            await requestLog.LogAsync(targetUrl, mode.ToString()!, allowed: true, clientIp);
            return Results.Ok(new { mode });
        });
    }
}
