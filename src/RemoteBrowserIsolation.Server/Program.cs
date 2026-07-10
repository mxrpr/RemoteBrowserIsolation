using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Http.Extensions;
using RemoteBrowserIsolation.Server.Models;
using RemoteBrowserIsolation.Server.Services;

var builder = WebApplication.CreateBuilder(args);

builder.Services.AddHttpClient<IPageDownloader, PageDownloader>(client =>
{
    client.Timeout = TimeSpan.FromSeconds(30);
});
builder.Services.AddSingleton<IWebRtcSessionManager, WebRtcSessionManager>();

var app = builder.Build();

app.Use(async (context, next) =>
{
    app.Logger.LogInformation(
        "Received request {Method} {Url} at {Timestamp:o}",
        context.Request.Method,
        context.Request.GetDisplayUrl(),
        DateTimeOffset.UtcNow);

    await next();
});

app.UseDefaultFiles();
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
        var answerSdp = await sessionManager.CreateSessionAsync(request.Sdp, targetUrl);
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

app.Run();
