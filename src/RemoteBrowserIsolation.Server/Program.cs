using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Http.Extensions;

var builder = WebApplication.CreateBuilder(args);

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

app.MapGet("/health", () => Results.Ok(new { status = "ok" }));

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
