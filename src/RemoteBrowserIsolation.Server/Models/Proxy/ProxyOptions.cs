namespace RemoteBrowserIsolation.Server.Models.Proxy;

// Bound from the "Proxy" appsettings.json section. Governs the TLS-intercepting forward proxy
// listener (see plans/9_TLS_proxy.md) -- a separate raw TcpListener, not part of Kestrel.
public sealed class ProxyOptions
{
    public int Port { get; set; } = 8080;

    public string Bind { get; set; } = "127.0.0.1";

    // CONNECT to any of these ports gets policy-checked + TLS-intercepted; any other port is a
    // blind TCP tunnel straight to origin, no policy/cert/MITM.
    public int[] InterceptPorts { get; set; } = [443];

    // Hostnames that mean "this server's own origin" -- CONNECT/absolute-URI requests to one of
    // these are tunneled straight to Kestrel with no policy check and no TLS interception, so the
    // admin UI and video viewer keep working when the same browser has this proxy configured
    // globally.
    public string[] SelfHosts { get; set; } = ["localhost", "127.0.0.1"];
}
