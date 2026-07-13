using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Models;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for VideoEncoderSettingsStore: GetModeAsync cache-hit vs DB-fallback vs
/// default-Auto-when-unset; SetModeAsync upserts a single row (Id fixed at 1) and refreshes
/// the in-memory cache.
public class VideoEncoderSettingsStoreTests
{
    /// Builds a shared in-memory database and returns the IServiceScopeFactory the store will use
    /// (scoped DbContext resolved per operation, exactly as in production) and a sibling
    /// AppDbContext that shares the same in-memory store so tests can seed rows and read results.
    private static (IServiceScopeFactory scopeFactory, AppDbContext db) CreateTestSetup()
    {
        var dbName = Guid.NewGuid().ToString();

        var services = new ServiceCollection();
        services.AddDbContext<AppDbContext>(opts => opts.UseInMemoryDatabase(dbName));

        var provider = services.BuildServiceProvider();
        var scopeFactory = provider.GetRequiredService<IServiceScopeFactory>();

        // Sibling context — shares the in-memory store via the same database name
        var options = new DbContextOptionsBuilder<AppDbContext>()
            .UseInMemoryDatabase(dbName)
            .Options;
        var db = new AppDbContext(options);

        return (scopeFactory, db);
    }

    #region GetModeAsync — default when unset

    [Fact]
    public async Task GetModeAsync_NoRowInDb_DefaultsToAuto()
    {
        // Arrange — empty DB, no cache
        var (scopeFactory, _) = CreateTestSetup();
        var store = new VideoEncoderSettingsStore(scopeFactory);

        // Act
        var mode = await store.GetModeAsync();

        // Assert — no row means the store must actively choose Auto, not just return a cached value
        Assert.Equal(VideoEncoderMode.Auto, mode);
    }

    #endregion

    #region GetModeAsync — DB fallback

    [Fact]
    public async Task GetModeAsync_NoCacheRowExists_ReturnsDbValue()
    {
        // Arrange — seed a non-Auto value so we can prove DB was actually read (not just default)
        var (scopeFactory, db) = CreateTestSetup();
        db.VideoEncoderSettings.Add(new VideoEncoderSetting { Id = 1, Mode = VideoEncoderMode.Cpu, UpdatedAt = DateTime.UtcNow });
        await db.SaveChangesAsync();

        var store = new VideoEncoderSettingsStore(scopeFactory);

        // Act
        var mode = await store.GetModeAsync();

        // Assert — must reflect the seeded DB value, not the Auto default
        Assert.Equal(VideoEncoderMode.Cpu, mode);
    }

    #endregion

    #region GetModeAsync — cache hit

    [Fact]
    public async Task GetModeAsync_AfterFirstRead_ReturnsCachedValueIgnoringDbChange()
    {
        // Arrange — seed Cpu in DB; first call should read and cache it
        var (scopeFactory, db) = CreateTestSetup();
        db.VideoEncoderSettings.Add(new VideoEncoderSetting { Id = 1, Mode = VideoEncoderMode.Cpu, UpdatedAt = DateTime.UtcNow });
        await db.SaveChangesAsync();

        var store = new VideoEncoderSettingsStore(scopeFactory);

        var first = await store.GetModeAsync();
        Assert.Equal(VideoEncoderMode.Cpu, first);

        // Mutate the DB row directly to a different mode — proves the second call does not re-read
        var row = await db.VideoEncoderSettings.FindAsync(1);
        row!.Mode = VideoEncoderMode.Gpu;
        await db.SaveChangesAsync();

        // Act — second call should serve the cached Cpu value, not the updated Gpu
        var second = await store.GetModeAsync();

        // Assert
        Assert.Equal(VideoEncoderMode.Cpu, second);
    }

    #endregion

    #region SetModeAsync — upsert single row

    [Fact]
    public async Task SetModeAsync_FirstCall_InsertsOneRow()
    {
        // Arrange — empty DB
        var (scopeFactory, db) = CreateTestSetup();
        var store = new VideoEncoderSettingsStore(scopeFactory);

        // Act
        await store.SetModeAsync(VideoEncoderMode.Gpu);

        // Assert — exactly one row with the right mode
        var count = await db.VideoEncoderSettings.CountAsync();
        Assert.Equal(1, count);

        var saved = await db.VideoEncoderSettings.AsNoTracking().FirstAsync();
        Assert.Equal(VideoEncoderMode.Gpu, saved.Mode);
    }

    [Fact]
    public async Task SetModeAsync_CalledTwice_UpsertsSingleRow()
    {
        // Arrange — call once to insert; second call must update, not insert a duplicate
        var (scopeFactory, db) = CreateTestSetup();
        var store = new VideoEncoderSettingsStore(scopeFactory);

        await store.SetModeAsync(VideoEncoderMode.Cpu);
        await store.SetModeAsync(VideoEncoderMode.Gpu);

        // Assert — still exactly one row, holding the latest value
        var count = await db.VideoEncoderSettings.CountAsync();
        Assert.Equal(1, count);

        var row = await db.VideoEncoderSettings.AsNoTracking().FirstAsync();
        Assert.Equal(VideoEncoderMode.Gpu, row.Mode);
    }

    #endregion

    #region SetModeAsync — cache refresh

    [Fact]
    public async Task SetModeAsync_RefreshesCacheSoNextGetReturnsNewMode()
    {
        // Arrange — first read caches Auto (no row in DB)
        var (scopeFactory, _) = CreateTestSetup();
        var store = new VideoEncoderSettingsStore(scopeFactory);

        var initial = await store.GetModeAsync();
        Assert.Equal(VideoEncoderMode.Auto, initial);

        // Act — set to Cpu; cache must be updated so the next get returns Cpu, not Auto
        await store.SetModeAsync(VideoEncoderMode.Cpu);

        var afterSet = await store.GetModeAsync();

        // Assert — if SetModeAsync did not refresh _cached, this would still return Auto
        Assert.Equal(VideoEncoderMode.Cpu, afterSet);
    }

    #endregion
}
