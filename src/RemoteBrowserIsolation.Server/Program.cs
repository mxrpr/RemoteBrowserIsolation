using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Http.Extensions;
using RemoteBrowserIsolation.Server.Models;
using RemoteBrowserIsolation.Server.Services;

var builder = WebApplication.CreateBuilder(args);

// Retained only for the /debug/fetch scaffolding endpoint — the main session flow renders
// server-side via HeadlessBrowserSessionManager instead of downloading raw bytes.
builder.Services.AddHttpClient<IPageDownloader, PageDownloader>(client =>
{
    client.Timeout = TimeSpan.FromSeconds(30);
});
builder.Services.AddSingleton<IHeadlessBrowserSessionManager, HeadlessBrowserSessionManager>();
builder.Services.AddSingleton<IVideoTrackStreamer, VideoTrackStreamer>();
builder.Services.AddSingleton<IInputEventForwarder, InputEventForwarder>();
builder.Services.AddSingleton<IWebRtcSessionManager, WebRtcSessionManager>();

var app = builder.Build();

// Bind the FFmpeg native libraries (VP8 encoder for the video track) before any session starts.
// The lib directory is machine-specific config ("FFmpeg:LibPath"); fail fast with an actionable
// message rather than letting the first session die with an obscure DllNotFoundException.
var ffmpegLibPath = app.Configuration["FFmpeg:LibPath"]
    ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), "apps", "ffmpeg-8.1", "lib");
try
{
    // AV_LOG_ERROR: FFmpeg's native warnings are noise here — notably a benign per-session
    // "deprecated pixel format" line from swscale (CDP's JPEGs decode to the legacy full-range
    // YUVJ420P format; the conversion handles it correctly, it just complains).
    SIPSorceryMedia.FFmpeg.FFmpegInit.Initialise(SIPSorceryMedia.FFmpeg.FfmpegLogLevelEnum.AV_LOG_ERROR, ffmpegLibPath, app.Logger);
    app.Logger.LogInformation("FFmpeg initialised from {LibPath}", ffmpegLibPath);
}
catch (Exception ex)
{
    throw new InvalidOperationException(
        $"Failed to initialise FFmpeg from '{ffmpegLibPath}'. Install an FFmpeg 8.x shared build there " +
        "or point config key 'FFmpeg:LibPath' at its lib directory.", ex);
}

app.Use(async (context, next) =>
{
    app.Logger.LogInformation(
        "Received request {Method} {Url} at {Timestamp:o}",
        context.Request.Method,
        context.Request.GetDisplayUrl(),
        DateTimeOffset.UtcNow);

    await next();
});
// URL rewriter. Request GET / → looks in wwwroot for a default doc (index.html, default.html, etc.),
// rewrites request path to /index.html internally. Doesn't serve anything itself.
app.UseDefaultFiles();
// actual file server. Serves any file under wwwroot/ matching the request path as a static response
// (correct Content-Type, etc). This is what actually returns index.html's bytes to the browser.
app.UseStaticFiles();

app.MapGet("/health", () => Results.Ok(new { status = "ok" }));

app.MapPost("/api/session/offer", async (OfferRequest request, IWebRtcSessionManager sessionManager) =>
{
    if (!Uri.TryCreate(request.Url, UriKind.Absolute, out var targetUrl))
    {
        return Results.BadRequest(new { error = "Invalid URL" });
    }

    try
    {
        var answerSdp = await sessionManager.CreateSessionAsync(request.Sdp, targetUrl, request.Width, request.Height);
        return Results.Ok(new AnswerResponse(answerSdp));
    }
    catch (InvalidOperationException ex)
    {
        return Results.BadRequest(new { error = ex.Message });
    }
});

app.MapGet("/debug/fetch", async (string url, IPageDownloader downloader) =>
{
    if (!Uri.TryCreate(url, UriKind.Absolute, out var uri))
    {
        return Results.BadRequest(new { error = "Invalid URL" });
    }

    var result = await downloader.DownloadAsync(uri);
    return result.Success
        ? Results.Ok(new { success = true, contentType = result.ContentType, byteLength = result.Content!.Length })
        : Results.Ok(new { success = false, error = result.ErrorMessage });
});

app.Lifetime.ApplicationStarted.Register(() =>
{
    var logger = app.Services.GetRequiredService<ILogger<Program>>();
    var addresses = app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()?.Addresses
        ?? Enumerable.Empty<string>();

    foreach (var address in addresses)
    {
        logger.LogInformation("Accepting browser connections on {Address}", address);
    }
});

app.Lifetime.ApplicationStopping.Register(() =>
{
    var logger = app.Services.GetRequiredService<ILogger<Program>>();
    logger.LogInformation("Server is shutting down...");
});

app.Lifetime.ApplicationStarted.Register(() =>
{
    Console.WriteLine("Server started. Press Ctrl+C to shut down.");  // add this
    // ... existing address logging ...
});
app.Run();
