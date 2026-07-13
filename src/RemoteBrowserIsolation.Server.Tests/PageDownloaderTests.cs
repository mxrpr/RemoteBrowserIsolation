using System.Net;
using System.Text;
using Microsoft.Extensions.Logging.Abstractions;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Tests;

/// Tests for PageDownloader: HTTP GET requests with error handling for network failures, invalid schemes, and timeouts.
public class PageDownloaderTests
{
    /// Fake HttpMessageHandler that delegates to a test-provided callback for flexible per-test response behavior.
    private sealed class FakeHttpMessageHandler : HttpMessageHandler
    {
        private readonly Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> _sendAsync;
        private int _invocationCount = 0;

        public int InvocationCount => _invocationCount;

        public FakeHttpMessageHandler(Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> sendAsync)
        {
            _sendAsync = sendAsync;
        }

        protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
        {
            _invocationCount++;
            return await _sendAsync(request, cancellationToken);
        }
    }

    #region Non-HTTP Scheme Tests

    [Fact]
    public async Task DownloadAsync_NonHttpScheme_ReturnsFailureWithoutCallingHandler()
    {
        var handlerInvoked = false;
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            handlerInvoked = true;
            return new HttpResponseMessage(HttpStatusCode.OK);
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("ftp://example.com/file"));

