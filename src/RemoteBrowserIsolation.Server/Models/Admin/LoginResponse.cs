namespace RemoteBrowserIsolation.Server.Models.Admin;

// Successful login/bootstrap response: a bearer JWT the client attaches to subsequent
// Authorization headers for all other /api/admin/* endpoints.
public sealed record LoginResponse(string Token);
