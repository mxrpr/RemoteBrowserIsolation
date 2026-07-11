namespace RemoteBrowserIsolation.Server.Services.Proxy;

// Shared hop-by-hop header logic for the TLS-intercepting proxy, per plans/9_TLS_proxy.md's tunnel
// parser spec. Used on both the browser->origin (OriginForwarder) and origin->browser (tunnel
// response writer) directions so the two can't drift apart.
public static class ProxyHeaders
{
    // Headers meaningful only to one specific hop (browser<->proxy or proxy<->origin), never
    // end-to-end -- always stripped, regardless of the request/response's own Connection value.
    private static readonly HashSet<string> AlwaysHopByHop = new(StringComparer.OrdinalIgnoreCase)
    {
        "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade",
    };

    // True if headerName must not be forwarded across this hop -- either because it's always
    // hop-by-hop, or because it was named in this specific message's own Connection header value
    // (RFC 7230 §6.1, e.g. "Connection: X-Custom-Header").
    public static bool IsHopByHop(string headerName, IEnumerable<string> connectionHeaderValues)
    {
        if (AlwaysHopByHop.Contains(headerName))
        {
            return true;
        }

        foreach (string connectionValue in connectionHeaderValues)
        {
            foreach (string token in connectionValue.Split(',', StringSplitOptions.TrimEntries | StringSplitOptions.RemoveEmptyEntries))
            {
                if (string.Equals(token, headerName, StringComparison.OrdinalIgnoreCase))
                {
                    return true;
                }
            }
        }

        return false;
    }
}
