namespace RemoteBrowserIsolation.Server.Models.Admin;

// GET /api/admin/auth/status response — lets the admin UI decide whether to show a "create admin"
// bootstrap form or a normal login form, without leaking any account details.
public sealed record AuthStatusResponse(bool Bootstrapped);
