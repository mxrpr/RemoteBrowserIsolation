using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for RequestLogService.LogAsync: verifies that exactly one RequestLog row is persisted
/// and that each mapped field (Url, Host, Decision, Allowed, ClientIp, Timestamp) is derived
/// correctly from the Uri and the scalar parameters.
public class RequestLogServiceTests
{
    /// Creates a fresh in-memory AppDbContext and a RequestLogService wired to it.
    /// RequestLogService takes AppDbContext directly, so no IServiceScopeFactory is required.
    private static (RequestLogService service, AppDbContext db) CreateTestSetup()
    {
        var options = new DbContextOptionsBuilder<AppDbContext>()
            .UseInMemoryDatabase(Guid.NewGuid().ToString())
            .Options;
        var db = new AppDbContext(options);
        var service = new RequestLogService(db);
        return (service, db);
    }

    #region Row count

    [Fact]
    public async Task LogAsync_PersistsExactlyOneRow()
    {
        // Arrange
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/path?q=1");

        // Act
        await service.LogAsync(uri, "HtmlAllowInput", allowed: true, clientIp: null);

        // Assert — one row and no more
        var count = await db.RequestLogs.CountAsync();
        Assert.Equal(1, count);
    }

    #endregion

    #region Url field

    [Fact]
    public async Task LogAsync_UrlField_IsFullUriString()
    {
        // Arrange — URI with path and query so we can confirm the whole string is stored,
        // not just the host or origin.
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/some/path?foo=bar");

        // Act
        await service.LogAsync(uri, "HtmlAllowInput", allowed: true, clientIp: null);

        // Assert — Url must equal url.ToString(), which includes scheme, host, path, and query.
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.Equal(uri.ToString(), row.Url);
    }

    [Fact]
    public async Task LogAsync_UrlField_IsNotJustTheHost()
    {
        // Arrange — path component must appear in the stored Url to rule out a "host-only" mapping.
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/distinct/path");

        // Act
        await service.LogAsync(uri, "HtmlAllowInput", allowed: true, clientIp: null);

        // Assert
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.Contains("/distinct/path", row.Url);
    }

    #endregion

    #region Host field

    [Fact]
    public async Task LogAsync_HostField_IsHostComponentOnly()
    {
        // Arrange — URI with a path so we can confirm the stored Host is stripped of the path.
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://www.example.com/page");

        // Act
        await service.LogAsync(uri, "HtmlAllowInput", allowed: true, clientIp: null);

        // Assert — Host must equal url.Host ("www.example.com"), not the full URL string.
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.Equal(uri.Host, row.Host);
        Assert.Equal("www.example.com", row.Host);
    }

    [Fact]
    public async Task LogAsync_HostField_DoesNotContainPath()
    {
        // Arrange
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/should/not/appear");

        // Act
        await service.LogAsync(uri, "HtmlNoInput", allowed: true, clientIp: null);

        // Assert — path must not leak into the Host column
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.DoesNotContain("/should/not/appear", row.Host);
    }

    #endregion

    #region Decision field

    [Fact]
    public async Task LogAsync_DecisionField_IsPersistedVerbatim()
    {
        // Arrange
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/");

        // Act
        await service.LogAsync(uri, "VideoNoInput", allowed: true, clientIp: null);

        // Assert
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.Equal("VideoNoInput", row.Decision);
    }

    [Fact]
    public async Task LogAsync_DecisionField_DenyIsPersistedVerbatim()
    {
        // Arrange — "deny" is the other common decision value used by the service's callers
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://blocked.example.com/");

        // Act
        await service.LogAsync(uri, "deny", allowed: false, clientIp: null);

        // Assert
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.Equal("deny", row.Decision);
    }

    #endregion

    #region Allowed field

    [Fact]
    public async Task LogAsync_AllowedTrue_IsPersistedAsTrue()
    {
        // Arrange — use allowed: true to ensure we're testing a non-default bool value
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/");

        // Act
        await service.LogAsync(uri, "HtmlAllowInput", allowed: true, clientIp: null);

        // Assert
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.True(row.Allowed);
    }

    [Fact]
    public async Task LogAsync_AllowedFalse_IsPersistedAsFalse()
    {
        // Arrange — verify the false value is driven by the parameter, not just the bool default
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://blocked.example.com/");

        // Seed a row with Allowed=true first so that finding Allowed=false proves the mapping,
        // not the zero-value default.
        await service.LogAsync(new Uri("https://other.com/"), "HtmlAllowInput", allowed: true, clientIp: null);

        // Act
        await service.LogAsync(uri, "deny", allowed: false, clientIp: null);

        // Assert — second row has Allowed=false
        var rows = await db.RequestLogs.AsNoTracking().OrderBy(r => r.Id).ToListAsync();
        Assert.Equal(2, rows.Count);
        Assert.False(rows[1].Allowed);
    }

    #endregion

    #region ClientIp field

    [Fact]
    public async Task LogAsync_ClientIp_NullIsPersistedAsNull()
    {
        // Arrange — seed a non-null row first so the null assertion proves the mapping,
        // not the zero-value default of string?.
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/");

        await service.LogAsync(new Uri("https://other.com/"), "HtmlAllowInput", allowed: true, clientIp: "1.2.3.4");

        // Act
        await service.LogAsync(uri, "HtmlAllowInput", allowed: true, clientIp: null);

        // Assert — second row has ClientIp=null
        var rows = await db.RequestLogs.AsNoTracking().OrderBy(r => r.Id).ToListAsync();
        Assert.Equal(2, rows.Count);
        Assert.Null(rows[1].ClientIp);
    }

    [Fact]
    public async Task LogAsync_ClientIp_NonNullValueIsPersistedVerbatim()
    {
        // Arrange
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/");

        // Act
        await service.LogAsync(uri, "HtmlAllowInput", allowed: true, clientIp: "192.168.1.42");

        // Assert
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.Equal("192.168.1.42", row.ClientIp);
    }

    #endregion

    #region Timestamp field

    [Fact]
    public async Task LogAsync_Timestamp_IsSetToRecentUtcTime()
    {
        // Arrange — capture approximate time before and after the call to bracket the expected value
        var (service, db) = CreateTestSetup();
        var uri = new Uri("https://example.com/");
        var before = DateTime.UtcNow.AddSeconds(-1);

        // Act
        await service.LogAsync(uri, "HtmlAllowInput", allowed: true, clientIp: null);

        var after = DateTime.UtcNow.AddSeconds(1);

        // Assert — Timestamp must be a real UTC time set by LogAsync, not the default DateTime.MinValue
        var row = await db.RequestLogs.AsNoTracking().FirstAsync();
        Assert.InRange(row.Timestamp, before, after);
    }

    #endregion
}
