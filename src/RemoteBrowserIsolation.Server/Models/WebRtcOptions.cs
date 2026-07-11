namespace RemoteBrowserIsolation.Server.Models;

// Bound from the "WebRtc" appsettings.json section. Governs how the server publishes its RTP/ICE
// UDP endpoint so a browser outside the container (or host firewall) can reach it -- see
// plans/10_docker_image.md for why a container can't rely on ephemeral ports / its own internal IP.
public sealed class WebRtcOptions
{
    // IP address baked into the answer SDP's host candidate in place of the container's internal
    // address. Default assumes the common case: browser and container share a host, reaching the
    // published UDP port range via loopback.
    public string AdvertisedIp { get; set; } = "127.0.0.1";

    // Inclusive UDP port range the peer connection's ICE/RTP socket is bound within, so the range
    // can be published deterministically (e.g. `-p 40000-40009:40000-40009/udp`) instead of relying
    // on an OS-chosen ephemeral port.
    public int UdpPortStart { get; set; } = 40000;

    public int UdpPortEnd { get; set; } = 40009;
}
