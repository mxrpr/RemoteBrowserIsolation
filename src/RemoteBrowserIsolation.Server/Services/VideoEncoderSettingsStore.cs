using Microsoft.EntityFrameworkCore;
using RemoteBrowserIsolation.Server.Data;
using RemoteBrowserIsolation.Server.Data.Entities;
using RemoteBrowserIsolation.Server.Models;

namespace RemoteBrowserIsolation.Server.Services;

public interface IVideoEncoderSettingsStore
{
    Task<VideoEncoderMode> GetModeAsync(CancellationToken cancellationToken = default);

    Task SetModeAsync(VideoEncoderMode mode, CancellationToken cancellationToken = default);
}

// Holds the admin-configured video encoder mode (Auto/CPU/GPU) in memory so VideoTrackStreamer can
// read it once per session without hitting the database per frame. Singleton -- same
// scoped-DbContext-via-IServiceScopeFactory pattern as RootCaStore. There is always exactly one
// meaningful row (Id fixed at 1); a fresh DB with no row yet defaults to Auto.
public sealed class VideoEncoderSettingsStore(IServiceScopeFactory scopeFactory) : IVideoEncoderSettingsStore
{
    private const int SettingsRowId = 1;

    private readonly SemaphoreSlim _lock = new(1, 1);
    private VideoEncoderMode? _cached;

    public async Task<VideoEncoderMode> GetModeAsync(CancellationToken cancellationToken = default)
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
            VideoEncoderSetting? row = await db.VideoEncoderSettings
                .AsNoTracking()
                .FirstOrDefaultAsync(s => s.Id == SettingsRowId, cancellationToken);

            _cached = row?.Mode ?? VideoEncoderMode.Auto;
            return _cached.Value;
        }
        finally
        {
            _lock.Release();
        }
    }

    // Upserts the single settings row and refreshes the in-memory cache so the change takes
    // effect for the next session started, without a restart.
    public async Task SetModeAsync(VideoEncoderMode mode, CancellationToken cancellationToken = default)
    {
        using IServiceScope scope = scopeFactory.CreateScope();
        AppDbContext db = scope.ServiceProvider.GetRequiredService<AppDbContext>();

        VideoEncoderSetting? row = await db.VideoEncoderSettings.FindAsync([SettingsRowId], cancellationToken);
        if (row is null)
        {
            db.VideoEncoderSettings.Add(new VideoEncoderSetting { Id = SettingsRowId, Mode = mode, UpdatedAt = DateTime.UtcNow });
        }
        else
        {
            row.Mode = mode;
            row.UpdatedAt = DateTime.UtcNow;
        }

        await db.SaveChangesAsync(cancellationToken);
        _cached = mode;
    }
}
