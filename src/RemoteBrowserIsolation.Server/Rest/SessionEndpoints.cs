using RemoteBrowserIsolation.Server.Models;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Rest;

// The actual browse surface: starting a video-mode WebRTC session, and fetching raw page bytes for
// HTML modes. Both endpoints independently re-resolve policy against IPolicyEngine rather than
// trusting a prior GET /api/policy/resolve call for the same URL — the client's earlier resolve is
// purely a UI hint, never a capability grant.
public static class SessionEndpoints
{
    // Registers POST /api/session/offer and GET /api/session/fetch.
    public static void MapSessionEndpoints(this WebApplication app)
    {
        app.MapPost("/api/session/offer", async (OfferRequest request, IWebRtcSessionManager sessionManager, IPolicyEngine policyEngine, IRequestLogService requestLog, HttpContext httpContext) =>
        {
            if (!Uri.TryCreate(request.Url, UriKind.Absolute, out Uri? targetUrl))
            {
                return Results.BadRequest(new { error = "Invalid URL" });
            }

            string? clientIp = httpContext.Connection.RemoteIpAddress?.ToString();
            ViewMode? mode = await policyEngine.ResolveAsync(targetUrl);

            if (mode is null)
            {
                await requestLog.LogAsync(targetUrl, "deny", allowed: false, clientIp);
                return Results.Json(new { error = "This site is not permitted by policy." }, statusCode: StatusCodes.Status403Forbidden);
            }

            if (mode is ViewMode.HtmlAllowInput or ViewMode.HtmlNoInput)
            {
                await requestLog.LogAsync(targetUrl, $"mode-mismatch:{mode}", allowed: false, clientIp);
                return Results.Json(new { error = "This site's policy requires HTML mode, not video." }, statusCode: StatusCodes.Status409Conflict);
            }

            // Only VideoAllowInput and VideoNoInput remain. wireInput is false only for
            // VideoNoInput, which is what makes that mode's "no input" server-authoritative.
            bool wireInput = mode == ViewMode.VideoAllowInput;

            try
            {
                await requestLog.LogAsync(targetUrl, mode.ToString()!, allowed: true, clientIp);
                string answerSdp = await sessionManager.CreateSessionAsync(request.Sdp, targetUrl, request.Width, request.Height, wireInput);
                return Results.Ok(new AnswerResponse(answerSdp));
            }
            catch (InvalidOperationException ex)
            {
                return Results.BadRequest(new { error = ex.Message });
            }
        });

        app.MapGet("/api/session/fetch", async (string? url, IPageDownloader downloader, IPolicyEngine policyEngine, IRequestLogService requestLog, IContentRewriter contentRewriter, HttpContext httpContext) =>
        {
            // The framed page runs with an opaque sandbox origin (no allow-same-origin, by design —
            // see ContentRewriter's runtime shim comments), so its own fetch()/XHR calls and any
            // <script type="module"> load are CORS-checked against this response regardless of the
            // requested URL being correct. Safe to allow from anywhere: this endpoint only ever
            // re-serves public page bytes it already fetched itself, carries no cookies/credentials,
            // and every request re-checks IPolicyEngine on its own — there's nothing here for a
            // cross-origin caller to gain by reading the response that policy didn't already allow.
            httpContext.Response.Headers.Append("Access-Control-Allow-Origin", "*");

            if (string.IsNullOrWhiteSpace(url) || !Uri.TryCreate(url, UriKind.Absolute, out Uri? targetUrl))
            {
                return Results.BadRequest(new { error = "A valid absolute url query parameter is required." });
            }

            string? clientIp = httpContext.Connection.RemoteIpAddress?.ToString();
            ViewMode? mode = await policyEngine.ResolveAsync(targetUrl);

            if (mode is null)
            {
                await requestLog.LogAsync(targetUrl, "deny", allowed: false, clientIp);
                return Results.Json(new { error = "This site is not permitted by policy." }, statusCode: StatusCodes.Status403Forbidden);
            }

            if (mode is ViewMode.VideoAllowInput or ViewMode.VideoNoInput)
            {
                await requestLog.LogAsync(targetUrl, $"mode-mismatch:{mode}", allowed: false, clientIp);
                return Results.Json(new { error = "This site's policy requires video mode, not HTML." }, statusCode: StatusCodes.Status409Conflict);
            }

            PageDownloadResult result = await downloader.DownloadAsync(targetUrl);
            if (!result.Success)
            {
                // Policy allowed the fetch; the fetch itself failed downstream (DNS/timeout/non-2xx).
                // Still logged as allowed since the policy decision, not the fetch, is what's audited.
                await requestLog.LogAsync(targetUrl, mode.ToString()!, allowed: true, clientIp);
                return Results.Json(new { error = result.ErrorMessage }, statusCode: StatusCodes.Status502BadGateway);
            }

            // Every in-scope internal URL (links, assets, inline/standalone CSS, and now JS module
            // specifiers) gets rewritten to route back through this same policy-gated endpoint instead
            // of resolving against our own origin (404) or the live site directly (escapes the relay)
            // -- see ContentRewriter and plans/6_iteration_html_rewrite_proxy.md for the origo.hu bug
            // this fixes. HtmlNoInput's "no text entry, but scroll/click still work" rule (iteration 5)
            // is folded into RewriteHtml's noInput parameter rather than living here. The JS branch
            // (iteration 8, plans/8_iteration_js_module_rewrite.md) closes the one gap iteration 7's
            // runtime shim couldn't: native ESM import()/import...from/export...from specifiers are
            // literal string text in the response, so they're rewritten server-side the same way
            // RewriteCss already rewrites url()/@import -- confirmed live that origo.hu serves its
            // chunks as "application/javascript; charset=UTF-8", hence matching on the substring
            // "javascript" rather than an exact media type. Anything that isn't HTML/CSS/JS (images,
            // fonts, etc.) passes through unchanged, as before.
            string contentType = result.ContentType ?? "application/octet-stream";
            byte[] content = contentType.Contains("html", StringComparison.OrdinalIgnoreCase)
                ? contentRewriter.RewriteHtml(result.Content!, targetUrl, noInput: mode == ViewMode.HtmlNoInput)
                : contentType.Contains("css", StringComparison.OrdinalIgnoreCase)
                    ? contentRewriter.RewriteCss(result.Content!, targetUrl)
                    : contentType.Contains("javascript", StringComparison.OrdinalIgnoreCase)
                        ? contentRewriter.RewriteJavaScript(result.Content!, targetUrl)
                        : result.Content!;

            await requestLog.LogAsync(targetUrl, mode.ToString()!, allowed: true, clientIp);
            return Results.Bytes(content, contentType);
        });
    }
}
