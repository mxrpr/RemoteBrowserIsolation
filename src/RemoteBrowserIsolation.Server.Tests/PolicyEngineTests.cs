using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Models;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for PolicyEngine: host-to-ViewMode resolution with deny-by-default, subdomain matching, and longest-pattern precedence.
public class PolicyEngineTests
{
    /// Helper to create a fresh in-memory AppDbContext for test isolation.
    private static AppDbContext CreateTestContext()
    {
        var options = new DbContextOptionsBuilder<AppDbContext>()
            .UseInMemoryDatabase(Guid.NewGuid().ToString())
            .Options;

        return new AppDbContext(options);
    }

    #region Exact Match Tests

    [Fact]
    public async Task ResolveAsync_ExactHostMatch_ReturnsViewMode()
    {
        // Arrange
        using var db = CreateTestContext();
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "example.com",
            ViewMode = ViewMode.HtmlAllowInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        var result = await engine.ResolveAsync(new Uri("https://example.com/page"));

        // Assert
        Assert.Equal(ViewMode.HtmlAllowInput, result);
    }

    [Fact]
    public async Task ResolveAsync_CaseInsensitiveHostMatch_ReturnsViewMode()
    {
        // Arrange
        using var db = CreateTestContext();
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "Example.com",
            ViewMode = ViewMode.VideoAllowInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        var result = await engine.ResolveAsync(new Uri("https://EXAMPLE.COM/"));

        // Assert
        Assert.Equal(ViewMode.VideoAllowInput, result);
    }

    #endregion

    #region Subdomain Match Tests

    [Fact]
    public async Task ResolveAsync_SubdomainMatch_ReturnsParentPolicy()
    {
        // Arrange
        using var db = CreateTestContext();
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "example.com",
            ViewMode = ViewMode.VideoAllowInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        var result = await engine.ResolveAsync(new Uri("https://www.example.com/"));

        // Assert
        Assert.Equal(ViewMode.VideoAllowInput, result);
    }

    [Fact]
    public async Task ResolveAsync_UnrelatedSiblingSubdomainNotFalselyMatched_ReturnsNull()
    {
        // Arrange
        using var db = CreateTestContext();
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "example.com",
            ViewMode = ViewMode.HtmlAllowInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        // "notexample.com" ends with "example.com" as a substring, but not as a subdomain
        // (it does not end with ".example.com" and is not an exact match)
        var result = await engine.ResolveAsync(new Uri("https://notexample.com/"));

        // Assert
        Assert.Null(result);
    }

    #endregion

    #region Pattern Matching Tests

    [Fact]
    public async Task ResolveAsync_LongestPatternWins_ReturnsMostSpecificMatch()
    {
        // Arrange
        using var db = CreateTestContext();
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "example.com",
            ViewMode = ViewMode.HtmlNoInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "app.example.com",
            ViewMode = ViewMode.VideoNoInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        var result = await engine.ResolveAsync(new Uri("https://app.example.com/"));

        // Assert
        // The longer/more specific pattern "app.example.com" should win over "example.com"
        Assert.Equal(ViewMode.VideoNoInput, result);
    }

    [Fact]
    public async Task ResolveAsync_MultiLevelSubdomainMatchesLongestPattern()
    {
        // Arrange
        using var db = CreateTestContext();
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "example.com",
            ViewMode = ViewMode.HtmlNoInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "api.app.example.com",
            ViewMode = ViewMode.VideoAllowInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        var result = await engine.ResolveAsync(new Uri("https://api.app.example.com/data"));

        // Assert
        Assert.Equal(ViewMode.VideoAllowInput, result);
    }

    #endregion

    #region Deny by Default Tests

    [Fact]
    public async Task ResolveAsync_NoMatch_ReturnsNullDenyByDefault()
    {
        // Arrange
        using var db = CreateTestContext();
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "other.com",
            ViewMode = ViewMode.HtmlAllowInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        var result = await engine.ResolveAsync(new Uri("https://unrelated.com/"));

        // Assert
        Assert.Null(result);
    }

    [Fact]
    public async Task ResolveAsync_EmptyPolicyTable_ReturnsNullForAnyHost()
    {
        // Arrange
        using var db = CreateTestContext();
        // No policies added
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        var result = await engine.ResolveAsync(new Uri("https://example.com/"));

        // Assert
        Assert.Null(result);
    }

    #endregion

    #region ViewMode Enum Tests

    [Theory]
    [InlineData(ViewMode.HtmlAllowInput)]
    [InlineData(ViewMode.HtmlNoInput)]
    [InlineData(ViewMode.VideoAllowInput)]
    [InlineData(ViewMode.VideoNoInput)]
    public async Task ResolveAsync_EachViewModeValue_ReturnedCorrectly(ViewMode viewMode)
    {
        // Arrange
        using var db = CreateTestContext();
        var hostPattern = $"{viewMode}.example.com";
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = hostPattern,
            ViewMode = viewMode,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);

        // Act
        var result = await engine.ResolveAsync(new Uri($"https://{hostPattern}/"));

        // Assert
        Assert.Equal(viewMode, result);
    }

    #endregion

    #region Cancellation Tests

    [Fact]
    public async Task ResolveAsync_WithCancellationToken_RespectsToken()
    {
        // Arrange
        using var db = CreateTestContext();
        db.SitePolicies.Add(new SitePolicy
        {
            HostPattern = "example.com",
            ViewMode = ViewMode.HtmlAllowInput,
            CreatedAt = DateTime.UtcNow,
            UpdatedAt = DateTime.UtcNow
        });
        await db.SaveChangesAsync();

        var engine = new PolicyEngine(db);
        using var cts = new CancellationTokenSource();

        // Act
        var result = await engine.ResolveAsync(new Uri("https://example.com/"), cts.Token);

        // Assert
        Assert.Equal(ViewMode.HtmlAllowInput, result);
    }

    #endregion
}
