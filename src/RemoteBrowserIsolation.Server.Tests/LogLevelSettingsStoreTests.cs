using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for LogLevelSettingsStore: GetLevelAsync cache-hit vs DB-fallback vs default-Information-when-unset;
/// SetLevelAsync upserts single row and mirrors new level into LogLevelState.
public class LogLevelSettingsStoreTests
{
    /// Builds a shared in-memory database, returns the IServiceScopeFactory the store will use
    /// (scoped DbContext resolved per operation, exactly as in production) and a sibling AppDbContext
    /// that shares the same in-memory store so tests can seed rows and read back results.
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

    #region GetLevelAsync — default when unset

    [Fact]
    public async Task GetLevelAsync_NoRowInDb_DefaultsToInformation()
    {
        // Arrange — empty DB, no cache
        var (scopeFactory, _) = CreateTestSetup();
        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        // Act
        var level = await store.GetLevelAsync();

        // Assert
        Assert.Equal(LogLevel.Information, level);
    }

    [Fact]
    public async Task GetLevelAsync_NoRowInDb_UpdatesLogLevelStateToInformation()
    {
        // Arrange
        var (scopeFactory, _) = CreateTestSetup();
        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        // Set to a non-default value so the assertion only passes if GetLevelAsync
        // actively writes Information back — not just because the field defaulted to it.
        state.CurrentLevel = LogLevel.Critical;

        // Act
        await store.GetLevelAsync();

        // Assert — state is updated even on the default path
        Assert.Equal(LogLevel.Information, state.CurrentLevel);
    }

    #endregion

    #region GetLevelAsync — DB fallback

    [Fact]
    public async Task GetLevelAsync_NoCacheRowExists_ReturnsDbValue()
    {
        // Arrange — seed a non-default level so we can tell DB was actually read
        var (scopeFactory, db) = CreateTestSetup();
        db.LogLevelSettings.Add(new LogLevelSetting { Id = 1, Level = LogLevel.Warning, UpdatedAt = DateTime.UtcNow });
        await db.SaveChangesAsync();

        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        // Act
        var level = await store.GetLevelAsync();

        // Assert
        Assert.Equal(LogLevel.Warning, level);
    }

    [Fact]
    public async Task GetLevelAsync_NoCacheRowExists_UpdatesLogLevelState()
    {
        // Arrange
        var (scopeFactory, db) = CreateTestSetup();
        db.LogLevelSettings.Add(new LogLevelSetting { Id = 1, Level = LogLevel.Debug, UpdatedAt = DateTime.UtcNow });
        await db.SaveChangesAsync();

        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        // Act
        await store.GetLevelAsync();

        // Assert — LogLevelState reflects the value read from DB
        Assert.Equal(LogLevel.Debug, state.CurrentLevel);
    }

    #endregion

    #region GetLevelAsync — cache hit

    [Fact]
    public async Task GetLevelAsync_AfterFirstRead_ReturnsCachedValueIgnoringDbChange()
    {
        // Arrange — seed Debug in DB, first call should cache it
        var (scopeFactory, db) = CreateTestSetup();
        db.LogLevelSettings.Add(new LogLevelSetting { Id = 1, Level = LogLevel.Debug, UpdatedAt = DateTime.UtcNow });
        await db.SaveChangesAsync();

        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        var first = await store.GetLevelAsync();
        Assert.Equal(LogLevel.Debug, first);

        // Mutate the DB row directly to a different level — proves the second call does not re-read
        var row = await db.LogLevelSettings.FindAsync(1);
        row!.Level = LogLevel.Error;
        await db.SaveChangesAsync();

        // Act — second call should serve cached value
        var second = await store.GetLevelAsync();

        // Assert
        Assert.Equal(LogLevel.Debug, second);
    }

    #endregion

    #region SetLevelAsync — upsert single row

    [Fact]
    public async Task SetLevelAsync_FirstCall_InsertsOneRow()
    {
        // Arrange — empty DB
        var (scopeFactory, db) = CreateTestSetup();
        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        // Act
        await store.SetLevelAsync(LogLevel.Trace);

        // Assert
        var count = await db.LogLevelSettings.CountAsync();
        Assert.Equal(1, count);

        var saved = await db.LogLevelSettings.AsNoTracking().FirstAsync();
        Assert.Equal(LogLevel.Trace, saved.Level);
    }

    [Fact]
    public async Task SetLevelAsync_CalledTwice_UpsertsSingleRow()
    {
        // Arrange — call once to insert, second call should update the same row
        var (scopeFactory, db) = CreateTestSetup();
        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        await store.SetLevelAsync(LogLevel.Debug);
        await store.SetLevelAsync(LogLevel.Warning);

        // Assert — only one row, not two
        var count = await db.LogLevelSettings.CountAsync();
        Assert.Equal(1, count);

        var row = await db.LogLevelSettings.AsNoTracking().FirstAsync();
        Assert.Equal(LogLevel.Warning, row.Level);
    }

    #endregion

    #region SetLevelAsync — mirrors to LogLevelState

    [Fact]
    public async Task SetLevelAsync_UpdatesLogLevelStateImmediately()
    {
        // Arrange
        var (scopeFactory, _) = CreateTestSetup();
        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        // Act
        await store.SetLevelAsync(LogLevel.Error);

        // Assert — LogLevelState reflects the new level without any further GetLevelAsync call
        Assert.Equal(LogLevel.Error, state.CurrentLevel);
    }

    [Fact]
    public async Task SetLevelAsync_CalledTwice_LogLevelStateReflectsLatestValue()
    {
        // Arrange
        var (scopeFactory, _) = CreateTestSetup();
        var state = new LogLevelState();
        var store = new LogLevelSettingsStore(scopeFactory, state);

        await store.SetLevelAsync(LogLevel.Debug);
        Assert.Equal(LogLevel.Debug, state.CurrentLevel);

        // Act
        await store.SetLevelAsync(LogLevel.Critical);

        // Assert
        Assert.Equal(LogLevel.Critical, state.CurrentLevel);
    }

    #endregion
}
