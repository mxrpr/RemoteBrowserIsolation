using System.IdentityModel.Tokens.Jwt;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Logging.Abstractions;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for AdminAuthService.LoginOrBootstrapAsync: first-login bootstrap, subsequent correct
/// login, wrong password rejection, case-insensitive email matching, and JWT claims/expiry.
public class AdminAuthServiceTests
{
    private const string TestEmail = "admin@example.com";
    private const string TestPassword = "P@ssw0rd!";
    private const string JwtKey = "super-secret-test-key-at-least-32-bytes!!";
    private const string JwtIssuer = "TestIssuer";
    private const string JwtAudience = "TestAudience";
    private const int JwtTtlMinutes = 30;

    /// Creates a fresh isolated in-memory AppDbContext for each test.
    private static AppDbContext CreateTestContext()
    {
        var options = new DbContextOptionsBuilder<AppDbContext>()
            .UseInMemoryDatabase(Guid.NewGuid().ToString())
            .Options;

        return new AppDbContext(options);
    }

    /// Builds an IConfiguration with the Jwt:* keys the service reads.
    private static IConfiguration CreateJwtConfig(int ttlMinutes = JwtTtlMinutes)
    {
        var values = new Dictionary<string, string?>
        {
            ["Jwt:Key"] = JwtKey,
            ["Jwt:Issuer"] = JwtIssuer,
            ["Jwt:Audience"] = JwtAudience,
            ["Jwt:TtlMinutes"] = ttlMinutes.ToString(),
        };

        return new ConfigurationBuilder()
            .AddInMemoryCollection(values)
            .Build();
    }

    /// Creates an AdminAuthService wired to the provided context and config.
    private static AdminAuthService CreateService(AppDbContext db, IConfiguration? config = null)
    {
        return new AdminAuthService(db, config ?? CreateJwtConfig(), NullLogger<AdminAuthService>.Instance);
    }

    #region Bootstrap Tests

    [Fact]
    public async Task LoginOrBootstrapAsync_NoExistingAdmin_CreatesAdminRowAndReturnsToken()
    {
        // Arrange
        using var db = CreateTestContext();
        var service = CreateService(db);

        // Act
        var token = await service.LoginOrBootstrapAsync(TestEmail, TestPassword);

        // Assert – token is issued
        Assert.NotNull(token);

        // Assert – admin row was persisted with the supplied email
        var admin = await db.AdminUsers.FirstOrDefaultAsync();
        Assert.NotNull(admin);
        Assert.Equal(TestEmail, admin.Email);

        // Assert – password was stored as a hash, not plaintext
        Assert.NotEqual(TestPassword, admin.PasswordHash);
        Assert.False(string.IsNullOrWhiteSpace(admin.PasswordHash));
    }

    #endregion

    #region Subsequent Login Tests

    [Fact]
    public async Task LoginOrBootstrapAsync_ExistingAdmin_CorrectPassword_ReturnsToken()
    {
        // Arrange – bootstrap first
        using var db = CreateTestContext();
        var service = CreateService(db);
        await service.LoginOrBootstrapAsync(TestEmail, TestPassword);

        // Act – subsequent login with same credentials
        var token = await service.LoginOrBootstrapAsync(TestEmail, TestPassword);

        // Assert
        Assert.NotNull(token);
    }

    [Fact]
    public async Task LoginOrBootstrapAsync_ExistingAdmin_WrongPassword_ReturnsNull()
    {
        // Arrange – bootstrap first
        using var db = CreateTestContext();
        var service = CreateService(db);
        await service.LoginOrBootstrapAsync(TestEmail, TestPassword);

        // Act – wrong password on subsequent attempt
        var token = await service.LoginOrBootstrapAsync(TestEmail, "wrong-password");

        // Assert
        Assert.Null(token);
    }

    [Fact]
    public async Task LoginOrBootstrapAsync_ExistingAdmin_WrongEmail_ReturnsNull()
    {
        // Arrange – bootstrap first
        using var db = CreateTestContext();
        var service = CreateService(db);
        await service.LoginOrBootstrapAsync(TestEmail, TestPassword);

        // Act – different email on subsequent attempt
        var token = await service.LoginOrBootstrapAsync("other@example.com", TestPassword);

        // Assert
        Assert.Null(token);
    }

    #endregion

    #region Case-Insensitive Email Tests

    [Theory]
    [InlineData("ADMIN@EXAMPLE.COM")]
    [InlineData("Admin@Example.Com")]
    [InlineData("admin@example.com")]
    public async Task LoginOrBootstrapAsync_ExistingAdmin_EmailCaseInsensitiveMatch_ReturnsToken(string loginEmail)
    {
        // Arrange – bootstrap with lower-case email
        using var db = CreateTestContext();
        var service = CreateService(db);
        await service.LoginOrBootstrapAsync("admin@example.com", TestPassword);

        // Act – login using various capitalizations
        var token = await service.LoginOrBootstrapAsync(loginEmail, TestPassword);

        // Assert
        Assert.NotNull(token);
    }

    #endregion

    #region JWT Claims and Expiry Tests

    [Fact]
    public async Task LoginOrBootstrapAsync_IssuedToken_ContainsSubAndEmailClaims()
    {
        // Arrange
        using var db = CreateTestContext();
        var service = CreateService(db);

        // Act
        var token = await service.LoginOrBootstrapAsync(TestEmail, TestPassword);

        // Assert – parse without validation to inspect claims
        Assert.NotNull(token);
        var parsed = new JwtSecurityTokenHandler().ReadJwtToken(token);

        var sub = parsed.Claims.FirstOrDefault(c => c.Type == JwtRegisteredClaimNames.Sub)?.Value;
        var email = parsed.Claims.FirstOrDefault(c => c.Type == JwtRegisteredClaimNames.Email)?.Value;

        Assert.Equal(TestEmail, sub);
        Assert.Equal(TestEmail, email);
    }

    [Fact]
    public async Task LoginOrBootstrapAsync_IssuedToken_HasConfiguredIssuerAndAudience()
    {
        // Arrange
        using var db = CreateTestContext();
        var service = CreateService(db);

        // Act
        var token = await service.LoginOrBootstrapAsync(TestEmail, TestPassword);

        Assert.NotNull(token);
        var parsed = new JwtSecurityTokenHandler().ReadJwtToken(token);

        // Assert
        Assert.Equal(JwtIssuer, parsed.Issuer);
        Assert.Contains(JwtAudience, parsed.Audiences);
    }

    [Fact]
    public async Task LoginOrBootstrapAsync_IssuedToken_ExpiryMatchesConfiguredTtl()
    {
        // Arrange
        const int ttl = 45;
        using var db = CreateTestContext();
        var service = CreateService(db, CreateJwtConfig(ttl));

        var before = DateTime.UtcNow;

        // Act
        var token = await service.LoginOrBootstrapAsync(TestEmail, TestPassword);

        var after = DateTime.UtcNow;

        Assert.NotNull(token);
        var parsed = new JwtSecurityTokenHandler().ReadJwtToken(token);

        // The token's ValidTo should be within [before+ttl, after+ttl] with a small tolerance
        var expectedMin = before.AddMinutes(ttl).AddSeconds(-5);
        var expectedMax = after.AddMinutes(ttl).AddSeconds(5);

        Assert.InRange(parsed.ValidTo, expectedMin, expectedMax);
    }

    #endregion
}
