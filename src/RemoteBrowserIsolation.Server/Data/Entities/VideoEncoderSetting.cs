using RemoteBrowserIsolation.Server.Models;

namespace RemoteBrowserIsolation.Server.Data.Entities;

// The single admin-configured row controlling which video encoder path VideoTrackStreamer uses.
// Always exactly one row (Id fixed at 1 by VideoEncoderSettingsStore) -- a whole-server setting,
// not per-session or per-host.
public sealed class VideoEncoderSetting
{
    public int Id { get; set; }

    public VideoEncoderMode Mode { get; set; }

    public DateTime UpdatedAt { get; set; }
}
