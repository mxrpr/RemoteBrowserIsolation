using FFmpeg.AutoGen;

namespace RemoteBrowserIsolation.Server.Services;

// Result of probing for a usable FFmpeg hardware-acceleration device: whether one was found and a
// human-readable description of what was tried/found, for the admin UI's status display.
public sealed record GpuProbeResult(bool Available, string Description);

public interface IGpuEncoderProbe
{
    Task<GpuProbeResult> ProbeAsync();
}

// Probes for a usable FFmpeg hardware-acceleration device (CUDA, then VAAPI) by actually
// attempting av_hwdevice_ctx_create, not just checking that driver files exist. Cached for the
// process lifetime -- hardware availability doesn't change at runtime.
//
// Note: a positive result here only means a hardware device context CAN be created. There is no
// hardware encode pipeline wired into VideoTrackStreamer yet (see
// plans/11_video_pipeline_speedup.md Step 4) -- this probe currently only feeds the admin-facing
// Auto/GPU video-encoder setting's status display and GPU mode's fail-loud check, not any actual
// encode path.
public sealed unsafe class GpuEncoderProbe : IGpuEncoderProbe
{
    private static readonly AVHWDeviceType[] CandidateTypes =
    [
        AVHWDeviceType.AV_HWDEVICE_TYPE_CUDA,
        AVHWDeviceType.AV_HWDEVICE_TYPE_VAAPI,
    ];

    private readonly Lazy<GpuProbeResult> result = new(Probe);

    public Task<GpuProbeResult> ProbeAsync() => Task.FromResult(result.Value);

    // Tries each candidate hardware device type in turn, returning the first that successfully
    // opens. A native failure on one candidate just means "try the next one", never a crash.
    private static GpuProbeResult Probe()
    {
        foreach (AVHWDeviceType type in CandidateTypes)
        {
            AVBufferRef* deviceContext = null;
            try
            {
                int openResult = ffmpeg.av_hwdevice_ctx_create(&deviceContext, type, null, null, 0);
                if (openResult >= 0)
                {
                    return new GpuProbeResult(true, $"{type} available");
                }
            }
            catch
            {
                // Native probe failure for this candidate -- treat as unavailable and keep trying
                // the rest.
            }
            finally
            {
                if (deviceContext != null)
                {
                    ffmpeg.av_buffer_unref(&deviceContext);
                }
            }
        }

        return new GpuProbeResult(false, "No hardware encoder device detected (tried CUDA, VAAPI)");
    }
}
