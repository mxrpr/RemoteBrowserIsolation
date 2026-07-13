using System.Text;
using RemoteBrowserIsolation.Server.Services.Proxy;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for HttpMessageIO static methods: ParseRequestLine, ReadHeadersAsync, ReadBodyAsync, WriteResponseAsync.
public class HttpMessageIOTests
{
    #region ParseRequestLine Tests

    [Fact]
    public void ParseRequestLine_ValidGetRequest_ReturnsCorrectParts()
    {
        var result = HttpMessageIO.ParseRequestLine("GET /foo HTTP/1.1");

        Assert.NotNull(result);
        Assert.Equal("GET", result.Method);
        Assert.Equal("/foo", result.Target);
        Assert.Equal("HTTP/1.1", result.HttpVersion);
    }

    [Fact]
    public void ParseRequestLine_ConnectRequest_ReturnsCorrectParts()
    {
        var result = HttpMessageIO.ParseRequestLine("CONNECT example.com:443 HTTP/1.1");

        Assert.NotNull(result);
        Assert.Equal("CONNECT", result.Method);
        Assert.Equal("example.com:443", result.Target);
        Assert.Equal("HTTP/1.1", result.HttpVersion);
    }

    [Fact]
    public void ParseRequestLine_AbsoluteUriTarget_ReturnsCorrectParts()
    {
        var result = HttpMessageIO.ParseRequestLine("POST http://example.com/path HTTP/1.0");

        Assert.NotNull(result);
        Assert.Equal("POST", result.Method);
        Assert.Equal("http://example.com/path", result.Target);
        Assert.Equal("HTTP/1.0", result.HttpVersion);
    }

    [Fact]
    public void ParseRequestLine_HttpVersionCaseInsensitive_ReturnsNonNull()
    {
        var result = HttpMessageIO.ParseRequestLine("GET / http/1.1");

        Assert.NotNull(result);
    }

    [Fact]
    public void ParseRequestLine_OnlyTwoTokens_ReturnsNull()
    {
        var result = HttpMessageIO.ParseRequestLine("GET /foo");

        Assert.Null(result);
    }

    [Fact]
    public void ParseRequestLine_EmptyString_ReturnsNull()
    {
        var result = HttpMessageIO.ParseRequestLine("");

        Assert.Null(result);
    }

    [Fact]
    public void ParseRequestLine_VersionTokenNotHttp_ReturnsNull()
    {
        var result = HttpMessageIO.ParseRequestLine("GET /foo BOGUS/1.1");

        Assert.Null(result);
    }

    [Fact]
    public void ParseRequestLine_FourTokens_ThirdTokenIncludesTrailing()
    {
        // Split(' ', 3) should split into at most 3 parts, so "extra" becomes part of HttpVersion.
        // The requirement is that parts[2] starts with "HTTP/" -- since " extra" doesn't, this should fail.
        // But "GET /foo HTTP/1.1 extra" when split with limit 3 gives ["GET", "/foo", "HTTP/1.1 extra"]
        // which starts with "HTTP/" so it should pass.
        var result = HttpMessageIO.ParseRequestLine("GET /foo HTTP/1.1 extra");

        Assert.NotNull(result);
        Assert.Equal("GET", result.Method);
        Assert.Equal("/foo", result.Target);
        Assert.Equal("HTTP/1.1 extra", result.HttpVersion);
    }

    #endregion

    #region ReadHeadersAsync Tests

    [Fact]
    public async Task ReadHeadersAsync_NormalHeaders_ReturnsAllParsed()
    {
        var data = "Host: example.com\r\nContent-Type: text/html\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Equal(2, headers.Count);
        Assert.Equal("Host", headers[0].Name);
        Assert.Equal("example.com", headers[0].Value);
        Assert.Equal("Content-Type", headers[1].Name);
        Assert.Equal("text/html", headers[1].Value);
    }

    [Fact]
    public async Task ReadHeadersAsync_StopsAtBlankLine_IgnoresRemainder()
    {
        var data = "A: 1\r\n\r\nB: 2\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Single(headers);
        Assert.Equal("A", headers[0].Name);
        Assert.Equal("1", headers[0].Value);
    }

    [Fact]
    public async Task ReadHeadersAsync_MalformedLineNoColon_Skipped()
    {
        var data = "Host: good\r\nNOCOLON\r\nX-Foo: bar\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Equal(2, headers.Count);
        Assert.Equal("Host", headers[0].Name);
        Assert.Equal("good", headers[0].Value);
        Assert.Equal("X-Foo", headers[1].Name);
        Assert.Equal("bar", headers[1].Value);
    }

