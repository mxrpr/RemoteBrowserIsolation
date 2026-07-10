namespace RemoteBrowserIsolation.Server.Services;

public sealed record PageDownloadResult(bool Success, byte[]? Content, string? ContentType, string? ErrorMessage);

public interface IPageDownloader
{
    Task<PageDownloadResult> DownloadAsync(Uri url, CancellationToken cancellationToken = default);
}

public sealed class PageDownloader(HttpClient httpClient, ILogger<PageDownloader> logger) : IPageDownloader
{
    public async Task<PageDownloadResult> DownloadAsync(Uri url, CancellationToken cancellationToken = default)
    {
        if (url.Scheme != Uri.UriSchemeHttp && url.Scheme != Uri.UriSchemeHttps)
        {
            return new PageDownloadResult(false, null, null, $"Unsupported URL scheme: {url.Scheme}");
        }

        try
        {
            using var response = await httpClient.GetAsync(url, HttpCompletionOption.ResponseHeadersRead, cancellationToken);
            if (!response.IsSuccessStatusCode)
            {
                logger.LogWarning("Fetch of {Url} failed with status {StatusCode}", url, (int)response.StatusCode);
                return new PageDownloadResult(false, null, null, $"Server returned {(int)response.StatusCode} {response.ReasonPhrase}");
            }

            var content = await response.Content.ReadAsByteArrayAsync(cancellationToken);
            var contentType = response.Content.Headers.ContentType?.ToString();
            return new PageDownloadResult(true, content, contentType, null);
        }
        catch (TaskCanceledException) when (!cancellationToken.IsCancellationRequested)
        {
            logger.LogWarning("Fetch of {Url} timed out", url);
            return new PageDownloadResult(false, null, null, "Request timed out");
        }
        catch (HttpRequestException ex)
        {
            logger.LogWarning(ex, "Fetch of {Url} failed", url);
            return new PageDownloadResult(false, null, null, $"Request failed: {ex.Message}");
        }
    }
}
