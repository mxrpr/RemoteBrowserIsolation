using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Authentication;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.Extensions.Options;
using RemoteBrowserIsolation.Server.Models;
using RemoteBrowserIsolation.Server.Models.Proxy;

namespace RemoteBrowserIsolation.Server.Services.Proxy;

// Background TcpListener-based forward proxy that intercepts TLS for policy-gated HTML/video
// hosting (see plans/9_TLS_proxy.md). Not Kestrel-hosted: Kestrel has no notion of "accept a plain
// CONNECT line, respond 200, then start a TLS handshake on the same socket," which is the actual
// forward-proxy protocol -- this needs a hand-rolled component. Registered as an IHostedService in
// Program.cs.
public sealed class TlsInterceptingProxyServer(
    IOptions<ProxyOptions> options,
    ILeafCertificateMinter certMinter,
    IOriginForwarder originForwarder,
    IHtmlNoInputInjector noInputInjector,
    IServiceScopeFactory scopeFactory,
    IServer kestrelServer,
    IHostApplicationLifetime appLifetime,
    ILogger<TlsInterceptingProxyServer> logger) : BackgroundService
{
    private readonly ProxyOptions _options = options.Value;
    private string? _selfOriginBaseUrl;
    private int? _selfOriginPort;

    // Accept loop: binds the configured (host-only, non-TLS) listener port and spawns one
    // fire-and-forget task per accepted connection, matching this project's existing
    // async-fire-and-forget pattern (WebRtcSessionManager).
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var listener = new TcpListener(IPAddress.Parse(_options.Bind), _options.Port);
        listener.Start();
        logger.LogInformation("TLS-intercepting proxy listening on {Bind}:{Port}", _options.Bind, _options.Port);

        try
        {
            while (!stoppingToken.IsCancellationRequested)
            {
                TcpClient client;
                try
                {
                    client = await listener.AcceptTcpClientAsync(stoppingToken);
                }
                catch (OperationCanceledException)
                {
                    break;
                }

                _ = HandleConnectionAsync(client, stoppingToken);
            }
        }
        finally
        {
            listener.Stop();
        }
    }

    // Top-level per-connection dispatch: reads the first request line to decide whether this is a
    // CONNECT (HTTPS interception path) or an absolute-URI request (plain-HTTP proxying path). A
    // line that's neither is dropped -- a browser configured with an HTTP proxy only ever sends one
    // of these two forms, so there's no destination to blind-tunnel an unrecognized line to.
    private async Task HandleConnectionAsync(TcpClient client, CancellationToken serverStoppingToken)
    {
        using (client)
        {
            try
            {
                NetworkStream ns = client.GetStream();
                var reader = new ProxyStreamReader(ns);
                string? firstLine = await reader.ReadLineAsync(serverStoppingToken);
                if (string.IsNullOrEmpty(firstLine))
                {
                    return;
                }

                HttpMessageIO.RequestLine? requestLine = HttpMessageIO.ParseRequestLine(firstLine);
                if (requestLine is null)
                {
                    return;
                }

                string? clientIp = (client.Client.RemoteEndPoint as IPEndPoint)?.Address.ToString();

                if (string.Equals(requestLine.Method, "CONNECT", StringComparison.OrdinalIgnoreCase))
                {
                    await HandleConnectAsync(ns, reader, requestLine, clientIp, serverStoppingToken);
                }
                else if (Uri.TryCreate(requestLine.Target, UriKind.Absolute, out Uri? absoluteUri)
                    && (absoluteUri.Scheme == Uri.UriSchemeHttp || absoluteUri.Scheme == Uri.UriSchemeHttps))
                {
                    await HandlePlainHttpAsync(ns, reader, requestLine, absoluteUri, clientIp, serverStoppingToken);
                }
            }
            catch (Exception ex) when (ex is not OperationCanceledException)
            {
                logger.LogWarning(ex, "Proxy connection from {RemoteEndPoint} failed", client.Client.RemoteEndPoint);
            }
        }
    }

    // Handles "CONNECT host:port HTTP/1.1" -- self-origin bypass, blind-tunnel for
    // non-intercepted ports, or policy-check + TLS-intercept for everything else.
    private async Task HandleConnectAsync(NetworkStream ns, ProxyStreamReader reader, HttpMessageIO.RequestLine requestLine, string? clientIp, CancellationToken cancellationToken)
    {
        (string host, int port) = ParseHostPort(requestLine.Target, defaultPort: 443);

        if (IsSelfHost(host))
        {
            await BlindTunnelToSelfOriginAsync(ns, reader, isConnect: true, requestLine, cancellationToken);
            return;
        }

        if (!_options.InterceptPorts.Contains(port))
        {
            await BlindTunnelToOriginAsync(ns, reader, host, port, writeConnectOk: true, cancellationToken);
            return;
        }

        using IServiceScope scope = scopeFactory.CreateScope();
        IPolicyEngine policyEngine = scope.ServiceProvider.GetRequiredService<IPolicyEngine>();
        IRequestLogService requestLog = scope.ServiceProvider.GetRequiredService<IRequestLogService>();

        var probeUrl = new Uri($"https://{host}/");
        ViewMode? mode = await policyEngine.ResolveAsync(probeUrl, cancellationToken);
        if (mode is null)
        {
            await requestLog.LogAsync(probeUrl, "deny", allowed: false, clientIp, cancellationToken);
            await WriteRawAsync(ns, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n", cancellationToken);
            return;
        }

        await requestLog.LogAsync(probeUrl, mode.ToString()!, allowed: true, clientIp, cancellationToken);
        await WriteRawAsync(ns, "HTTP/1.1 200 Connection Established\r\n\r\n", cancellationToken);

        using var sslStream = new SslStream(ns, leaveInnerStreamOpen: false);
        ServerOptionsSelectionCallback selectCert = async (_, helloInfo, _, ct) =>
        {
            // SNI, not the CONNECT target string, is authoritative for what cert to present (they
            // normally match, but SNI is what the TLS layer actually checks) -- fall back to the
            // CONNECT host if the client sent no SNI at all.
            string sni = string.IsNullOrEmpty(helloInfo.ServerName) ? host : helloInfo.ServerName;
            X509Certificate2? leaf = await certMinter.GetOrMintAsync(sni, ct);
            if (leaf is null)
            {
                throw new AuthenticationException($"No leaf certificate available for '{sni}' -- is a root CA configured?");
            }

            return new SslServerAuthenticationOptions
            {
                ServerCertificate = leaf,
                // ALPN forced to http/1.1 only: parsing HTTP/2 frames is out of scope. Browsers
                // fall back to 1.1 cleanly when a server doesn't offer h2 in ALPN.
                ApplicationProtocols = [SslApplicationProtocol.Http11],
            };
        };

        try
        {
            await sslStream.AuthenticateAsServerAsync(selectCert, state: null, cancellationToken);
        }
        catch (Exception ex) when (ex is AuthenticationException or IOException)
        {
            logger.LogWarning(ex, "TLS handshake failed for {Host}", host);
            return;
        }

        await ProcessSingleExchangeAsync(sslStream, host, mode.Value, clientIp, scheme: "https", cancellationToken);
    }

    // Handles a plain-HTTP absolute-URI proxy request ("GET http://host/path HTTP/1.1", no
    // CONNECT). Same self-origin/policy logic as the CONNECT path, minus any TLS step.
    private async Task HandlePlainHttpAsync(NetworkStream ns, ProxyStreamReader reader, HttpMessageIO.RequestLine requestLine, Uri absoluteUri, string? clientIp, CancellationToken cancellationToken)
    {
        if (IsSelfHost(absoluteUri.Host))
        {
            await BlindTunnelToSelfOriginAsync(ns, reader, isConnect: false, requestLine, cancellationToken, absoluteUri);
            return;
        }

        using IServiceScope scope = scopeFactory.CreateScope();
        IPolicyEngine policyEngine = scope.ServiceProvider.GetRequiredService<IPolicyEngine>();
        IRequestLogService requestLog = scope.ServiceProvider.GetRequiredService<IRequestLogService>();

        ViewMode? mode = await policyEngine.ResolveAsync(absoluteUri, cancellationToken);
        if (mode is null)
        {
            await requestLog.LogAsync(absoluteUri, "deny", allowed: false, clientIp, cancellationToken);
            await WriteRawAsync(ns, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n", cancellationToken);
            return;
        }

        List<ProxyHeader> headers = await HttpMessageIO.ReadHeadersAsync(reader, cancellationToken);
        byte[]? body = await HttpMessageIO.ReadBodyAsync(reader, headers, cancellationToken);

        ProxyHttpResponse response = await BuildResponseAsync(requestLine.Method, absoluteUri, headers, body, mode.Value, cancellationToken);
        await requestLog.LogAsync(absoluteUri, mode.ToString()!, allowed: true, clientIp, cancellationToken);
        await HttpMessageIO.WriteResponseAsync(ns, response, cancellationToken);
    }

    // Reads exactly one HTTP/1.1 request off an already-established tunnel (post-CONNECT TLS
    // stream) and responds to it, then the caller closes the connection -- see HttpMessageIO's
    // "always close after one exchange" doc comment for why this project doesn't keep tunnels open
    // across multiple requests.
    private async Task ProcessSingleExchangeAsync(Stream tunnelStream, string host, ViewMode mode, string? clientIp, string scheme, CancellationToken cancellationToken)
    {
        var reader = new ProxyStreamReader(tunnelStream);
        string? requestLineText = await reader.ReadLineAsync(cancellationToken);
        if (string.IsNullOrEmpty(requestLineText))
        {
            return;
        }

        HttpMessageIO.RequestLine? requestLine = HttpMessageIO.ParseRequestLine(requestLineText);
        if (requestLine is null)
        {
            return;
        }

        List<ProxyHeader> headers = await HttpMessageIO.ReadHeadersAsync(reader, cancellationToken);
        byte[]? body = await HttpMessageIO.ReadBodyAsync(reader, headers, cancellationToken);

        // Requests inside a CONNECT tunnel use origin-form paths ("/path"), not absolute-URI --
        // reconstruct the absolute URL from the CONNECT host plus this request's path.
        string path = requestLine.Target.StartsWith('/') ? requestLine.Target : "/" + requestLine.Target;
        var targetUrl = new Uri($"{scheme}://{host}{path}");

        using IServiceScope scope = scopeFactory.CreateScope();
        IRequestLogService requestLog = scope.ServiceProvider.GetRequiredService<IRequestLogService>();

        ProxyHttpResponse response = await BuildResponseAsync(requestLine.Method, targetUrl, headers, body, mode, cancellationToken);
        await requestLog.LogAsync(targetUrl, mode.ToString()!, allowed: true, clientIp, cancellationToken);
        await HttpMessageIO.WriteResponseAsync(tunnelStream, response, cancellationToken);
    }

    // Mode branch shared by the CONNECT-tunnel and plain-HTTP paths: video modes get the static
    // interstitial regardless of the actual request; HTML modes get forwarded to origin, with
    // HtmlNoInput additionally running through HtmlNoInputInjector.
    private async Task<ProxyHttpResponse> BuildResponseAsync(string method, Uri targetUrl, List<ProxyHeader> headers, byte[]? body, ViewMode mode, CancellationToken cancellationToken)
    {
        if (mode is ViewMode.VideoAllowInput or ViewMode.VideoNoInput)
        {
            return await BuildVideoInterstitialResponseAsync(targetUrl, cancellationToken);
        }

        var proxyRequest = new ProxyHttpRequest { Method = method, Uri = targetUrl, Headers = headers, Body = body };
        ProxyHttpResponse originResponse = await originForwarder.ForwardAsync(proxyRequest, cancellationToken);

        bool isHtml = originResponse.Headers.Any(h =>
            string.Equals(h.Name, "Content-Type", StringComparison.OrdinalIgnoreCase)
            && h.Value.Contains("html", StringComparison.OrdinalIgnoreCase));

        if (mode != ViewMode.HtmlNoInput || !isHtml)
        {
            return originResponse;
        }

        byte[] processed = noInputInjector.Process(originResponse.Body, targetUrl, noInput: true);
        // Content-Encoding must be dropped explicitly (unlike Transfer-Encoding, it's an
        // end-to-end header) since the injected body is no longer compressed the way the origin's
        // header claims.
        List<ProxyHeader> headersWithoutEncoding = originResponse.Headers
            .Where(h => !string.Equals(h.Name, "Content-Encoding", StringComparison.OrdinalIgnoreCase))
            .ToList();

        return new ProxyHttpResponse
        {
            StatusCode = originResponse.StatusCode,
            ReasonPhrase = originResponse.ReasonPhrase,
            Headers = headersWithoutEncoding,
            Body = processed,
        };
    }

    // The response shown for VideoAllowInput/VideoNoInput hosts instead of real content: a static
    // page linking to this server's own WebRTC video viewer for the same target URL. Resolves the
    // self-origin base URL itself (async) rather than relying on it having been populated as a
    // side effect of some earlier self-origin-bypass request -- if the first proxied request in a
    // session is straight to a video-mode host, nothing else would have triggered that resolution
    // yet, and the interstitial's link would otherwise fall back to a portless "http://localhost"
    // (i.e. port 80), which nothing listens on.
    private async Task<ProxyHttpResponse> BuildVideoInterstitialResponseAsync(Uri targetUrl, CancellationToken cancellationToken)
    {
        await ResolveSelfOriginPortAsync(cancellationToken);
        string selfOrigin = _selfOriginBaseUrl ?? "http://localhost";
        string viewerUrl = $"{selfOrigin}/index.html?url={Uri.EscapeDataString(targetUrl.ToString())}";
        string encodedViewerUrl = WebUtility.HtmlEncode(viewerUrl);
        string html = $"""
            <!doctype html>
            <html><head><meta charset="utf-8"><title>Video mode required</title></head>
            <body style="font-family:sans-serif;max-width:560px;margin:80px auto;text-align:center;">
              <h1>This site is only viewable in video mode</h1>
              <p>Policy requires <strong>{WebUtility.HtmlEncode(targetUrl.Host)}</strong> to be shown through the isolated video viewer, not directly in this browser.</p>
              <p><a href="{encodedViewerUrl}">Open in video viewer</a></p>
            </body></html>
            """;

        return new ProxyHttpResponse
        {
            StatusCode = 200,
            ReasonPhrase = "OK",
            Headers = [new ProxyHeader("Content-Type", "text/html; charset=utf-8")],
            Body = Encoding.UTF8.GetBytes(html),
        };
    }

    // CONNECT to a non-intercepted port (not in Proxy:InterceptPorts): no policy check, no cert
    // minting -- open a raw TCP connection to host:port and splice bytes both ways until either
    // side closes.
    private async Task BlindTunnelToOriginAsync(NetworkStream clientStream, ProxyStreamReader reader, string host, int port, bool writeConnectOk, CancellationToken cancellationToken)
    {
        using var origin = new TcpClient();
        try
        {
            await origin.ConnectAsync(host, port, cancellationToken);
        }
        catch (Exception ex) when (ex is SocketException or IOException)
        {
            await WriteRawAsync(clientStream, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n", cancellationToken);
            return;
        }

        if (writeConnectOk)
        {
            await WriteRawAsync(clientStream, "HTTP/1.1 200 Connection Established\r\n\r\n", cancellationToken);
        }

        NetworkStream originStream = origin.GetStream();
        byte[] leftover = reader.DrainBuffered();
        if (leftover.Length > 0)
        {
            await originStream.WriteAsync(leftover, cancellationToken);
        }

        await SpliceAsync(clientStream, originStream, cancellationToken);
    }

    // Self-origin bypass: the CONNECT/absolute-URI target is this server's own host (per
    // Proxy:SelfHosts). Tunnels straight to Kestrel's real bound port with no policy check and no
    // TLS interception, so the browser negotiates TLS (or plain HTTP) with Kestrel directly and
    // gets Kestrel's real certificate -- required for the admin UI/video viewer to keep working
    // when this same browser has the proxy configured globally.
    private async Task BlindTunnelToSelfOriginAsync(NetworkStream clientStream, ProxyStreamReader reader, bool isConnect, HttpMessageIO.RequestLine requestLine, CancellationToken cancellationToken, Uri? absoluteUri = null)
    {
        int kestrelPort = await ResolveSelfOriginPortAsync(cancellationToken);

        using var origin = new TcpClient();
        try
        {
            await origin.ConnectAsync(IPAddress.Loopback, kestrelPort, cancellationToken);
        }
        catch (Exception ex) when (ex is SocketException or IOException)
        {
            await WriteRawAsync(clientStream, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n", cancellationToken);
            return;
        }

        NetworkStream originStream = origin.GetStream();

        if (isConnect)
        {
            await WriteRawAsync(clientStream, "HTTP/1.1 200 Connection Established\r\n\r\n", cancellationToken);
        }
        else
        {
            // Kestrel expects origin-form request lines ("GET /path HTTP/1.1"), not the proxy's
            // absolute-URI form -- rewrite just this one line, then splice everything else
            // (headers/body) through unparsed.
            string path = absoluteUri!.PathAndQuery;
            string originFormLine = $"{requestLine.Method} {path} {requestLine.HttpVersion}\r\n";
            await originStream.WriteAsync(Encoding.ASCII.GetBytes(originFormLine), cancellationToken);
        }

        byte[] leftover = reader.DrainBuffered();
        if (leftover.Length > 0)
        {
            await originStream.WriteAsync(leftover, cancellationToken);
        }

        await SpliceAsync(clientStream, originStream, cancellationToken);
    }

    // Pumps bytes in both directions between two already-connected streams until either side
    // closes -- the "no parsing at all" tunnel mode used by every blind-tunnel path.
    private static async Task SpliceAsync(Stream a, Stream b, CancellationToken cancellationToken)
    {
        Task aToB = a.CopyToAsync(b, cancellationToken);
        Task bToA = b.CopyToAsync(a, cancellationToken);
        try
        {
            await Task.WhenAny(aToB, bToA);
        }
        catch (Exception ex) when (ex is IOException or OperationCanceledException)
        {
            // Either side closing/resetting mid-splice is the normal way a tunnel ends.
        }
    }

    private bool IsSelfHost(string host) =>
        _options.SelfHosts.Any(selfHost => string.Equals(selfHost, host, StringComparison.OrdinalIgnoreCase));

    // Resolves Kestrel's actual bound port once (lazily, after ApplicationStarted so
    // IServerAddressesFeature is populated) and caches it for every subsequent self-origin bypass.
    private async Task<int> ResolveSelfOriginPortAsync(CancellationToken cancellationToken)
    {
        if (_selfOriginPort is { } cachedPort)
        {
            return cachedPort;
        }

        if (!appLifetime.ApplicationStarted.IsCancellationRequested)
        {
            var startedTcs = new TaskCompletionSource();
            using (appLifetime.ApplicationStarted.Register(() => startedTcs.TrySetResult()))
            {
                await startedTcs.Task.WaitAsync(cancellationToken);
            }
        }

        string? address = kestrelServer.Features.Get<IServerAddressesFeature>()?.Addresses.FirstOrDefault();
        _selfOriginBaseUrl = address ?? "http://localhost:5000";
        _selfOriginPort = Uri.TryCreate(_selfOriginBaseUrl, UriKind.Absolute, out Uri? parsed) ? parsed.Port : 5000;
        return _selfOriginPort.Value;
    }

    // Splits a CONNECT target ("host:port") or falls back to defaultPort if no port is present.
    private static (string Host, int Port) ParseHostPort(string target, int defaultPort)
    {
        int lastColon = target.LastIndexOf(':');
        if (lastColon > 0 && int.TryParse(target[(lastColon + 1)..], out int port))
        {
            return (target[..lastColon], port);
        }

        return (target, defaultPort);
    }

    private static async Task WriteRawAsync(Stream stream, string text, CancellationToken cancellationToken)
    {
        await stream.WriteAsync(Encoding.ASCII.GetBytes(text), cancellationToken);
        await stream.FlushAsync(cancellationToken);
    }
}
