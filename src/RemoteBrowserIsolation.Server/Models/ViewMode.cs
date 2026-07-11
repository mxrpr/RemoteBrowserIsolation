namespace RemoteBrowserIsolation.Server.Models;

// The four ways a policy-permitted site can be shown to the client. "Deny" is deliberately not a
// member here — it's represented by the absence of a matching SitePolicy row (see PolicyEngine).
public enum ViewMode
{
    // Raw byte relay rendered client-side in a sandboxed iframe; full input.
    HtmlAllowInput,

    // Same raw byte relay, but the client must disable input — enforcement is client-side only.
    HtmlNoInput,

    // Server-side headless render streamed as VP8 video; mouse and keyboard both forwarded to the
    // page.
    VideoAllowInput,

    // Same server-side video stream; mouse input is still forwarded (so the page remains
    // navigable/scrollable), but keyboard events are dropped server-side before replay — enforcement
    // is server-side and therefore trustworthy even against a malicious client.
    VideoNoInput,
}
