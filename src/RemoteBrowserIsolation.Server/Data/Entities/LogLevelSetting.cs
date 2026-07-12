namespace RemoteBrowserIsolation.Server.Data.Entities;

// The single admin-configured row controlling the whole-server minimum log level. Always exactly
// one row (Id fixed at 1 by LogLevelSettingsStore) -- mirrors VideoEncoderSetting's singleton-row
// shape.
public sealed class LogLevelSetting
{
    public int Id { get; set; }

    public LogLevel Level { get; set; }

    public DateTime UpdatedAt { get; set; }
}
