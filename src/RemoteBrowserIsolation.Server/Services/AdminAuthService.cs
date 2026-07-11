using System.IdentityModel.Tokens.Jwt;
using System.Security.Claims;
using System.Text;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.IdentityModel.Tokens;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;

namespace RemoteBrowserIsolation.Server.Services;

// Implements the "no admin exists yet -> first login bootstraps it" flow from policy_plan.md, plus
// normal login for all calls after that. Registered scoped (matches AppDbContext) since it's only
// ever used within a request, unlike WebRtcSessionManager's singleton.
public sealed class AdminAuthService(AppDbContext db, IConfiguration configuration, ILogger<AdminAuthService> logger) : IAdminAuthService
{
    // Serializes the check-then-create bootstrap sequence across concurrent requests. Without this,
    // two simultaneous "first" login calls could both observe "no admin exists" and race to insert
    // — the unique email index would reject the loser, but only after doing avoidable work, and the
    // loser's caller would get a confusing 500 instead of a token. A process-wide lock is enough
    // for this single-operator dev deployment.
    private static readonly SemaphoreSlim BootstrapLock = new SemaphoreSlim(1, 1);

    private readonly PasswordHasher<AdminUser> passwordHasher = new PasswordHasher<AdminUser>();

    public async Task<bool> IsBootstrappedAsync(CancellationToken cancellationToken = default)
    {
        return await db.AdminUsers.AnyAsync(cancellationToken);
    }

    public async Task<string?> LoginOrBootstrapAsync(string email, string password, CancellationToken cancellationToken = default)
    {
        await BootstrapLock.WaitAsync(cancellationToken);
        try
        {
            // At most one row ever exists — bootstrap semantics allow exactly one admin.
            AdminUser? existing = await db.AdminUsers.FirstOrDefaultAsync(cancellationToken);
            if (existing is null)
            {
                AdminUser admin = new AdminUser
                {
                    Email = email,
                    CreatedAt = DateTime.UtcNow,
                };
                admin.PasswordHash = passwordHasher.HashPassword(admin, password);

                db.AdminUsers.Add(admin);
                await db.SaveChangesAsync(cancellationToken);

                logger.LogInformation("Bootstrapped admin account for {Email}", email);
                return IssueToken(admin);
            }

            if (!string.Equals(existing.Email, email, StringComparison.OrdinalIgnoreCase))
            {
                return null;
            }

            PasswordVerificationResult verificationResult = passwordHasher.VerifyHashedPassword(existing, existing.PasswordHash, password);
            if (verificationResult == PasswordVerificationResult.Failed)
            {
                return null;
            }

            return IssueToken(existing);
        }
        finally
        {
            BootstrapLock.Release();
        }
    }

    // Builds a short-lived signed JWT for the given admin using the symmetric key/issuer/audience
    // configured under "Jwt:*" in appsettings.json. The subject/email claims let downstream code
    // attribute an authenticated action to this admin if that's ever needed.
    private string IssueToken(AdminUser admin)
    {
        string key = configuration["Jwt:Key"] ?? throw new InvalidOperationException("Jwt:Key is not configured.");
        string issuer = configuration["Jwt:Issuer"] ?? "RemoteBrowserIsolation.Server";
        string audience = configuration["Jwt:Audience"] ?? "RemoteBrowserIsolation.Admin";
        int ttlMinutes = configuration.GetValue<int?>("Jwt:TtlMinutes") ?? 60;

        SymmetricSecurityKey signingKey = new SymmetricSecurityKey(Encoding.UTF8.GetBytes(key));
        SigningCredentials credentials = new SigningCredentials(signingKey, SecurityAlgorithms.HmacSha256);

        List<Claim> claims = new List<Claim>
        {
            new Claim(JwtRegisteredClaimNames.Sub, admin.Email),
            new Claim(JwtRegisteredClaimNames.Email, admin.Email),
        };

        JwtSecurityToken token = new JwtSecurityToken(
            issuer: issuer,
            audience: audience,
            claims: claims,
            expires: DateTime.UtcNow.AddMinutes(ttlMinutes),
            signingCredentials: credentials);

        return new JwtSecurityTokenHandler().WriteToken(token);
    }
}
