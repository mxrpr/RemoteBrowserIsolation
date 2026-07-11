namespace RemoteBrowserIsolation.Server.Services;

public interface IAdminAuthService
{
    // True once an AdminUser row exists — lets the UI show "log in" instead of "create admin".
    Task<bool> IsBootstrappedAsync(CancellationToken cancellationToken = default);

    // Bootstrap-or-login per policy_plan: if no admin exists yet, this call creates one with the
    // given email+password and returns a token for it (first caller wins). If an admin already
    // exists, verifies the credentials against it. Returns null on any failure (wrong email, wrong
    // password) — the caller maps that to 401 without distinguishing which check failed.
    Task<string?> LoginOrBootstrapAsync(string email, string password, CancellationToken cancellationToken = default);
}
