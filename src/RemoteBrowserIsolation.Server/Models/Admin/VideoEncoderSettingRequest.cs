namespace RemoteBrowserIsolation.Server.Models.Admin;

// PUT /api/admin/settings/video-encoder body — sets the whole-server video encoder mode.
public sealed record VideoEncoderSettingRequest(VideoEncoderMode Mode);
