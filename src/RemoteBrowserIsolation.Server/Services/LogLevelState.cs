namespace RemoteBrowserIsolation.Server.Services;

// Mutable holder for the admin-configured log level. The global logging filter (registered in
// Program.cs via builder.Logging, before the DI container exists) reads CurrentLevel synchronously
// on every single log call, so this has to be a plain field on an object constructed and shared by
// reference before builder.Build() runs -- it can't be resolved through DI at filter-registration
// time. LogLevelSettingsStore updates this field whenever the admin changes the setting, so the
// change takes effect immediately with no restart.
public sealed class LogLevelState
{
    public LogLevel CurrentLevel = LogLevel.Information;
}
