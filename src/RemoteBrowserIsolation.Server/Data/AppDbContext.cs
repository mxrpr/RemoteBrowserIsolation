using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data.Entities;

namespace RemoteBrowserIsolation.Server.Data;

// EF Core context for all admin/policy persistence (SQLite). Registered scoped via
// AddDbContext in Program.cs; services that need it outside the request lifetime (none currently)
// would have to take an IDbContextFactory instead — see WebRtcSessionManager's note on why a
// scoped dependency can't be captured by a singleton.
public sealed class AppDbContext(DbContextOptions<AppDbContext> options) : DbContext(options)
{
    public DbSet<AdminUser> AdminUsers => Set<AdminUser>();

    public DbSet<SitePolicy> SitePolicies => Set<SitePolicy>();

    public DbSet<RequestLog> RequestLogs => Set<RequestLog>();

    public DbSet<RootCertificateAuthority> RootCertificateAuthorities => Set<RootCertificateAuthority>();

    public DbSet<VideoEncoderSetting> VideoEncoderSettings => Set<VideoEncoderSetting>();

    public DbSet<LogLevelSetting> LogLevelSettings => Set<LogLevelSetting>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        // Unique email so bootstrap-or-login can look up "the" admin unambiguously; comparisons in
        // AdminAuthService are done case-insensitively at the application level since SQLite's
        // default collation for a plain unique index is ordinal (case-sensitive).
        modelBuilder.Entity<AdminUser>()
            .HasIndex(u => u.Email)
            .IsUnique();

        // Unique host so there's exactly one rule per registered host — PolicyEngine relies on this
        // to pick a single row rather than disambiguating duplicates.
        modelBuilder.Entity<SitePolicy>()
            .HasIndex(p => p.HostPattern)
            .IsUnique();

        // Id is always explicitly set to 1 by VideoEncoderSettingsStore (single whole-server
        // settings row, not an auto-incrementing list) — override EF's default int-PK convention
        // so it doesn't try to autoincrement.
        modelBuilder.Entity<VideoEncoderSetting>()
            .Property(s => s.Id)
            .ValueGeneratedNever();

        // Same single-row-settings override as VideoEncoderSetting above -- Id is always explicitly
        // set to 1 by LogLevelSettingsStore.
        modelBuilder.Entity<LogLevelSetting>()
            .Property(s => s.Id)
            .ValueGeneratedNever();

        base.OnModelCreating(modelBuilder);
    }
}
