using Microsoft.Extensions.Logging;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for LogLevelState: a mutable holder for the current log level.
public class LogLevelStateTests
{
    [Fact]
    public void New_DefaultsToInformation()
    {
        var state = new LogLevelState();

        Assert.Equal(LogLevel.Information, state.CurrentLevel);
    }

    [Fact]
    public void SetCurrentLevel_ToDebug_ReturnsDebug()
    {
        var state = new LogLevelState();

        state.CurrentLevel = LogLevel.Debug;

        Assert.Equal(LogLevel.Debug, state.CurrentLevel);
    }

    [Fact]
    public void SetCurrentLevel_Multiple_ReturnsLatestValue()
    {
        var state = new LogLevelState();

        state.CurrentLevel = LogLevel.Debug;
        Assert.Equal(LogLevel.Debug, state.CurrentLevel);

        state.CurrentLevel = LogLevel.Warning;
        Assert.Equal(LogLevel.Warning, state.CurrentLevel);

        state.CurrentLevel = LogLevel.Error;
        Assert.Equal(LogLevel.Error, state.CurrentLevel);

        state.CurrentLevel = LogLevel.Information;
        Assert.Equal(LogLevel.Information, state.CurrentLevel);
    }
}