    [Fact]
    public async Task ReadHeadersAsync_ColonAtPositionZero_Skipped()
    {
        var data = ":bad\r\nX-Ok: yes\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Single(headers);
        Assert.Equal("X-Ok", headers[0].Name);
        Assert.Equal("yes", headers[0].Value);
    }

    [Fact]
    public async Task ReadHeadersAsync_ValueWhitespaceTrimmed()
    {
        var data = "X-Foo:  bar  \r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Single(headers);
        Assert.Equal("bar", headers[0].Value);
    }

    [Fact]
    public async Task ReadHeadersAsync_NameWhitespaceTrimmed()
    {
        var data = " X-Foo : bar\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Single(headers);
        Assert.Equal("X-Foo", headers[0].Name);
    }

    [Fact]
    public async Task ReadHeadersAsync_ImmediateBlankLine_ReturnsEmptyList()
    {
        var data = "\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Empty(headers);
    }

    [Fact]
    public async Task ReadHeadersAsync_BareLineFeed_ParsedCorrectly()
    {
        var data = "Host: example.com\nX-Foo: bar\n\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Equal(2, headers.Count);
        Assert.Equal("Host", headers[0].Name);
        Assert.Equal("example.com", headers[0].Value);
        Assert.Equal("X-Foo", headers[1].Name);
        Assert.Equal("bar", headers[1].Value);
    }

    [Fact]
    public async Task ReadHeadersAsync_MultipleColonsInValue_FirstColonSplits()
    {
        var data = "Date: Mon, 01 Jan 2024 00:00:00 GMT\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);

        var headers = await HttpMessageIO.ReadHeadersAsync(reader, CancellationToken.None);

        Assert.Single(headers);
        Assert.Equal("Date", headers[0].Name);
        Assert.Equal("Mon, 01 Jan 2024 00:00:00 GMT", headers[0].Value);
    }

    #endregion

    #region ReadBodyAsync Tests

