using RemoteBrowserIsolation.Server.Models.Admin;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Rest.Admin;

// Admin authentication endpoints: bootstrap-or-login and a status probe. Deliberately not
// [Authorize] — they're the entry point that produces the token everything else requires.
public static class AdminAuthEndpoints
{
    // Registers POST /api/admin/auth/login and GET /api/admin/auth/status.
    public static void MapAdminAuthEndpoints(this WebApplication app)
    {
        app.MapPost("/api/admin/auth/login", async (LoginRequest request, IAdminAuthService authService) =>
        {
            if (string.IsNullOrWhiteSpace(request.Email) || string.IsNullOrWhiteSpace(request.Password))
            {
                return Results.BadRequest(new { error = "Email and password are required." });
            }

            string? token = await authService.LoginOrBootstrapAsync(request.Email, request.Password);
            return token is not null
                ? Results.Ok(new LoginResponse(token))
                : Results.Unauthorized();
        });

        app.MapGet("/api/admin/auth/status", async (IAdminAuthService authService) =>
        {
            bool bootstrapped = await authService.IsBootstrappedAsync();
            return Results.Ok(new AuthStatusResponse(bootstrapped));
        });
    }
}
