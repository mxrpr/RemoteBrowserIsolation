namespace RemoteBrowserIsolation.Server.Models;

// Signaling request: the target URL, the client's WebRTC offer SDP, and the client's viewport size
// in CSS pixels so the server can render the page at (roughly) the size the user's browser window
// actually has. Width/Height are optional — old clients that omit them get a server-side default.
public sealed record OfferRequest(string Url, string Sdp, int? Width, int? Height);
