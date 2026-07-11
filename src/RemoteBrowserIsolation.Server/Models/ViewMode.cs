namespace RemoteBrowserIsolation.Server.Models;

// The four ways a policy-permitted site can be shown to the client. "Deny" is deliberately not a
// member here — it's represented by the absence of a matching SitePolicy row (see PolicyEngine).
public enum ViewMode
{
    // Raw byte relay rendered client-side in a sandboxed iframe; full input.
    HtmlAllowInput,

    // Same raw byte relay, but the client must disable input — enforcement is client-side only.
    HtmlNoInput,

    // Server-side headless render streamed as VP8 video; full input forwarded to the page.
    VideoAllowInput,

    // Same server-side video stream, but the input data channel is never wired up — enforcement is
    // server-side and therefore trustworthy even against a malicious client.
    VideoNoInput,
}