        Assert.False(result.Success);
        Assert.Null(result.Content);
        Assert.Null(result.ContentType);
        Assert.NotNull(result.ErrorMessage);
        Assert.Contains("Unsupported URL scheme", result.ErrorMessage);
        Assert.Contains("ftp", result.ErrorMessage);
        Assert.False(handlerInvoked);
        Assert.Equal(0, handler.InvocationCount);
    }

    [Fact]
    public async Task DownloadAsync_FileScheme_ReturnsFailureWithoutCallingHandler()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            return new HttpResponseMessage(HttpStatusCode.OK);
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("file:///etc/passwd"));

        Assert.False(result.Success);
        Assert.Null(result.Content);
        Assert.Null(result.ContentType);
        Assert.NotNull(result.ErrorMessage);
        Assert.Contains("Unsupported URL scheme", result.ErrorMessage);
        Assert.Contains("file", result.ErrorMessage);
        Assert.Equal(0, handler.InvocationCount);
    }

    #endregion

    #region Success Cases

    [Fact]
    public async Task DownloadAsync_2xxSuccess_ReturnsContentAndContentType()
    {
        var expectedContent = Encoding.UTF8.GetBytes("<html><body>Hello</body></html>");
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            var response = new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent(expectedContent)
            };
            response.Content.Headers.ContentType = new System.Net.Http.Headers.MediaTypeHeaderValue("text/html");
            response.Content.Headers.ContentType.CharSet = "utf-8";
            return response;
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/index.html"));

        Assert.True(result.Success);
        Assert.NotNull(result.Content);
        Assert.Equal(expectedContent, result.Content);
        Assert.NotNull(result.ContentType);
        Assert.Contains("text/html", result.ContentType);
        Assert.Null(result.ErrorMessage);
    }

    [Fact]
    public async Task DownloadAsync_2xxSuccessNoContentType_ReturnsContentWithNullContentType()
    {
        var expectedContent = Encoding.UTF8.GetBytes("data");
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            var response = new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent(expectedContent)
            };
            // No Content-Type header set
            return response;
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/data"));

        Assert.True(result.Success);
        Assert.NotNull(result.Content);
        Assert.Equal(expectedContent, result.Content);
        Assert.Null(result.ContentType);
        Assert.Null(result.ErrorMessage);
    }

    #endregion

    #region Non-Success Status Code Tests

    [Fact]
    public async Task DownloadAsync_NonSuccessStatusCode_ReturnsFailureWithStatus()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            return new HttpResponseMessage(HttpStatusCode.NotFound)
            {
                ReasonPhrase = "Not Found"
            };
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/missing"));

        Assert.False(result.Success);
        Assert.Null(result.Content);
        Assert.Null(result.ContentType);
        Assert.NotNull(result.ErrorMessage);
        Assert.Contains("404", result.ErrorMessage);
        Assert.Contains("Not Found", result.ErrorMessage);
    }

    [Fact]
    public async Task DownloadAsync_500ServerError_ReturnsFailureWithStatus()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            return new HttpResponseMessage(HttpStatusCode.InternalServerError)
            {
                ReasonPhrase = "Internal Server Error"
            };
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/error"));

        Assert.False(result.Success);
        Assert.Null(result.Content);
        Assert.NotNull(result.ErrorMessage);
        Assert.Contains("500", result.ErrorMessage);
    }

    [Fact]
    public async Task DownloadAsync_403Forbidden_ReturnsFailureWithStatus()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            return new HttpResponseMessage(HttpStatusCode.Forbidden)
            {
                ReasonPhrase = "Forbidden"
            };
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/forbidden"));

        Assert.False(result.Success);
        Assert.Null(result.Content);
        Assert.NotNull(result.ErrorMessage);
        Assert.Contains("403", result.ErrorMessage);
    }

    #endregion

    #region Timeout/Cancellation Tests

    [Fact]
    public async Task DownloadAsync_TaskCanceledDueToTimeout_ReturnsTimeoutMessage()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            throw new TaskCanceledException();
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/slow"), CancellationToken.None);

        Assert.False(result.Success);
        Assert.Null(result.Content);
        Assert.Null(result.ContentType);
        Assert.NotNull(result.ErrorMessage);
        Assert.Equal("Request timed out", result.ErrorMessage);
    }

    [Fact]
    public async Task DownloadAsync_ExplicitCancellationToken_DoesNotTreatAsCancellationDueToTimeout()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            throw new TaskCanceledException();
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        using var cts = new CancellationTokenSource();
        cts.Cancel();

        // Even though TaskCanceledException is thrown, cancellationToken.IsCancellationRequested is true,
        // so the exception is not caught (it re-throws or propagates).
        // However, the code catches TaskCanceledException only when !cancellationToken.IsCancellationRequested,
        // so an already-cancelled token will let the exception propagate.
        // In this case, the exception should propagate (throw).
        var ex = await Assert.ThrowsAsync<TaskCanceledException>(
            () => downloader.DownloadAsync(new Uri("https://example.com/slow"), cts.Token)
        );
    }

    #endregion

    #region HttpRequestException Tests

    [Fact]
    public async Task DownloadAsync_HttpRequestException_ReturnsFailureWithMessage()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            throw new HttpRequestException("Connection refused");
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/unreachable"));

        Assert.False(result.Success);
        Assert.Null(result.Content);
        Assert.Null(result.ContentType);
        Assert.NotNull(result.ErrorMessage);
        Assert.Contains("Request failed", result.ErrorMessage);
        Assert.Contains("Connection refused", result.ErrorMessage);
    }

    [Fact]
    public async Task DownloadAsync_HttpRequestExceptionDns_ReturnsFailureWithMessage()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            throw new HttpRequestException("Name or service not known");
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://nonexistent.invalid/path"));

        Assert.False(result.Success);
        Assert.Null(result.Content);
        Assert.NotNull(result.ErrorMessage);
        Assert.Contains("Request failed", result.ErrorMessage);
        Assert.Contains("Name or service not known", result.ErrorMessage);
    }

    #endregion

    #region HTTP vs HTTPS Tests

    [Fact]
    public async Task DownloadAsync_HttpScheme_Succeeds()
    {
        var expectedContent = Encoding.UTF8.GetBytes("http content");
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            var response = new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent(expectedContent)
            };
            return response;
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("http://example.com/page"));

        Assert.True(result.Success);
        Assert.Equal(expectedContent, result.Content);
    }

    [Fact]
    public async Task DownloadAsync_HttpsScheme_Succeeds()
    {
        var expectedContent = Encoding.UTF8.GetBytes("https content");
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            var response = new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent(expectedContent)
            };
            return response;
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/page"));

        Assert.True(result.Success);
        Assert.Equal(expectedContent, result.Content);
    }

    #endregion

    #region Content Size Tests

    [Fact]
    public async Task DownloadAsync_EmptyContent_ReturnsEmptyByteArray()
    {
        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            var response = new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent([])
            };
            response.Content.Headers.ContentType = new System.Net.Http.Headers.MediaTypeHeaderValue("text/plain");
            return response;
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/empty"));

        Assert.True(result.Success);
        Assert.NotNull(result.Content);
        Assert.Empty(result.Content);
    }

    [Fact]
    public async Task DownloadAsync_LargeContent_ReturnsFullContent()
    {
        var largeContent = new byte[1024 * 100]; // 100 KB
        for (int i = 0; i < largeContent.Length; i++)
        {
            largeContent[i] = (byte)(i % 256);
        }

        var handler = new FakeHttpMessageHandler(async (req, ct) =>
        {
            var response = new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new ByteArrayContent(largeContent)
            };
            response.Content.Headers.ContentType = new System.Net.Http.Headers.MediaTypeHeaderValue("application/octet-stream");
            return response;
        });
        var httpClient = new HttpClient(handler);
        var downloader = new PageDownloader(httpClient, NullLogger<PageDownloader>.Instance);

        var result = await downloader.DownloadAsync(new Uri("https://example.com/large"));

        Assert.True(result.Success);
        Assert.NotNull(result.Content);
        Assert.Equal(largeContent.Length, result.Content.Length);
        Assert.Equal(largeContent, result.Content);
    }

    #endregion
}