    [Fact]
    public async Task ReadBodyAsync_NoHeaders_ReturnsNull()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(""));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader>();

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.Null(result);
    }

    [Fact]
    public async Task ReadBodyAsync_ContentLengthZero_ReturnsNull()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(""));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Content-Length", "0") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.Null(result);
    }

    [Fact]
    public async Task ReadBodyAsync_ContentLengthNegative_ReturnsNull()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes("hello"));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Content-Length", "-5") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.Null(result);
    }

    [Fact]
    public async Task ReadBodyAsync_ContentLengthValid_ReadsExactBytes()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes("hello"));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Content-Length", "5") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal("hello", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_ContentLengthExceedsAvailableBytes_ReturnsTruncated()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes("hi"));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Content-Length", "10") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal(2, result.Length);
        Assert.Equal("hi", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_ContentLengthCaseInsensitive_Recognized()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes("abc"));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("content-length", "3") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal("abc", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_TransferEncodingGzipNoChunked_FallsBackToContentLength()
    {
        var stream = new MemoryStream(Encoding.ASCII.GetBytes("hello"));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader>
        {
            new("Transfer-Encoding", "gzip"),
            new("Content-Length", "5")
        };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal("hello", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_ChunkedTakesPriorityOverContentLength()
    {
        var data = "5\r\nhello\r\n0\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader>
        {
            new("Transfer-Encoding", "chunked"),
            new("Content-Length", "999")
        };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal("hello", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_SingleChunk_ReturnsChunkData()
    {
        var data = "5\r\nhello\r\n0\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal("hello", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_MultipleChunks_Concatenated()
    {
        var data = "3\r\nfoo\r\n3\r\nbar\r\n0\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal("foobar", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_ChunkWithExtension_ExtensionIgnored()
    {
        var data = "5;name=value\r\nhello\r\n0\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal("hello", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_ChunkSizeUppercaseHex_Parsed()
    {
        var data = "A\r\n0123456789\r\n0\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal(10, result.Length);
        Assert.Equal("0123456789", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_ChunkSizeMixedCaseHex_Parsed()
    {
        var body = new string('x', 31);
        var data = $"1f\r\n{body}\r\n0\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal(31, result.Length);
        Assert.Equal(body, Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_InvalidHexChunkSize_ReturnsEmptyArray()
    {
        var data = "ZZZZ\r\nhello\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Empty(result);
    }

    [Fact]
    public async Task ReadBodyAsync_EmptyChunkSizeLine_ReturnsEmptyArray()
    {
        var data = "\r\nhello\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Empty(result);
    }

    [Fact]
    public async Task ReadBodyAsync_ZeroChunkTerminatesEarly()
    {
        var data = "3\r\nfoo\r\n0\r\n\r\n3\r\nbar\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal(3, result.Length);
        Assert.Equal("foo", Encoding.ASCII.GetString(result));
    }

    [Fact]
    public async Task ReadBodyAsync_TransferEncodingValueCaseInsensitive()
    {
        var data = "5\r\nhello\r\n0\r\n\r\n";
        var stream = new MemoryStream(Encoding.ASCII.GetBytes(data));
        var reader = new ProxyStreamReader(stream);
        var headers = new List<ProxyHeader> { new("Transfer-Encoding", "Chunked") };

        var result = await HttpMessageIO.ReadBodyAsync(reader, headers, CancellationToken.None);

        Assert.NotNull(result);
        Assert.Equal("hello", Encoding.ASCII.GetString(result));
    }

    #endregion

    #region WriteResponseAsync Tests

    [Fact]
    public async Task WriteResponseAsync_StatusLine_CorrectFormat()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = [],
            Body = []
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());
        Assert.StartsWith("HTTP/1.1 200 OK\r\n", result);
    }

    [Fact]
    public async Task WriteResponseAsync_NonOkStatus_CorrectStatusCode()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 404,
            ReasonPhrase = "Not Found",
            Headers = [],
            Body = []
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());
        Assert.StartsWith("HTTP/1.1 404 Not Found\r\n", result);
    }

    [Fact]
    public async Task WriteResponseAsync_EmptyReasonPhrase_FallsBackToOK()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 404,
            ReasonPhrase = "",
            Headers = [],
            Body = []
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());
        Assert.StartsWith("HTTP/1.1 404 OK\r\n", result);
    }

    [Fact]
    public async Task WriteResponseAsync_HopByHopHeadersStripped()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = new List<ProxyHeader>
            {
                new("Connection", "keep-alive"),
                new("Transfer-Encoding", "chunked"),
                new("Keep-Alive", "timeout=5"),
                new("Upgrade", "websocket"),
                new("TE", "trailers"),
                new("Trailer", "Expires"),
                new("Proxy-Authenticate", "Basic"),
                new("Proxy-Authorization", "Basic xyz")
            },
            Body = []
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());

        Assert.DoesNotContain("keep-alive", result);
        Assert.DoesNotContain("Transfer-Encoding:", result);
        Assert.DoesNotContain("Keep-Alive:", result);
        Assert.DoesNotContain("Upgrade:", result);
        Assert.DoesNotContain("TE:", result);
        Assert.DoesNotContain("Trailer:", result);
        Assert.DoesNotContain("Proxy-Authenticate:", result);
        Assert.DoesNotContain("Proxy-Authorization:", result);
        Assert.Contains("Connection: close", result);
    }

    [Fact]
    public async Task WriteResponseAsync_ContentLengthFromResponseHeaderStripped_ReplacedWithBodyLength()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = new List<ProxyHeader> { new("Content-Length", "999") },
            Body = new byte[] { 1, 2, 3 }
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());

        Assert.Contains("Content-Length: 3\r\n", result);
        Assert.DoesNotContain("Content-Length: 999", result);
    }

    [Fact]
    public async Task WriteResponseAsync_ConnectionCloseAlwaysPresent()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = [],
            Body = []
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());
        Assert.Contains("Connection: close\r\n", result);
    }

    [Fact]
    public async Task WriteResponseAsync_NormalHeadersPassThrough()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = new List<ProxyHeader>
            {
                new("Content-Type", "text/html"),
                new("X-Custom", "value")
            },
            Body = []
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());
        Assert.Contains("Content-Type: text/html\r\n", result);
        Assert.Contains("X-Custom: value\r\n", result);
    }

    [Fact]
    public async Task WriteResponseAsync_RFC7230ConnectionNamed_HeaderAlsoStripped()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = new List<ProxyHeader>
            {
                new("Connection", "X-Remove"),
                new("X-Remove", "sensitive")
            },
            Body = []
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());

        // X-Remove should not appear in the output because it's named in the Connection header
        Assert.DoesNotContain("X-Remove:", result);
        // Connection: close should be present (forced by WriteResponseAsync)
        Assert.Contains("Connection: close", result);
    }

    [Fact]
    public async Task WriteResponseAsync_EmptyBody_ZeroContentLength_NoBodyBytes()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = [],
            Body = []
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());
        Assert.Contains("Content-Length: 0\r\n", result);

        // The output should end with the blank line (no body appended)
        Assert.EndsWith("\r\n", result);
    }

    [Fact]
    public async Task WriteResponseAsync_NonEmptyBody_BodyBytesAppendedAfterBlankLine()
    {
        var output = new MemoryStream();
        var response = new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = [],
            Body = Encoding.ASCII.GetBytes("hello")
        };

        await HttpMessageIO.WriteResponseAsync(output, response, CancellationToken.None);

        var result = Encoding.ASCII.GetString(output.ToArray());

        Assert.EndsWith("\r\nhello", result);
    }

    #endregion
}
