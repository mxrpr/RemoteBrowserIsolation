namespace RemoteBrowserIsolation.Server.Models.Admin;

// Full read-side shape of a SitePolicy row, returned by GET/POST/PUT /api/admin/sites.
public sealed record SitePolicyResponse(int Id, string HostPattern, ViewMode ViewMode, DateTime CreatedAt, DateTime UpdatedAt);
