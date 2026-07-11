namespace RemoteBrowserIsolation.Server.Models.Admin;

// POST /api/admin/auth/login body. Doubles as the bootstrap payload: if no admin exists yet, this
// same email+password becomes the admin account (see AdminAuthService.LoginOrBootstrapAsync).
public sealed record LoginRequest(string Email, string Password);
