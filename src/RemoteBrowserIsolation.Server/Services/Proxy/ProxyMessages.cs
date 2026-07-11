namespace RemoteBrowserIsolation.Server.Services.Proxy;

// One header name/value pair. A plain list (not a dictionary) since HTTP allows repeated header
// names (e.g. multiple Set-Cookie) and order can matter for some clients.
public sealed record ProxyHeader(string Name, string Value);

// A browser request parsed off the tunnel (CONNECT-then-decrypt or plain-HTTP absolute-URI), ready
// to be forwarded to its origin by IOriginForwarder.
public sealed class ProxyHttpRequest
{
    public required string Method { get; init; }
    public required Uri Uri { get; init; }
    public required List<ProxyHeader> Headers { get; init; }
    public byte[]? Body { get; init; }
}

// An origin's response, fully buffered (see plans/9_TLS_proxy.md's tunnel parser spec: the body may
// need rewriting by HtmlNoInputInjector before it's written back to the browser, so this project
// buffers rather than streams for this iteration).
public sealed class ProxyHttpResponse
{
    public required int StatusCode { get; init; }
    public required string ReasonPhrase { get; init; }
    public required List<ProxyHeader> Headers { get; init; }
    public required byte[] Body { get; init; }
}
