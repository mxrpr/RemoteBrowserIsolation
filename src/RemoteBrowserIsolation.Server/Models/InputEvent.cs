namespace RemoteBrowserIsolation.Server.Models;

// One input action forwarded from the client's canvas over the input data channel, to be replayed
// against the server-side rendered page. Type selects which of the other fields are meaningful:
// "mousemove"/"click" use X/Y, "wheel" uses DeltaX/DeltaY, "keydown"/"keyup" use Key.
public sealed record InputEvent(string Type, float? X, float? Y, float? DeltaX, float? DeltaY, string? Key);
