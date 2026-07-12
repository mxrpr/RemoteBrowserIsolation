namespace RemoteBrowserIsolation.Server.Models.Admin;

// GPU probe status shown alongside the mode dropdown, so the admin sees detected reality rather
// than just the configured wish.
public sealed record GpuProbeResponse(bool Available, string Description);

// Full read-side shape returned by GET/PUT /api/admin/settings/video-encoder.
public sealed record VideoEncoderSettingResponse(VideoEncoderMode Mode, GpuProbeResponse DetectedGpu);
