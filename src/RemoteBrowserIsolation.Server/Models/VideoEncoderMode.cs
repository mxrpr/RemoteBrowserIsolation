namespace RemoteBrowserIsolation.Server.Models;

// Which encoder path VideoTrackStreamer uses for the VP8 transcode. See
// plans/11_video_pipeline_speedup.md Step 4 -- GPU hardware encode isn't implemented yet, so
// selecting Gpu currently fails the session loudly (rather than silently running on CPU) even if
// hardware is detected; Auto probes for hardware and logs what it found but always falls back to
// Cpu, since there is no GPU encode pipeline to actually use yet.
public enum VideoEncoderMode
{
    Auto,
    Cpu,
    Gpu,
}
