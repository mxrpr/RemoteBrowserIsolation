using RemoteBrowserIsolation.Server.Models;
using RemoteBrowserIsolation.Server.Models.Admin;
using RemoteBrowserIsolation.Server.Services;

namespace RemoteBrowserIsolation.Server.Rest.Admin;

// Admin-only read/write for the whole-server video encoder mode (Auto/CPU/GPU) -- see
// VideoEncoderSettingsStore and VideoTrackStreamer for how the mode is applied, and
// plans/11_video_pipeline_speedup.md Step 4 for why GPU mode currently fails loudly rather than
// actually encoding on hardware.
public static class AdminVideoEncoderSettingsEndpoints
{
    // Registers GET/PUT /api/admin/settings/video-encoder.
    public static void MapAdminVideoEncoderSettingsEndpoints(this WebApplication app)
    {
        RouteGroupBuilder group = app.MapGroup("/api/admin/settings/video-encoder").RequireAuthorization();

        group.MapGet("", async (IVideoEncoderSettingsStore store, IGpuEncoderProbe probe) =>
        {
            VideoEncoderMode mode = await store.GetModeAsync();
            return Results.Ok(await ToResponseAsync(mode, probe));
        });

        group.MapPut("", async (VideoEncoderSettingRequest request, IVideoEncoderSettingsStore store, IGpuEncoderProbe probe) =>
        {
            await store.SetModeAsync(request.Mode);
            return Results.Ok(await ToResponseAsync(request.Mode, probe));
        });
    }

    // Combines the stored mode with a fresh GPU probe into the wire response shape.
    private static async Task<VideoEncoderSettingResponse> ToResponseAsync(VideoEncoderMode mode, IGpuEncoderProbe probe)
    {
        GpuProbeResult detected = await probe.ProbeAsync();
        return new VideoEncoderSettingResponse(mode, new GpuProbeResponse(detected.Available, detected.Description));
    }
}
