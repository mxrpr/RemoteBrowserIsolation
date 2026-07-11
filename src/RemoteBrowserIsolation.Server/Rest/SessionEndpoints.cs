using RemoteBrowserIsolation.Server.Models;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Rest;

// The video-mode browse surface: starting a WebRTC session. HTML mode has no app-level endpoint
// anymore -- it's served directly by the TLS-intercepting proxy (see
// Services/Proxy/TlsInterceptingProxyServer.cs), which independently re-resolves policy per
// request the same way this endpoint does, rather than trusting any prior client-side hint.
public static class SessionEndpoints
{
    // Registers POST /api/session/offer.
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
    }
}
