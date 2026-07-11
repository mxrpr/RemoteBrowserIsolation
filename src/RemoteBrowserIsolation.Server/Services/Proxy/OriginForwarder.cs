namespace RemoteBrowserIsolation.Server.Services.Proxy;

public interface IOriginForwarder
{
    // Forwards a parsed browser request straight to its origin (the CONNECT/absolute-URI target)
    // and returns the origin's real status/headers/body. Deliberately NOT built on IPageDownloader
    // -- see plans/9_TLS_proxy.md's grounding section: PageDownloader is GET-only and flattens every
    // response to a bare 200 + content-type, which would silently break redirects, cookies, logins,
    // and non-GET methods (POST forms, XHR).
    Task<ProxyHttpResponse> ForwardAsync(ProxyHttpRequest request, CancellationToken cancellationToken = default);
}

// Dedicated HttpClient-based forwarder for the TLS-intercepting proxy's HTML-mode path. Registered
// as a typed client in Program.cs with AllowAutoRedirect=false (relay 3xx to the browser rather than
// following it ourselves), UseCookies=false (the browser owns cookies; this just relays
// Cookie/Set-Cookie as plain headers), and automatic decompression off (so a response's
// Content-Encoding and body bytes stay consistent with each other unless the tunnel loop explicitly
// rewrites the body, per the parser spec).
public sealed class OriginForwarder(HttpClient httpClient) : IOriginForwarder
{
    public async Task<ProxyHttpResponse> ForwardAsync(ProxyHttpRequest request, CancellationToken cancellationToken = default)
    {
        using var message = new HttpRequestMessage(new HttpMethod(request.Method), request.Uri);

        IEnumerable<string> connectionValues = request.Headers
            .Where(h => string.Equals(h.Name, "Connection", StringComparison.OrdinalIgnoreCase))
            .Select(h => h.Value);

        if (request.Body is { Length: > 0 })
        {
            message.Content = new ByteArrayContent(request.Body);
        }

        foreach (ProxyHeader header in request.Headers)
        {
            // Host is set implicitly from request.Uri by HttpClient; forwarding the browser's
            // original Host header explicitly would conflict with that setting.
            if (ProxyHeaders.IsHopByHop(header.Name, connectionValues)
                || string.Equals(header.Name, "Host", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            // Content-* headers (Content-Type, Content-Length, ...) must land on
            // message.Content.Headers, not message.Headers -- HttpRequestMessage.Headers rejects
            // them outright, hence the fallback to Content.Headers on failure.
            if (!message.Headers.TryAddWithoutValidation(header.Name, header.Value))
            {
                message.Content?.Headers.TryAddWithoutValidation(header.Name, header.Value);
            }
        }

        using HttpResponseMessage response = await httpClient.SendAsync(message, HttpCompletionOption.ResponseContentRead, cancellationToken);
        byte[] body = await response.Content.ReadAsByteArrayAsync(cancellationToken);

        List<ProxyHeader> headers = [];
        foreach (var header in response.Headers)
        {
            foreach (string value in header.Value)
            {
                headers.Add(new ProxyHeader(header.Key, value));
            }
        }
        foreach (var header in response.Content.Headers)
        {
            foreach (string value in header.Value)
            {
                headers.Add(new ProxyHeader(header.Key, value));
            }
        }

        return new ProxyHttpResponse
        {
            StatusCode = (int)response.StatusCode,
            ReasonPhrase = response.ReasonPhrase ?? string.Empty,
            Headers = headers,
            Body = body,
        };
    }
}
