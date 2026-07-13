using RemoteBrowserIsolation.Server.Services.Proxy;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for ProxyHeaders.IsHopByHop: hop-by-hop header detection per RFC 7230.
public class ProxyHeadersTests
{
    #region Always Hop-By-Hop Names Tests

    [Theory]
    [InlineData("Connection")]
    [InlineData("Keep-Alive")]
    [InlineData("Transfer-Encoding")]
    [InlineData("TE")]
    [InlineData("Trailer")]
    [InlineData("Upgrade")]
    [InlineData("Proxy-Authenticate")]
    [InlineData("Proxy-Authorization")]
    public void IsHopByHop_AlwaysHopByHopNames_ReturnsTrue(string headerName)
    {
        var result = ProxyHeaders.IsHopByHop(headerName, []);

        Assert.True(result);
    }

    #endregion

    #region Case-Insensitivity Tests

    [Fact]
    public void IsHopByHop_AlwaysHopByHop_CaseInsensitive_Uppercase()
    {
        var result = ProxyHeaders.IsHopByHop("TRANSFER-ENCODING", []);

        Assert.True(result);
    }

    [Fact]
    public void IsHopByHop_AlwaysHopByHop_CaseInsensitive_Lowercase()
    {
        var result = ProxyHeaders.IsHopByHop("transfer-encoding", []);

        Assert.True(result);
    }

    [Fact]
    public void IsHopByHop_AlwaysHopByHop_CaseInsensitive_MixedCase()
    {
        var result = ProxyHeaders.IsHopByHop("Keep-alive", []);

        Assert.True(result);
    }

    #endregion

    #region Custom Header Named In Connection Value Tests

    [Fact]
    public void IsHopByHop_CustomHeader_NamedInConnection_SingleToken_ReturnsTrue()
    {
        var result = ProxyHeaders.IsHopByHop("X-Custom", ["X-Custom"]);

        Assert.True(result);
    }

    [Fact]
    public void IsHopByHop_CustomHeader_NamedInConnection_CommaSeparatedTokens_ReturnsTrue()
    {
        var result = ProxyHeaders.IsHopByHop("X-Custom", ["close, X-Custom"]);

        Assert.True(result);
    }

    [Fact]
    public void IsHopByHop_CustomHeader_NamedInConnection_MultipleHeaderLines_ReturnsTrue()
    {
        var result = ProxyHeaders.IsHopByHop("X-Custom", ["close", "X-Custom"]);

        Assert.True(result);
    }

    [Fact]
    public void IsHopByHop_CustomHeader_NamedInConnection_TokensWithWhitespace_ReturnsTrue()
    {
        var result = ProxyHeaders.IsHopByHop("X-Custom", ["close ,  X-Custom  "]);

        Assert.True(result);
    }

    [Fact]
    public void IsHopByHop_CustomHeader_NamedInConnection_CaseInsensitiveToken_ReturnsTrue()
    {
        var result = ProxyHeaders.IsHopByHop("x-custom", ["X-Custom"]);

        Assert.True(result);
    }

    #endregion

    #region Not Hop-By-Hop Tests

    [Fact]
    public void IsHopByHop_OrdinaryHeader_EmptyConnectionValues_ReturnsFalse()
    {
        var result = ProxyHeaders.IsHopByHop("Accept", []);

        Assert.False(result);
    }

    [Fact]
    public void IsHopByHop_OrdinaryHeader_UnrelatedConnectionValue_ReturnsFalse()
    {
        var result = ProxyHeaders.IsHopByHop("Accept", ["close"]);

        Assert.False(result);
    }

    [Fact]
    public void IsHopByHop_ProxyConnection_NotInAlwaysSet_EmptyConnection_ReturnsFalse()
    {
        var result = ProxyHeaders.IsHopByHop("Proxy-Connection", []);

        Assert.False(result);
    }

    [Fact]
    public void IsHopByHop_ProxyConnection_NamedInConnection_ReturnsTrue()
    {
        var result = ProxyHeaders.IsHopByHop("Proxy-Connection", ["Proxy-Connection"]);

        Assert.True(result);
    }

    #endregion
}
