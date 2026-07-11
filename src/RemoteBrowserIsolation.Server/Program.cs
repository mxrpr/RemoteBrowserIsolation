using System.Text;
using System.Text.Json.Serialization;
using Microsoft.AspNetCore.Authentication.JwtBearer;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Http.Extensions;
using Microsoft.EntityFrameworkCore;
using Microsoft.IdentityModel.Tokens;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Rest;
using RemoteBrowserIsolation.Server.Rest.Admin;
using RemoteBrowserIsolation.Server.Services;

var builder = WebApplication.CreateBuilder(args);

// ViewMode (and any other enum) is serialized/deserialized as its name ("HtmlAllowInput") rather
// than its numeric ordinal — required for the admin REST DTOs (SitePolicyRequest/Response) to be
// readable/writable as plain JSON strings instead of magic numbers.
builder.Services.ConfigureHttpJsonOptions(options =>
{
    options.SerializerOptions.Converters.Add(new JsonStringEnumConverter());
});

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

// Needed so ContentRewriter (a singleton) can still read the current request's Host header per call
// via IHttpContextAccessor -- see that class's GetServerOrigin comment for why.
builder.Services.AddHttpContextAccessor();

// Stateless (holds only an immutable AngleSharp configuration, no per-request or cross-request
// mutable state) -- singleton is the simplest lifetime and avoids a DbContext-scoped dependency
// this service doesn't need. See ContentRewriter's class doc comment.
builder.Services.AddSingleton<IContentRewriter, ContentRewriter>();

// Scoped (default EF Core lifetime) is fine here — unlike WebRtcSessionManager's singleton, admin
// and policy endpoints are only ever used within a normal HTTP request, never from an async
// callback that outlives the request.
builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseSqlite(builder.Configuration.GetConnectionString("Sqlite") ?? "Data Source=rbi.db"));

// Scoped: each depends on AppDbContext, so their lifetime must match it.
builder.Services.AddScoped<IAdminAuthService, AdminAuthService>();
builder.Services.AddScoped<IPolicyEngine, PolicyEngine>();
builder.Services.AddScoped<IRequestLogService, RequestLogService>();

// Bearer JWT auth for all /api/admin/* endpoints except login/status. Signing key/issuer/audience
// come from config (see appsettings.json "Jwt" section) so the dev default can be overridden per
// deployment without a code change.
string jwtKey = builder.Configuration["Jwt:Key"] ?? throw new InvalidOperationException("Jwt:Key is not configured.");
string jwtIssuer = builder.Configuration["Jwt:Issuer"] ?? "RemoteBrowserIsolation.Server";
string jwtAudience = builder.Configuration["Jwt:Audience"] ?? "RemoteBrowserIsolation.Admin";
builder.Services.AddAuthentication(JwtBearerDefaults.AuthenticationScheme)
    .AddJwtBearer(options =>
    {
        options.TokenValidationParameters = new TokenValidationParameters
        {
            ValidateIssuer = true,
            ValidIssuer = jwtIssuer,
            ValidateAudience = true,
            ValidAudience = jwtAudience,
            ValidateIssuerSigningKey = true,
            IssuerSigningKey = new SymmetricSecurityKey(Encoding.UTF8.GetBytes(jwtKey)),
            ValidateLifetime = true,
        };
    });
builder.Services.AddAuthorization();

var app = builder.Build();

// Applies any pending EF Core migrations at startup so the SQLite schema is always up to date
// without a separate manual migration step in deployment.
using (IServiceScope migrationScope = app.Services.CreateScope())
{
    AppDbContext db = migrationScope.ServiceProvider.GetRequiredService<AppDbContext>();
    db.Database.Migrate();
}

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

// Must run in this order and before any [Authorize]'d endpoint mapping: authentication resolves
// the bearer token into a ClaimsPrincipal, authorization then checks it against each endpoint's
// requirements.
app.UseAuthentication();
app.UseAuthorization();

app.MapGet("/health", () => Results.Ok(new { status = "ok" }));

// Admin REST surface (auth, site-policy CRUD, request-log reads) — see Rest/Admin/*.cs. Kept as
// separate extension methods per CLAUDE.md's requirement that admin endpoints live under Rest/Admin.
app.MapAdminAuthEndpoints();
app.MapAdminSiteEndpoints();
app.MapAdminLogEndpoints();

// Public browse surface: policy-resolution hint plus the two policy-gated ways to actually view a
// site (video-mode WebRTC session, HTML-mode raw byte fetch). See Rest/PolicyEndpoints.cs and
// Rest/SessionEndpoints.cs.
app.MapPolicyEndpoints();
app.MapSessionEndpoints();

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
