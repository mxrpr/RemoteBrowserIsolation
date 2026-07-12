using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;

namespace RemoteBrowserIsolation.Server.Services;

public interface ILogLevelSettingsStore
{
    Task<LogLevel> GetLevelAsync(CancellationToken cancellationToken = default);

    Task SetLevelAsync(LogLevel level, CancellationToken cancellationToken = default);
}

// Holds the admin-configured minimum log level in the DB and mirrors it into LogLevelState (the
// mutable holder the logging filter reads on every log call), so a change takes effect immediately
// with no restart -- same scoped-DbContext-via-IServiceScopeFactory pattern as
// VideoEncoderSettingsStore. There is always exactly one meaningful row (Id fixed at 1); a fresh DB
// with no row yet defaults to Information.
public sealed class LogLevelSettingsStore(IServiceScopeFactory scopeFactory, LogLevelState state) : ILogLevelSettingsStore
{
    private const int SettingsRowId = 1;

    private readonly SemaphoreSlim _lock = new(1, 1);
    private LogLevel? _cached;

    public async Task<LogLevel> GetLevelAsync(CancellationToken cancellationToken = default)
    {
        if (_cached is { } cached)
        {
            return cached;
        }

        await _lock.WaitAsync(cancellationToken);
        try
        {
            if (_cached is { } cachedInner)
            {
                return cachedInner;
            }

            using IServiceScope scope = scopeFactory.CreateScope();
            AppDbContext db = scope.ServiceProvider.GetRequiredService<AppDbContext>();
            LogLevelSetting? row = await db.LogLevelSettings
                .AsNoTracking()
                .FirstOrDefaultAsync(s => s.Id == SettingsRowId, cancellationToken);

            _cached = row?.Level ?? LogLevel.Information;
            state.CurrentLevel = _cached.Value;
            return _cached.Value;
        }
        finally
        {
            _lock.Release();
        }
    }

    // Upserts the single settings row, refreshes the in-memory cache, and updates LogLevelState so
    // the new level applies to the very next log call, not just the next session/request.
    public async Task SetLevelAsync(LogLevel level, CancellationToken cancellationToken = default)
    {
        using IServiceScope scope = scopeFactory.CreateScope();
        AppDbContext db = scope.ServiceProvider.GetRequiredService<AppDbContext>();

        LogLevelSetting? row = await db.LogLevelSettings.FindAsync([SettingsRowId], cancellationToken);
        if (row is null)
        {
            db.LogLevelSettings.Add(new LogLevelSetting { Id = SettingsRowId, Level = level, UpdatedAt = DateTime.UtcNow });
        }
        else
        {
            row.Level = level;
            row.UpdatedAt = DateTime.UtcNow;
        }

        await db.SaveChangesAsync(cancellationToken);
        _cached = level;
        state.CurrentLevel = level;
    }
}
