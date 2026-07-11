namespace RemoteBrowserIsolation.Server.Data.Entities;

// The single administrator account. Bootstrap semantics (see AdminAuthService) mean this table
// holds at most one row: the first successful login call creates it.
public sealed class AdminUser
{
    public int Id { get; set; }

    // Compared case-insensitively at login; stored as provided.
    public string Email { get; set; } = string.Empty;

    // PBKDF2 hash produced by ASP.NET Core's PasswordHasher<AdminUser> — never the raw password.
    public string PasswordHash { get; set; } = string.Empty;

    public DateTime CreatedAt { get; set; }
}
